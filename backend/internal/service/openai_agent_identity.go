package service

// OpenAI Agent Identity support follows the wire format used by the official
// Codex client. DengDeng imports the durable Ed25519 identity from auth.json;
// model requests then carry short-lived AgentAssertion signatures without
// storing or replaying the OAuth credentials that may coexist in that file.

import (
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
	"sync"
	"time"

	"dengdeng/internal/model"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
	"gorm.io/gorm"
)

const (
	OpenAIAgentIdentityAuthBaseURL          = "https://auth.openai.com/api/accounts"
	openAIAgentTaskRegistrationTimeout      = 30 * time.Second
	openAIAgentTaskRegistrationResponseSize = 64 << 10
)

var openAIAgentIdentityAuthBaseURL = OpenAIAgentIdentityAuthBaseURL

var openAIAgentIdentityTaskLocks sync.Map

// AgentIdentityClientFactory builds the account-scoped HTTP client used for
// task registration. Keeping the factory here lets gateway traffic and quota
// refreshes share one task lifecycle while still honoring per-account proxies.
type AgentIdentityClientFactory func(*model.UpstreamAccount) (*http.Client, error)

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

// IsChatGPTAccountFedRAMP reports whether Codex must address the FedRAMP
// account boundary. The flag is carried by Agent Identity auth.json files and
// must be applied to every ChatGPT backend request, not only retained as
// import metadata.
func IsChatGPTAccountFedRAMP(account *model.UpstreamAccount) bool {
	if account == nil || account.Platform != model.PlatformOpenAI {
		return false
	}
	return extraBool(account.DecodeExtra(), "chatgpt_account_is_fedramp")
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
		client = &http.Client{Timeout: openAIAgentTaskRegistrationTimeout}
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
	url := strings.TrimRight(strings.TrimSpace(openAIAgentIdentityAuthBaseURL), "/") + "/v1/agent/" + record.AgentRuntimeID + "/task/register"
	requestContext, cancel := context.WithTimeout(ctx, openAIAgentTaskRegistrationTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return "", errors.New("failed to build agent task registration request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("agent task registration request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("agent task registration returned status %d", resp.StatusCode)
	}
	var response agentTaskRegisterResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, openAIAgentTaskRegistrationResponseSize)).Decode(&response); err != nil {
		return "", errors.New("agent identity registration response is invalid")
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

// EnsureOpenAIAgentIdentityTask returns a usable identity record and registers
// a task only when one is absent or the caller observed a task-specific 401.
// The package-level per-account lock is intentionally shared by the gateway,
// quota refresher and admin probes so concurrent paths cannot rotate the same
// runtime repeatedly.
func EnsureOpenAIAgentIdentityTask(
	ctx context.Context,
	db *gorm.DB,
	clientFactory AgentIdentityClientFactory,
	account *model.UpstreamAccount,
	expectedTaskID string,
) (AgentIdentityRecord, error) {
	if !IsOpenAIAgentIdentity(account) {
		return AgentIdentityRecord{}, errors.New("account is not OpenAI Agent Identity")
	}
	lock := &sync.Mutex{}
	if account.ID > 0 {
		actual, _ := openAIAgentIdentityTaskLocks.LoadOrStore(account.ID, lock)
		shared, ok := actual.(*sync.Mutex)
		if !ok {
			return AgentIdentityRecord{}, errors.New("agent identity task lock has invalid type")
		}
		lock = shared
	}
	lock.Lock()
	defer lock.Unlock()

	if db != nil && account.ID > 0 {
		var fresh model.UpstreamAccount
		if err := db.Preload("Proxy").First(&fresh, account.ID).Error; err != nil {
			return AgentIdentityRecord{}, fmt.Errorf("reload Agent Identity account: %w", err)
		}
		*account = fresh
		if !IsOpenAIAgentIdentity(account) {
			return AgentIdentityRecord{}, errors.New("agent identity credentials are unavailable")
		}
	}

	record, err := AgentIdentityRecordFromAccount(account)
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	expectedTaskID = strings.TrimSpace(expectedTaskID)
	if record.TaskID != "" && (expectedTaskID == "" || record.TaskID != expectedTaskID) {
		return record, nil
	}

	var client *http.Client
	if clientFactory != nil {
		client, err = clientFactory(account)
		if err != nil {
			return AgentIdentityRecord{}, err
		}
	}
	taskID, err := RegisterOpenAIAgentTask(ctx, client, record)
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	record.TaskID = taskID
	extra, err := model.EncodeExtra(AgentIdentityExtra(record))
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	if db != nil && account.ID > 0 {
		if err := db.Model(&model.UpstreamAccount{}).Where("id = ?", account.ID).Update("extra", extra).Error; err != nil {
			return AgentIdentityRecord{}, err
		}
	}
	account.Extra = extra
	return record, nil
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

// RedactOpenAIAgentIdentitySensitiveBody prevents upstream errors from
// reflecting durable identity fields or a complete AgentAssertion into logs,
// monitoring records or downstream error responses.
func RedactOpenAIAgentIdentitySensitiveBody(account *model.UpstreamAccount, body []byte) []byte {
	if !IsOpenAIAgentIdentity(account) || len(body) == 0 {
		return body
	}
	redacted := string(body)
	extra := account.DecodeExtra()
	for _, key := range []string{
		"agent_private_key",
		"agent_runtime_id",
		"task_id",
		"access_token",
		"refresh_token",
		"id_token",
		"api_key",
		"session_key",
		"cookie",
	} {
		if value := extraString(extra, key); value != "" {
			redacted = strings.ReplaceAll(redacted, value, "[redacted]")
		}
	}
	for _, value := range []string{string(account.AccessToken), string(account.RefreshToken), string(account.APIKey)} {
		if value = strings.TrimSpace(value); value != "" {
			redacted = strings.ReplaceAll(redacted, value, "[redacted]")
		}
	}
	const assertionPrefix = "AgentAssertion "
	for offset := 0; offset < len(redacted); {
		relativeStart := strings.Index(redacted[offset:], assertionPrefix)
		if relativeStart < 0 {
			break
		}
		start := offset + relativeStart
		valueStart := start + len(assertionPrefix)
		end := valueStart
		for end < len(redacted) && !strings.ContainsRune(" \t\r\n\"',}", rune(redacted[end])) {
			end++
		}
		redacted = redacted[:valueStart] + "[redacted]" + redacted[end:]
		offset = valueStart + len("[redacted]")
	}
	return []byte(redacted)
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
