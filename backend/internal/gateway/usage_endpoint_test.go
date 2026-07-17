package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUsageEndpointReturnsKeyScopedBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:gateway-usage-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Group{}, &model.APIKey{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "usage@example.test", PasswordHash: "x", Status: model.StatusActive, BalanceMicro: 2_500_000, RateMultiplier: 1}
	group := model.Group{Name: "usage-group", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-usage-endpoint-test-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-usage", Name: "usage", Status: model.StatusActive, QuotaMicro: 3_000_000, QuotaUsedMicro: 1_000_000, DailyQuotaMicro: 1_500_000}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UsageLog{UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, CostMicro: 250_000, StatusCode: http.StatusOK, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	(&Gateway{db: db}).Register(router)
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Active    bool    `json:"is_active"`
		Remaining float64 `json:"remaining"`
		Unit      string  `json:"unit"`
		Quota     struct {
			Remaining float64 `json:"remaining"`
		} `json:"quota"`
		DailyQuota struct {
			Used float64 `json:"used"`
		} `json:"daily_quota"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Active || body.Remaining != 2.5 || body.Unit != "USD" || body.Quota.Remaining != 2 || body.DailyQuota.Used != 0.25 {
		t.Fatalf("unexpected usage response: %s", res.Body.String())
	}
}

func TestRelayNoAccountWritesTraceableFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:gateway-no-account-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Group{}, &model.APIKey{}, &model.UpstreamAccount{}, &model.UsageLog{}, &model.ModelPrice{}, &model.ModelConfig{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "relay@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "empty-openai", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-no-account-test-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-no-account", Name: "relay", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	gw := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("ctx_request_id", "ddr_test_failure")
		c.Header("X-Request-ID", "ddr_test_failure")
		c.Next()
	})
	gw.Register(router)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+plain)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var logEntry model.UsageLog
	if err := db.Where("request_id = ?", "ddr_test_failure").First(&logEntry).Error; err != nil {
		t.Fatalf("traceable failure missing: %v", err)
	}
	if logEntry.StatusCode != http.StatusServiceUnavailable || logEntry.ErrorMessage == "" || logEntry.CostMicro != 0 {
		t.Fatalf("unexpected failure entry: %#v", logEntry)
	}
}
