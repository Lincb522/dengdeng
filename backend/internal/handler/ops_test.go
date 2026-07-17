package handler

import (
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBuildOpsSnapshotAggregatesLedgerAndHealth(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Group{}, &model.APIKey{}, &model.UpstreamAccount{}, &model.AccountProbe{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "ops@example.com", Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "openai-pool", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, Name: "primary", KeyHash: "ops-key", KeyPreview: "dd-ops"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	cooldown := time.Now().UTC().Add(10 * time.Minute)
	ready := model.UpstreamAccount{GroupID: group.ID, Name: "ready-account", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	cooling := model.UpstreamAccount{GroupID: group.ID, Name: "cooling-account", Platform: model.PlatformOpenAI, Status: model.StatusActive, CooldownUntil: &cooldown, ErrorCount: 2, LastError: "rate limited"}
	if err := db.Create(&ready).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&cooling).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.AccountProbe{AccountID: ready.ID, Mode: "api", State: "healthy", StatusCode: 200, LatencyMs: 42, CheckedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	logs := []model.UsageLog{
		{UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AccountID: ready.ID, Model: "gpt-test", InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20, CacheWriteTokens: 10, CacheWrite5mTokens: 7, CacheWrite1hTokens: 3, CostMicro: 1000, DurationMs: 120, StatusCode: 200, CreatedAt: now.Add(-3 * time.Minute)},
		{UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AccountID: cooling.ID, Model: "gpt-test", InputTokens: 20, OutputTokens: 10, CostMicro: 200, DurationMs: 400, StatusCode: 429, ErrorMessage: "upstream limited", CreatedAt: now.Add(-2 * time.Minute)},
		{UserID: user.ID, APIKeyID: key.ID, GroupID: group.ID, AccountID: cooling.ID, Model: "gpt-second", InputTokens: 5, OutputTokens: 5, CostMicro: 50, DurationMs: 900, StatusCode: 503, ErrorMessage: "unavailable", CreatedAt: now.Add(-time.Minute)},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	h := NewAdminHandler(db, nil, nil, nil)
	snapshot, err := h.buildOpsSnapshot(opsFilter{Range: "1h", Start: now.Add(-time.Hour), End: now.Add(time.Minute), Platform: model.PlatformOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Overview.Requests != 3 || snapshot.Overview.SuccessRequests != 1 || snapshot.Overview.ErrorRequests != 2 {
		t.Fatalf("unexpected aggregate: %#v", snapshot.Overview)
	}
	if snapshot.Overview.InputTokens != 125 || snapshot.Overview.OutputTokens != 65 || snapshot.Overview.CacheReadTokens != 20 || snapshot.Overview.CacheWriteTokens != 10 || snapshot.Overview.CacheWrite5mTokens != 7 || snapshot.Overview.CacheWrite1hTokens != 3 {
		t.Fatalf("unexpected token aggregate: %#v", snapshot.Overview)
	}
	if snapshot.Overview.AccountTotal != 2 || snapshot.Overview.AccountAvailable != 1 || snapshot.Overview.AccountCooling != 1 {
		t.Fatalf("unexpected account health: %#v", snapshot.Overview)
	}
	if len(snapshot.RecentErrors) != 2 || snapshot.RecentErrors[0].AccountName != "cooling-account" {
		t.Fatalf("unexpected decorated errors: %#v", snapshot.RecentErrors)
	}
	if len(snapshot.TopModels) != 2 || snapshot.TopModels[0].Name != "gpt-test" {
		t.Fatalf("unexpected model ranks: %#v", snapshot.TopModels)
	}
	if len(snapshot.ModelUsage) != 2 || snapshot.ModelUsage[0].Name != "gpt-test" || snapshot.ModelUsage[0].CacheWrite5mTokens != 7 || snapshot.ModelUsage[0].CacheWrite1hTokens != 3 {
		t.Fatalf("unexpected detailed model usage: %#v", snapshot.ModelUsage)
	}
	if len(snapshot.RateProfiles) != 1 || snapshot.RateProfiles[0].Name != "openai-pool" {
		t.Fatalf("unexpected rate profiles: %#v", snapshot.RateProfiles)
	}
	if len(snapshot.TopGroups) != 1 || snapshot.TopGroups[0].Name != "openai-pool" || snapshot.TopGroups[0].Requests != 3 {
		t.Fatalf("unexpected group ranks: %#v", snapshot.TopGroups)
	}
	if len(snapshot.TopUsers) != 1 || snapshot.TopUsers[0].Name != "ops@example.com" || len(snapshot.TopAccounts) != 2 {
		t.Fatalf("unexpected user/account ranks: users=%#v accounts=%#v", snapshot.TopUsers, snapshot.TopAccounts)
	}
	var trendRequests int64
	for _, bucket := range snapshot.Trend {
		trendRequests += bucket.Requests
	}
	if trendRequests != 3 {
		t.Fatalf("trend requests = %d, want 3", trendRequests)
	}
}
