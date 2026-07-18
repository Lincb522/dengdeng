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

func TestRelayFailsOverAcrossSelectedGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "gateway-multi-group-failover"); err != nil {
		t.Fatal(err)
	}
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-multi", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer healthy.Close()

	db := openMultiGroupGatewayDB(t, "gateway-multi-group-failover")
	user := model.User{Email: "multi@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	groups := []model.Group{
		{Name: "empty", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1},
		{Name: "healthy", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1},
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-multi-group-failover-key"
	key := model.APIKey{UserID: user.ID, GroupID: groups[0].ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-multi", Name: "multi", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.APIKeyGroup{{APIKeyID: key.ID, GroupID: groups[0].ID}, {APIKeyID: key.ID, GroupID: groups[1].ID}}).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: groups[1].ID, Name: "healthy", Platform: model.PlatformOpenAI, BaseURL: healthy.URL, AuthType: model.AuthAPIKey, APIKey: "sk-upstream", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}

	gw := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	router := gin.New()
	router.Use(middleware.RequestID())
	gw.Register(router)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+plain)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"ok"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var usage model.UsageLog
	if err := db.Order("id DESC").First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if usage.GroupID != groups[1].ID || usage.AccountID != account.ID {
		t.Fatalf("usage routed through group/account %d/%d", usage.GroupID, usage.AccountID)
	}
}

func TestSelectedGroupsExposeModelsAcrossPlatforms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openMultiGroupGatewayDB(t, "gateway-multi-group-models")
	user := model.User{Email: "models@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	groups := []model.Group{
		{Name: "openai", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "claude", Platform: model.PlatformAnthropic, Status: model.StatusActive},
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-multi-platform-model-key"
	key := model.APIKey{UserID: user.ID, GroupID: groups[0].ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-models", Name: "models", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.APIKeyGroup{{APIKeyID: key.ID, GroupID: groups[0].ID}, {APIKeyID: key.ID, GroupID: groups[1].ID}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.ModelConfig{
		{Name: "gpt-test", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "claude-test", Platform: model.PlatformAnthropic, Status: model.StatusActive},
		{Name: "gemini-hidden", Platform: model.PlatformGemini, Status: model.StatusActive},
	}).Error; err != nil {
		t.Fatal(err)
	}

	gw := &Gateway{db: db}
	ak := &authedKey{Group: groups[0], Groups: groups}
	if !gw.selectGroupForModel(ak, "claude-test", model.PlatformOpenAI, model.PlatformAnthropic) || ak.Group.ID != groups[1].ID {
		t.Fatalf("model-aware selection chose %#v", ak.Group)
	}

	router := gin.New()
	gw.Register(router)
	all := requestModels(t, router, plain, "/v1/models")
	if !strings.Contains(all, `"gpt-test"`) || !strings.Contains(all, `"claude-test"`) || strings.Contains(all, `"gemini-hidden"`) {
		t.Fatalf("unexpected combined models: %s", all)
	}
	claude := requestModels(t, router, plain, "/v1/models?platform=anthropic")
	if strings.Contains(claude, `"gpt-test"`) || !strings.Contains(claude, `"claude-test"`) {
		t.Fatalf("unexpected filtered models: %s", claude)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/models?platform=gemini", nil)
	request.Header.Set("Authorization", "Bearer "+plain)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized platform status=%d body=%s", response.Code, response.Body.String())
	}
}

func openMultiGroupGatewayDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.APIKey{}, &model.APIKeyGroup{}, &model.UpstreamAccount{}, &model.Proxy{},
		&model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}, &model.UsageLog{}, &model.ModelPrice{}, &model.ModelConfig{}, &model.UserGroupRate{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func requestModels(t *testing.T, router http.Handler, key, path string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("models status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Body.String()
}
