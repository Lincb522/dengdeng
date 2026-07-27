package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"dengdeng/internal/config"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
)

func newChannelStatusTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:channel-status-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Group{}, &model.UpstreamAccount{}, &model.UpstreamAccountGroup{}, &model.AccountProbe{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func requestChannelStatus(t *testing.T, handler *UserHandler, user *model.User, target string) (*httptest.ResponseRecorder, channelStatusResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	ctx.Set(middleware.CtxUser, user)
	handler.ChannelStatus(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data channelStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return recorder, response.Data
}

func TestChannelStatusFiltersPrivateGroupsAndAggregatesRealMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newChannelStatusTestDB(t)
	now := time.Now().UTC()

	publicGroup := model.Group{Name: "public-openai", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusActive, RateMultiplier: 1}
	privateGroup := model.Group{Name: "private-openai", Platform: model.PlatformOpenAI, IsPublic: false, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&publicGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&privateGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&privateGroup).Update("is_public", false).Error; err != nil {
		t.Fatal(err)
	}

	publicAccount := model.UpstreamAccount{GroupID: publicGroup.ID, Name: "public-upstream-secret", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	privateAccount := model.UpstreamAccount{GroupID: privateGroup.ID, Name: "private-upstream-secret", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&publicAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&privateAccount).Error; err != nil {
		t.Fatal(err)
	}
	links := []model.UpstreamAccountGroup{{UpstreamAccountID: publicAccount.ID, GroupID: publicGroup.ID}, {UpstreamAccountID: privateAccount.ID, GroupID: privateGroup.ID}}
	if err := db.Create(&links).Error; err != nil {
		t.Fatal(err)
	}
	probes := []model.AccountProbe{
		{AccountID: publicAccount.ID, Mode: "api", State: "healthy", StatusCode: 200, LatencyMs: 120, CheckedAt: now.Add(-2 * time.Minute)},
		{AccountID: publicAccount.ID, Mode: "api", State: "healthy", StatusCode: 200, LatencyMs: 180, CheckedAt: now.Add(-time.Minute)},
		{AccountID: privateAccount.ID, Mode: "api", State: "down", StatusCode: 503, LatencyMs: 400, ErrorMessage: "private failure detail", CheckedAt: now.Add(-time.Minute)},
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatal(err)
	}
	logs := []model.UsageLog{
		{GroupID: publicGroup.ID, AccountID: publicAccount.ID, Model: "gpt-test", FirstTokenMs: 100, DurationMs: 200, StatusCode: 200, CreatedAt: now.Add(-3 * time.Minute)},
		{GroupID: publicGroup.ID, AccountID: publicAccount.ID, Model: "gpt-test", FirstTokenMs: 300, DurationMs: 500, StatusCode: 500, CreatedAt: now.Add(-2 * time.Minute)},
		{GroupID: privateGroup.ID, AccountID: privateAccount.ID, Model: "private-model", FirstTokenMs: 900, DurationMs: 1000, StatusCode: 503, CreatedAt: now.Add(-time.Minute)},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewUserHandler(db, &config.Config{})
	user := &model.User{ID: 100, Email: "user@example.com", Role: model.RoleUser, Status: model.StatusActive}
	recorder, response := requestChannelStatus(t, handler, user, "/api/user/channel-status?range=1h")
	if response.AdminView || len(response.Groups) != 1 {
		t.Fatalf("unexpected public response: %#v", response)
	}
	group := response.Groups[0]
	if group.Name != publicGroup.Name || group.State != "healthy" || group.AccountTotal != 1 || group.AccountAvailable != 1 {
		t.Fatalf("unexpected group state: %#v", group)
	}
	if group.ProbeTotal != 2 || group.ProbeSuccesses != 2 || group.ProbeSuccessRate != 100 || group.AverageProbeLatencyMs != 150 {
		t.Fatalf("unexpected probe aggregate: %#v", group)
	}
	if group.RequestTotal != 2 || group.RequestSuccesses != 1 || group.RequestSuccessRate != 50 || group.AverageTTFTMs != 200 || group.TopModel != "gpt-test" {
		t.Fatalf("unexpected request aggregate: %#v", group)
	}
	if len(group.Timeline) != channelStatusBucketCount {
		t.Fatalf("timeline buckets = %d", len(group.Timeline))
	}
	body := recorder.Body.String()
	for _, secret := range []string{"private-openai", "private-upstream-secret", "private-model", "private failure detail", "public-upstream-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("privacy-safe response leaked %q: %s", secret, body)
		}
	}
}

func TestChannelStatusAdminSeesPrivateGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newChannelStatusTestDB(t)
	privateGroup := model.Group{Name: "admin-private-group", Platform: model.PlatformAnthropic, IsPublic: false, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&privateGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&privateGroup).Update("is_public", false).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewUserHandler(db, &config.Config{})
	admin := &model.User{ID: 1, Email: "admin@example.com", Role: model.RoleAdmin, Status: model.StatusActive}
	_, response := requestChannelStatus(t, handler, admin, "/api/user/channel-status?range=7d")
	if !response.AdminView || response.Range != "7d" || response.Hours != 168 {
		t.Fatalf("unexpected admin response: %#v", response)
	}
	found := false
	for _, group := range response.Groups {
		if group.ID == privateGroup.ID {
			found = true
			if group.IsPublic {
				t.Fatalf("private group marked public: %#v", group)
			}
		}
	}
	if !found {
		t.Fatalf("private group missing from admin response: %#v", response.Groups)
	}
}
