package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type siteErrorHandler struct {
	db *gorm.DB
}

type clientErrorReport struct {
	Source    string `json:"source"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message" binding:"required"`
	Stack     string `json:"stack"`
	Path      string `json:"path"`
	RequestID string `json:"request_id"`
}

type resolveErrorsBatchRequest struct {
	IDs []int64 `json:"ids" binding:"required"`
}

func NewSiteErrorHandler(db *gorm.DB) *siteErrorHandler {
	return &siteErrorHandler{db: db}
}

func (h *siteErrorHandler) Report(c *gin.Context) {
	var req clientErrorReport
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailCode(c, http.StatusBadRequest, "request.invalid", "invalid client error report")
		return
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source != "vue" && source != "promise" && source != "window" && source != "network" {
		source = "browser"
	}
	requestID := strings.TrimSpace(req.RequestID)
	if !strings.HasPrefix(requestID, "ddr_") || len(requestID) > 32 {
		requestID = middleware.RequestIDFromContext(c)
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/"
	}
	item := model.OpsSystemLog{
		Level:     "error",
		Category:  "frontend",
		Component: "frontend." + source,
		ErrorCode: trimErrorCenterText(req.ErrorCode, 96),
		Message:   trimErrorCenterText(req.Message, 2048),
		Details:   trimErrorCenterText(req.Stack, 12000),
		Method:    "CLIENT",
		Path:      trimErrorCenterText(path, 512),
		RequestID: requestID,
		ClientIP:  trimErrorCenterText(c.ClientIP(), 64),
		UserAgent: trimErrorCenterText(c.Request.UserAgent(), 512),
		CreatedAt: time.Now().UTC(),
	}
	if err := h.db.Create(&item).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "record client error failed")
		return
	}
	util.OK(c, gin.H{"recorded": true})
}

type errorCenterCategory struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type errorCenterScope struct {
	Total           int64                 `json:"total"`
	Open            int64                 `json:"open"`
	Resolved        int64                 `json:"resolved"`
	Critical        int64                 `json:"critical"`
	LastHour        int64                 `json:"last_hour"`
	Retryable       int64                 `json:"retryable,omitempty"`
	BusinessLimited int64                 `json:"business_limited,omitempty"`
	Categories      []errorCenterCategory `json:"categories"`
}

func (h *AdminHandler) ErrorCenterSummary(c *gin.Context) {
	filter, err := parseOpsFilter(c)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	siteQuery := h.db.Model(&model.OpsSystemLog{}).Where("created_at >= ? AND created_at < ?", filter.Start, filter.End)
	apiQuery := h.db.Model(&model.OpsErrorLog{}).Where("created_at >= ? AND created_at < ?", filter.Start, filter.End)
	if filter.Platform != "" {
		apiQuery = apiQuery.Where("platform = ?", filter.Platform)
	}
	if filter.GroupID > 0 {
		apiQuery = apiQuery.Where("group_id = ?", filter.GroupID)
	}
	site, err := buildSiteErrorSummary(siteQuery, filter.End.Add(-time.Hour))
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load site error summary failed")
		return
	}
	apiScope, err := buildAPIErrorSummary(apiQuery, filter.End.Add(-time.Hour))
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load API error summary failed")
		return
	}
	util.OK(c, gin.H{
		"generated_at": time.Now().UTC(),
		"range":        filter.Range,
		"start":        filter.Start,
		"end":          filter.End,
		"site":         site,
		"api":          apiScope,
	})
}

func buildSiteErrorSummary(query *gorm.DB, lastHour time.Time) (errorCenterScope, error) {
	result := errorCenterScope{Categories: []errorCenterCategory{}}
	for destination, condition := range map[*int64]string{
		&result.Total:    "1 = 1",
		&result.Open:     "resolved_at IS NULL",
		&result.Resolved: "resolved_at IS NOT NULL",
		&result.Critical: "(level = 'error' OR status_code >= 500)",
		&result.LastHour: "created_at >= ?",
	} {
		var err error
		scoped := query.Session(&gorm.Session{})
		if condition == "created_at >= ?" {
			err = scoped.Where(condition, lastHour).Count(destination).Error
		} else {
			err = scoped.Where(condition).Count(destination).Error
		}
		if err != nil {
			return result, err
		}
	}
	if err := query.Session(&gorm.Session{}).Select("CASE WHEN category = '' THEN component ELSE category END AS name, COUNT(*) AS count").
		Group("CASE WHEN category = '' THEN component ELSE category END").Order("count DESC").Limit(8).
		Scan(&result.Categories).Error; err != nil {
		return result, err
	}
	return result, nil
}

func buildAPIErrorSummary(query *gorm.DB, lastHour time.Time) (errorCenterScope, error) {
	result := errorCenterScope{Categories: []errorCenterCategory{}}
	for destination, condition := range map[*int64]string{
		&result.Total:           "1 = 1",
		&result.Open:            "resolved_at IS NULL",
		&result.Resolved:        "resolved_at IS NOT NULL",
		&result.Critical:        "severity = 'P1'",
		&result.LastHour:        "created_at >= ?",
		&result.Retryable:       "retryable = ?",
		&result.BusinessLimited: "business_limited = ?",
	} {
		var err error
		scoped := query.Session(&gorm.Session{})
		switch condition {
		case "created_at >= ?":
			err = scoped.Where(condition, lastHour).Count(destination).Error
		case "retryable = ?", "business_limited = ?":
			err = scoped.Where(condition, true).Count(destination).Error
		default:
			err = scoped.Where(condition).Count(destination).Error
		}
		if err != nil {
			return result, err
		}
	}
	if err := query.Session(&gorm.Session{}).Select("CASE WHEN error_type = '' THEN error_source ELSE error_type END AS name, COUNT(*) AS count").
		Group("CASE WHEN error_type = '' THEN error_source ELSE error_type END").Order("count DESC").Limit(8).
		Scan(&result.Categories).Error; err != nil {
		return result, err
	}
	return result, nil
}

