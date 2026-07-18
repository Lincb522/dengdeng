package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

var ErrBackupUnsupported = errors.New("database snapshots are currently supported for SQLite deployments only")

// BackupService creates consistent SQLite snapshots using VACUUM INTO rather
// than copying a live WAL database file. It deliberately never offers restore
// from the web: restoring is an operator-run maintenance action that requires
// stopping the service and verifying the selected snapshot.
type BackupService struct {
	db            *gorm.DB
	cfg           *config.Config
	mu            sync.Mutex
	schedulerOnce sync.Once
	wake          chan struct{}
}

func NewBackupService(db *gorm.DB, cfg *config.Config) *BackupService {
	return &BackupService{db: db, cfg: cfg, wake: make(chan struct{}, 1)}
}

const (
	backupPolicyKey   = "backup.policy.v1"
	automaticBackupBy = "system:auto"
)

// BackupPolicy is persisted in the database so the administration setting
// survives both service restarts and binary updates. Retention only applies to
// records created by the automatic scheduler; administrator snapshots are
// deliberately excluded.
type BackupPolicy struct {
	Enabled        bool `json:"enabled"`
	IntervalHours  int  `json:"interval_hours"`
	RetentionDays  int  `json:"retention_days"`
	RetentionCount int  `json:"retention_count"`
}

func (s *BackupService) defaultPolicy() BackupPolicy {
	policy := BackupPolicy{Enabled: true, IntervalHours: 24, RetentionDays: 30, RetentionCount: 30}
	if s != nil && s.cfg != nil {
		policy = BackupPolicy{
			Enabled:        s.cfg.Backup.AutoEnabled,
			IntervalHours:  s.cfg.Backup.IntervalHours,
			RetentionDays:  s.cfg.Backup.RetentionDays,
			RetentionCount: s.cfg.Backup.RetentionCount,
		}
	}
	return normalizeBackupPolicy(policy)
}

func normalizeBackupPolicy(policy BackupPolicy) BackupPolicy {
	if policy.IntervalHours < 1 {
		policy.IntervalHours = 1
	} else if policy.IntervalHours > 720 {
		policy.IntervalHours = 720
	}
	if policy.RetentionDays < 1 {
		policy.RetentionDays = 1
	} else if policy.RetentionDays > 3650 {
		policy.RetentionDays = 3650
	}
	if policy.RetentionCount < 1 {
		policy.RetentionCount = 1
	} else if policy.RetentionCount > 365 {
		policy.RetentionCount = 365
	}
	return policy
}

func (s *BackupService) GetPolicy() (BackupPolicy, error) {
	if s == nil || s.db == nil {
		return BackupPolicy{}, fmt.Errorf("backup store unavailable")
	}
	policy := s.defaultPolicy()
	var setting model.Setting
	err := s.db.First(&setting, "key = ?", backupPolicyKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return policy, nil
	}
	if err != nil {
		return BackupPolicy{}, err
	}
	if err := json.Unmarshal([]byte(setting.Value), &policy); err != nil {
		return BackupPolicy{}, fmt.Errorf("decode backup policy: %w", err)
	}
	return normalizeBackupPolicy(policy), nil
}

func (s *BackupService) UpdatePolicy(policy BackupPolicy) (BackupPolicy, error) {
	if s == nil || s.db == nil {
		return BackupPolicy{}, fmt.Errorf("backup store unavailable")
	}
	policy = normalizeBackupPolicy(policy)
	data, err := json.Marshal(policy)
	if err != nil {
		return BackupPolicy{}, err
	}
	setting := model.Setting{Key: backupPolicyKey}
	if err := s.db.Where("key = ?", backupPolicyKey).
		Assign(model.Setting{Value: string(data)}).
		FirstOrCreate(&setting).Error; err != nil {
		return BackupPolicy{}, err
	}
	s.WakeScheduler()
	return policy, nil
}

// StartScheduler runs a lightweight policy check once a minute. Backup work is
// still serialized by mu, so a scheduled snapshot can never overlap a manual
// snapshot or retention cleanup.
func (s *BackupService) StartScheduler() {
	if s == nil || s.cfg == nil || (s.cfg.Database.Driver != "" && s.cfg.Database.Driver != "sqlite") {
		return
	}
	s.schedulerOnce.Do(func() {
		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-timer.C:
				case <-s.wake:
				}
				if err := s.runScheduled(); err != nil {
					log.Printf("automatic database backup: %v", err)
				}
				timer.Reset(time.Minute)
			}
		}()
	})
}

func (s *BackupService) WakeScheduler() {
	if s == nil || s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *BackupService) runScheduled() error {
	policy, err := s.GetPolicy()
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return nil
	}
	var latest model.BackupRecord
	err = s.db.Where("created_by = ?", automaticBackupBy).Order("created_at DESC").First(&latest).Error
	shouldCreate := errors.Is(err, gorm.ErrRecordNotFound)
	if err == nil {
		age := time.Since(latest.CreatedAt)
		shouldCreate = latest.Status == "ready" && age >= time.Duration(policy.IntervalHours)*time.Hour
		// A transient disk or SQLite error should not suppress automatic
		// protection for the entire normal interval. Retry failed attempts after
		// a quiet period while still avoiding a tight failure loop.
		if latest.Status == "failed" && age >= 15*time.Minute {
			shouldCreate = true
		}
	}
	if shouldCreate {
		if _, createErr := s.Create(automaticBackupBy); createErr != nil {
			return createErr
		}
	} else if err != nil {
		return err
	}
	_, err = s.PruneAuto(policy)
	return err
}

