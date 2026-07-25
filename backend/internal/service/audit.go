package service

import (
	"strings"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// AuditService keeps a compact, credential-free trail for operator actions.
// It is intentionally best effort at call sites: a completed security change
// must not be rolled back because a secondary history insert has failed.
type AuditService struct{ db *gorm.DB }

func NewAuditService(db *gorm.DB) *AuditService { return &AuditService{db: db} }

func (s *AuditService) Record(actor *model.User, action, targetType, targetID, detail, sourceIP string) error {
	if s == nil || s.db == nil {
		return nil
	}
	entry := model.AuditLog{
		Action: clipAudit(action, 96), TargetType: clipAudit(targetType, 64), TargetID: clipAudit(targetID, 128),
		Detail: clipAudit(detail, 2048), SourceIP: clipAudit(sourceIP, 64),
	}
	if actor != nil {
		entry.ActorUserID, entry.ActorEmail = actor.ID, clipAudit(actor.Email, 255)
	}
	return s.db.Create(&entry).Error
}

func (s *AuditService) Cleanup(retentionDays int) (int64, error) {
	if s == nil || s.db == nil || retentionDays <= 0 {
		return 0, nil
	}
	result := s.db.Where("created_at < ?", time.Now().UTC().AddDate(0, 0, -retentionDays)).Delete(&model.AuditLog{})
	return result.RowsAffected, result.Error
}

func (s *AuditService) StartRetention(settings *SystemSettingsService) {
	if s == nil || settings == nil {
		return
	}
	run := func() {
		current, err := settings.Get()
		if err == nil {
			_, _ = s.Cleanup(current.Security.AuditLogRetentionDays)
		}
	}
	go func() {
		run()
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()
}

func clipAudit(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}
