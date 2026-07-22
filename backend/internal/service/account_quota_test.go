package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestAccountQuotaAPIKeyQueriesCompatibleUsageEndpoint(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"is_active": true,
			"remaining": 12.5,
			"unit":      "USD",
			"plan_name": "标准余额",
			"quota": map[string]any{
				"limit": 20, "used": 5, "remaining": 15,
			},
			"daily_quota": map[string]any{
				"limit": 2, "used": 0.5, "remaining": 1.5,
			},
			"remaining_requests": 80,
		})
	}))
	defer server.Close()

	account := model.UpstreamAccount{GroupID: 1, Name: "openai-key", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "key", BaseURL: server.URL + "/v1", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := NewAccountQuotaService(db, nil, nil, server.Client())
	snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || snapshot.Source != "api_key_usage" || snapshot.PlanType != "标准余额" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	windows := make(map[string]model.AccountQuotaWindow, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		windows[window.Key] = window
	}
	if len(windows) != 4 || windows["balance"].Remaining == nil || *windows["balance"].Remaining != 12.5 {
		t.Fatalf("windows = %#v", snapshot.Windows)
	}
	if windows["total"].UsedPercent == nil || *windows["total"].UsedPercent != 25 {
		t.Fatalf("total window = %#v", windows["total"])
	}
	if windows["daily"].UsedPercent == nil || *windows["daily"].UsedPercent != 25 {
		t.Fatalf("daily window = %#v", windows["daily"])
	}
}

func TestAccountQuotaAPIKeyProbeSupportsEveryPlatform(t *testing.T) {
	tests := []struct {
		platform string
		path     string
		header   string
		value    string
	}{
		{model.PlatformOpenAI, "/v1/models", "Authorization", "Bearer key"},
		{model.PlatformAnthropic, "/v1/models", "x-api-key", "key"},
		{model.PlatformGemini, "/v1beta/models", "x-goog-api-key", "key"},
		{model.PlatformGrok, "/v1/models", "Authorization", "Bearer key"},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			db := newAccountQuotaTestDB(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					http.NotFound(w, r)
					return
				}
				if r.Header.Get(tt.header) != tt.value {
					t.Fatalf("unexpected %s header: %q", tt.header, r.Header.Get(tt.header))
				}
				if tt.platform == model.PlatformAnthropic && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Fatalf("missing anthropic version: %#v", r.Header)
				}
				w.Header().Set("x-ratelimit-limit-requests", "100")
				w.Header().Set("x-ratelimit-remaining-requests", "75")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			}))
			defer server.Close()

			account := model.UpstreamAccount{GroupID: 1, Name: tt.platform + "-key", Platform: tt.platform, AuthType: model.AuthAPIKey, APIKey: "key", BaseURL: server.URL, Status: model.StatusActive}
			if err := db.Create(&account).Error; err != nil {
				t.Fatal(err)
			}
			quota := NewAccountQuotaService(db, nil, nil, server.Client())
			snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.State != "ready" || snapshot.Source != "rate_limit_headers" || len(snapshot.Windows) != 1 {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			if snapshot.Windows[0].Key != "rate_requests" || snapshot.Windows[0].UsedPercent == nil || *snapshot.Windows[0].UsedPercent != 25 {
				t.Fatalf("window = %#v", snapshot.Windows[0])
			}
		})
	}
}

func TestFetchGrokBillingUsesVersionedCLIPathsAndHeaders(t *testing.T) {
	requests := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]any{"creditUsagePercent": 12.5}})
	}))
	defer server.Close()
	previousURL := grokCLIBillingBaseURL
	grokCLIBillingBaseURL = server.URL + "/v1"
	defer func() { grokCLIBillingBaseURL = previousURL }()

	quota := NewAccountQuotaService(nil, nil, nil, server.Client())
	account := &model.UpstreamAccount{Platform: model.PlatformGrok, AuthType: model.AuthOAuth}
	for _, weekly := range []bool{true, false} {
		if _, err := quota.fetchGrokBilling(context.Background(), account, "grok-access", weekly); err != nil {
			t.Fatal(err)
		}
		req := <-requests
		wantPath := "/v1/billing"
		wantQuery := ""
		if weekly {
			wantQuery = "format=credits"
		}
		if req.URL.Path != wantPath || req.URL.RawQuery != wantQuery {
			t.Fatalf("billing URL = %s, want %s?%s", req.URL.String(), wantPath, wantQuery)
		}
		if req.Header.Get("Authorization") != "Bearer grok-access" || req.Header.Get("x-xai-token-auth") != "xai-grok-cli" || req.Header.Get("x-grok-client-version") != "0.2.93" {
			t.Fatalf("unexpected Grok billing headers: %#v", req.Header)
		}
	}
}

