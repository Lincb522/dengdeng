package service

import (
	"errors"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

var ErrNoAccount = errors.New("no available upstream account")

// Scheduler picks upstream accounts for a group and tracks failures.
// Selection: active accounts not in cooldown, highest priority first,
// least-recently-used within the same priority (cheap round-robin).
type Scheduler struct {
	db     *gorm.DB
	policy *RuntimePolicyService
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{db: db}
}

// SetRuntimePolicy keeps the constructor compatible with embedders and tests
// while allowing a running server to apply operator-selected cooldowns.
func (s *Scheduler) SetRuntimePolicy(policy *RuntimePolicyService) {
	s.policy = policy
}

// Pick returns candidate accounts ordered by preference; callers walk the
// list for failover. exclude contains account IDs already tried this request.
func (s *Scheduler) Pick(groupID int64, exclude []int64) (*model.UpstreamAccount, error) {
	q := s.db.Preload("Proxy").Where("group_id = ? AND status = ?", groupID, model.StatusActive).
		Where("cooldown_until IS NULL OR cooldown_until < ?", time.Now())
	if len(exclude) > 0 {
		q = q.Where("id NOT IN ?", exclude)
	}
	var acc model.UpstreamAccount
	err := q.Order("priority DESC").
		Order("last_used_at ASC NULLS FIRST").
		First(&acc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoAccount
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s.db.Model(&model.UpstreamAccount{}).Where("id = ?", acc.ID).Update("last_used_at", now)
	return &acc, nil
}

// ReportFailure applies an escalating cooldown so a broken account stops
// receiving traffic without being permanently disabled.
func (s *Scheduler) ReportFailure(accountID int64, statusCode int, message string) {
	cooldown := DefaultGatewayRuntimePolicy().CooldownFor(statusCode)
	if s.policy != nil {
		cooldown = s.policy.Current().CooldownFor(statusCode)
	}
	until := time.Now().Add(cooldown)
	if len(message) > 1000 {
		message = message[:1000]
	}
	s.db.Model(&model.UpstreamAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"error_count":    gorm.Expr("error_count + 1"),
		"cooldown_until": until,
		"last_error":     message,
	})
}

// ReportSuccess clears failure state after a healthy response.
func (s *Scheduler) ReportSuccess(accountID int64) {
	s.db.Model(&model.UpstreamAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"error_count":    0,
		"cooldown_until": nil,
		"last_error":     "",
	})
}