func (s *BackupService) directory() (string, error) {
	if s == nil || s.cfg == nil {
		return "", fmt.Errorf("backup configuration unavailable")
	}
	dir := strings.TrimSpace(s.cfg.Backup.Directory)
	if dir == "" {
		if s.cfg.Database.Driver != "" && s.cfg.Database.Driver != "sqlite" {
			return "", ErrBackupUnsupported
		}
		path, err := filepath.Abs(s.cfg.Database.Path)
		if err != nil {
			return "", err
		}
		dir = filepath.Join(filepath.Dir(path), "backups")
	}
	return filepath.Abs(dir)
}

func (s *BackupService) List(limit int) ([]model.BackupRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("backup store unavailable")
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	var items []model.BackupRecord
	err := s.db.Order("id DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (s *BackupService) Create(createdBy string) (model.BackupRecord, error) {
	if s == nil || s.db == nil || s.cfg == nil {
		return model.BackupRecord{}, fmt.Errorf("backup service unavailable")
	}
	if s.cfg.Database.Driver != "" && s.cfg.Database.Driver != "sqlite" {
		return model.BackupRecord{}, ErrBackupUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.directory()
	if err != nil {
		return model.BackupRecord{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.BackupRecord{}, fmt.Errorf("create backup directory: %w", err)
	}
	filename := "dengdeng-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".sqlite"
	record := model.BackupRecord{Filename: filename, Status: "creating", CreatedBy: strings.TrimSpace(createdBy)}
	if err := s.db.Create(&record).Error; err != nil {
		return model.BackupRecord{}, err
	}
	path := filepath.Join(dir, filename)
	// VACUUM INTO does not accept the filename as a normal SELECT parameter.
	// It is generated by this method and additionally SQL-quoted here.
	sqlPath := strings.ReplaceAll(path, "'", "''")
	if err := s.db.Exec("VACUUM INTO '" + sqlPath + "'").Error; err != nil {
		_ = os.Remove(path)
		_ = s.db.Model(&record).Updates(map[string]any{"status": "failed", "error": backupError(err.Error())}).Error
		record.Status, record.Error = "failed", backupError(err.Error())
		return record, fmt.Errorf("create sqlite snapshot: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return record, fmt.Errorf("inspect sqlite snapshot: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return record, fmt.Errorf("protect sqlite snapshot: %w", err)
	}
	now := time.Now().UTC()
	if err := s.db.Model(&record).Updates(map[string]any{"status": "ready", "size_bytes": info.Size(), "completed_at": now, "error": ""}).Error; err != nil {
		return record, err
	}
	record.Status, record.SizeBytes, record.CompletedAt, record.Error = "ready", info.Size(), &now, ""
	return record, nil
}

func (s *BackupService) SnapshotPath(id int64) (model.BackupRecord, string, error) {
	if s == nil || s.db == nil || id <= 0 {
		return model.BackupRecord{}, "", fmt.Errorf("invalid backup")
	}
	var record model.BackupRecord
	if err := s.db.First(&record, id).Error; err != nil {
		return record, "", err
	}
	if record.Status != "ready" {
		return record, "", fmt.Errorf("backup is not ready")
	}
	dir, err := s.directory()
	if err != nil {
		return record, "", err
	}
	filename := filepath.Base(record.Filename)
	if filename != record.Filename || filename == "." {
		return record, "", fmt.Errorf("invalid backup record")
	}
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); err != nil {
		return record, "", err
	}
	return record, path, nil
}

func (s *BackupService) Delete(id int64) error {
	if s == nil || s.db == nil || id <= 0 {
		return fmt.Errorf("invalid backup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var record model.BackupRecord
	if err := s.db.First(&record, id).Error; err != nil {
		return err
	}
	return s.deleteRecord(record)
}

// PruneAuto removes expired or excess automatic snapshots and returns the
// number removed. Manual snapshots are never selected, even if they are older
// than the configured retention period.
func (s *BackupService) PruneAuto(policy BackupPolicy) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("backup store unavailable")
	}
	policy = normalizeBackupPolicy(policy)
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []model.BackupRecord
	if err := s.db.Where("created_by = ?", automaticBackupBy).Order("created_at DESC").Find(&records).Error; err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(policy.RetentionDays) * 24 * time.Hour)
	kept, deleted := 0, 0
	for _, record := range records {
		if record.Status == "creating" {
			kept++
			continue
		}
		if kept < policy.RetentionCount && !record.CreatedAt.Before(cutoff) {
			kept++
			continue
		}
		if err := s.deleteRecord(record); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (s *BackupService) deleteRecord(record model.BackupRecord) error {
	if record.Status == "ready" {
		dir, err := s.directory()
		if err != nil {
			return err
		}
		filename := filepath.Base(record.Filename)
		if filename != record.Filename || filename == "." {
			return fmt.Errorf("invalid backup record")
		}
		if err := os.Remove(filepath.Join(dir, filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.db.Delete(&record).Error
}

func backupError(value string) string {
	if len(value) > 480 {
		return value[:480]
	}
	return value
}
