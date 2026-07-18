package importer

import (
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
	if got.AccessToken != "access" || got.RefreshToken != "refresh" || got.Email != "codex@example.com" || got.AccountID != "acct-1" {
		t.Fatalf("credential metadata was not retained: %#v", got)
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
