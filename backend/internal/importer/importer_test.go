package importer

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"
	"time"

	"dengdeng/internal/model"
)

func TestParseCodexAuthJSON(t *testing.T) {
	raw := []byte(`{
  "auth_mode":"chatgpt",
  "tokens":{
    "access_token":"access",
    "refresh_token":"refresh",
    "id_token":"eyJhbGciOiJub25lIn0.eyJlbWFpbCI6ImNvZGV4QGV4YW1wbGUuY29tIiwiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC0xIn0.",
    "account_id":"acct-1"
  },
  "last_refresh":"2026-07-15T00:00:00Z"
}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.Platform != model.PlatformOpenAI || got.AuthType != model.AuthOAuth {
		t.Fatalf("platform/auth = %q/%q", got.Platform, got.AuthType)
	}
	if !got.PlatformDetected {
		t.Fatal("native Codex auth_mode must be treated as an explicit OpenAI signal")
	}
	if got.AccessToken != "access" || got.RefreshToken != "refresh" || got.Email != "codex@example.com" || got.AccountID != "acct-1" {
		t.Fatalf("credential metadata was not retained: %#v", got)
	}
}

func TestParseGenericOAuthLeavesPlatformForTargetGroup(t *testing.T) {
	raw := []byte(`{"accounts":[{"name":"generic-oauth","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","client_id":"client"}}]}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.Platform != model.PlatformOpenAI {
		t.Fatalf("legacy fallback platform = %q, want openai", got.Platform)
	}
	if got.PlatformDetected {
		t.Fatal("generic OAuth credentials must let the selected group decide the platform")
	}
}

