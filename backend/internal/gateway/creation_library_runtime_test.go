package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

func TestSelectedSkillPackageReachesUpstreamRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "creation-library-runtime-test"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	captured := make([][]byte, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		captured = append(captured, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-skill-runtime", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer upstream.Close()

	db := openMultiGroupGatewayDB(t, "creation-library-runtime")
	if err := db.AutoMigrate(&model.Setting{}, &model.UserCreationSelection{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "skill-runtime@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "skill-runtime", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-synthetic-skill-runtime-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-synthetic", Name: "runtime", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: group.ID, Name: "capture", Platform: model.PlatformOpenAI, BaseURL: upstream.URL, AuthType: model.AuthAPIKey, APIKey: "synthetic-upstream-key", Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}

	settings := service.NewSystemSettingsService(db, config.Default())
	if _, err := settings.UpdateUserCreationSelection(user.ID, service.UserCreationSelection{SkillIDs: []string{"backend-engineer"}}); err != nil {
		t.Fatal(err)
	}
	gw := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	gw.SetSystemSettings(settings)
	router := gin.New()
	router.Use(middleware.RequestID())
	gw.Register(router)

	call := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
		request.Header.Set("Authorization", "Bearer "+plain)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return response
	}

	selected := call()
	if got := selected.Header().Get("X-DengDeng-Applied-Skills"); got != "backend-engineer" {
		t.Fatalf("applied skill header=%q", got)
	}
	if got := selected.Header().Get("X-DengDeng-Skill-Runs"); !strings.HasPrefix(got, "backend-engineer@") || !strings.HasSuffix(got, "/package") {
		t.Fatalf("skill run header=%q", got)
	}
	if _, err := settings.UpdateUserCreationSelection(user.ID, service.UserCreationSelection{}); err != nil {
		t.Fatal(err)
	}
	unselected := call()
	if got := unselected.Header().Get("X-DengDeng-Skill-Runs"); got != "" {
		t.Fatalf("unselected request skill run header=%q", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("captured requests=%d", len(captured))
	}
	selectedBody := string(captured[0])
	unselectedBody := string(captured[1])
	for _, marker := range []string{"# 后端工程", "references/service-contracts.md", "## 数据与事务"} {
		if !strings.Contains(selectedBody, marker) {
			t.Fatalf("selected request is missing complete skill package marker %q", marker)
		}
		if strings.Contains(unselectedBody, marker) {
			t.Fatalf("unselected request contains skill package marker %q", marker)
		}
	}
}
