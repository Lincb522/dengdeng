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
