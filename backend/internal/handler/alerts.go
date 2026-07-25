package handler

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AlertHandler struct {
	db    *gorm.DB
	audit *service.AuditService
}

func NewAlertHandler(db *gorm.DB, audit *service.AuditService) *AlertHandler {
	return &AlertHandler{db: db, audit: audit}
}

type alertRuleRequest struct {
	Name             string  `json:"name"`
	Enabled          bool    `json:"enabled"`
	Condition        string  `json:"condition"`
	Platform         string  `json:"platform"`
	GroupID          int64   `json:"group_id"`
	AccountID        int64   `json:"account_id"`
	NotifyEmail      string  `json:"notify_email"`
	MetricType       string  `json:"metric_type"`
	Operator         string  `json:"operator"`
	Threshold        float64 `json:"threshold"`
	WindowMinutes    int     `json:"window_minutes"`
	SustainedMinutes int     `json:"sustained_minutes"`
	CooldownMinutes  int     `json:"cooldown_minutes"`
	Severity         string  `json:"severity"`
	Description      string  `json:"description"`
}

func normalizeAlertRule(req alertRuleRequest) (model.AlertRule, error) {
	rule := model.AlertRule{
		Name: strings.TrimSpace(req.Name), Enabled: req.Enabled, Condition: strings.TrimSpace(req.Condition),
		Platform: strings.TrimSpace(req.Platform), GroupID: req.GroupID, AccountID: req.AccountID, NotifyEmail: strings.TrimSpace(req.NotifyEmail),
		MetricType: strings.TrimSpace(req.MetricType), Operator: strings.TrimSpace(req.Operator), Threshold: req.Threshold,
		WindowMinutes: req.WindowMinutes, SustainedMinutes: req.SustainedMinutes, CooldownMinutes: req.CooldownMinutes,
		Severity: strings.TrimSpace(req.Severity), Description: strings.TrimSpace(req.Description),
	}
	if rule.Name == "" || len(rule.Name) > 120 {
		return rule, gin.Error{Err: http.ErrNotSupported, Type: gin.ErrorTypePublic}.Err
	}
	if rule.MetricType == "" && rule.Condition != "down" && rule.Condition != "degraded_or_down" && rule.Condition != "not_healthy" {
		return rule, errText("condition must be down, degraded_or_down or not_healthy")
	}
	validMetrics := map[string]bool{"success_rate": true, "error_rate": true, "upstream_error_rate": true, "cpu_percent": true, "memory_percent": true, "queue_depth": true, "qps": true, "tps": true, "available_account_ratio": true, "available_account_count": true, "rate_limited_account_count": true, "error_account_count": true}
	if rule.MetricType != "" && !validMetrics[rule.MetricType] {
		return rule, errText("invalid metric_type")
	}
	if rule.MetricType != "" {
		if rule.Operator == "" {
			rule.Operator = "gte"
		}
		if rule.Operator != "gt" && rule.Operator != "gte" && rule.Operator != "lt" && rule.Operator != "lte" {
			return rule, errText("invalid operator")
		}
		if rule.WindowMinutes <= 0 {
			rule.WindowMinutes = 5
		}
		if rule.SustainedMinutes <= 0 {
			rule.SustainedMinutes = 1
		}
		if rule.CooldownMinutes <= 0 {
			rule.CooldownMinutes = 10
		}
		if rule.WindowMinutes > 1440 || rule.SustainedMinutes > 1440 || rule.CooldownMinutes > 10080 {
			return rule, errText("alert timing is too large")
		}
		if rule.Severity == "" {
			rule.Severity = "warning"
		}
		if rule.Severity != "info" && rule.Severity != "warning" && rule.Severity != "critical" {
			return rule, errText("invalid severity")
		}
	}
	if rule.Platform != "" && !validPlatform(rule.Platform) {
		return rule, errText("invalid platform")
	}
	if rule.GroupID < 0 || rule.AccountID < 0 {
		return rule, errText("group_id and account_id cannot be negative")
	}
	if rule.NotifyEmail != "" {
		parsed, err := mail.ParseAddress(rule.NotifyEmail)
		if err != nil || !strings.EqualFold(parsed.Address, rule.NotifyEmail) {
			return rule, errText("invalid notification email")
		}
	}
	return rule, nil
}

type errText string

func (e errText) Error() string { return string(e) }

func (h *AlertHandler) ListRules(c *gin.Context) {
	var items []model.AlertRule
	if err := h.db.Order("id DESC").Find(&items).Error; err != nil {
		util.Fail(c, 500, "load alert rules failed")
		return
	}
	util.OK(c, items)
}

