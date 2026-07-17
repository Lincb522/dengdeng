package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newAccountQuotaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := crypto.Init("", "account-quota-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.AccountQuotaSnapshot{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAccountQuotaRefreshesClaudeSubscriptionAndObservedUsage(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer claude-access" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Fatalf("unexpected headers: %#v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"five_hour":        map[string]any{"utilization": 12.5, "resets_at": "2026-07-18T01:00:00Z"},
			"seven_day":        map[string]any{"utilization": 34.0, "resets_at": "2026-07-24T01:00:00Z"},
			"seven_day_sonnet": map[string]any{"utilization": 56.0, "resets_at": "2026-07-24T02:00:00Z"},
		})
	}))
	defer server.Close()
	previousURL := claudeOAuthUsageURL
	claudeOAuthUsageURL = server.URL
	defer func() { claudeOAuthUsageURL = previousURL }()

	future := time.Now().UTC().Add(time.Hour)
	account := model.UpstreamAccount{
		GroupID: 1, Name: "claude", Platform: model.PlatformAnthropic, AuthType: model.AuthOAuth,
		AccessToken: "claude-access", RefreshToken: "claude-refresh", ExpiresAt: &future, Status: model.StatusActive,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UsageLog{AccountID: account.ID, InputTokens: 120, OutputTokens: 30, CostMicro: 4000, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}

	manager := oauth.NewManager(db, config.OAuthConfig{}, server.Client())
	quota := NewAccountQuotaService(db, nil, manager, server.Client())
	snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || snapshot.Source != "claude_subscription" || len(snapshot.Windows) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.ObservedUsage) != 3 || snapshot.ObservedUsage[0].Requests != 1 || snapshot.ObservedUsage[0].InputTokens != 120 {
		t.Fatalf("observed usage = %#v", snapshot.ObservedUsage)
	}

	var stored model.AccountQuotaSnapshot
	if err := db.Where("upstream_account_id = ?", account.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.Windows) != 3 || stored.Windows[0].UsedPercent == nil || *stored.Windows[0].UsedPercent != 12.5 {
		t.Fatalf("stored windows = %#v", stored.Windows)
	}
}

func TestAccountQuotaAPIKeyUsesLocalUsageAndRateLimitHeaders(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "openai-key", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "key", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := NewAccountQuotaService(db, nil, nil, nil)
	snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "local_only" || snapshot.Source != "local_observed" {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "100")
	headers.Set("x-ratelimit-remaining-requests", "75")
	headers.Set("x-ratelimit-reset-requests", "30s")
	if err := quota.ObserveRateLimitHeaders(&account, headers, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("upstream_account_id = ?", account.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || snapshot.Source != "rate_limit_headers" || len(snapshot.Windows) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Windows[0].UsedPercent == nil || *snapshot.Windows[0].UsedPercent != 25 {
		t.Fatalf("window = %#v", snapshot.Windows[0])
	}
}

func TestAccountSubscriptionExpiryComesFromSubscriptionClaim(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_subscription_active_until":"2026-08-15T02:50:12+00:00"}}`))
	extra, err := model.EncodeExtra(map[string]any{"id_token": "x." + payload + ".x"})
	if err != nil {
		t.Fatal(err)
	}
	tokenExpiry := time.Date(2026, 10, 15, 5, 44, 15, 0, time.UTC)
	account := model.UpstreamAccount{ExpiresAt: &tokenExpiry, Extra: extra}
	got := accountSubscriptionExpiresAt(&account)
	if got == nil || got.UTC().Format(time.RFC3339) != "2026-08-15T02:50:12Z" {
		t.Fatalf("subscription expiry = %v", got)
	}
}

func TestQuotaResetAtAcceptsMilliseconds(t *testing.T) {
	want := time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)
	got := quotaResetAt(want.UnixMilli(), 0)
	if got == nil || !got.Equal(want) {
		t.Fatalf("reset at = %v, want %v", got, want)
	}
}
