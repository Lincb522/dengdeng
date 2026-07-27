package gateway

import (
	"net/http"
	"testing"
	"time"

	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGatewayPersistsRateLimitHeadersFromRealAPIKeyResponse(t *testing.T) {
	if err := crypto.Init("", "gateway-quota-headers-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:gateway-quota-headers?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.AccountQuotaSnapshot{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{
		GroupID: 1, Name: "static-key", Platform: model.PlatformOpenAI,
		AuthType: model.AuthAPIKey, APIKey: "key", Status: model.StatusActive,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{quota: service.NewAccountQuotaService(db, nil, nil, nil)}
	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "200")
	headers.Set("x-ratelimit-remaining-requests", "150")
	headers.Set("x-ratelimit-limit-tokens", "50000")
	headers.Set("x-ratelimit-remaining-tokens", "42000")
	gw.observeAccountQuota(&account, headers)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var snapshot model.AccountQuotaSnapshot
		if err := db.Where("upstream_account_id = ?", account.ID).First(&snapshot).Error; err == nil {
			if snapshot.Source != "rate_limit_headers" || snapshot.State != "ready" || len(snapshot.Windows) != 2 {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			if snapshot.Windows[0].Key != "rate_requests" || snapshot.Windows[1].Key != "rate_tokens" {
				t.Fatalf("windows = %#v", snapshot.Windows)
			}
			if snapshot.Windows[0].Unit != "requests" || snapshot.Windows[1].Unit != "tokens" {
				t.Fatalf("window units = %#v", snapshot.Windows)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("quota snapshot was not persisted")
}

func TestGatewayMergesOAuthRateLimitHeadersWithoutErasingSubscriptionWindows(t *testing.T) {
	if err := crypto.Init("", "gateway-oauth-quota-headers-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:gateway-oauth-quota-headers?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.AccountQuotaSnapshot{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: 1, Name: "oauth", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	used := 72.0
	snapshot := model.AccountQuotaSnapshot{
		UpstreamAccountID: account.ID, Platform: account.Platform, Source: "codex_subscription", State: "ready",
		Windows: []model.AccountQuotaWindow{{Key: "primary", Label: "5 小时", UsedPercent: &used}}, FetchedAt: &now, LastAttemptAt: now,
	}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "100")
	headers.Set("x-ratelimit-remaining-requests", "0")
	headers.Set("x-ratelimit-reset-requests", "5h")
	gw := &Gateway{quota: service.NewAccountQuotaService(db, nil, nil, nil)}
	gw.observeAccountQuota(&account, headers)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var updated model.AccountQuotaSnapshot
		if err := db.Where("upstream_account_id = ?", account.ID).First(&updated).Error; err == nil && len(updated.Windows) == 2 {
			if updated.Source != "codex_subscription" {
				t.Fatalf("source changed to %q", updated.Source)
			}
			if updated.Windows[0].Key != "primary" || updated.Windows[1].Key != "rate_requests" {
				t.Fatalf("windows = %#v", updated.Windows)
			}
			if updated.Windows[1].Remaining == nil || *updated.Windows[1].Remaining != 0 || updated.Windows[1].ResetAt == nil {
				t.Fatalf("rate-limit window = %#v", updated.Windows[1])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("OAuth rate-limit headers were not merged")
}