func (h *AlertHandler) CreateRule(c *gin.Context) {
	var req alertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, 400, "invalid alert rule")
		return
	}
	rule, err := normalizeAlertRule(req)
	if err != nil {
		util.Fail(c, 400, err.Error())
		return
	}
	if err := h.db.Create(&rule).Error; err != nil {
		util.Fail(c, 500, "create alert rule failed")
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "alert_rule.created", "alert_rule", strconv.FormatInt(rule.ID, 10), rule.Name, c.ClientIP())
	util.OK(c, rule)
}

func (h *AlertHandler) UpdateRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid alert rule id")
		return
	}
	var existing model.AlertRule
	if err := h.db.First(&existing, id).Error; err != nil {
		util.Fail(c, 404, "alert rule not found")
		return
	}
	var req alertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, 400, "invalid alert rule")
		return
	}
	next, err := normalizeAlertRule(req)
	if err != nil {
		util.Fail(c, 400, err.Error())
		return
	}
	if err := h.db.Model(&existing).Updates(map[string]any{"name": next.Name, "enabled": next.Enabled, "condition": next.Condition, "platform": next.Platform, "group_id": next.GroupID, "account_id": next.AccountID, "notify_email": next.NotifyEmail, "metric_type": next.MetricType, "operator": next.Operator, "threshold": next.Threshold, "window_minutes": next.WindowMinutes, "sustained_minutes": next.SustainedMinutes, "cooldown_minutes": next.CooldownMinutes, "severity": next.Severity, "description": next.Description}).Error; err != nil {
		util.Fail(c, 500, "update alert rule failed")
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "alert_rule.updated", "alert_rule", strconv.FormatInt(id, 10), next.Name, c.ClientIP())
	h.db.First(&existing, id)
	util.OK(c, existing)
}

func (h *AlertHandler) DeleteRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid alert rule id")
		return
	}
	res := h.db.Delete(&model.AlertRule{}, id)
	if res.Error != nil {
		util.Fail(c, 500, "delete alert rule failed")
		return
	}
	if res.RowsAffected == 0 {
		util.Fail(c, 404, "alert rule not found")
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "alert_rule.deleted", "alert_rule", strconv.FormatInt(id, 10), "", c.ClientIP())
	util.OK(c, gin.H{"deleted": true})
}

type alertEventView struct {
	model.AlertEvent
	RuleName    string `json:"rule_name"`
	AccountName string `json:"account_name"`
}

func (h *AlertHandler) ListEvents(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 {
		limit = 1
	}
	if limit > 300 {
		limit = 300
	}
	state := strings.TrimSpace(c.Query("state"))
	if state != "" && state != "open" && state != "resolved" {
		util.Fail(c, 400, "state must be open or resolved")
		return
	}
	q := h.db.Order("id DESC").Limit(limit)
	if state != "" {
		q = q.Where("state = ?", state)
	}
	var events []model.AlertEvent
	if err := q.Find(&events).Error; err != nil {
		util.Fail(c, 500, "load alert events failed")
		return
	}
	util.OK(c, gin.H{"items": h.decorateEvents(events), "limit": limit})
}

func (h *AlertHandler) AcknowledgeEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid alert event id")
		return
	}
	actor := middleware.CurrentUser(c)
	now := time.Now().UTC()
	res := h.db.Model(&model.AlertEvent{}).Where("id = ?", id).Updates(map[string]any{"acknowledged_at": now, "acknowledged_by": actor.Email})
	if res.Error != nil {
		util.Fail(c, 500, "acknowledge alert failed")
		return
	}
	if res.RowsAffected == 0 {
		util.Fail(c, 404, "alert event not found")
		return
	}
	_ = h.audit.Record(actor, "alert_event.acknowledged", "alert_event", strconv.FormatInt(id, 10), "", c.ClientIP())
	var event model.AlertEvent
	h.db.First(&event, id)
	util.OK(c, h.decorateEvents([]model.AlertEvent{event})[0])
}

func (h *AlertHandler) ListSilences(c *gin.Context) {
	var items []model.OpsAlertSilence
	if err := h.db.Where("deleted_at IS NULL").Order("ends_at DESC").Limit(300).Find(&items).Error; err != nil {
		util.Fail(c, 500, "load alert silences failed")
		return
	}
	util.OK(c, items)
}

