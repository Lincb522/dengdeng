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
	if err := s.db.Preload("Groups").First(&account, probe.AccountID).Error; err != nil {
		return
	}
	var rules []model.AlertRule
	if err := s.db.Where("enabled = ?", true).Find(&rules).Error; err != nil {
		return
	}
	for _, rule := range rules {
		if rule.MetricType != "" {
			continue
		}
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

// EvaluateMetricSnapshot evaluates generic operational rules after each
// durable minute snapshot. Probe rules continue through EvaluateProbe.
func (s *AlertService) EvaluateMetricSnapshot(snapshot model.OpsSystemMetric) {
	if s == nil || s.db == nil {
		return
	}
	var rules []model.AlertRule
	if err := s.db.Where("enabled = ? AND metric_type <> ''", true).Find(&rules).Error; err != nil {
		return
	}
	for _, rule := range rules {
		if s.metricRuleSilenced(rule.ID) {
			continue
		}
		value, ok := s.metricRuleValue(rule, snapshot)
		if !ok {
			continue
		}
		firing := compareMetric(value, rule.Operator, rule.Threshold)
		if firing {
			s.openOrRefreshMetric(rule, value, snapshot.BucketAt)
		} else {
			s.resolve(rule.ID, 0, snapshot.BucketAt)
		}
	}
}

func (s *AlertService) metricRuleSilenced(ruleID int64) bool {
	now := time.Now().UTC()
	var count int64
	_ = s.db.Model(&model.OpsAlertSilence{}).
		Where("deleted_at IS NULL AND starts_at <= ? AND ends_at > ? AND (rule_id = 0 OR rule_id = ?)", now, now, ruleID).
		Count(&count).Error
	return count > 0
}

func (s *AlertService) metricRuleValue(rule model.AlertRule, snapshot model.OpsSystemMetric) (float64, bool) {
	switch rule.MetricType {
	case "cpu_percent":
		return snapshot.CPUPercent, true
	case "memory_percent":
		return snapshot.MemoryPercent, true
	case "queue_depth":
		return float64(snapshot.QueueDepth), true
	case "qps":
		return snapshot.QPS, true
	case "tps":
		return snapshot.TPS, true
	case "success_rate", "error_rate", "upstream_error_rate":
		window := rule.WindowMinutes
		if window < 1 {
			window = 1
		}
		q := s.db.Model(&model.UsageLog{}).Where("usage_logs.created_at >= ?", time.Now().UTC().Add(-time.Duration(window)*time.Minute))
		if rule.GroupID > 0 {
			q = q.Where("usage_logs.group_id = ?", rule.GroupID)
		}
		if rule.Platform != "" {
			q = q.Joins("JOIN groups alert_groups ON alert_groups.id = usage_logs.group_id").Where("alert_groups.platform = ?", rule.Platform)
		}
		var row struct{ Total, Success, Upstream int64 }
		if err := q.Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN usage_logs.status_code >= 200 AND usage_logs.status_code < 400 THEN 1 ELSE 0 END),0) AS success, COALESCE(SUM(CASE WHEN usage_logs.status_code >= 500 THEN 1 ELSE 0 END),0) AS upstream").Scan(&row).Error; err != nil || row.Total == 0 {
			return 0, false
		}
		successRate := float64(row.Success) / float64(row.Total) * 100
		if rule.MetricType == "success_rate" {
			return successRate, true
		}
		if rule.MetricType == "upstream_error_rate" {
			return float64(row.Upstream) / float64(row.Total) * 100, true
		}
		return 100 - successRate, true
	case "available_account_ratio", "available_account_count", "rate_limited_account_count", "error_account_count":
		q := s.db.Model(&model.UpstreamAccount{})
		if rule.GroupID > 0 {
			q = q.Where("EXISTS (SELECT 1 FROM upstream_account_groups account_membership WHERE account_membership.upstream_account_id = upstream_accounts.id AND account_membership.group_id = ?) OR upstream_accounts.group_id = ?", rule.GroupID, rule.GroupID)
		}
		if rule.Platform != "" {
			q = q.Where("platform = ?", rule.Platform)
		}
		var accounts []model.UpstreamAccount
		if err := q.Find(&accounts).Error; err != nil || len(accounts) == 0 {
			return 0, false
		}
		now, available, limited, failed := time.Now().UTC(), 0, 0, 0
		for _, account := range accounts {
			cooling := account.CooldownUntil != nil && account.CooldownUntil.After(now)
			if account.Status == model.StatusActive && !cooling {
				available++
			}
			if cooling {
				limited++
			}
			if account.Status == model.StatusError || account.ErrorCount > 0 {
				failed++
			}
		}
		switch rule.MetricType {
		case "available_account_ratio":
			return float64(available) / float64(len(accounts)) * 100, true
		case "available_account_count":
			return float64(available), true
		case "rate_limited_account_count":
			return float64(limited), true
		default:
			return float64(failed), true
		}
	}
	return 0, false
}

