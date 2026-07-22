package service

// OpenAI Agent Identity support follows the wire format used by the official
// Codex client. A durable Ed25519 identity is registered once with a valid
// ChatGPT access token; model requests then carry short-lived AgentAssertion
// signatures instead of storing or replaying that OAuth token.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"dengdeng/internal/model"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	OpenAIAgentIdentityAuthBaseURL = "https://auth.openai.com/api/accounts"
	openAIWebSessionURL            = "https://chatgpt.com/api/auth/session"
	agentIdentityVersion           = "0.144.1"
	agentIdentityKeyContext        = "codex-agent-identity-ed25519-v1"
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

type AgentIdentityMetadata struct {
	AccountID               string
	ChatGPTUserID           string
	Email                   string
	PlanType                string
	ChatGPTAccountIsFedRAMP bool
}

type agentIdentityKeyMaterial struct {
	privateKeyPKCS8Base64 string
	publicKeySSH          string
}

type agentIdentityRegisterResponse struct {
	AgentRuntimeID string `json:"agent_runtime_id"`
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

// RegisterOpenAIAgentIdentity creates the durable identity and its first task.
// The supplied access token is used only for /agent/register and is not kept.
func RegisterOpenAIAgentIdentity(ctx context.Context, client *http.Client, accessToken string, metadata AgentIdentityMetadata) (AgentIdentityRecord, error) {
	if strings.TrimSpace(accessToken) == "" {
		return AgentIdentityRecord{}, errors.New("OpenAI access token is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	keyMaterial, err := generateAgentIdentityKeyMaterial()
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	body, err := json.Marshal(map[string]any{
		"abom": map[string]string{
			"agent_version":    agentIdentityVersion,
			"agent_harness_id": "codex-cli",
			"running_location": "cli-" + runtime.GOOS,
		},
		"agent_public_key": keyMaterial.publicKeySSH,
		"capabilities":     []string{"responsesapi"},
		"ttl":              nil,
	})
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	var registered agentIdentityRegisterResponse
	if err := doAgentIdentityRegistration(ctx, client, http.MethodPost, OpenAIAgentIdentityAuthBaseURL+"/v1/agent/register", body, "Bearer "+strings.TrimSpace(accessToken), metadata.ChatGPTAccountIsFedRAMP, &registered); err != nil {
		return AgentIdentityRecord{}, err
	}
	if strings.TrimSpace(registered.AgentRuntimeID) == "" {
		return AgentIdentityRecord{}, errors.New("agent identity registration omitted runtime id")
	}
	record := AgentIdentityRecord{
		AgentRuntimeID:          strings.TrimSpace(registered.AgentRuntimeID),
		AgentPrivateKey:         keyMaterial.privateKeyPKCS8Base64,
		AccountID:               strings.TrimSpace(metadata.AccountID),
		ChatGPTUserID:           strings.TrimSpace(metadata.ChatGPTUserID),
		Email:                   strings.TrimSpace(metadata.Email),
		PlanType:                strings.TrimSpace(metadata.PlanType),
		ChatGPTAccountIsFedRAMP: metadata.ChatGPTAccountIsFedRAMP,
	}
	if err := ValidateAgentIdentityRecord(record); err != nil {
		return AgentIdentityRecord{}, err
	}
	taskID, err := RegisterOpenAIAgentTask(ctx, client, record)
	if err != nil {
		return AgentIdentityRecord{}, err
	}
	record.TaskID = taskID
	return record, nil
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

// ResolveOpenAIWebSession exchanges an already authenticated ChatGPT browser
// session cookie for the short-lived access token needed only to register the
// durable runtime. Raw Cookie header input is accepted for compatibility.
func ResolveOpenAIWebSession(ctx context.Context, client *http.Client, session string) (string, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return "", errors.New("web session is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	candidates := []string{session}
	if !strings.Contains(session, "=") {
		candidates = []string{
			"__Secure-next-auth.session-token=" + session,
			"next-auth.session-token=" + session,
			"__Secure-authjs.session-token=" + session,
		}
	}
	var lastStatus int
	for _, cookie := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIWebSessionURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Cookie", cookie)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("OpenAI session request failed: %w", err)
		}
		lastStatus = resp.StatusCode
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			if token := firstNonEmptyString(extraString(payload, "accessToken"), extraString(payload, "access_token")); token != "" {
				return token, nil
			}
		}
	}
	return "", fmt.Errorf("OpenAI web session is invalid or expired (status %d)", lastStatus)
}

// OpenAIIdentityFromAccessToken reads identity metadata from a JWT. The token
// itself is subsequently verified by OpenAI during runtime registration.
func OpenAIIdentityFromAccessToken(token string) (AgentIdentityMetadata, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return AgentIdentityMetadata{}, errors.New("OpenAI access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return AgentIdentityMetadata{}, errors.New("OpenAI access token payload is invalid")
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return AgentIdentityMetadata{}, errors.New("OpenAI access token claims are invalid")
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	profile, _ := claims["https://api.openai.com/profile"].(map[string]any)
	metadata := AgentIdentityMetadata{
		AccountID:               firstNonEmptyString(extraString(auth, "chatgpt_account_id"), extraString(auth, "account_id"), extraString(auth, "poid")),
		ChatGPTUserID:           firstNonEmptyString(extraString(auth, "chatgpt_user_id"), extraString(auth, "user_id"), extraString(claims, "sub")),
		Email:                   firstNonEmptyString(extraString(profile, "email"), extraString(claims, "email")),
		PlanType:                firstNonEmptyString(extraString(auth, "chatgpt_plan_type"), extraString(auth, "plan_type")),
		ChatGPTAccountIsFedRAMP: extraBool(auth, "chatgpt_account_is_fedramp"),
	}
	if metadata.AccountID == "" || metadata.ChatGPTUserID == "" {
		return AgentIdentityMetadata{}, errors.New("OpenAI token does not contain account identity")
	}
	return metadata, nil
}

func generateAgentIdentityKeyMaterial() (agentIdentityKeyMaterial, error) {
	seedMaterial := make([]byte, 64)
	if _, err := rand.Read(seedMaterial); err != nil {
		return agentIdentityKeyMaterial{}, errors.New("failed to generate agent identity seed")
	}
	digest := sha512.New()
	_, _ = digest.Write([]byte(agentIdentityKeyContext))
	_, _ = digest.Write(seedMaterial)
	seed := digest.Sum(nil)[:ed25519.SeedSize]
	privateKey := ed25519.NewKeyFromSeed(seed)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return agentIdentityKeyMaterial{}, errors.New("failed to encode agent identity private key")
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return agentIdentityKeyMaterial{
		privateKeyPKCS8Base64: base64.StdEncoding.EncodeToString(der),
		publicKeySSH:          encodeSSHEd25519PublicKey(publicKey),
	}, nil
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

func encodeSSHEd25519PublicKey(publicKey ed25519.PublicKey) string {
	var blob bytes.Buffer
	writeSSHString(&blob, []byte("ssh-ed25519"))
	writeSSHString(&blob, publicKey)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob.Bytes())
}

func writeSSHString(dst *bytes.Buffer, value []byte) {
	_ = binary.Write(dst, binary.BigEndian, uint32(len(value)))
	_, _ = dst.Write(value)
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
