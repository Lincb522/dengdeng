package service

// OpenAI Agent Identity support follows the wire format used by the official
// Codex client. DengDeng imports the durable Ed25519 identity from auth.json;
// model requests then carry short-lived AgentAssertion signatures without
// storing or replaying the OAuth credentials that may coexist in that file.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	OpenAIAgentIdentityAuthBaseURL = "https://auth.openai.com/api/accounts"
)

// AgentIdentityRecord is the durable, token-free authentication record stored
// in UpstreamAccount.Extra. The private key remains encrypted at rest by the
// model's EncryptedString field and is never serialized back to the browser.
type AgentIdentityRecord struct {
	AgentRuntimeID          string `json:"agent_runtime_id"`
	AgentPrivateKey         string `json:"agent_private_key"`
	TaskID                  string `json:"task_id,omitempty"`
	AccountID               string `json:"account_id"`
	ChatGPTUserID           string `json:"chatgpt_user_id"`
	Email                   string `json:"email,omitempty"`
	PlanType                string `json:"plan_type,omitempty"`
	ChatGPTAccountIsFedRAMP bool   `json:"chatgpt_account_is_fedramp"`
}

type agentTaskRegisterResponse struct {
	TaskID               string `json:"task_id"`
	TaskIDCamel          string `json:"taskId"`
	EncryptedTaskID      string `json:"encrypted_task_id"`
	EncryptedTaskIDCamel string `json:"encryptedTaskId"`
}

