package importer

import (
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
}