func TestRefreshGrokSupportsFreeUnifiedBillingPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("format") == "credits" {
			_, _ = w.Write([]byte(`{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-20T00:00:00Z","end":"2026-07-27T00:00:00Z"},"isUnifiedBillingUser":true,"onDemandCap":{"val":0},"onDemandUsed":{"val":0},"prepaidBalance":{"val":0}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":0},"used":{"val":0},"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z"}}`))
	}))
	defer server.Close()
	previousURL := grokCLIBillingBaseURL
	grokCLIBillingBaseURL = server.URL + "/v1"
	defer func() { grokCLIBillingBaseURL = previousURL }()

	quota := NewAccountQuotaService(nil, nil, nil, server.Client())
	snapshot := model.AccountQuotaSnapshot{}
	account := &model.UpstreamAccount{Platform: model.PlatformGrok, AuthType: model.AuthOAuth}
	if err := quota.refreshGrok(context.Background(), account, "grok-access", &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || len(snapshot.Windows) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	weekly, monthly := snapshot.Windows[0], snapshot.Windows[1]
	if weekly.Key != "weekly" || weekly.UsedPercent != nil || weekly.ResetAt == nil || weekly.ResetAt.Format(time.RFC3339) != "2026-07-27T00:00:00Z" {
		t.Fatalf("weekly window = %#v", weekly)
	}
	if monthly.Key != "monthly" || monthly.Limit == nil || *monthly.Limit != 0 || monthly.Remaining == nil || *monthly.Remaining != 0 || monthly.UsedPercent != nil {
		t.Fatalf("monthly window = %#v", monthly)
	}
}