func compareMetric(value float64, operator string, threshold float64) bool {
	switch strings.ToLower(strings.TrimSpace(operator)) {
	case "lt", "<":
		return value < threshold
	case "lte", "<=":
		return value <= threshold
	case "gt", ">":
		return value > threshold
	default:
		return value >= threshold
	}
}

func (s *AlertService) openOrRefreshMetric(rule model.AlertRule, value float64, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var event model.AlertEvent
	err := s.db.Where("rule_id = ? AND account_id = 0 AND state = ?", rule.ID, "open").Order("id DESC").First(&event).Error
	message := fmt.Sprintf("%s 当前值 %.2f，阈值 %s %.2f", rule.MetricType, value, rule.Operator, rule.Threshold)
	if err == nil {
		_ = s.db.Model(&event).Updates(map[string]any{"last_seen_at": at, "message": message, "metric_value": value, "threshold_value": rule.Threshold}).Error
		return
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return
	}
	if rule.LastTriggeredAt != nil && time.Since(*rule.LastTriggeredAt) < time.Duration(max(rule.CooldownMinutes, 1))*time.Minute {
		return
	}
	event = model.AlertEvent{RuleID: rule.ID, GroupID: rule.GroupID, Platform: rule.Platform, State: "open", Severity: rule.Severity, Title: rule.Name, Message: message, MetricValue: value, ThresholdValue: rule.Threshold, FirstSeenAt: at, LastSeenAt: at, DeliveryStatus: "console"}
	if event.Severity == "" {
		event.Severity = "warning"
	}
	if err := s.db.Create(&event).Error; err != nil {
		return
	}
	_ = s.db.Model(&model.AlertRule{}).Where("id = ?", rule.ID).Update("last_triggered_at", at).Error
	to := strings.TrimSpace(rule.NotifyEmail)
	if to == "" {
		to = s.fallbackEmail
	}
	if to != "" && s.mailer != nil && s.mailer.Configured() {
		go s.deliver(event.ID, to, event.Title, event.Message)
	}
}

func ruleApplies(rule model.AlertRule, account model.UpstreamAccount) bool {
	return (rule.AccountID == 0 || rule.AccountID == account.ID) &&
		(rule.GroupID == 0 || upstreamAccountBelongsToGroup(account, rule.GroupID)) &&
		(rule.Platform == "" || rule.Platform == account.Platform)
}

func upstreamAccountBelongsToGroup(account model.UpstreamAccount, groupID int64) bool {
	if account.GroupID == groupID {
		return true
	}
	for _, group := range account.Groups {
		if group.ID == groupID {
			return true
		}
	}
	return false
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
	eventGroupID := account.GroupID
	if rule.GroupID > 0 {
		eventGroupID = rule.GroupID
	}
	event = model.AlertEvent{
		RuleID: rule.ID, AccountID: account.ID, GroupID: eventGroupID, Platform: account.Platform, State: "open",
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
