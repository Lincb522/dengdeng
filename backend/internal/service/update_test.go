package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dengdeng/internal/config"
)

func TestUpdateServiceQueuesFixedAction(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Update.Enabled = true
	cfg.Update.StateDirectory = dir
	service := NewUpdateService(cfg)
	triggered := false
	service.trigger = func(context.Context) error {
		triggered = true
		return nil
	}

	status, err := service.Request(context.Background(), "check", "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !triggered || status.Status != "queued" || status.Action != "check" {
		t.Fatalf("unexpected queued status: %#v triggered=%v", status, triggered)
	}
	data, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request updateRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Action != "check" || request.RequestedBy != "admin@example.com" || request.RequestedAt == "" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if _, err := service.Request(context.Background(), "apply", "admin@example.com"); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("expected ErrUpdateBusy, got %v", err)
	}
}

func TestUpdateServiceRejectsDisabledAndUnknownActions(t *testing.T) {
	cfg := config.Default()
	service := NewUpdateService(cfg)
	if _, err := service.Request(context.Background(), "check", "admin"); !errors.Is(err, ErrUpdateDisabled) {
		t.Fatalf("expected ErrUpdateDisabled, got %v", err)
	}
	cfg.Update.Enabled = true
	cfg.Update.StateDirectory = t.TempDir()
	service = NewUpdateService(cfg)
	if _, err := service.Request(context.Background(), "shell", "admin"); !errors.Is(err, ErrUpdateAction) {
		t.Fatalf("expected ErrUpdateAction, got %v", err)
	}
}

func TestUpdateStatusUsesConfiguredRepositoryAndPersistedRelease(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Update.Enabled = true
	cfg.Update.Repository = "https://example.test/trusted.git"
	cfg.Update.Branch = "stable"
	cfg.Update.StateDirectory = dir
	if err := writeUpdateJSON(filepath.Join(dir, "status.json"), UpdateStatus{
		Status: "succeeded", CurrentCommit: "abc", PreviousCommit: "def", UpdateAvailable: true,
		Changes: []UpdateChange{{Commit: "abc", Title: "新增自动备份", CommittedAt: "2026-07-18T10:00:00Z", Details: []string{"支持定时备份", "自动清理过期数据"}}},
	}); err != nil {
		t.Fatal(err)
	}
	status, err := NewUpdateService(cfg).Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Repository != cfg.Update.Repository || status.Branch != "stable" || !status.Enabled || !status.CanRollback {
		t.Fatalf("unexpected status: %#v", status)
	}
	if len(status.Changes) != 1 || status.Changes[0].Title != "新增自动备份" || len(status.Changes[0].Details) != 2 {
		t.Fatalf("unexpected changes: %#v", status.Changes)
	}
}

func TestUpdateStatusLoadsReleaseHistoryAndAddsCurrentRelease(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Update.Enabled = true
	cfg.Update.StateDirectory = dir
	service := NewUpdateService(cfg)

	older := UpdateRelease{
		Version: "v0.1.0", Commit: "older", Action: "apply", FinishedAt: "2026-07-20T10:00:00Z",
		Changes: []UpdateChange{{Commit: "older", Title: "旧版本"}},
	}
	if err := writeUpdateJSON(filepath.Join(dir, "history.json"), []UpdateRelease{older}); err != nil {
		t.Fatal(err)
	}
	current := UpdateStatus{
		Status: "succeeded", Action: "apply", CurrentVersion: "v0.2.0", CurrentCommit: "current",
		FinishedAt: "2026-07-25T10:00:00Z", Changes: []UpdateChange{{Commit: "current", Title: "当前版本"}},
	}
	if err := writeUpdateJSON(filepath.Join(dir, "status.json"), current); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.History) != 2 || status.History[0].Commit != "current" || status.History[1].Commit != "older" {
		t.Fatalf("unexpected release history: %#v", status.History)
	}
}

func TestUpdateStatusDoesNotRecordRepositoryCheckAsRelease(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Update.Enabled = true
	cfg.Update.StateDirectory = dir
	if err := writeUpdateJSON(filepath.Join(dir, "status.json"), UpdateStatus{
		Status: "succeeded", Action: "check", CurrentCommit: "current", FinishedAt: "2026-07-25T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	status, err := NewUpdateService(cfg).Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.History) != 0 {
		t.Fatalf("repository check should not be release history: %#v", status.History)
	}
}
