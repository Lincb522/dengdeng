package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/crypto"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRetryableUpstreamIncludesAccountSpecificPayloadFailures(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusRequestEntityTooLarge, `<html>413 Request Entity Too Large</html>`, true},
		{http.StatusMethodNotAllowed, `method not allowed`, true},
		{http.StatusBadRequest, `{"error":{"message":"The model is not supported when using Codex with a ChatGPT account"}}`, true},
		{http.StatusUnprocessableEntity, `{"error":{"code":"model_not_found"}}`, true},
		{http.StatusBadRequest, `{"error":{"message":"Invalid value: input_text"}}`, false},
		{http.StatusNotFound, `not found`, true},
	}
	for _, test := range tests {
		if got := retryableUpstream(test.status, []byte(test.body)); got != test.want {
			t.Fatalf("retryableUpstream(%d, %q) = %v, want %v", test.status, test.body, got, test.want)
		}
	}
}

func TestRelayFailsOverAfterUpstream413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "gateway-413-failover-test"); err != nil {
		t.Fatal(err)
	}
	tooSmall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`<html>413 Request Entity Too Large</html>`))
	}))
	defer tooSmall.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-ok", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer healthy.Close()

	db, err := gorm.Open(sqlite.Open("file:gateway-413-failover?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.APIKey{}, &model.UpstreamAccount{}, &model.Proxy{},
		&model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}, &model.UsageLog{}, &model.ModelPrice{}, &model.ModelConfig{}, &model.UserGroupRate{},
	); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "failover@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "failover", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-413-failover-test-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-413", Name: "failover", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []model.UpstreamAccount{
		{GroupID: group.ID, Name: "small-body", Platform: model.PlatformOpenAI, BaseURL: tooSmall.URL, AuthType: model.AuthAPIKey, APIKey: "sk-small", Priority: 100, Status: model.StatusActive},
		{GroupID: group.ID, Name: "healthy", Platform: model.PlatformOpenAI, BaseURL: healthy.URL, AuthType: model.AuthAPIKey, APIKey: "sk-healthy", Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}

	gateway := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	router := gin.New()
	router.Use(middleware.RequestID())
	gateway.Register(router)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+plain)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"ok"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if attempts := response.Header().Get("X-DengDeng-Upstream-Attempts"); attempts != "2" {
		t.Fatalf("attempt header = %q", attempts)
	}
	if timing := response.Header().Get("Server-Timing"); !strings.Contains(timing, "route;dur=") || !strings.Contains(timing, "upstream;dur=") {
		t.Fatalf("Server-Timing = %q", timing)
	}
	var usage model.UsageLog
	if err := db.Order("id DESC").First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if usage.AccountID != accounts[1].ID || usage.AttemptCount != 2 {
		t.Fatalf("usage account/attempts = %d/%d", usage.AccountID, usage.AttemptCount)
	}
}
