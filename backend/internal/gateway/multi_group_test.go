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
	if err := crypto.Init("", "gateway-multi-group-models"); err != nil {
		t.Fatal(err)
	}
	openAIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer openai-upstream-key" {
			http.Error(w, "unexpected OpenAI model request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-upstream","created":123}]}`))
	}))
	defer openAIUpstream.Close()
	anthropicUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "anthropic-upstream-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			http.Error(w, "unexpected Anthropic model request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-upstream"}]}`))
	}))
	defer anthropicUpstream.Close()

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
		{Name: "gpt-local-only", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "claude-local-only", Platform: model.PlatformAnthropic, Status: model.StatusActive},
		{Name: "gemini-hidden", Platform: model.PlatformGemini, Status: model.StatusActive},
	}).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []model.UpstreamAccount{
		{GroupID: groups[0].ID, Name: "OpenAI upstream", Platform: model.PlatformOpenAI, BaseURL: openAIUpstream.URL, AuthType: model.AuthAPIKey, APIKey: "openai-upstream-key", Status: model.StatusActive},
		{GroupID: groups[1].ID, Name: "Anthropic upstream", Platform: model.PlatformAnthropic, BaseURL: anthropicUpstream.URL, AuthType: model.AuthAPIKey, APIKey: "anthropic-upstream-key", Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.UpstreamAccountGroup{
		{UpstreamAccountID: accounts[0].ID, GroupID: groups[0].ID},
		{UpstreamAccountID: accounts[1].ID, GroupID: groups[1].ID},
	}).Error; err != nil {
		t.Fatal(err)
	}

	gw := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	ak := &authedKey{Group: groups[0], Groups: groups}
	if !gw.selectGroupForModel(ak, "claude-local-only", model.PlatformOpenAI, model.PlatformAnthropic) || ak.Group.ID != groups[1].ID {
		t.Fatalf("model-aware selection chose %#v", ak.Group)
	}

	router := gin.New()
	gw.Register(router)
	all := requestModels(t, router, plain, "/v1/models")
	if !strings.Contains(all, `"gpt-upstream"`) || !strings.Contains(all, `"claude-upstream"`) || strings.Contains(all, `"local-only"`) || strings.Contains(all, `"gemini-hidden"`) {
		t.Fatalf("unexpected combined models: %s", all)
	}
	claude := requestModels(t, router, plain, "/v1/models?platform=anthropic")
	if strings.Contains(claude, `"gpt-upstream"`) || !strings.Contains(claude, `"claude-upstream"`) || strings.Contains(claude, `"claude-local-only"`) {
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

func TestAPIKeyModelDiscoveryDoesNotFallBackToLocalCatalogue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "gateway-model-discovery-no-fallback"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	db := openMultiGroupGatewayDB(t, "gateway-model-discovery-no-fallback")
	user := model.User{Email: "models-failure@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "openai", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-model-discovery-no-fallback"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-model", Name: "models", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: group.ID, Name: "unavailable", Platform: model.PlatformOpenAI, BaseURL: upstream.URL, AuthType: model.AuthAPIKey, APIKey: "upstream-key", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UpstreamAccountGroup{UpstreamAccountID: account.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ModelConfig{Name: "local-model-must-not-leak", Platform: model.PlatformOpenAI, Status: model.StatusActive}).Error; err != nil {
		t.Fatal(err)
	}

	gw := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	router := gin.New()
	gw.Register(router)
	request := httptest.NewRequest(http.MethodGet, "/v1/models?platform=openai", nil)
	request.Header.Set("Authorization", "Bearer "+plain)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "local-model-must-not-leak") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestParseGeminiUpstreamModels(t *testing.T) {
	items, err := parseUpstreamModels([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`), model.PlatformGemini)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "gemini-2.5-pro" || items[1].ID != "gemini-2.5-flash" {
		t.Fatalf("unexpected Gemini models: %#v", items)
	}
}

func openMultiGroupGatewayDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.APIKey{}, &model.APIKeyGroup{}, &model.UpstreamAccount{}, &model.UpstreamAccountGroup{}, &model.Proxy{},
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
