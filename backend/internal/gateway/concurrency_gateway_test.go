package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dengdeng/internal/crypto"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRelayWaitsForUpstreamAccountConcurrencySlot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "gateway-concurrency-test"); err != nil {
		t.Fatal(err)
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-concurrency", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer upstream.Close()

	db, err := gorm.Open(sqlite.Open("file:gateway-concurrency?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.APIKey{}, &model.UpstreamAccount{}, &model.Proxy{},
		&model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}, &model.UsageLog{}, &model.ModelPrice{}, &model.ModelConfig{}, &model.UserGroupRate{},
	); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "concurrency@example.test", PasswordHash: "x", Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "one-slot", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	plain := "dd-concurrency-test-key"
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: util.HashAPIKey(plain), KeyPreview: "dd-concurrency", Name: "concurrency", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{
		GroupID: group.ID, Name: "single-slot", Platform: model.PlatformOpenAI, BaseURL: upstream.URL,
		AuthType: model.AuthAPIKey, APIKey: "sk-upstream", Priority: 10, Concurrency: 1, Status: model.StatusActive,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}

	scheduler := service.NewScheduler(db)
	runtime := service.NewRuntimeMetrics()
	gw := New(db, scheduler, service.NewBillingService(db, service.NewPricingService(db)), service.NewUserGroupRateResolver(db), nil, runtime, nil)
	router := gin.New()
	router.Use(middleware.RequestID())
	gw.Register(router)

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+plain)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	var first, second *httptest.ResponseRecorder
	var requests sync.WaitGroup
	requests.Add(2)
	go func() { defer requests.Done(); first = request() }()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach upstream")
	}
	go func() { defer requests.Done(); second = request() }()

	deadline := time.Now().Add(time.Second)
	for scheduler.WaitingCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if scheduler.WaitingCount() != 1 || runtime.Snapshot("", 0).Waiting != 1 {
		t.Fatalf("second request did not enter the account queue: scheduler=%d runtime=%d", scheduler.WaitingCount(), runtime.Snapshot("", 0).Waiting)
	}
	close(releaseFirst)
	requests.Wait()

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d/%d, bodies = %s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if timing := second.Header().Get("Server-Timing"); !strings.Contains(timing, "queue;dur=") || strings.Contains(timing, "queue;dur=0,") {
		t.Fatalf("second request timing does not show queueing: %q", timing)
	}
	if snapshot := runtime.Snapshot("", 0); snapshot.InFlight != 0 || snapshot.Waiting != 0 {
		t.Fatalf("runtime counts leaked after completion: %+v", snapshot)
	}
}
