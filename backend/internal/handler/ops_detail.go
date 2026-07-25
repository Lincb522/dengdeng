package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

type opsErrorView struct {
	model.OpsErrorLog
	UserEmail   string `json:"user_email,omitempty"`
	KeyName     string `json:"key_name,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
	AccountName string `json:"account_name,omitempty"`
}

func (h *AdminHandler) OpsErrors(c *gin.Context) {
	page, err := parsePositiveInt(c.DefaultQuery("page", "1"), "page", 1, 1_000_000)
	if err != nil {
		util.Fail(c, 400, err.Error())
		return
	}
	size, err := parsePositiveInt(c.DefaultQuery("size", "30"), "size", 1, 100)
	if err != nil {
		util.Fail(c, 400, err.Error())
		return
	}
	q := h.db.Model(&model.OpsErrorLog{})
	for name, column := range map[string]string{"platform": "platform", "model": "model", "error_type": "error_type", "error_phase": "error_phase", "request_id": "request_id", "client_ip": "client_ip"} {
		if value := strings.TrimSpace(c.Query(name)); value != "" {
			if name == "model" {
				q = q.Where(column+" LIKE ?", "%"+value+"%")
			} else {
				q = q.Where(column+" = ?", value)
			}
		}
	}
	if status := strings.TrimSpace(c.Query("status")); status == "open" {
		q = q.Where("resolved_at IS NULL")
	} else if status == "resolved" {
		q = q.Where("resolved_at IS NOT NULL")
	}
	for name, column := range map[string]string{"user_id": "user_id", "api_key_id": "api_key_id", "group_id": "group_id", "account_id": "account_id", "status_code": "status_code"} {
		if raw := strings.TrimSpace(c.Query(name)); raw != "" {
			value, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				util.Fail(c, 400, fmt.Sprintf("invalid %s", name))
				return
			}
			q = q.Where(column+" = ?", value)
		}
	}
	if raw := strings.TrimSpace(c.Query("start")); raw != "" {
		value, ok, parseErr := parseUsageTime(raw, false)
		if parseErr != nil || !ok {
			util.Fail(c, 400, "invalid start")
			return
		}
		q = q.Where("created_at >= ?", *value)
	}
	if raw := strings.TrimSpace(c.Query("end")); raw != "" {
		value, ok, parseErr := parseUsageTime(raw, true)
		if parseErr != nil || !ok {
			util.Fail(c, 400, "invalid end")
			return
		}
		q = q.Where("created_at < ?", *value)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		util.Fail(c, 500, "count ops errors failed")
		return
	}
	var rows []model.OpsErrorLog
	if err := q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		util.Fail(c, 500, "load ops errors failed")
		return
	}
	util.OK(c, gin.H{"items": h.decorateOpsErrors(rows), "total": total, "page": page, "size": size})
}

func (h *AdminHandler) decorateOpsErrors(rows []model.OpsErrorLog) []opsErrorView {
	result := make([]opsErrorView, len(rows))
	users, keys, groups, accounts := map[int64]string{}, map[int64]string{}, map[int64]string{}, map[int64]string{}
	var userRows []model.User
	h.db.Select("id", "email").Find(&userRows)
	for _, row := range userRows {
		users[row.ID] = row.Email
	}
	var keyRows []model.APIKey
	h.db.Unscoped().Select("id", "name").Find(&keyRows)
	for _, row := range keyRows {
		keys[row.ID] = row.Name
	}
	var groupRows []model.Group
	h.db.Select("id", "name").Find(&groupRows)
	for _, row := range groupRows {
		groups[row.ID] = row.Name
	}
	var accountRows []model.UpstreamAccount
	h.db.Select("id", "name").Find(&accountRows)
	for _, row := range accountRows {
		accounts[row.ID] = row.Name
	}
	for i, row := range rows {
		result[i] = opsErrorView{OpsErrorLog: row, UserEmail: users[row.UserID], KeyName: keys[row.APIKeyID], GroupName: groups[row.GroupID], AccountName: accounts[row.AccountID]}
	}
	return result
}

func (h *AdminHandler) ResolveOpsError(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid error id")
		return
	}
	actor := middleware.CurrentUser(c)
	now := time.Now().UTC()
	result := h.db.Model(&model.OpsErrorLog{}).Where("id = ?", id).Updates(map[string]any{"resolved_at": now, "resolved_by": actor.Email})
	if result.Error != nil {
		util.Fail(c, 500, "resolve ops error failed")
		return
	}
	if result.RowsAffected == 0 {
		util.Fail(c, 404, "ops error not found")
		return
	}
	util.OK(c, gin.H{"resolved": true, "resolved_at": now})
}

func (h *AdminHandler) OpsSystemHistory(c *gin.Context) {
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	if hours < 1 {
		hours = 1
	}
	if hours > 24*35 {
		hours = 24 * 35
	}
	var items []model.OpsSystemMetric
	if err := h.db.Where("bucket_at >= ?", time.Now().UTC().Add(-time.Duration(hours)*time.Hour)).Order("bucket_at ASC").Limit(10080).Find(&items).Error; err != nil {
		util.Fail(c, 500, "load system metrics failed")
		return
	}
	util.OK(c, gin.H{"items": items, "hours": hours})
}

func (h *AdminHandler) OpsJobHeartbeats(c *gin.Context) {
	var items []model.OpsJobHeartbeat
	if err := h.db.Order("job_name ASC").Find(&items).Error; err != nil {
		util.Fail(c, 500, "load job heartbeats failed")
		return
	}
	util.OK(c, items)
}

func (h *AdminHandler) OpsSystemLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	q := h.db.Model(&model.OpsSystemLog{})
	if v := strings.TrimSpace(c.Query("level")); v != "" {
		q = q.Where("level = ?", v)
	}
	if v := strings.TrimSpace(c.Query("component")); v != "" {
		q = q.Where("component = ?", v)
	}
	if v := strings.TrimSpace(c.Query("keyword")); v != "" {
		q = q.Where("message LIKE ?", "%"+v+"%")
	}
	var items []model.OpsSystemLog
	if err := q.Order("created_at DESC").Limit(limit).Find(&items).Error; err != nil {
		util.Fail(c, 500, "load system logs failed")
		return
	}
	util.OK(c, gin.H{"items": items, "limit": limit})
}

func (h *AdminHandler) ClearOpsSystemLogs(c *gin.Context) {
	q := h.db.Model(&model.OpsSystemLog{})
	if raw := strings.TrimSpace(c.Query("before")); raw != "" {
		value, ok, err := parseUsageTime(raw, false)
		if err != nil || !ok {
			util.Fail(c, 400, "invalid before")
			return
		}
		q = q.Where("created_at < ?", *value)
	}
	result := q.Delete(&model.OpsSystemLog{})
	if result.Error != nil {
		util.Fail(c, 500, "clear system logs failed")
		return
	}
	util.OK(c, gin.H{"deleted": result.RowsAffected})
}