func TestParseGrokOAuthSignals(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://auth.x.ai","aud":"grok-cli","email":"grok@example.com","sub":"grok-user"}`))
	raw := []byte(`{"type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","id_token":"x.` + claims + `.x","client_id":"grok-client","scope":"openid grok-cli:access","subscription_tier":"supergrok","entitlement_status":"active"}}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.Platform != model.PlatformGrok || !got.PlatformDetected || got.AuthType != model.AuthOAuth {
		t.Fatalf("unexpected Grok import: %#v", got)
	}
	if got.AccountID != "grok-user" || got.Email != "grok@example.com" {
		t.Fatalf("Grok identity was not retained: %#v", got)
	}
	if got.Extra["subscription_tier"] != "supergrok" || got.Extra["entitlement_status"] != "active" {
		t.Fatalf("Grok entitlement metadata was not retained: %#v", got.Extra)
	}
}

func TestParseClaudeCodeCredentials(t *testing.T) {
	raw := []byte(`{
  "claudeAiOauth": {
    "accessToken":"access",
    "refreshToken":"refresh",
    "expiresAt": 1893456000000,
    "subscriptionType":"max"
  }
}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.Platform != model.PlatformAnthropic || got.AuthType != model.AuthOAuth {
		t.Fatalf("platform/auth = %q/%q", got.Platform, got.AuthType)
	}
	if got.AccessToken != "access" || got.RefreshToken != "refresh" || got.ExpiresAt == nil {
		t.Fatalf("Claude credentials were not retained: %#v", got)
	}
	if got.ExpiresAt.Before(time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expiresAt parsed incorrectly: %v", got.ExpiresAt)
	}
	if got.Extra["plan_type"] != "max" {
		t.Fatalf("subscription type was not retained: %#v", got.Extra)
	}
}

func TestParseSub2APIWithCamelCaseCredentials(t *testing.T) {
	raw := []byte(`{
  "exported_at":"2026-07-15T00:00:00Z",
  "accounts":[{
    "name":"imported",
    "platform":"openai",
    "auth_type":"oauth",
    "priority":42,
    "concurrency":3,
    "group_ids":[1,2],
    "credentials":{"accessToken":"access","refreshToken":"refresh","accountId":"acct-2"}
  }]
}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.AccessToken != "access" || got.RefreshToken != "refresh" || got.AccountID != "acct-2" {
		t.Fatalf("sub2api credential was not retained: %#v", got)
	}
	if got.Priority == nil || *got.Priority != 42 || len(got.GroupIDs) != 2 {
		t.Fatalf("sub2api routing metadata was not retained: %#v", got)
	}
	if got.Concurrency == nil || *got.Concurrency != 3 {
		t.Fatalf("sub2api concurrency was not retained: %#v", got)
	}
}

func TestParseGeminiOAuthRetainsRefreshClientMetadata(t *testing.T) {
	raw := []byte(`{
  "accounts":[{
    "name":"gemini-cli",
    "platform":"gemini",
    "type":"oauth",
    "credentials":{
      "access_token":"access",
      "refresh_token":"refresh",
      "client_id":"desktop-client",
      "client_secret":"desktop-secret",
      "project_id":"project-1",
      "oauth_type":"gemini_cli",
      "scope":"cloud-platform"
    }
  }]
}`)
	accounts, err := Parse("sub2api", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	extra := accounts[0].Extra
	for key, want := range map[string]string{
		"client_id": "desktop-client", "client_secret": "desktop-secret",
		"project_id": "project-1", "oauth_type": "gemini_cli", "scope": "cloud-platform",
	} {
		if got, _ := extra[key].(string); got != want {
			t.Fatalf("extra[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestParseOpenAISeparatesTokenAndSubscriptionExpiry(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_subscription_active_until":"2026-08-15T02:50:12Z"}}`))
	raw := []byte(`{"accounts":[{"platform":"openai","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","expires_at":"2026-07-25T11:33:52Z","id_token":"x.` + payload + `.x"}}]}`)
	accounts, err := Parse("sub2api", raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ExpiresAt == nil {
		t.Fatalf("account token expiry missing: %#v", accounts)
	}
	if got := accounts[0].ExpiresAt.UTC().Format(time.RFC3339); got != "2026-07-25T11:33:52Z" {
		t.Fatalf("token expiry = %q", got)
	}
	if got, _ := accounts[0].Extra["subscription_expires_at"].(string); got != "2026-08-15T02:50:12Z" {
		t.Fatalf("subscription expiry = %q", got)
	}
}

func TestParseSub2APIAgentIdentity(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(der)
	raw := []byte(`{"accounts":[{"name":"agent@example.com","platform":"openai","type":"oauth","credentials":{"auth_mode":"agentIdentity","agent_runtime_id":"runtime-1","agent_private_key":"` + encodedKey + `","task_id":"task-1","chatgpt_account_id":"acct-1","chatgpt_user_id":"user-1","expires_at":"2020-01-01T00:00:00Z"}}]}`)
	accounts, err := Parse("sub2api", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].AuthType != model.AuthAgentIdentity {
		t.Fatalf("unexpected accounts: %#v", accounts)
	}
	if accounts[0].Extra["agent_runtime_id"] != "runtime-1" || accounts[0].Extra["agent_private_key"] != encodedKey || accounts[0].AccountID != "acct-1" {
		t.Fatalf("agent identity fields were not retained: %#v", accounts[0])
	}
	if accounts[0].ExpiresAt != nil {
		t.Fatalf("bootstrap OAuth expiry must not expire Agent Identity: %v", accounts[0].ExpiresAt)
	}
}

func TestParseNativeCodexAgentIdentityAuthJSON(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(der)
	raw := []byte(`{"auth_mode":"agentIdentity","agent_identity":{"agent_runtime_id":"runtime-native","agent_private_key":"` + encodedKey + `","account_id":"acct-native","chatgpt_user_id":"user-native","email":"native@example.com","plan_type":"plus"}}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	got := accounts[0]
	if got.AuthType != model.AuthAgentIdentity || got.Name != "native@example.com" {
		t.Fatalf("unexpected imported identity: %#v", got)
	}
	if got.Extra["agent_runtime_id"] != "runtime-native" || got.Extra["chatgpt_user_id"] != "user-native" || got.AccountID != "acct-native" {
		t.Fatalf("native Agent Identity fields were not retained: %#v", got)
	}
}

func TestParseManagedChatGPTAuthJSONWithAgentIdentity(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(der)
	raw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"bootstrap-token","refresh_token":"bootstrap-refresh","id_token":"bootstrap-id"},"session_token":"bootstrap-session","agent_identity":{"agent_runtime_id":"runtime-managed","agent_private_key":"` + encodedKey + `","account_id":"team-managed","chatgpt_user_id":"user-managed","email":"managed@example.com","plan_type":"team"}}`)
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].AuthType != model.AuthAgentIdentity {
		t.Fatalf("unexpected accounts: %#v", accounts)
	}
	if accounts[0].AccessToken != "" || accounts[0].RefreshToken != "" {
		t.Fatalf("managed identity import retained OAuth tokens: %#v", accounts[0])
	}
	if accounts[0].Extra["id_token"] != nil || accounts[0].Extra["session_token"] != nil {
		t.Fatalf("managed identity import retained bootstrap metadata: %#v", accounts[0].Extra)
	}
	if accounts[0].AccountID != "team-managed" || accounts[0].Extra["chatgpt_user_id"] != "user-managed" {
		t.Fatalf("managed identity metadata was not retained: %#v", accounts[0])
	}
}

func TestParseAgentIdentityJSONLines(t *testing.T) {
	makeEntry := func(runtimeID, accountID, userID string) string {
		_, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return `{"auth_mode":"agentIdentity","agent_identity":{"agent_runtime_id":"` + runtimeID + `","agent_private_key":"` + base64.StdEncoding.EncodeToString(der) + `","account_id":"` + accountID + `","chatgpt_user_id":"` + userID + `"}}`
	}
	raw := []byte(makeEntry("runtime-a", "team-a", "user-a") + "\n" + makeEntry("runtime-b", "team-b", "user-b"))
	accounts, err := Parse("auto", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	if accounts[0].AccountID != "team-a" || accounts[1].AccountID != "team-b" {
		t.Fatalf("unexpected JSONL accounts: %#v", accounts)
	}
}
