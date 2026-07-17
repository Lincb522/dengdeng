package service

import (
	"path/filepath"
	"testing"

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
	if err := db.AutoMigrate(&model.User{}, &model.BackupRecord{}); err != nil {
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