func TestRawFloatSupportsGrokMoneyWrapper(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{"val":15000}`), json.RawMessage(`{"val":"15000"}`), json.RawMessage(`15000`)} {
		value := rawFloat(raw)
		if value == nil || *value != 15000 {
			t.Fatalf("rawFloat(%s) = %v", raw, value)
		}
	}
}

func TestFriendlyQuotaErrorDistinguishesGrokPathFailure(t *testing.T) {
	got := friendlyQuotaError(errors.New("Grok billing: upstream status 404: not found"))
	if got != "Grok 额度接口地址无效，请检查上游 Base URL" {
		t.Fatalf("message = %q", got)
	}
}

func TestAccountQuotaAPIKeyProbeRetainsVerifiedStateWithoutAllowanceHeaders(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer server.Close()
	account := model.UpstreamAccount{GroupID: 1, Name: "verified-key", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "key", BaseURL: server.URL, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := NewAccountQuotaService(db, nil, nil, server.Client())
	snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "partial" || snapshot.Source != "api_key_probe" || len(snapshot.Windows) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestAccountQuotaRateLimitHeadersAreStoredForAPIKeys(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "openai-key", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "key", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := NewAccountQuotaService(db, nil, nil, nil)

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "100")
	headers.Set("x-ratelimit-remaining-requests", "75")
	headers.Set("x-ratelimit-reset-requests", "30s")
	if err := quota.ObserveRateLimitHeaders(&account, headers, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var snapshot model.AccountQuotaSnapshot
	if err := db.Where("upstream_account_id = ?", account.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || snapshot.Source != "rate_limit_headers" || len(snapshot.Windows) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !snapshot.LastAttemptAt.IsZero() {
		t.Fatalf("passive headers must not postpone an active quota refresh: %v", snapshot.LastAttemptAt)
	}
	if snapshot.Windows[0].Key != "rate_requests" || snapshot.Windows[0].UsedPercent == nil || *snapshot.Windows[0].UsedPercent != 25 {
		t.Fatalf("window = %#v", snapshot.Windows[0])
	}
}

func TestParseAPIKeyUsageSkipsUnlimitedQuotaSentinel(t *testing.T) {
	windows, _, active, recognized := parseAPIKeyUsage(map[string]any{
		"is_active": true,
		"remaining": 8.5,
		"quota":     map[string]any{"limit": 0, "used": 3, "remaining": 0},
	})
	if !active || !recognized || len(windows) != 1 || windows[0].Key != "balance" {
		t.Fatalf("windows=%#v active=%v recognized=%v", windows, active, recognized)
	}
}

func TestParseAPIKeyUsageSupportsSub2APIAndNewAPI(t *testing.T) {
	t.Run("sub2api", func(t *testing.T) {
		windows, plan, active, recognized := parseAPIKeyUsage(map[string]any{
			"mode":     "quota_limited",
			"isValid":  true,
			"planName": "团队套餐",
			"unit":     "USD",
			"quota": map[string]any{
				"limit": 30.0, "used": 10.0, "remaining": 20.0,
			},
			"rate_limits": []any{
				map[string]any{"window": "5h", "limit": 5.0, "used": 1.0, "remaining": 4.0, "reset_at": "2026-07-22T12:00:00Z"},
			},
			"subscription": map[string]any{
				"daily_limit_usd": 8.0, "daily_usage_usd": 2.0,
			},
		})
		if !active || !recognized || plan != "团队套餐" {
			t.Fatalf("plan=%q active=%v recognized=%v", plan, active, recognized)
		}
		byKey := make(map[string]model.AccountQuotaWindow, len(windows))
		for _, window := range windows {
			byKey[window.Key] = window
		}
		if len(byKey) != 3 || byKey["rate_5h"].ResetAt == nil || byKey["subscription_daily"].Remaining == nil || *byKey["subscription_daily"].Remaining != 6 {
			t.Fatalf("windows = %#v", windows)
		}
	})

	t.Run("new-api-token-usage", func(t *testing.T) {
		windows, _, active, recognized := parseAPIKeyUsage(map[string]any{
			"code": true,
			"data": map[string]any{
				"object": "token_usage", "total_granted": 1000.0, "total_used": 250.0, "total_available": 750.0,
				"unlimited_quota": false, "expires_at": 1784678400.0,
			},
		})
		if !active || !recognized {
			t.Fatalf("active=%v recognized=%v", active, recognized)
		}
		if len(windows) != 2 || windows[0].Unit != "quota" || windows[1].Unit != "quota" {
			t.Fatalf("windows = %#v", windows)
		}
		expiry := apiKeyUsageExpiry(map[string]any{"data": map[string]any{"expires_at": 1784678400.0}})
		if expiry == nil || expiry.Unix() != 1784678400 {
			t.Fatalf("expiry = %v", expiry)
		}
	})
}

func TestResolveAPIKeyQuotaURLRequiresSameOrigin(t *testing.T) {
	got, err := resolveAPIKeyQuotaURL("/custom/usage?scope=key", "https://relay.example/openai")
	if err != nil || got != "https://relay.example/custom/usage?scope=key" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := resolveAPIKeyQuotaURL("https://evil.example/usage", "https://relay.example"); err == nil {
		t.Fatal("expected cross-origin quota URL to be rejected")
	}
}

func TestAccountQuotaAPIKeySupportsOneAPIDashboardBilling(t *testing.T) {
	db := newAccountQuotaTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/dashboard/billing/subscription":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "billing_subscription", "hard_limit_usd": 10.0, "access_until": 1784678400,
			})
		case "/v1/dashboard/billing/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "total_usage": 250.0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	account := model.UpstreamAccount{GroupID: 1, Name: "one-api-key", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "key", BaseURL: server.URL, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := NewAccountQuotaService(db, nil, nil, server.Client())
	snapshot, err := quota.RefreshAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "ready" || snapshot.Source != "api_key_usage" || len(snapshot.Windows) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	window := snapshot.Windows[0]
	if window.Limit == nil || *window.Limit != 10 || window.Remaining == nil || *window.Remaining != 7.5 || window.UsedPercent == nil || *window.UsedPercent != 25 {
		t.Fatalf("window = %#v", window)
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

func TestEnrichOpenAISubscriptionUsesUpstreamActiveUntil(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("account_id") != "acct-1" || r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("unexpected subscription request: %s %#v", r.URL.String(), r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"plan_type": "plus", "active_until": "2026-08-15T02:50:12Z"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	oldSubscriptionsURL, oldAccountsCheckURL := openAISubscriptionsURL, openAIAccountsCheckURL
	openAISubscriptionsURL, openAIAccountsCheckURL = server.URL+"/subscriptions", server.URL+"/accounts/check"
	defer func() { openAISubscriptionsURL, openAIAccountsCheckURL = oldSubscriptionsURL, oldAccountsCheckURL }()

	account := &model.UpstreamAccount{AccountID: "acct-1"}
	snapshot := &model.AccountQuotaSnapshot{}
	enrichOpenAISubscription(context.Background(), server.Client(), account, "access", snapshot)
	if snapshot.PlanType != "plus" || snapshot.SubscriptionExpiresAt == nil || snapshot.SubscriptionExpiresAt.UTC().Format(time.RFC3339) != "2026-08-15T02:50:12Z" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestEnrichOpenAISubscriptionFallsBackToAccountEntitlement(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/subscriptions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"plan_type": "k12"})
	})
	mux.HandleFunc("/accounts/check", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": map[string]any{
			"org-school": map[string]any{
				"account":     map[string]any{"plan_type": "k12", "is_default": true},
				"entitlement": map[string]any{"expires_at": "2026-09-01T00:00:00Z"},
			},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	oldSubscriptionsURL, oldAccountsCheckURL := openAISubscriptionsURL, openAIAccountsCheckURL
	openAISubscriptionsURL, openAIAccountsCheckURL = server.URL+"/subscriptions", server.URL+"/accounts/check"
	defer func() { openAISubscriptionsURL, openAIAccountsCheckURL = oldSubscriptionsURL, oldAccountsCheckURL }()
	extra, err := model.EncodeExtra(map[string]any{"organization_id": "org-school"})
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{AccountID: "acct-1", Extra: extra}
	snapshot := &model.AccountQuotaSnapshot{}
	enrichOpenAISubscription(context.Background(), server.Client(), account, "access", snapshot)
	if snapshot.PlanType != "k12" || snapshot.SubscriptionExpiresAt == nil || snapshot.SubscriptionExpiresAt.UTC().Format(time.RFC3339) != "2026-09-01T00:00:00Z" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