func (h *AlertHandler) CreateSilence(c *gin.Context) {
	var req struct {
		RuleID   int64      `json:"rule_id"`
		Reason   string     `json:"reason"`
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   time.Time  `json:"ends_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RuleID < 0 {
		util.Fail(c, 400, "invalid alert silence")
		return
	}
	now := time.Now().UTC()
	starts := now
	if req.StartsAt != nil {
		starts = req.StartsAt.UTC()
	}
	ends := req.EndsAt.UTC()
	if !ends.After(starts) || ends.Sub(starts) > 31*24*time.Hour {
		util.Fail(c, 400, "silence end must be within 31 days after start")
		return
	}
	actor := middleware.CurrentUser(c)
	item := model.OpsAlertSilence{RuleID: req.RuleID, Reason: strings.TrimSpace(req.Reason), CreatedBy: actor.Email, StartsAt: starts, EndsAt: ends, CreatedAt: now}
	if err := h.db.Create(&item).Error; err != nil {
		util.Fail(c, 500, "create alert silence failed")
		return
	}
	_ = h.audit.Record(actor, "alert_silence.created", "alert_silence", strconv.FormatInt(item.ID, 10), item.Reason, c.ClientIP())
	util.OK(c, item)
}

func (h *AlertHandler) DeleteSilence(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid alert silence id")
		return
	}
	now := time.Now().UTC()
	result := h.db.Model(&model.OpsAlertSilence{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		util.Fail(c, 500, "delete alert silence failed")
		return
	}
	if result.RowsAffected == 0 {
		util.Fail(c, 404, "alert silence not found")
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "alert_silence.deleted", "alert_silence", strconv.FormatInt(id, 10), "", c.ClientIP())
	util.OK(c, gin.H{"deleted": true})
}

type channelProbeView struct {
	model.AccountProbe
	AccountName string `json:"account_name"`
	GroupName   string `json:"group_name"`
	Platform    string `json:"platform"`
}

// ChannelHistory is the non-billable probe ledger. Unlike the dashboard's
// latest-status card it intentionally exposes every saved check so operators
// can distinguish a transient network dip from a persistent failure.
func (h *AlertHandler) ChannelHistory(c *gin.Context) {
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	if hours < 1 {
		hours = 1
	}
	if hours > 24*31 {
		hours = 24 * 31
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	q := h.db.Where("checked_at >= ?", time.Now().UTC().Add(-time.Duration(hours)*time.Hour)).Order("checked_at DESC").Limit(limit)
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			util.Fail(c, 400, "invalid account_id")
			return
		}
		q = q.Where("account_id = ?", id)
	}
	var probes []model.AccountProbe
	if err := q.Find(&probes).Error; err != nil {
		util.Fail(c, 500, "load channel history failed")
		return
	}
	accounts := map[int64]model.UpstreamAccount{}
	if len(probes) > 0 {
		ids := make([]int64, 0, len(probes))
		for _, probe := range probes {
			ids = append(ids, probe.AccountID)
		}
		var rows []model.UpstreamAccount
		h.db.Where("id IN ?", ids).Find(&rows)
		for _, row := range rows {
			accounts[row.ID] = row
		}
	}
	groupIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		groupIDs = append(groupIDs, account.GroupID)
	}
	groups := map[int64]model.Group{}
	if len(groupIDs) > 0 {
		var rows []model.Group
		h.db.Where("id IN ?", groupIDs).Find(&rows)
		for _, row := range rows {
			groups[row.ID] = row
		}
	}
	items := make([]channelProbeView, 0, len(probes))
	for _, probe := range probes {
		account := accounts[probe.AccountID]
		items = append(items, channelProbeView{AccountProbe: probe, AccountName: account.Name, GroupName: groups[account.GroupID].Name, Platform: account.Platform})
	}
	util.OK(c, gin.H{"items": items, "hours": hours, "limit": limit})
}

func (h *AlertHandler) decorateEvents(events []model.AlertEvent) []alertEventView {
	ruleIDs, accountIDs := make([]int64, 0, len(events)), make([]int64, 0, len(events))
	for _, event := range events {
		ruleIDs = append(ruleIDs, event.RuleID)
		accountIDs = append(accountIDs, event.AccountID)
	}
	rules := map[int64]model.AlertRule{}
	accounts := map[int64]model.UpstreamAccount{}
	if len(ruleIDs) > 0 {
		var rows []model.AlertRule
		h.db.Where("id IN ?", ruleIDs).Find(&rows)
		for _, row := range rows {
			rules[row.ID] = row
		}
	}
	if len(accountIDs) > 0 {
		var rows []model.UpstreamAccount
		h.db.Where("id IN ?", accountIDs).Find(&rows)
		for _, row := range rows {
			accounts[row.ID] = row
		}
	}
	result := make([]alertEventView, 0, len(events))
	for _, event := range events {
		result = append(result, alertEventView{AlertEvent: event, RuleName: rules[event.RuleID].Name, AccountName: accounts[event.AccountID].Name})
	}
	return result
}
