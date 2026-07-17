package service

import (
	"fmt"
	"strings"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

type OperationalAlertMailer interface {
	Configured() bool
	SendOperationalAlert(to, title, summary string) error
}

type AlertService struct {
	db            *gorm.DB
	mailer        OperationalAlertMailer
	fallbackEmail string
}

func NewAlertService(db *gorm.DB, mailer OperationalAlertMailer, fallbackEmail string) *AlertService {
	s := &AlertService{db: db, mailer: mailer, fallbackEmail: strings.TrimSpace(fallbackEmail)}
	s.ensureDefaultRule()
	return s
}

func (s *AlertService) ensureDefaultRule() {
	if s == nil || s.db == nil {
		return
	}
	var count int64
	if s.db.Model(&model.AlertRule{}).Where("name = ?", "上游账号不可用").Count(&count).Error != nil || count > 0 {
		return
	}
	_ = s.db.Create(&model.AlertRule{Name: "上游账号不可用", Enabled: true, Condition: "down"}).Error
}

// EvaluateProbe opens one incident per matching rule/account, refreshes an
// existing incident while it persists, and resolves it when later probes are
// healthy. It does not alter traffic scheduling; Scheduler remains the sole
// authority for cooldowns and routing.
func (s *AlertService) EvaluateProbe(probe model.AccountProbe) {
	if s == nil || s.db == nil || probe.AccountID == 0 {
		return
	}
	var account model.UpstreamAccount
	if err := s.db.First(&account, probe.AccountID).Error; err != nil {
		return
	}
	var rules []model.AlertRule
	if err := s.db.Where("enabled = ?", true).Find(&rules).Error; err != nil {
		return
	}
	for _, rule := range rules {
		if !ruleApplies(rule, account) {
			continue
		}
		if ruleMatchesProbe(rule, probe) {
			s.openOrRefresh(rule, account, probe)
		} else {
			s.resolve(rule.ID, account.ID, probe.CheckedAt)
		}
	}
}

func ruleApplies(rule model.AlertRule, account model.UpstreamAccount) bool {
	return (rule.AccountID == 0 || rule.AccountID == account.ID) &&
		(rule.GroupID == 0 || rule.GroupID == account.GroupID) &&
		(rule.Platform == "" || rule.Platform == account.Platform)
}

func ruleMatchesProbe(rule model.AlertRule, probe model.AccountProbe) bool {
	switch rule.Condition {
	case "down":
		return probe.State == "down"
	case "degraded_or_down":
		return probe.State == "degraded" || probe.State == "down"
	case "not_healthy":
		return probe.State != "healthy"
	default:
		return false
	}
}

func (s *AlertService) openOrRefresh(rule model.AlertRule, account model.UpstreamAccount, probe model.AccountProbe) {
	now := probe.CheckedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var event model.AlertEvent
	err := s.db.Where("rule_id = ? AND account_id = ? AND state = ?", rule.ID, account.ID, "open").Order("id DESC").First(&event).Error
	if err == nil {
		_ = s.db.Model(&event).Updates(map[string]any{"last_seen_at": now, "message": alertProbeMessage(probe), "severity": alertSeverity(probe)}).Error
		return
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return
	}
	event = model.AlertEvent{
		RuleID: rule.ID, AccountID: account.ID, GroupID: account.GroupID, Platform: account.Platform, State: "open",
		Severity: alertSeverity(probe), Title: fmt.Sprintf("%s：%s", rule.Name, account.Name), Message: alertProbeMessage(probe),
		FirstSeenAt: now, LastSeenAt: now, DeliveryStatus: "console",
	}
	if err := s.db.Create(&event).Error; err != nil {
		return
	}
	to := strings.TrimSpace(rule.NotifyEmail)
	if to == "" {
		to = s.fallbackEmail
	}
	if to != "" && s.mailer != nil && s.mailer.Configured() {
		go s.deliver(event.ID, to, event.Title, event.Message)
	}
}

func (s *AlertService) resolve(ruleID, accountID int64, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_ = s.db.Model(&model.AlertEvent{}).Where("rule_id = ? AND account_id = ? AND state = ?", ruleID, accountID, "open").Updates(map[string]any{"state": "resolved", "resolved_at": at, "last_seen_at": at}).Error
}

func (s *AlertService) deliver(eventID int64, to, title, message string) {
	err := s.mailer.SendOperationalAlert(to, title, message)
	updates := map[string]any{"delivery_status": "sent", "delivery_error": ""}
	if err != nil {
		updates["delivery_status"], updates["delivery_error"] = "failed", safeAlertError(err.Error())
	}
	_ = s.db.Model(&model.AlertEvent{}).Where("id = ?", eventID).Updates(updates).Error
}

func alertSeverity(probe model.AccountProbe) string {
	if probe.State == "down" || probe.State == "expired" {
		return "critical"
	}
	return "warning"
}

func alertProbeMessage(probe model.AccountProbe) string {
	message := strings.TrimSpace(probe.ErrorMessage)
	if message == "" {
		message = "account health probe returned " + probe.State
	}
	if probe.StatusCode > 0 {
		message += fmt.Sprintf(" (HTTP %d)", probe.StatusCode)
	}
	if len(message) > 900 {
		message = message[:900]
	}
	return message
}

func safeAlertError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 480 {
		return value[:480]
	}
	return value
}
