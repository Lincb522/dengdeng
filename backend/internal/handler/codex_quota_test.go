package handler

import (
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestProjectCodexQuotaSnapshotPreservesProviderWindows(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	payload := codexQuotaUsagePayload{
		PlanType: "plus",
		RateLimit: &codexQuotaRateLimitPayload{
			Allowed:      true,
			LimitReached: false,
			PrimaryWindow: &codexQuotaWindowPayload{
				UsedPercent: 42.5, LimitWindowSeconds: 5 * 60 * 60, ResetAfterSeconds: 90 * 60,
			},
			SecondaryWindow: &codexQuotaWindowPayload{
				UsedPercent: 12, LimitWindowSeconds: 7 * 24 * 60 * 60, ResetAt: now.Add(24 * time.Hour).Unix(),
			},
		},
	}

	snapshot := projectCodexQuotaSnapshot(17, nil, payload, now)
	if snapshot.UpstreamAccountID != 17 || snapshot.PlanType != "plus" {
		t.Fatalf("identity = %#v", snapshot)
	}
	if !snapshot.HasPrimaryWindow || snapshot.PrimaryUsedPercent != 42.5 || snapshot.PrimaryResetAt == nil {
		t.Fatalf("primary window = %#v", snapshot)
	}
	if !snapshot.HasSecondaryWindow || snapshot.SecondaryUsedPercent != 12 || snapshot.SecondaryResetAt == nil {
		t.Fatalf("secondary window = %#v", snapshot)
	}
	if !snapshot.SecondaryResetAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("secondary reset = %v", snapshot.SecondaryResetAt)
	}
}

func TestProjectCodexQuotaSnapshotUsesImportedPlanFallback(t *testing.T) {
	snapshot := projectCodexQuotaSnapshot(3, map[string]any{"plan_type": "pro"}, codexQuotaUsagePayload{}, time.Now())
	if snapshot.PlanType != "pro" || !snapshot.Allowed || snapshot.HasPrimaryWindow {
		t.Fatalf("fallback snapshot = %#v", snapshot)
	}
}

func TestCodexChatGPTAccountIDFallsBackToImportedMetadata(t *testing.T) {
	extra, err := model.EncodeExtra(map[string]any{"chatgpt_account_id": "chatgpt-id"})
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{Extra: extra}
	if got := codexChatGPTAccountID(account); got != "chatgpt-id" {
		t.Fatalf("account id = %q", got)
	}
	account.AccountID = "stored-id"
	if got := codexChatGPTAccountID(account); got != "stored-id" {
		t.Fatalf("stored account id = %q", got)
	}
}

func TestUpsertCodexQuotaSnapshotReplacesExistingSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CodexQuotaSnapshot{}); err != nil {
		t.Fatal(err)
	}
	first := model.CodexQuotaSnapshot{UpstreamAccountID: 8, PlanType: "plus", Allowed: true, FetchedAt: time.Now().UTC()}
	if err := upsertCodexQuotaSnapshot(db, &first); err != nil {
		t.Fatal(err)
	}
	second := model.CodexQuotaSnapshot{UpstreamAccountID: 8, PlanType: "pro", Allowed: false, LimitReached: true, FetchedAt: time.Now().UTC()}
	if err := upsertCodexQuotaSnapshot(db, &second); err != nil {
		t.Fatal(err)
	}
	var stored model.CodexQuotaSnapshot
	if err := db.First(&stored, "upstream_account_id = ?", 8).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PlanType != "pro" || stored.Allowed || !stored.LimitReached {
		t.Fatalf("stored snapshot = %#v", stored)
	}
	var count int64
	db.Model(&model.CodexQuotaSnapshot{}).Count(&count)
	if count != 1 {
		t.Fatalf("snapshot count = %d, want 1", count)
	}
}
