package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBackupServiceCreatesConsistentSQLiteSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dengdeng.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.BackupRecord{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Email: "backup@example.com", PasswordHash: "hash", Status: model.StatusActive, RateMultiplier: 1}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Database: config.DatabaseConfig{Driver: "sqlite", Path: dbPath}}
	svc := NewBackupService(db, cfg)
	record, err := svc.Create("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "ready" || record.SizeBytes <= 0 {
		t.Fatalf("unexpected record: %#v", record)
	}
	_, path, err := svc.SnapshotPath(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := snapshot.Model(&model.User{}).Where("email = ?", "backup@example.com").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("snapshot count=%d err=%v", count, err)
	}
	if err := svc.Delete(record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBackupPolicyPersistsAndNormalizes(t *testing.T) {
	svc := newBackupTestService(t)
	policy, err := svc.GetPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Enabled || policy.IntervalHours != 24 || policy.RetentionDays != 30 || policy.RetentionCount != 30 {
		t.Fatalf("unexpected default policy: %#v", policy)
	}
	updated, err := svc.UpdatePolicy(BackupPolicy{Enabled: false, IntervalHours: 0, RetentionDays: 9999, RetentionCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.IntervalHours != 1 || updated.RetentionDays != 3650 || updated.RetentionCount != 1 {
		t.Fatalf("unexpected normalized policy: %#v", updated)
	}
	loaded, err := svc.GetPolicy()
	if err != nil || loaded != updated {
		t.Fatalf("loaded policy=%#v err=%v", loaded, err)
	}
}

func TestBackupPruneOnlyDeletesAutomaticSnapshots(t *testing.T) {
	svc := newBackupTestService(t)
	manual, err := svc.Create("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	autoOld, err := svc.Create(automaticBackupBy)
	if err != nil {
		t.Fatal(err)
	}
	autoMiddle, err := svc.Create(automaticBackupBy)
	if err != nil {
		t.Fatal(err)
	}
	autoNewest, err := svc.Create(automaticBackupBy)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.db.Model(&model.BackupRecord{}).Where("id = ?", autoOld.ID).Update("created_at", now.Add(-72*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Model(&model.BackupRecord{}).Where("id = ?", autoMiddle.ID).Update("created_at", now.Add(-48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	_, oldPath, err := svc.SnapshotPath(autoOld.ID)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.PruneAuto(BackupPolicy{Enabled: true, IntervalHours: 24, RetentionDays: 365, RetentionCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted=%d, want 2", deleted)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old automatic snapshot still exists: %v", err)
	}
	for _, id := range []int64{manual.ID, autoNewest.ID} {
		if _, _, err := svc.SnapshotPath(id); err != nil {
			t.Fatalf("retained snapshot %d unavailable: %v", id, err)
		}
	}
	var count int64
	if err := svc.db.Model(&model.BackupRecord{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("remaining records=%d err=%v", count, err)
	}
}

func TestBackupSchedulerCreatesFirstSnapshotWithoutDuplicatingIt(t *testing.T) {
	svc := newBackupTestService(t)
	if err := svc.runScheduled(); err != nil {
		t.Fatal(err)
	}
	if err := svc.runScheduled(); err != nil {
		t.Fatal(err)
	}
	var records []model.BackupRecord
	if err := svc.db.Where("created_by = ?", automaticBackupBy).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != "ready" {
		t.Fatalf("unexpected automatic backups: %#v", records)
	}
}

func newBackupTestService(t *testing.T) *BackupService {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dengdeng.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.BackupRecord{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Database = config.DatabaseConfig{Driver: "sqlite", Path: dbPath}
	return NewBackupService(db, cfg)
}
