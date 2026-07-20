package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dengdeng/internal/crypto"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

func TestRelayRecordsFirstTokenAndTotalDuration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "gateway-latency-metrics-test"); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		time.Sleep(25 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-latency\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":null}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		time.Sleep(35 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-latency\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()

	db := openMultiGroupGatewayDB(t, "gateway-latency-metrics")
	user := model.User{Email: "latency@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "latency", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-latency-test-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-latency", Name: "latency", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{
		GroupID: group.ID, Name: "latency-upstream", Platform: model.PlatformOpenAI, BaseURL: upstream.URL,
		AuthType: model.AuthAPIKey, APIKey: "sk-upstream", Status: model.StatusActive,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}

	gateway := New(db, service.NewScheduler(db), service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, service.NewRuntimeMetrics(), nil)
	router := gin.New()
	router.Use(middleware.RequestID())
	gateway.Register(router)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+plain)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var usage model.UsageLog
	if err := db.Order("id DESC").First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if !usage.Stream {
		t.Fatal("usage entry should be marked as streaming")
	}
	if usage.FirstTokenMs < 15 {
		t.Fatalf("first_token_ms=%d, want the delayed first chunk", usage.FirstTokenMs)
	}
	if usage.DurationMs < usage.FirstTokenMs+20 {
		t.Fatalf("duration_ms=%d first_token_ms=%d, total should include the remaining stream", usage.DurationMs, usage.FirstTokenMs)
	}
}