// IsOpenAIAgentIdentity distinguishes the signing flow from refreshable OAuth.
func IsOpenAIAgentIdentity(account *model.UpstreamAccount) bool {
	if account == nil || account.Platform != model.PlatformOpenAI {
		return false
	}
	if account.AuthType == model.AuthAgentIdentity {
		return true
	}
	extra := account.DecodeExtra()
	mode, _ := extra["auth_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "agentIdentity")
}

func AgentIdentityRecordFromAccount(account *model.UpstreamAccount) (AgentIdentityRecord, error) {
	if !IsOpenAIAgentIdentity(account) {
		return AgentIdentityRecord{}, errors.New("account is not OpenAI Agent Identity")
	}
	extra := account.DecodeExtra()
	record := AgentIdentityRecord{
		AgentRuntimeID:          extraString(extra, "agent_runtime_id"),
		AgentPrivateKey:         extraString(extra, "agent_private_key"),
		TaskID:                  extraString(extra, "task_id"),
		AccountID:               firstNonEmptyString(account.AccountID, extraString(extra, "account_id"), extraString(extra, "chatgpt_account_id")),
		ChatGPTUserID:           extraString(extra, "chatgpt_user_id"),
		Email:                   firstNonEmptyString(account.Email, extraString(extra, "email")),
		PlanType:                extraString(extra, "plan_type"),
		ChatGPTAccountIsFedRAMP: extraBool(extra, "chatgpt_account_is_fedramp"),
	}
	if err := ValidateAgentIdentityRecord(record); err != nil {
		return AgentIdentityRecord{}, err
	}
	return record, nil
}

func AgentIdentityExtra(record AgentIdentityRecord) map[string]any {
	return map[string]any{
		"auth_mode":                  "agentIdentity",
		"agent_runtime_id":           record.AgentRuntimeID,
		"agent_private_key":          record.AgentPrivateKey,
		"task_id":                    record.TaskID,
		"account_id":                 record.AccountID,
		"chatgpt_account_id":         record.AccountID,
		"chatgpt_user_id":            record.ChatGPTUserID,
		"email":                      record.Email,
		"plan_type":                  record.PlanType,
		"chatgpt_account_is_fedramp": record.ChatGPTAccountIsFedRAMP,
	}
}

func ValidateAgentIdentityRecord(record AgentIdentityRecord) error {
	if strings.TrimSpace(record.AgentRuntimeID) == "" {
		return errors.New("agent identity runtime id is missing")
	}
	if strings.TrimSpace(record.AgentPrivateKey) == "" {
		return errors.New("agent identity private key is missing")
	}
	if strings.TrimSpace(record.AccountID) == "" || strings.TrimSpace(record.ChatGPTUserID) == "" {
		return errors.New("agent identity account metadata is incomplete")
	}
	_, err := parseAgentIdentityPrivateKey(record.AgentPrivateKey)
	return err
}

func RegisterOpenAIAgentTask(ctx context.Context, client *http.Client, record AgentIdentityRecord) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	privateKey, err := parseAgentIdentityPrivateKey(record.AgentPrivateKey)
	if err != nil {
		return "", err
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	signature, err := privateKey.Sign(nil, []byte(record.AgentRuntimeID+":"+timestamp), crypto.Hash(0))
	if err != nil {
		return "", errors.New("failed to sign agent task registration")
	}
	body, _ := json.Marshal(map[string]string{
		"timestamp": timestamp,
		"signature": base64.StdEncoding.EncodeToString(signature),
	})
	var response agentTaskRegisterResponse
	url := OpenAIAgentIdentityAuthBaseURL + "/v1/agent/" + record.AgentRuntimeID + "/task/register"
	if err := doAgentIdentityRegistration(ctx, client, http.MethodPost, url, body, "", false, &response); err != nil {
		return "", err
	}
	if taskID := strings.TrimSpace(firstNonEmptyString(response.TaskID, response.TaskIDCamel)); taskID != "" {
		return taskID, nil
	}
	encrypted := firstNonEmptyString(response.EncryptedTaskID, response.EncryptedTaskIDCamel)
	if encrypted == "" {
		return "", errors.New("agent task registration omitted task id")
	}
	return decryptAgentTaskID(privateKey, encrypted)
}

func OpenAIAgentIdentityAuthorization(record AgentIdentityRecord, now time.Time) (string, error) {
	if strings.TrimSpace(record.TaskID) == "" {
		return "", errors.New("agent identity task id is missing")
	}
	privateKey, err := parseAgentIdentityPrivateKey(record.AgentPrivateKey)
	if err != nil {
		return "", err
	}
	timestamp := now.UTC().Format(time.RFC3339)
	payload := []byte(record.AgentRuntimeID + ":" + record.TaskID + ":" + timestamp)
	signature, err := privateKey.Sign(nil, payload, crypto.Hash(0))
	if err != nil {
		return "", errors.New("failed to sign agent assertion")
	}
	envelope := map[string]string{
		"agent_runtime_id": record.AgentRuntimeID,
		"task_id":          record.TaskID,
		"timestamp":        timestamp,
		"signature":        base64.StdEncoding.EncodeToString(signature),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return "AgentAssertion " + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func IsOpenAIAgentTaskInvalid(status int, body []byte) bool {
	if status != http.StatusUnauthorized {
		return false
	}
	lower := strings.ToLower(string(body))
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(lower)
	for _, marker := range []string{`"code":"invalid_task_id"`, `"code":"task_not_found"`, `"code":"task_expired"`, `"error":"invalid_task_id"`} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	for _, marker := range []string{"invalid task_id", "invalid task id", "task_id is invalid", "task id is invalid", "task not found", "task expired", "unknown task_id", "unknown task id"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseAgentIdentityPrivateKey(encoded string) (ed25519.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("agent identity private key is not valid base64")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("agent identity private key is not valid PKCS#8")
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("agent identity private key is not Ed25519")
	}
	return privateKey, nil
}

func decryptAgentTaskID(privateKey ed25519.PrivateKey, encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", errors.New("encrypted agent task id is not valid base64")
	}
	digest := sha512.Sum512(privateKey.Seed())
	var curvePrivate [32]byte
	copy(curvePrivate[:], digest[:32])
	curvePrivate[0] &= 248
	curvePrivate[31] &= 127
	curvePrivate[31] |= 64
	curvePublicBytes, err := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
	if err != nil {
		return "", errors.New("failed to derive agent identity decryption key")
	}
	var curvePublic [32]byte
	copy(curvePublic[:], curvePublicBytes)
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &curvePublic, &curvePrivate)
	if !ok {
		return "", errors.New("failed to decrypt encrypted agent task id")
	}
	taskID := strings.TrimSpace(string(plaintext))
	if taskID == "" {
		return "", errors.New("decrypted agent task id is empty")
	}
	return taskID, nil
}

func doAgentIdentityRegistration(ctx context.Context, client *http.Client, method, url string, body []byte, authorization string, fedramp bool, target any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if fedramp {
			req.Header.Set("X-OpenAI-Fedramp", "true")
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := json.Unmarshal(responseBody, target); err != nil {
				return errors.New("agent identity registration response is invalid")
			}
			return nil
		}
		message := strings.TrimSpace(string(responseBody))
		if len(message) > 512 {
			message = message[:512]
		}
		lastErr = fmt.Errorf("agent identity registration returned status %d: %s", resp.StatusCode, message)
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			break
		}
	}
	return lastErr
}

func extraString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func extraBool(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, _ := values[key].(bool)
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