type siteErrorView struct {
	model.OpsSystemLog
	UserEmail string `json:"user_email,omitempty"`
}

func (h *AdminHandler) SiteErrors(c *gin.Context) {
	page, err := parsePositiveInt(c.DefaultQuery("page", "1"), "page", 1, 1_000_000)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	size, err := parsePositiveInt(c.DefaultQuery("size", "30"), "size", 1, 100)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	query := h.db.Model(&model.OpsSystemLog{})
	if value := strings.TrimSpace(c.Query("category")); value != "" {
		query = query.Where("category = ? OR component = ?", value, value)
	}
	if value := strings.TrimSpace(c.Query("level")); value != "" {
		query = query.Where("level = ?", value)
	}
	if value := strings.TrimSpace(c.Query("request_id")); value != "" {
		query = query.Where("request_id = ?", value)
	}
	if value := strings.TrimSpace(c.Query("keyword")); value != "" {
		pattern := "%" + value + "%"
		query = query.Where("message LIKE ? OR error_code LIKE ? OR path LIKE ? OR request_id LIKE ?", pattern, pattern, pattern, pattern)
	}
	if status := strings.TrimSpace(c.Query("status")); status == "open" {
		query = query.Where("resolved_at IS NULL")
	} else if status == "resolved" {
		query = query.Where("resolved_at IS NOT NULL")
	}
	if raw := strings.TrimSpace(c.Query("status_code")); raw != "" {
		code, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			util.Fail(c, http.StatusBadRequest, "invalid status_code")
			return
		}
		query = query.Where("status_code = ?", code)
	}
	for parameter, inclusive := range map[string]bool{"start": true, "end": false} {
		if raw := strings.TrimSpace(c.Query(parameter)); raw != "" {
			value, ok, parseErr := parseUsageTime(raw, parameter == "end")
			if parseErr != nil || !ok {
				util.Fail(c, http.StatusBadRequest, "invalid "+parameter)
				return
			}
			operator := ">="
			if !inclusive {
				operator = "<"
			}
			query = query.Where("created_at "+operator+" ?", *value)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "count site errors failed")
		return
	}
	var rows []model.OpsSystemLog
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load site errors failed")
		return
	}
	users := map[int64]string{}
	var userRows []model.User
	h.db.Select("id", "email").Find(&userRows)
	for _, row := range userRows {
		users[row.ID] = row.Email
	}
	items := make([]siteErrorView, len(rows))
	for index, row := range rows {
		items[index] = siteErrorView{OpsSystemLog: row, UserEmail: users[row.UserID]}
	}
	util.OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

func (h *AdminHandler) ResolveSiteError(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid site error id")
		return
	}
	actor := middleware.CurrentUser(c)
	now := time.Now().UTC()
	result := h.db.Model(&model.OpsSystemLog{}).Where("id = ?", id).
		Updates(map[string]any{"resolved_at": now, "resolved_by": actor.Email})
	if result.Error != nil {
		util.Fail(c, http.StatusInternalServerError, "resolve site error failed")
		return
	}
	if result.RowsAffected == 0 {
		util.Fail(c, http.StatusNotFound, "site error not found")
		return
	}
	util.OK(c, gin.H{"resolved": true, "resolved_at": now})
}

func (h *AdminHandler) ResolveSiteErrorsBatch(c *gin.Context) {
	h.resolveErrorsBatch(c, &model.OpsSystemLog{})
}

func (h *AdminHandler) ResolveAPIErrorsBatch(c *gin.Context) {
	h.resolveErrorsBatch(c, &model.OpsErrorLog{})
}

func (h *AdminHandler) resolveErrorsBatch(c *gin.Context, target any) {
	var req resolveErrorsBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailCode(c, http.StatusBadRequest, "request.invalid", "请选择需要处理的错误")
		return
	}
	ids := make([]int64, 0, len(req.IDs))
	seen := make(map[int64]struct{}, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		util.FailCode(c, http.StatusBadRequest, "request.invalid", "请选择需要处理的错误")
		return
	}
	if len(ids) > 500 {
		util.FailCode(c, http.StatusBadRequest, "request.invalid", "单次最多处理 500 条错误")
		return
	}
	actor := middleware.CurrentUser(c)
	now := time.Now().UTC()
	result := h.db.Model(target).Where("id IN ? AND resolved_at IS NULL", ids).
		Updates(map[string]any{"resolved_at": now, "resolved_by": actor.Email})
	if result.Error != nil {
		util.Fail(c, http.StatusInternalServerError, "batch resolve errors failed")
		return
	}
	util.OK(c, gin.H{
		"requested":   len(ids),
		"resolved":    result.RowsAffected,
		"resolved_at": now,
	})
}

func trimErrorCenterText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
