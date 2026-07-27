package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/crypto"
	"dengdeng/internal/importer"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db           *gorm.DB
	pricing      *service.PricingService
	rates        *service.UserGroupRateResolver
	oauth        *oauth.Manager
	monitor      *service.AccountMonitor
	quota        *service.AccountQuotaService
	runtime      *service.RuntimeMetrics
	scheduler    *service.Scheduler
	imageStorage *service.ImageStorageService
	// codexQuotaHTTPClient carries the deployment-wide outbound route. Account
	// specific proxies still take precedence for individual quota lookups.
	codexQuotaHTTPClient *http.Client
}

func (h *AdminHandler) SetImageStorageService(storage *service.ImageStorageService) {
	h.imageStorage = storage
}

func (h *AdminHandler) GetImageStorage(c *gin.Context) {
	if h.imageStorage == nil {
		util.Fail(c, http.StatusServiceUnavailable, "image storage service unavailable")
		return
	}
	view, err := h.imageStorage.View(c.Request.Context())
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load image storage settings failed")
		return
	}
	util.OK(c, view)
}

func (h *AdminHandler) UpdateImageStorage(c *gin.Context) {
	if h.imageStorage == nil {
		util.Fail(c, http.StatusServiceUnavailable, "image storage service unavailable")
		return
	}
	var req service.ImageStorageUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid image storage settings")
		return
	}
	view, err := h.imageStorage.Update(c.Request.Context(), req)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	util.OK(c, view)
}

func (h *AdminHandler) TestImageStorage(c *gin.Context) {
	if h.imageStorage == nil {
		util.Fail(c, http.StatusServiceUnavailable, "image storage service unavailable")
		return
	}
	if err := h.imageStorage.Test(c.Request.Context()); err != nil {
		util.Fail(c, http.StatusBadGateway, "object storage connection failed")
		return
	}
	util.OK(c, gin.H{"connected": true})
}

func NewAdminHandler(db *gorm.DB, pricing *service.PricingService, oauthManager *oauth.Manager, rates *service.UserGroupRateResolver) *AdminHandler {
	return &AdminHandler{db: db, pricing: pricing, rates: rates, oauth: oauthManager}
}

func (h *AdminHandler) SetAccountMonitor(monitor *service.AccountMonitor) {
	h.monitor = monitor
}

func (h *AdminHandler) SetAccountQuotaService(quota *service.AccountQuotaService) {
	h.quota = quota
}

func (h *AdminHandler) SetRuntimeMetrics(runtime *service.RuntimeMetrics) {
	h.runtime = runtime
}

func (h *AdminHandler) SetScheduler(scheduler *service.Scheduler) {
	h.scheduler = scheduler
}

func (h *AdminHandler) SetCodexQuotaHTTPClient(client *http.Client) {
	h.codexQuotaHTTPClient = client
}

// ---- dashboard ----

func (h *AdminHandler) Dashboard(c *gin.Context) {
	var users, groups, accounts, keys int64
	h.db.Model(&model.User{}).Count(&users)
	h.db.Model(&model.Group{}).Count(&groups)
	h.db.Model(&model.UpstreamAccount{}).Count(&accounts)
	h.db.Model(&model.APIKey{}).Count(&keys)
	summary := usageSummary(h.db, nil)
	summary["counts"] = gin.H{"users": users, "groups": groups, "accounts": accounts, "keys": keys}
	util.OK(c, summary)
}

func (h *AdminHandler) Usage(c *gin.Context) {
	filter, err := parseUsageQuery(c)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	logs, total, err := queryUsage(h.db, filter, nil)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "query usage failed")
		return
	}
	util.OK(c, gin.H{"items": logs, "total": total, "page": filter.Page, "size": filter.Size})
}

// ExportUsage emits the same filtered data as GET /usage in a spreadsheet-
// friendly CSV. Exports intentionally have a finite, visible cap so one click
// cannot make the console process scan an unbounded historical ledger.
func (h *AdminHandler) ExportUsage(c *gin.Context) {
	filter, err := parseUsageQuery(c)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := prepareUsageExport(&filter); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := writeUsageCSV(c, h.db, filter, nil, true); err != nil {
		util.Fail(c, http.StatusInternalServerError, "export usage failed")
	}
}

// ---- users ----

func (h *AdminHandler) ListUsers(c *gin.Context) {
	var users []model.User
	q := h.db.Model(&model.User{})
	if kw := c.Query("q"); kw != "" {
		q = q.Where("email LIKE ?", "%"+kw+"%")
	}
	q.Order("id DESC").Limit(500).Find(&users)
	util.OK(c, users)
}

type adminUpdateUserReq struct {
	Status         *string  `json:"status"`
	Role           *string  `json:"role"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	Concurrency    *int     `json:"concurrency"`
	AddBalance     *int64   `json:"add_balance_micro"`
	Password       *string  `json:"password"`
	Note           *string  `json:"note"`
}

func (h *AdminHandler) UpdateUser(c *gin.Context) {
	var user model.User
	if err := h.db.First(&user, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "user not found")
		return
	}
	var req adminUpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	me := middleware.CurrentUser(c)
	updates := map[string]any{}
	revoke := false // bump TokenVersion to force re-login on security-sensitive changes
	if req.Status != nil {
		if user.ID == me.ID && *req.Status != model.StatusActive {
			util.Fail(c, http.StatusBadRequest, "cannot disable yourself")
			return
		}
		updates["status"] = *req.Status
		if *req.Status != model.StatusActive {
			revoke = true
		}
	}
	if req.Role != nil && (*req.Role == model.RoleUser || *req.Role == model.RoleAdmin) {
		if user.ID == me.ID && *req.Role != model.RoleAdmin {
			util.Fail(c, http.StatusBadRequest, "cannot demote yourself")
			return
		}
		updates["role"] = *req.Role
		revoke = true
	}
	if req.RateMultiplier != nil && *req.RateMultiplier > 0 {
		updates["rate_multiplier"] = *req.RateMultiplier
	}
	if req.Concurrency != nil {
		if *req.Concurrency < 0 || *req.Concurrency > 10000 {
			util.Fail(c, http.StatusBadRequest, "user concurrency must be between 0 and 10000")
			return
		}
		updates["concurrency"] = *req.Concurrency
	}
	if req.Note != nil {
		updates["note"] = *req.Note
	}
	if req.Password != nil && len(*req.Password) >= 8 {
		hash, err := util.HashPassword(*req.Password)
		if err == nil {
			updates["password_hash"] = hash
			revoke = true
		}
	}
	if req.AddBalance != nil && *req.AddBalance != 0 {
		updates["balance_micro"] = gorm.Expr("balance_micro + ?", *req.AddBalance)
	}
	if revoke {
		updates["token_version"] = gorm.Expr("token_version + 1")
	}
	if len(updates) > 0 {
		h.db.Model(&user).Updates(updates)
	}
	h.db.First(&user, user.ID)
	util.OK(c, user)
}

type userGroupRateInput struct {
	GroupID        int64   `json:"group_id"`
	RateMultiplier float64 `json:"rate_multiplier"`
}

type userGroupRateReq struct {
	Rates []userGroupRateInput `json:"rates"`
}

// ListUserGroupRates exposes only explicit overrides. Absence means the user
// inherits the current group multiplier, so administrators can see exactly
// which special prices are in effect.
func (h *AdminHandler) ListUserGroupRates(c *gin.Context) {
	var user model.User
	if err := h.db.First(&user, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "user not found")
		return
	}
	var rates []model.UserGroupRate
	h.db.Where("user_id = ?", user.ID).Order("group_id").Find(&rates)
	util.OK(c, rates)
}

// ReplaceUserGroupRates atomically replaces all of a user's explicit group
// multipliers. Empty rates intentionally clears all overrides and restores
// group defaults.
func (h *AdminHandler) ReplaceUserGroupRates(c *gin.Context) {
	var user model.User
	if err := h.db.First(&user, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "user not found")
		return
	}
	var req userGroupRateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}

	unique := make(map[int64]float64, len(req.Rates))
	for _, item := range req.Rates {
		if item.GroupID <= 0 || item.RateMultiplier <= 0 || item.RateMultiplier > 1000 {
			util.Fail(c, http.StatusBadRequest, "group rate must be between 0 and 1000")
			return
		}
		unique[item.GroupID] = item.RateMultiplier
	}
	if len(unique) > 0 {
		ids := make([]int64, 0, len(unique))
		for id := range unique {
			ids = append(ids, id)
		}
		var count int64
		if err := h.db.Model(&model.Group{}).Where("id IN ?", ids).Count(&count).Error; err != nil || count != int64(len(ids)) {
			util.Fail(c, http.StatusBadRequest, "one or more groups do not exist")
			return
		}
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.UserGroupRate{}).Error; err != nil {
			return err
		}
		if len(unique) == 0 {
			return nil
		}
		rows := make([]model.UserGroupRate, 0, len(unique))
		for groupID, multiplier := range unique {
			rows = append(rows, model.UserGroupRate{UserID: user.ID, GroupID: groupID, RateMultiplier: multiplier})
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "save group rates failed")
		return
	}
	h.rates.Invalidate(user.ID, 0)
	h.ListUserGroupRates(c)
}

// ---- groups ----

func (h *AdminHandler) ListGroups(c *gin.Context) {
	var groups []model.Group
	h.db.Order("id").Find(&groups)

	type row struct {
		GroupID int64
		Total   int64
		Alive   int64
	}
	var rows []row
	h.db.Table("upstream_account_groups account_membership").
		Select("account_membership.group_id AS group_id, COUNT(DISTINCT upstream_accounts.id) AS total, SUM(CASE WHEN upstream_accounts.status = 'active' AND (upstream_accounts.cooldown_until IS NULL OR upstream_accounts.cooldown_until < ?) THEN 1 ELSE 0 END) AS alive", time.Now()).
		Joins("JOIN upstream_accounts ON upstream_accounts.id = account_membership.upstream_account_id").
		Group("account_membership.group_id").Scan(&rows)
	counts := map[int64]row{}
	for _, r := range rows {
		counts[r.GroupID] = r
	}
	type groupOut struct {
		model.Group
		AccountTotal int64 `json:"account_total"`
		AccountAlive int64 `json:"account_alive"`
	}
	out := make([]groupOut, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupOut{Group: g, AccountTotal: counts[g.ID].Total, AccountAlive: counts[g.ID].Alive})
	}
	util.OK(c, out)
}

type groupReq struct {
	Name                    string            `json:"name" binding:"required,max=64"`
	Platform                string            `json:"platform" binding:"required"`
	Description             string            `json:"description"`
	RateMultiplier          float64           `json:"rate_multiplier"`
	CacheReadMultiplier     float64           `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier  float64           `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier  float64           `json:"cache_write_1h_multiplier"`
	ImageRateIndependent    *bool             `json:"image_rate_independent"`
	ImageRateMultiplier     float64           `json:"image_rate_multiplier"`
	MaxReasoningEffort      string            `json:"max_reasoning_effort"`
	ReasoningEffortMappings map[string]string `json:"reasoning_effort_mappings"`
	IsPublic                *bool             `json:"is_public"`
	Status                  string            `json:"status"`
}

// groupUpdateReq intentionally uses pointers throughout. A group may be made
// private with `is_public: false`; a value-type boolean would make that change
// indistinguishable from an omitted field in partial updates.
type groupUpdateReq struct {
	Name                    *string            `json:"name"`
	Description             *string            `json:"description"`
	RateMultiplier          *float64           `json:"rate_multiplier"`
	CacheReadMultiplier     *float64           `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier  *float64           `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier  *float64           `json:"cache_write_1h_multiplier"`
	ImageRateIndependent    *bool              `json:"image_rate_independent"`
	ImageRateMultiplier     *float64           `json:"image_rate_multiplier"`
	MaxReasoningEffort      *string            `json:"max_reasoning_effort"`
	ReasoningEffortMappings *map[string]string `json:"reasoning_effort_mappings"`
	IsPublic                *bool              `json:"is_public"`
	Status                  *string            `json:"status"`
}

func validPlatform(p string) bool {
	for _, x := range model.AllPlatforms {
		if x == p {
			return true
		}
	}
	return false
}

var validReasoningEfforts = map[string]bool{
	"none": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

func normalizeReasoningPolicy(maxEffort string, mappings map[string]string) (string, map[string]string, error) {
	maxEffort = strings.ToLower(strings.TrimSpace(maxEffort))
	if maxEffort == "" {
		maxEffort = "auto"
	}
	if maxEffort != "auto" && !validReasoningEfforts[maxEffort] {
		return "", nil, fmt.Errorf("invalid maximum reasoning effort")
	}
	if len(mappings) > len(validReasoningEfforts) {
		return "", nil, fmt.Errorf("too many reasoning effort mappings")
	}
	normalized := make(map[string]string, len(mappings))
	for source, target := range mappings {
		source = strings.ToLower(strings.TrimSpace(source))
		target = strings.ToLower(strings.TrimSpace(target))
		if !validReasoningEfforts[source] || (target != "" && !validReasoningEfforts[target]) {
			return "", nil, fmt.Errorf("invalid reasoning effort mapping")
		}
		if target != "" && target != source {
			normalized[source] = target
		}
	}
	return maxEffort, normalized, nil
}

func (h *AdminHandler) CreateGroup(c *gin.Context) {
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil || !validPlatform(req.Platform) {
		util.Fail(c, http.StatusBadRequest, "name and a valid platform are required")
		return
	}
	g := model.Group{
		Name: req.Name, Platform: req.Platform, Description: req.Description,
		RateMultiplier:      1,
		CacheReadMultiplier: 1, CacheWrite5mMultiplier: 1, CacheWrite1hMultiplier: 1,
		ImageRateMultiplier: 1, MaxReasoningEffort: "auto", IsPublic: true, Status: model.StatusActive,
	}
	maxEffort, mappings, err := normalizeReasoningPolicy(req.MaxReasoningEffort, req.ReasoningEffortMappings)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	g.MaxReasoningEffort = maxEffort
	g.ReasoningEffortMappings = mappings
	if req.RateMultiplier > 0 {
		g.RateMultiplier = req.RateMultiplier
	}
	if req.CacheReadMultiplier > 0 {
		g.CacheReadMultiplier = req.CacheReadMultiplier
	}
	if req.CacheWrite5mMultiplier > 0 {
		g.CacheWrite5mMultiplier = req.CacheWrite5mMultiplier
	}
	if req.CacheWrite1hMultiplier > 0 {
		g.CacheWrite1hMultiplier = req.CacheWrite1hMultiplier
	}
	if req.ImageRateIndependent != nil {
		g.ImageRateIndependent = *req.ImageRateIndependent
	}
	if req.ImageRateMultiplier > 0 {
		g.ImageRateMultiplier = req.ImageRateMultiplier
	}
	if req.IsPublic != nil {
		g.IsPublic = *req.IsPublic
	}
	if err := h.db.Create(&g).Error; err != nil {
		util.Fail(c, http.StatusConflict, "group name already exists")
		return
	}
	util.OK(c, g)
}

func (h *AdminHandler) UpdateGroup(c *gin.Context) {
	var g model.Group
	if err := h.db.First(&g, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
		return
	}
	var req groupUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 64 {
			util.Fail(c, http.StatusBadRequest, "group name must be between 1 and 64 characters")
			return
		}
		updates["name"] = name
	}
	if req.Description != nil {
		updates["description"] = strings.TrimSpace(*req.Description)
	}
	if req.RateMultiplier != nil {
		if *req.RateMultiplier <= 0 {
			util.Fail(c, http.StatusBadRequest, "rate multiplier must be positive")
			return
		}
		updates["rate_multiplier"] = *req.RateMultiplier
	}
	if req.CacheReadMultiplier != nil {
		if *req.CacheReadMultiplier <= 0 {
			util.Fail(c, http.StatusBadRequest, "cache read multiplier must be positive")
			return
		}
		updates["cache_read_multiplier"] = *req.CacheReadMultiplier
	}
	if req.CacheWrite5mMultiplier != nil {
		if *req.CacheWrite5mMultiplier <= 0 {
			util.Fail(c, http.StatusBadRequest, "5m cache multiplier must be positive")
			return
		}
		// Keep the physical column name compatible with the schema produced by
		// GORM for CacheWrite5mMultiplier (cache_write5m_multiplier). The JSON
		// field deliberately keeps the clearer public cache_write_5m spelling.
		updates["cache_write5m_multiplier"] = *req.CacheWrite5mMultiplier
	}
	if req.CacheWrite1hMultiplier != nil {
		if *req.CacheWrite1hMultiplier <= 0 {
			util.Fail(c, http.StatusBadRequest, "1h cache multiplier must be positive")
			return
		}
		updates["cache_write1h_multiplier"] = *req.CacheWrite1hMultiplier
	}
	if req.ImageRateIndependent != nil {
		updates["image_rate_independent"] = *req.ImageRateIndependent
	}
	if req.ImageRateMultiplier != nil {
		if *req.ImageRateMultiplier <= 0 {
			util.Fail(c, http.StatusBadRequest, "image multiplier must be positive")
			return
		}
		updates["image_rate_multiplier"] = *req.ImageRateMultiplier
	}
	if req.MaxReasoningEffort != nil || req.ReasoningEffortMappings != nil {
		maxEffort := g.MaxReasoningEffort
		mappings := g.ReasoningEffortMappings
		if req.MaxReasoningEffort != nil {
			maxEffort = *req.MaxReasoningEffort
		}
		if req.ReasoningEffortMappings != nil {
			mappings = *req.ReasoningEffortMappings
		}
		normalizedMax, normalizedMappings, err := normalizeReasoningPolicy(maxEffort, mappings)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		encodedMappings, err := json.Marshal(normalizedMappings)
		if err != nil {
			util.Fail(c, http.StatusInternalServerError, "encode reasoning effort mappings failed")
			return
		}
		updates["max_reasoning_effort"] = normalizedMax
		// GORM serializers are applied when a struct field is saved, but map-based
		// Updates bypass that serializer. Store the JSON text explicitly so SQLite
		// and MySQL never receive a raw Go map as a driver argument.
		updates["reasoning_effort_mappings"] = string(encodedMappings)
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
	}
	if req.Status != nil {
		if *req.Status != model.StatusActive && *req.Status != model.StatusDisabled {
			util.Fail(c, http.StatusBadRequest, "invalid group status")
			return
		}
		updates["status"] = *req.Status
	}
	if len(updates) > 0 {
		if err := h.db.Model(&g).Updates(updates).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "update group failed")
			return
		}
	}
	h.db.First(&g, g.ID)
	util.OK(c, g)
}

type groupDependencySummary struct {
	APIKeys           int64 `json:"api_keys"`
	SharedAPIKeys     int64 `json:"shared_api_keys"`
	ExclusiveAPIKeys  int64 `json:"exclusive_api_keys"`
	Accounts          int64 `json:"accounts"`
	SharedAccounts    int64 `json:"shared_accounts"`
	ExclusiveAccounts int64 `json:"exclusive_accounts"`
	Subscriptions     int64 `json:"subscriptions"`
	UserRates         int64 `json:"user_rates"`
	ImageModels       int64 `json:"image_models"`
	AlertRules        int64 `json:"alert_rules"`
}

func (summary groupDependencySummary) total() int64 {
	return summary.APIKeys + summary.Accounts + summary.Subscriptions + summary.UserRates + summary.ImageModels + summary.AlertRules
}

func loadGroupDependencies(db *gorm.DB, groupID int64) (groupDependencySummary, error) {
	var summary groupDependencySummary
	var keys []model.APIKey
	keyIDs := db.Model(&model.APIKeyGroup{}).Select("api_key_id").Where("group_id = ?", groupID)
	if err := db.Where("id IN (?) OR group_id = ?", keyIDs, groupID).Find(&keys).Error; err != nil {
		return summary, err
	}
	for _, key := range keys {
		var alternatives int64
		if err := db.Model(&model.APIKeyGroup{}).Where("api_key_id = ? AND group_id <> ?", key.ID, groupID).Count(&alternatives).Error; err != nil {
			return summary, err
		}
		summary.APIKeys++
		if alternatives > 0 {
			summary.SharedAPIKeys++
		} else {
			summary.ExclusiveAPIKeys++
		}
	}

	var accounts []model.UpstreamAccount
	accountIDs := db.Model(&model.UpstreamAccountGroup{}).Select("upstream_account_id").Where("group_id = ?", groupID)
	if err := db.Where("id IN (?) OR group_id = ?", accountIDs, groupID).Find(&accounts).Error; err != nil {
		return summary, err
	}
	for _, account := range accounts {
		var alternatives int64
		if err := db.Model(&model.UpstreamAccountGroup{}).Where("upstream_account_id = ? AND group_id <> ?", account.ID, groupID).Count(&alternatives).Error; err != nil {
			return summary, err
		}
		summary.Accounts++
		if alternatives > 0 {
			summary.SharedAccounts++
		} else {
			summary.ExclusiveAccounts++
		}
	}

	counts := []struct {
		model any
		query string
		value *int64
	}{
		{model: &model.UserGroupSubscription{}, query: "group_id = ?", value: &summary.Subscriptions},
		{model: &model.UserGroupRate{}, query: "group_id = ?", value: &summary.UserRates},
		{model: &model.ModelConfig{}, query: "image_group_id = ?", value: &summary.ImageModels},
		{model: &model.AlertRule{}, query: "group_id = ?", value: &summary.AlertRules},
	}
	for _, item := range counts {
		if err := db.Model(item.model).Where(item.query, groupID).Count(item.value).Error; err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func (h *AdminHandler) GroupDependencies(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid group id")
		return
	}
	var group model.Group
	if err := h.db.First(&group, id).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
		return
	}
	summary, err := loadGroupDependencies(h.db, id)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load group dependencies failed")
		return
	}
	var targets []model.Group
	if err := h.db.Where("platform = ? AND id <> ?", group.Platform, group.ID).Order("status ASC, name ASC").Find(&targets).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load target groups failed")
		return
	}
	util.OK(c, gin.H{
		"group":               group,
		"dependencies":        summary,
		"total_dependencies":  summary.total(),
		"can_delete_directly": summary.total() == 0,
		"target_groups":       targets,
	})
}

func (h *AdminHandler) DeleteGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid group id")
		return
	}
	var group model.Group
	if err := h.db.First(&group, id).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
		return
	}
	force := strings.EqualFold(strings.TrimSpace(c.Query("force")), "true")
	targetGroupID, err := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("target_group_id", "0")), 10, 64)
	if err != nil || targetGroupID < 0 || targetGroupID == id {
		util.Fail(c, http.StatusBadRequest, "invalid target group")
		return
	}
	if targetGroupID > 0 {
		var target model.Group
		if err := h.db.First(&target, targetGroupID).Error; err != nil {
			util.Fail(c, http.StatusBadRequest, "target group not found")
			return
		}
		if target.Platform != group.Platform {
			util.Fail(c, http.StatusBadRequest, "target group platform mismatch")
			return
		}
	}
	summary, err := loadGroupDependencies(h.db, id)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load group dependencies failed")
		return
	}
	if summary.total() > 0 && !force {
		util.Fail(c, http.StatusConflict, "group still has bound resources; unbind them before deletion")
		return
	}
	var deletedKeys, retainedKeys, deletedAccounts, retainedAccounts int64
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		var keyMemberships []model.APIKeyGroup
		if err := tx.Where("group_id = ?", id).Find(&keyMemberships).Error; err != nil {
			return err
		}
		for _, membership := range keyMemberships {
			var key model.APIKey
			if err := tx.First(&key, membership.APIKeyID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					if err := tx.Delete(&membership).Error; err != nil {
						return err
					}
					continue
				}
				return err
			}
			var alternatives []model.APIKeyGroup
			if err := tx.Where("api_key_id = ? AND group_id <> ?", membership.APIKeyID, id).Order("group_id ASC").Find(&alternatives).Error; err != nil {
				return err
			}
			if len(alternatives) == 0 {
				if targetGroupID > 0 {
					binding := model.APIKeyGroup{APIKeyID: key.ID, GroupID: targetGroupID}
					if err := tx.Where("api_key_id = ? AND group_id = ?", key.ID, targetGroupID).FirstOrCreate(&binding).Error; err != nil {
						return err
					}
					if err := tx.Model(&key).Update("group_id", targetGroupID).Error; err != nil {
						return err
					}
					if err := tx.Where("api_key_id = ? AND group_id = ?", key.ID, id).Delete(&model.APIKeyGroup{}).Error; err != nil {
						return err
					}
					retainedKeys++
					continue
				}
				if err := tx.Where("api_key_id = ?", key.ID).Delete(&model.APIKeyGroup{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&key).Error; err != nil {
					return err
				}
				deletedKeys++
				continue
			}
			if key.GroupID == id {
				if err := tx.Model(&key).Update("group_id", alternatives[0].GroupID).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("api_key_id = ? AND group_id = ?", key.ID, id).Delete(&model.APIKeyGroup{}).Error; err != nil {
				return err
			}
			retainedKeys++
		}

		// Handle legacy keys that still point at the group but have no join row.
		var legacyKeys []model.APIKey
		if err := tx.Where("group_id = ?", id).Find(&legacyKeys).Error; err != nil {
			return err
		}
		for _, key := range legacyKeys {
			var alternatives []model.APIKeyGroup
			if err := tx.Where("api_key_id = ? AND group_id <> ?", key.ID, id).Order("group_id ASC").Find(&alternatives).Error; err != nil {
				return err
			}
			if len(alternatives) > 0 {
				if err := tx.Model(&key).Update("group_id", alternatives[0].GroupID).Error; err != nil {
					return err
				}
				retainedKeys++
				continue
			}
			if targetGroupID > 0 {
				binding := model.APIKeyGroup{APIKeyID: key.ID, GroupID: targetGroupID}
				if err := tx.Where("api_key_id = ? AND group_id = ?", key.ID, targetGroupID).FirstOrCreate(&binding).Error; err != nil {
					return err
				}
				if err := tx.Model(&key).Update("group_id", targetGroupID).Error; err != nil {
					return err
				}
				retainedKeys++
				continue
			}
			if err := tx.Where("api_key_id = ?", key.ID).Delete(&model.APIKeyGroup{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&key).Error; err != nil {
				return err
			}
			deletedKeys++
		}
		if err := tx.Where("group_id = ?", id).Delete(&model.APIKeyGroup{}).Error; err != nil {
			return err
		}

		var memberships []model.UpstreamAccountGroup
		if err := tx.Where("group_id = ?", id).Find(&memberships).Error; err != nil {
			return err
		}
		for _, membership := range memberships {
			var alternatives []model.UpstreamAccountGroup
			if err := tx.Where("upstream_account_id = ? AND group_id <> ?", membership.UpstreamAccountID, id).Order("group_id ASC").Find(&alternatives).Error; err != nil {
				return err
			}
			if len(alternatives) == 0 {
				if targetGroupID > 0 {
					binding := model.UpstreamAccountGroup{UpstreamAccountID: membership.UpstreamAccountID, GroupID: targetGroupID}
					if err := tx.Where("upstream_account_id = ? AND group_id = ?", membership.UpstreamAccountID, targetGroupID).FirstOrCreate(&binding).Error; err != nil {
						return err
					}
					if err := tx.Model(&model.UpstreamAccount{}).Where("id = ?", membership.UpstreamAccountID).Update("group_id", targetGroupID).Error; err != nil {
						return err
					}
					if err := tx.Where("upstream_account_id = ? AND group_id = ?", membership.UpstreamAccountID, id).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
						return err
					}
					retainedAccounts++
					continue
				}
				if err := tx.Where("upstream_account_id = ?", membership.UpstreamAccountID).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
					return err
				}
				if err := tx.Delete(&model.UpstreamAccount{}, membership.UpstreamAccountID).Error; err != nil {
					return err
				}
				deletedAccounts++
				continue
			}
			if err := tx.Model(&model.UpstreamAccount{}).
				Where("id = ? AND group_id = ?", membership.UpstreamAccountID, id).
				Update("group_id", alternatives[0].GroupID).Error; err != nil {
				return err
			}
			if err := tx.Where("upstream_account_id = ? AND group_id = ?", membership.UpstreamAccountID, id).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
				return err
			}
			retainedAccounts++
		}

		// Handle legacy accounts that still point at the group but have no join row.
		var legacyAccounts []model.UpstreamAccount
		if err := tx.Where("group_id = ?", id).Find(&legacyAccounts).Error; err != nil {
			return err
		}
		for _, account := range legacyAccounts {
			var alternatives []model.UpstreamAccountGroup
			if err := tx.Where("upstream_account_id = ? AND group_id <> ?", account.ID, id).Order("group_id ASC").Find(&alternatives).Error; err != nil {
				return err
			}
			if len(alternatives) > 0 {
				if err := tx.Model(&account).Update("group_id", alternatives[0].GroupID).Error; err != nil {
					return err
				}
				retainedAccounts++
				continue
			}
			if targetGroupID > 0 {
				binding := model.UpstreamAccountGroup{UpstreamAccountID: account.ID, GroupID: targetGroupID}
				if err := tx.Where("upstream_account_id = ? AND group_id = ?", account.ID, targetGroupID).FirstOrCreate(&binding).Error; err != nil {
					return err
				}
				if err := tx.Model(&account).Update("group_id", targetGroupID).Error; err != nil {
					return err
				}
				retainedAccounts++
				continue
			}
			if err := tx.Where("upstream_account_id = ?", account.ID).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&account).Error; err != nil {
				return err
			}
			deletedAccounts++
		}
		if err := tx.Where("group_id = ?", id).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
			return err
		}
		if targetGroupID > 0 {
			var rates []model.UserGroupRate
			if err := tx.Where("group_id = ?", id).Find(&rates).Error; err != nil {
				return err
			}
			for _, rate := range rates {
				var existing int64
				if err := tx.Model(&model.UserGroupRate{}).Where("user_id = ? AND group_id = ?", rate.UserID, targetGroupID).Count(&existing).Error; err != nil {
					return err
				}
				if existing == 0 {
					if err := tx.Model(&rate).Update("group_id", targetGroupID).Error; err != nil {
						return err
					}
				} else if err := tx.Delete(&rate).Error; err != nil {
					return err
				}
			}

			var subscriptions []model.UserGroupSubscription
			if err := tx.Where("group_id = ?", id).Find(&subscriptions).Error; err != nil {
				return err
			}
			for _, subscription := range subscriptions {
				var existing model.UserGroupSubscription
				err := tx.Where("user_id = ? AND group_id = ?", subscription.UserID, targetGroupID).First(&existing).Error
				switch {
				case errors.Is(err, gorm.ErrRecordNotFound):
					if err := tx.Model(&subscription).Update("group_id", targetGroupID).Error; err != nil {
						return err
					}
				case err != nil:
					return err
				default:
					if subscription.ExpiresAt.After(existing.ExpiresAt) {
						if err := tx.Model(&existing).Update("expires_at", subscription.ExpiresAt).Error; err != nil {
							return err
						}
					}
					if err := tx.Delete(&subscription).Error; err != nil {
						return err
					}
				}
			}
			if err := tx.Model(&model.AlertRule{}).Where("group_id = ?", id).Update("group_id", targetGroupID).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.ModelConfig{}).Where("image_group_id = ?", id).Update("image_group_id", targetGroupID).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Where("group_id = ?", id).Delete(&model.UserGroupRate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("group_id = ?", id).Delete(&model.UserGroupSubscription{}).Error; err != nil {
				return err
			}
			if err := tx.Where("group_id = ?", id).Delete(&model.AlertRule{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.ModelConfig{}).Where("image_group_id = ?", id).Update("image_group_id", 0).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&model.Group{}, id).Error
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "delete group failed")
		return
	}
	if h.scheduler != nil {
		h.scheduler.InvalidateGroup(id)
		if targetGroupID > 0 {
			h.scheduler.InvalidateGroup(targetGroupID)
		}
	}
	util.OK(c, gin.H{
		"deleted":           true,
		"deleted_keys":      deletedKeys,
		"retained_keys":     retainedKeys,
		"deleted_accounts":  deletedAccounts,
		"retained_accounts": retainedAccounts,
		"target_group_id":   targetGroupID,
	})
}

// ---- upstream accounts ----

const maxAccountPageSize = 100

// accountListQuery is intentionally separate from the legacy unpaged list.
// Older console surfaces still use the latter for short select menus, while
// the account workspace opts into paging explicitly.
type accountListQuery struct {
	Page     int
	Size     int
	GroupID  int64
	Platform string
	AuthType string
	Sort     string
	Order    string
}

func parseAccountListQuery(c *gin.Context) (accountListQuery, error) {
	page, err := parsePositiveInt(c.DefaultQuery("page", "1"), "page", 1, 1_000_000)
	if err != nil {
		return accountListQuery{}, err
	}
	size, err := parsePositiveInt(c.DefaultQuery("size", "24"), "size", 1, maxAccountPageSize)
	if err != nil {
		return accountListQuery{}, err
	}
	query := accountListQuery{
		Page:     page,
		Size:     size,
		Platform: strings.TrimSpace(c.Query("platform")),
		AuthType: strings.TrimSpace(c.Query("auth_type")),
		Sort:     strings.TrimSpace(c.DefaultQuery("sort", "custom")),
		Order:    strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "asc"))),
	}
	if query.Platform != "" && !validPlatform(query.Platform) {
		return accountListQuery{}, fmt.Errorf("invalid platform")
	}
	if query.AuthType != "" && query.AuthType != model.AuthAPIKey && query.AuthType != model.AuthOAuth && query.AuthType != model.AuthAgentIdentity {
		return accountListQuery{}, fmt.Errorf("invalid auth_type")
	}
	if query.Sort != "custom" && query.Sort != "name" && query.Sort != "platform" && query.Sort != "group" && query.Sort != "priority" && query.Sort != "availability" && query.Sort != "last_used" {
		return accountListQuery{}, fmt.Errorf("invalid sort")
	}
	if query.Order != "asc" && query.Order != "desc" {
		return accountListQuery{}, fmt.Errorf("order must be asc or desc")
	}
	if raw := strings.TrimSpace(c.Query("group_id")); raw != "" {
		groupID, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || groupID <= 0 {
			return accountListQuery{}, fmt.Errorf("group_id must be a positive integer")
		}
		query.GroupID = groupID
	}
	return query, nil
}

func applyAccountListFilters(q *gorm.DB, query accountListQuery) *gorm.DB {
	if query.GroupID > 0 {
		q = q.Where("EXISTS (SELECT 1 FROM upstream_account_groups account_membership WHERE account_membership.upstream_account_id = upstream_accounts.id AND account_membership.group_id = ?) OR upstream_accounts.group_id = ?", query.GroupID, query.GroupID)
	}
	if query.Platform != "" {
		q = q.Where("upstream_accounts.platform = ?", query.Platform)
	}
	if query.AuthType != "" {
		q = q.Where("upstream_accounts.auth_type = ?", query.AuthType)
	}
	return q
}

func applyAccountListOrder(q *gorm.DB, query accountListQuery) *gorm.DB {
	if query.Sort == "custom" {
		return q.
			Order("CASE WHEN upstream_accounts.display_order = 0 THEN 1 ELSE 0 END ASC").
			Order("upstream_accounts.display_order ASC").
			Order("upstream_accounts.id DESC")
	}
	// Availability includes normalized quota windows and is therefore sorted
	// after preloading those snapshots in ListAccounts.
	if query.Sort == "availability" {
		return q
	}

	column := ""
	switch query.Sort {
	case "name":
		column = "LOWER(upstream_accounts.name)"
	case "platform":
		column = "LOWER(upstream_accounts.platform)"
	case "group":
		q = q.Joins("LEFT JOIN groups account_groups ON account_groups.id = upstream_accounts.group_id").Select("upstream_accounts.*")
		column = "LOWER(account_groups.name)"
	case "priority":
		column = "upstream_accounts.priority"
	case "last_used":
		// Put never-used accounts last in either direction; a NULL position must
		// not depend on the database engine's default NULL ordering.
		q = q.Order("CASE WHEN upstream_accounts.last_used_at IS NULL THEN 1 ELSE 0 END ASC")
		column = "upstream_accounts.last_used_at"
	}
	return q.Order(column + " " + strings.ToUpper(query.Order)).Order("upstream_accounts.id " + strings.ToUpper(query.Order))
}

func accountQuotaIsExhausted(snapshot *model.AccountQuotaSnapshot) bool {
	if snapshot == nil {
		return false
	}
	for _, window := range snapshot.Windows {
		if window.UsedPercent != nil && *window.UsedPercent >= 100 {
			return true
		}
	}
	return false
}

func accountAvailabilityScore(account model.UpstreamAccount, now time.Time) int {
	if account.Status != model.StatusActive {
		return 0
	}
	if account.AuthType == model.AuthOAuth && account.ExpiresAt != nil && !account.ExpiresAt.After(now) && account.Quota != nil && account.Quota.State == "error" {
		return 0
	}
	if accountQuotaIsExhausted(account.Quota) {
		return 0
	}
	if account.CooldownUntil != nil && account.CooldownUntil.After(now) {
		return 10
	}
	if account.ErrorCount >= 4 {
		return 45
	}
	if account.ErrorCount > 0 {
		return 75
	}
	return 100
}

func sortAccountsByAvailability(accounts []model.UpstreamAccount, order string, now time.Time) {
	descending := order == "desc"
	sort.SliceStable(accounts, func(left, right int) bool {
		leftScore := accountAvailabilityScore(accounts[left], now)
		rightScore := accountAvailabilityScore(accounts[right], now)
		if leftScore == rightScore {
			if descending {
				return accounts[left].ID > accounts[right].ID
			}
			return accounts[left].ID < accounts[right].ID
		}
		if descending {
			return leftScore > rightScore
		}
		return leftScore < rightScore
	})
}

func (h *AdminHandler) ListAccounts(c *gin.Context) {
	// Keep the original array response for compact legacy select menus. The
	// management screen always passes page/size and receives a bounded result.
	if c.Query("page") != "" || c.Query("size") != "" {
		query, err := parseAccountListQuery(c)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		var total int64
		if err := applyAccountListFilters(h.db.Model(&model.UpstreamAccount{}), query).Count(&total).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "query accounts failed")
			return
		}
		var accounts []model.UpstreamAccount
		list := applyAccountListFilters(h.db.Model(&model.UpstreamAccount{}).Preload("Group").Preload("Groups").Preload("Proxy").Preload("Quota").Preload("CodexQuota"), query)
		if query.Sort == "availability" {
			if err := list.Find(&accounts).Error; err != nil {
				util.Fail(c, http.StatusInternalServerError, "query accounts failed")
				return
			}
			sortAccountsByAvailability(accounts, query.Order, time.Now().UTC())
			start := (query.Page - 1) * query.Size
			if start >= len(accounts) {
				accounts = []model.UpstreamAccount{}
			} else {
				end := min(start+query.Size, len(accounts))
				accounts = accounts[start:end]
			}
		} else if err := applyAccountListOrder(list, query).Offset((query.Page - 1) * query.Size).Limit(query.Size).Find(&accounts).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "query accounts failed")
			return
		}
		for index := range accounts {
			hydrateUpstreamAccountGroups(&accounts[index])
		}
		util.OK(c, gin.H{"items": accounts, "total": total, "page": query.Page, "size": query.Size})
		return
	}

	var accounts []model.UpstreamAccount
	q := h.db.Preload("Group").Preload("Groups").Preload("Proxy").Preload("Quota").Preload("CodexQuota")
	if gid := c.Query("group_id"); gid != "" {
		q = q.Where("EXISTS (SELECT 1 FROM upstream_account_groups account_membership WHERE account_membership.upstream_account_id = upstream_accounts.id AND account_membership.group_id = ?) OR upstream_accounts.group_id = ?", gid, gid)
	}
	q.Order("CASE WHEN display_order = 0 THEN 1 ELSE 0 END ASC").Order("display_order ASC").Order("id DESC").Find(&accounts)
	for index := range accounts {
		hydrateUpstreamAccountGroups(&accounts[index])
	}
	util.OK(c, accounts)
}

type reorderAccountsReq struct {
	AccountIDs []int64 `json:"account_ids"`
	SourceID   int64   `json:"source_id"`
	TargetID   int64   `json:"target_id"`
	Placement  string  `json:"placement"`
}

// ReorderAccounts persists the administrator's console order. It deliberately
// does not touch Priority, because display order must never change gateway
// scheduling behaviour.
func (h *AdminHandler) ReorderAccounts(c *gin.Context) {
	var req reorderAccountsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.SourceID > 0 || req.TargetID > 0 {
		h.reorderAccountByPlacement(c, req)
		return
	}
	if len(req.AccountIDs) == 0 {
		util.Fail(c, http.StatusBadRequest, "account_ids is required")
		return
	}
	seen := make(map[int64]struct{}, len(req.AccountIDs))
	for _, id := range req.AccountIDs {
		if id <= 0 {
			util.Fail(c, http.StatusBadRequest, "account_ids contains an invalid account")
			return
		}
		if _, duplicate := seen[id]; duplicate {
			util.Fail(c, http.StatusBadRequest, "account_ids must not contain duplicates")
			return
		}
		seen[id] = struct{}{}
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		var accounts []model.UpstreamAccount
		if err := orderedAccountsForDisplay(tx, &accounts); err != nil {
			return err
		}
		byID := make(map[int64]model.UpstreamAccount, len(accounts))
		for _, account := range accounts {
			byID[account.ID] = account
		}
		ordered := make([]model.UpstreamAccount, 0, len(accounts))
		for _, id := range req.AccountIDs {
			account, ok := byID[id]
			if !ok {
				return fmt.Errorf("unknown account")
			}
			ordered = append(ordered, account)
		}
		for _, account := range accounts {
			if _, explicitlyOrdered := seen[account.ID]; !explicitlyOrdered {
				ordered = append(ordered, account)
			}
		}
		return saveAccountDisplayOrder(tx, ordered)
	}); err != nil {
		if strings.Contains(err.Error(), "unknown account") {
			util.Fail(c, http.StatusBadRequest, "account_ids contains an unknown account")
		} else {
			util.Fail(c, http.StatusInternalServerError, "save account order failed")
		}
		return
	}
	util.OK(c, gin.H{"saved": true})
}

func orderedAccountsForDisplay(tx *gorm.DB, accounts *[]model.UpstreamAccount) error {
	return tx.Model(&model.UpstreamAccount{}).
		Order("CASE WHEN display_order = 0 THEN 1 ELSE 0 END ASC").
		Order("display_order ASC").
		Order("id DESC").
		Find(accounts).Error
}

func saveAccountDisplayOrder(tx *gorm.DB, accounts []model.UpstreamAccount) error {
	for index, account := range accounts {
		if err := tx.Model(&model.UpstreamAccount{}).Where("id = ?", account.ID).Update("display_order", index+1).Error; err != nil {
			return err
		}
	}
	return nil
}

// reorderAccountByPlacement is pagination-safe: the browser only needs to
// identify the dragged account and its visible drop target, while the server
// re-numbers the full account set atomically.
func (h *AdminHandler) reorderAccountByPlacement(c *gin.Context, req reorderAccountsReq) {
	if req.SourceID <= 0 || req.TargetID <= 0 || req.SourceID == req.TargetID || (req.Placement != "before" && req.Placement != "after") {
		util.Fail(c, http.StatusBadRequest, "source_id, target_id and placement are required")
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		var accounts []model.UpstreamAccount
		if err := orderedAccountsForDisplay(tx, &accounts); err != nil {
			return err
		}
		var source model.UpstreamAccount
		remaining := make([]model.UpstreamAccount, 0, len(accounts)-1)
		foundSource, foundTarget := false, false
		for _, account := range accounts {
			if account.ID == req.SourceID {
				source, foundSource = account, true
				continue
			}
			if account.ID == req.TargetID {
				foundTarget = true
			}
			remaining = append(remaining, account)
		}
		if !foundSource || !foundTarget {
			return fmt.Errorf("unknown account")
		}
		targetIndex := -1
		for index, account := range remaining {
			if account.ID == req.TargetID {
				targetIndex = index
				break
			}
		}
		if targetIndex < 0 {
			return fmt.Errorf("unknown account")
		}
		if req.Placement == "after" {
			targetIndex++
		}
		ordered := append(remaining[:targetIndex:targetIndex], append([]model.UpstreamAccount{source}, remaining[targetIndex:]...)...)
		return saveAccountDisplayOrder(tx, ordered)
	}); err != nil {
		if strings.Contains(err.Error(), "unknown account") {
			util.Fail(c, http.StatusBadRequest, "source or target account not found")
		} else {
			util.Fail(c, http.StatusInternalServerError, "save account order failed")
		}
		return
	}
	util.OK(c, gin.H{"saved": true})
}

type accountReq struct {
	GroupID      int64      `json:"group_id"`
	GroupIDs     *[]int64   `json:"group_ids"`
	ProxyID      *int64     `json:"proxy_id"`
	Name         string     `json:"name"`
	BaseURL      *string    `json:"base_url"`
	QuotaURL     *string    `json:"quota_url"`
	AuthType     string     `json:"auth_type"`
	APIKey       string     `json:"api_key"`
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Email        string     `json:"email"`
	AccountID    string     `json:"account_id"`
	Priority     *int       `json:"priority"`
	Concurrency  *int       `json:"concurrency"`
	Status       string     `json:"status"`
}

const maxUpstreamAccountGroups = 32

func normalizeAccountGroupIDs(primary int64, groupIDs *[]int64) []int64 {
	values := make([]int64, 0, maxUpstreamAccountGroups)
	if groupIDs != nil {
		values = append(values, (*groupIDs)...)
	} else if primary > 0 {
		values = append(values, primary)
	}
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, id := range values {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func trimOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (h *AdminHandler) resolveAccountGroups(ids []int64, platform string) ([]model.Group, error) {
	if len(ids) == 0 || len(ids) > maxUpstreamAccountGroups {
		return nil, fmt.Errorf("select between 1 and %d groups", maxUpstreamAccountGroups)
	}
	var found []model.Group
	if err := h.db.Where("id IN ?", ids).Find(&found).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]model.Group, len(found))
	for _, group := range found {
		byID[group.ID] = group
	}
	ordered := make([]model.Group, 0, len(ids))
	for _, id := range ids {
		group, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("group not found")
		}
		if platform != "" && group.Platform != platform {
			return nil, fmt.Errorf("all account groups must use platform %s", platform)
		}
		if platform == "" {
			platform = group.Platform
		}
		ordered = append(ordered, group)
	}
	return ordered, nil
}

func accountGroupBindings(accountID int64, groups []model.Group) []model.UpstreamAccountGroup {
	bindings := make([]model.UpstreamAccountGroup, 0, len(groups))
	for _, group := range groups {
		bindings = append(bindings, model.UpstreamAccountGroup{UpstreamAccountID: accountID, GroupID: group.ID})
	}
	return bindings
}

func replaceUpstreamAccountGroups(tx *gorm.DB, account *model.UpstreamAccount, groups []model.Group) error {
	if account == nil || len(groups) == 0 {
		return fmt.Errorf("at least one group is required")
	}
	if err := tx.Where("upstream_account_id = ?", account.ID).Delete(&model.UpstreamAccountGroup{}).Error; err != nil {
		return err
	}
	if err := tx.Create(accountGroupBindings(account.ID, groups)).Error; err != nil {
		return err
	}
	account.GroupID = groups[0].ID
	account.Platform = groups[0].Platform
	return tx.Model(account).Updates(map[string]any{"group_id": account.GroupID, "platform": account.Platform}).Error
}

func appendUpstreamAccountGroups(tx *gorm.DB, accountID int64, groups []model.Group) error {
	for _, group := range groups {
		binding := model.UpstreamAccountGroup{UpstreamAccountID: accountID, GroupID: group.ID}
		if err := tx.Where("upstream_account_id = ? AND group_id = ?", accountID, group.ID).FirstOrCreate(&binding).Error; err != nil {
			return err
		}
	}
	return nil
}

func hydrateUpstreamAccountGroups(account *model.UpstreamAccount) {
	if account == nil {
		return
	}
	ordered := make([]model.Group, 0, len(account.Groups)+1)
	seen := make(map[int64]struct{}, len(account.Groups)+1)
	if account.Group != nil && account.Group.ID > 0 {
		ordered = append(ordered, *account.Group)
		seen[account.Group.ID] = struct{}{}
	}
	for _, group := range account.Groups {
		if _, exists := seen[group.ID]; exists {
			continue
		}
		ordered = append(ordered, group)
		seen[group.ID] = struct{}{}
	}
	account.Groups = ordered
	account.GroupIDs = make([]int64, 0, len(ordered))
	for _, group := range ordered {
		account.GroupIDs = append(account.GroupIDs, group.ID)
	}
}

func (h *AdminHandler) CreateAccount(c *gin.Context) {
	var req accountReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		util.Fail(c, http.StatusBadRequest, "group_ids and name are required")
		return
	}
	groupIDs := normalizeAccountGroupIDs(req.GroupID, req.GroupIDs)
	groups, err := h.resolveAccountGroups(groupIDs, "")
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	authType := req.AuthType
	if authType == "" {
		authType = model.AuthAPIKey
	}
	if authType == model.AuthAPIKey && req.APIKey == "" {
		util.Fail(c, http.StatusBadRequest, "api_key is required for api_key accounts")
		return
	}
	if authType == model.AuthOAuth && req.AccessToken == "" && req.RefreshToken == "" {
		util.Fail(c, http.StatusBadRequest, "access_token or refresh_token is required for oauth accounts")
		return
	}
	if authType == model.AuthAgentIdentity {
		util.Fail(c, http.StatusBadRequest, "use the JSON import endpoint for Agent Identity accounts")
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 0 || *req.Concurrency > 10000) {
		util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
		return
	}
	proxyID := int64(0)
	if req.ProxyID != nil {
		proxyID = *req.ProxyID
	}
	if err := h.validateProxyAssignment(proxyID); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var maxDisplayOrder int
	_ = h.db.Model(&model.UpstreamAccount{}).Select("COALESCE(MAX(display_order), 0)").Scan(&maxDisplayOrder).Error
	acc := model.UpstreamAccount{
		GroupID: groups[0].ID, ProxyID: proxyID, Name: req.Name, Platform: groups[0].Platform,
		BaseURL: trimOptionalString(req.BaseURL), QuotaURL: trimOptionalString(req.QuotaURL), AuthType: authType,
		APIKey:       crypto.EncryptedString(req.APIKey),
		AccessToken:  crypto.EncryptedString(req.AccessToken),
		RefreshToken: crypto.EncryptedString(req.RefreshToken),
		ExpiresAt:    req.ExpiresAt, Email: req.Email, AccountID: req.AccountID,
		Priority: 10, DisplayOrder: maxDisplayOrder + 1, Status: model.StatusActive,
	}
	if req.Priority != nil {
		acc.Priority = *req.Priority
	}
	if req.Concurrency != nil {
		acc.Concurrency = *req.Concurrency
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&acc).Error; err != nil {
			return err
		}
		return tx.Create(accountGroupBindings(acc.ID, groups)).Error
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "create account failed")
		return
	}
	acc.Group = &groups[0]
	acc.Groups = groups
	acc.GroupIDs = groupIDs
	if proxyID > 0 {
		var proxy model.Proxy
		if h.db.First(&proxy, proxyID).Error == nil {
			acc.Proxy = &proxy
		}
	}
	util.OK(c, acc)
}

func (h *AdminHandler) UpdateAccount(c *gin.Context) {
	var acc model.UpstreamAccount
	if err := h.db.First(&acc, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "account not found")
		return
	}
	var req accountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	existingAgentIdentity := service.IsOpenAIAgentIdentity(&acc)
	if req.AuthType == model.AuthAgentIdentity && !existingAgentIdentity {
		util.Fail(c, http.StatusBadRequest, "use the JSON import endpoint to convert an account to Agent Identity")
		return
	}
	if existingAgentIdentity && req.AuthType != "" && req.AuthType != model.AuthAgentIdentity {
		util.Fail(c, http.StatusBadRequest, "import or create a separate credential to replace an Agent Identity account")
		return
	}
	var nextGroups []model.Group
	if req.GroupIDs != nil || req.GroupID > 0 {
		groupIDs := normalizeAccountGroupIDs(req.GroupID, req.GroupIDs)
		var err error
		nextGroups, err = h.resolveAccountGroups(groupIDs, acc.Platform)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	updates := map[string]any{}
	if req.BaseURL != nil {
		updates["base_url"] = trimOptionalString(req.BaseURL)
	}
	if req.QuotaURL != nil {
		updates["quota_url"] = trimOptionalString(req.QuotaURL)
	}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.AuthType == model.AuthAPIKey || req.AuthType == model.AuthOAuth || req.AuthType == model.AuthAgentIdentity {
		updates["auth_type"] = req.AuthType
	}
	// Wrap secrets so GORM's Valuer encrypts before writing.
	if req.APIKey != "" {
		updates["api_key"] = crypto.EncryptedString(req.APIKey)
	}
	if req.AccessToken != "" {
		updates["access_token"] = crypto.EncryptedString(req.AccessToken)
	}
	if req.RefreshToken != "" {
		updates["refresh_token"] = crypto.EncryptedString(req.RefreshToken)
	}
	if req.ExpiresAt != nil {
		updates["expires_at"] = req.ExpiresAt
	}
	if req.Email != "" {
		updates["email"] = req.Email
	}
	if req.AccountID != "" {
		updates["account_id"] = req.AccountID
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.Concurrency != nil {
		if *req.Concurrency < 0 || *req.Concurrency > 10000 {
			util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
			return
		}
		updates["concurrency"] = *req.Concurrency
	}
	if req.ProxyID != nil {
		if err := h.validateProxyAssignment(*req.ProxyID); err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updates["proxy_id"] = *req.ProxyID
	}
	if req.Status == model.StatusActive || req.Status == model.StatusDisabled {
		updates["status"] = req.Status
		if req.Status == model.StatusActive {
			updates["cooldown_until"] = nil
			updates["error_count"] = 0
			updates["last_error"] = ""
		}
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&acc).Updates(updates).Error; err != nil {
			return err
		}
		if len(nextGroups) > 0 {
			return replaceUpstreamAccountGroups(tx, &acc, nextGroups)
		}
		return nil
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "update account failed")
		return
	}
	h.db.Preload("Group").Preload("Groups").Preload("Proxy").First(&acc, acc.ID)
	hydrateUpstreamAccountGroups(&acc)
	util.OK(c, acc)
}

type importReq struct {
	GroupID     int64    `json:"group_id"`
	GroupIDs    *[]int64 `json:"group_ids"`
	ProxyID     int64    `json:"proxy_id"`
	Name        string   `json:"name"`
	Format      string   `json:"format"` // sub2api | cpa | auto
	Data        string   `json:"data"`   // raw export JSON
	BaseURL     string   `json:"base_url"`
	Priority    *int     `json:"priority"`
	Concurrency *int     `json:"concurrency"`
	SkipExpired bool     `json:"skip_expired"`
}

// ImportAccounts bulk-creates upstream accounts from a sub2api or cpa export.
// Accounts whose platform differs from the target group are skipped.
func (h *AdminHandler) ImportAccounts(c *gin.Context) {
	var req importReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Data == "" {
		util.Fail(c, http.StatusBadRequest, "group_ids and data are required")
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 0 || *req.Concurrency > 10000) {
		util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
		return
	}
	if err := h.validateProxyAssignment(req.ProxyID); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	groups, err := h.resolveAccountGroups(normalizeAccountGroupIDs(req.GroupID, req.GroupIDs), "")
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	group := groups[0]

	parsed, err := importer.Parse(req.Format, []byte(req.Data))
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "parse failed: "+err.Error())
		return
	}

	imported := make([]string, 0, len(parsed))
	updated := make([]string, 0, len(parsed))
	skipped := make([]gin.H, 0)
	seenAgentIdentities := make(map[string]struct{})
	now := time.Now()
	var maxDisplayOrder int
	_ = h.db.Model(&model.UpstreamAccount{}).Select("COALESCE(MAX(display_order), 0)").Scan(&maxDisplayOrder).Error
	for _, p := range parsed {
		if len(parsed) == 1 && strings.TrimSpace(req.Name) != "" {
			p.Name = strings.TrimSpace(req.Name)
		}
		if p.Concurrency != nil && (*p.Concurrency < 0 || *p.Concurrency > 10000) {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "invalid concurrency"})
			continue
		}
		if !p.PlatformDetected {
			p.Platform = group.Platform
		}
		if p.Platform != "" && p.Platform != group.Platform {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "platform " + p.Platform + " != group " + group.Platform})
			continue
		}
		if req.SkipExpired && p.AuthType != model.AuthAgentIdentity && p.ExpiresAt != nil && p.ExpiresAt.Before(now) {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "token expired"})
			continue
		}
		if p.AuthType == model.AuthAPIKey && p.APIKey == "" {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "missing api_key"})
			continue
		}
		if p.AuthType == model.AuthOAuth && p.AccessToken == "" && p.RefreshToken == "" {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "missing access/refresh token"})
			continue
		}
		if p.AuthType == model.AuthAgentIdentity {
			record := service.AgentIdentityRecord{
				AgentRuntimeID:          stringMapValue(p.Extra, "agent_runtime_id"),
				AgentPrivateKey:         stringMapValue(p.Extra, "agent_private_key"),
				TaskID:                  stringMapValue(p.Extra, "task_id"),
				AccountID:               firstNonEmpty(p.AccountID, stringMapValue(p.Extra, "account_id"), stringMapValue(p.Extra, "chatgpt_account_id")),
				ChatGPTUserID:           stringMapValue(p.Extra, "chatgpt_user_id"),
				Email:                   firstNonEmpty(p.Email, stringMapValue(p.Extra, "email")),
				PlanType:                stringMapValue(p.Extra, "plan_type"),
				ChatGPTAccountIsFedRAMP: boolMapValue(p.Extra, "chatgpt_account_is_fedramp"),
			}
			if err := service.ValidateAgentIdentityRecord(record); err != nil {
				skipped = append(skipped, gin.H{"name": p.Name, "reason": err.Error()})
				continue
			}
			p.Extra = service.AgentIdentityExtra(record)
			p.AccountID = record.AccountID
			p.Email = record.Email
			// A new runtime replaces the old runtime for the same ChatGPT
			// account. The user id is shared across Team workspaces and the
			// runtime id changes on re-registration, so neither is a safe
			// deduplication key.
			identityKey := record.AccountID
			if _, duplicate := seenAgentIdentities[identityKey]; duplicate {
				skipped = append(skipped, gin.H{"name": p.Name, "reason": "duplicate Agent Identity for the same ChatGPT account"})
				continue
			}
			seenAgentIdentities[identityKey] = struct{}{}
		}
		extra, _ := model.EncodeExtra(p.Extra)
		if p.AuthType == model.AuthAgentIdentity {
			existing, findErr := h.findAgentIdentityImportTarget(p.AccountID)
			if findErr != nil && findErr != gorm.ErrRecordNotFound {
				skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
				continue
			}
			if existing != nil {
				updates := map[string]any{
					"auth_type":      model.AuthAgentIdentity,
					"api_key":        crypto.EncryptedString(""),
					"access_token":   crypto.EncryptedString(""),
					"refresh_token":  crypto.EncryptedString(""),
					"expires_at":     nil,
					"email":          p.Email,
					"account_id":     p.AccountID,
					"extra":          extra,
					"status":         model.StatusActive,
					"error_count":    0,
					"cooldown_until": nil,
					"last_error":     "",
				}
				if baseURL := firstNonEmpty(p.BaseURL, req.BaseURL); baseURL != "" {
					updates["base_url"] = baseURL
				}
				if req.ProxyID > 0 || existing.ProxyID != 0 {
					updates["proxy_id"] = req.ProxyID
				}
				if p.Priority != nil {
					updates["priority"] = *p.Priority
				} else if req.Priority != nil {
					updates["priority"] = *req.Priority
				}
				if p.Concurrency != nil {
					updates["concurrency"] = *p.Concurrency
				} else if req.Concurrency != nil {
					updates["concurrency"] = *req.Concurrency
				}
				if err := h.db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Model(existing).Updates(updates).Error; err != nil {
						return err
					}
					return appendUpstreamAccountGroups(tx, existing.ID, groups)
				}); err != nil {
					skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
					continue
				}
				updated = append(updated, existing.Name)
				continue
			}
		}
		if p.AuthType == model.AuthOAuth && strings.TrimSpace(p.AccountID) != "" {
			existing, findErr := h.findOAuthImportTarget(group.Platform, p.AccountID)
			if findErr != nil && findErr != gorm.ErrRecordNotFound {
				skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
				continue
			}
			if existing != nil {
				mergedExtra := existing.DecodeExtra()
				for key, value := range p.Extra {
					mergedExtra[key] = value
				}
				extra, _ = model.EncodeExtra(mergedExtra)
				updates := map[string]any{
					"email":          firstNonEmpty(p.Email, existing.Email),
					"account_id":     p.AccountID,
					"extra":          extra,
					"status":         model.StatusActive,
					"error_count":    0,
					"cooldown_until": nil,
					"last_error":     "",
				}
				if p.AccessToken != "" {
					updates["access_token"] = crypto.EncryptedString(p.AccessToken)
				}
				if p.RefreshToken != "" {
					updates["refresh_token"] = crypto.EncryptedString(p.RefreshToken)
				}
				if p.ExpiresAt != nil {
					updates["expires_at"] = p.ExpiresAt
				}
				if baseURL := firstNonEmpty(p.BaseURL, req.BaseURL); baseURL != "" {
					updates["base_url"] = baseURL
				}
				if req.ProxyID > 0 || existing.ProxyID != 0 {
					updates["proxy_id"] = req.ProxyID
				}
				if p.Priority != nil {
					updates["priority"] = *p.Priority
				} else if req.Priority != nil {
					updates["priority"] = *req.Priority
				}
				if p.Concurrency != nil {
					updates["concurrency"] = *p.Concurrency
				} else if req.Concurrency != nil {
					updates["concurrency"] = *req.Concurrency
				}
				if err := h.db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Model(existing).Updates(updates).Error; err != nil {
						return err
					}
					return appendUpstreamAccountGroups(tx, existing.ID, groups)
				}); err != nil {
					skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
					continue
				}
				updated = append(updated, existing.Name)
				continue
			}
		}
		maxDisplayOrder++
		acc := model.UpstreamAccount{
			GroupID: group.ID, ProxyID: req.ProxyID, Name: p.Name, Platform: group.Platform,
			AuthType:     p.AuthType,
			BaseURL:      firstNonEmpty(p.BaseURL, req.BaseURL),
			APIKey:       crypto.EncryptedString(p.APIKey),
			AccessToken:  crypto.EncryptedString(p.AccessToken),
			RefreshToken: crypto.EncryptedString(p.RefreshToken),
			ExpiresAt:    p.ExpiresAt, Email: p.Email, AccountID: p.AccountID,
			Extra:    extra,
			Priority: 10, DisplayOrder: maxDisplayOrder, Status: model.StatusActive,
		}
		if p.Priority != nil {
			acc.Priority = *p.Priority
		} else if req.Priority != nil {
			acc.Priority = *req.Priority
		}
		if p.Concurrency != nil {
			acc.Concurrency = *p.Concurrency
		} else if req.Concurrency != nil {
			acc.Concurrency = *req.Concurrency
		}
		if err := h.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&acc).Error; err != nil {
				return err
			}
			return tx.Create(accountGroupBindings(acc.ID, groups)).Error
		}); err != nil {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
			continue
		}
		imported = append(imported, acc.Name)
	}
	reasons := make(map[string]int)
	for _, item := range skipped {
		if reason, ok := item["reason"].(string); ok && reason != "" {
			reasons[reason]++
		}
	}
	log.Printf("account import result: group_id=%d group_platform=%s imported=%d updated=%d skipped=%d reasons=%v", group.ID, group.Platform, len(imported), len(updated), len(skipped), reasons)

	util.OK(c, gin.H{
		"imported":       len(imported),
		"updated":        len(updated),
		"skipped":        len(skipped),
		"imported_names": imported,
		"updated_names":  updated,
		"skipped_detail": skipped,
	})
}

func (h *AdminHandler) findOAuthImportTarget(platform, accountID string) (*model.UpstreamAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var existing model.UpstreamAccount
	err := h.db.Where(
		"platform = ? AND auth_type = ? AND account_id = ?",
		platform, model.AuthOAuth, accountID,
	).Order("id ASC").First(&existing).Error
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// findAgentIdentityImportTarget mirrors Sub2API's Agent Identity key: one
// durable runtime per ChatGPT account. Re-importing the same account rotates
// the runtime/private key in place; different Team accounts remain isolated
// even when they belong to the same user.
func (h *AdminHandler) findAgentIdentityImportTarget(accountID string) (*model.UpstreamAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var existing model.UpstreamAccount
	err := h.db.Where(
		"platform = ? AND account_id = ?",
		model.PlatformOpenAI, accountID,
	).Order("id ASC").First(&existing).Error
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

func stringMapValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func boolMapValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- browser OAuth sign-in ----

type oauthStartReq struct {
	GroupID     int64    `json:"group_id"`
	GroupIDs    *[]int64 `json:"group_ids"`
	Name        string   `json:"name"`
	BaseURL     string   `json:"base_url"`
	Priority    *int     `json:"priority"`
	Concurrency *int     `json:"concurrency"`
}

// StartOAuthLogin creates a short-lived PKCE flow. The frontend opens the
// returned URL in a popup; the callback below creates the account after the
// authorization code has been exchanged server-side.
func (h *AdminHandler) StartOAuthLogin(c *gin.Context) {
	platform := c.Param("platform")
	if !oauth.SupportsOAuth(platform) {
		util.Fail(c, http.StatusBadRequest, "this platform does not support OAuth login")
		return
	}
	var req oauthStartReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "group_ids are required")
		return
	}
	groupIDs := normalizeAccountGroupIDs(req.GroupID, req.GroupIDs)
	groups, err := h.resolveAccountGroups(groupIDs, platform)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 0 || *req.Concurrency > 10000) {
		util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
		return
	}
	callbackURL, completionURL, err := h.oauth.CallbackURLs(platform, c.Request.Host, c.Request.TLS != nil)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	priority := 10
	concurrency := 0
	if req.Priority != nil {
		priority = *req.Priority
	}
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	authorizeURL, err := h.oauth.BeginLoginWithCompletion(platform, callbackURL, completionURL, oauth.LoginIntent{
		GroupID: groups[0].ID, GroupIDs: groupIDs, Name: trimAccountName(req.Name), BaseURL: strings.TrimSpace(req.BaseURL), Priority: priority, Concurrency: concurrency,
	})
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "start oauth login failed")
		return
	}
	payload := gin.H{"authorize_url": authorizeURL, "completion_mode": "callback"}
	if parsed, parseErr := url.Parse(authorizeURL); parseErr == nil {
		payload["state"] = parsed.Query().Get("state")
		if platform == model.PlatformOpenAI || (platform == model.PlatformAnthropic && parsed.Query().Get("code") == "true") {
			payload["completion_mode"] = "code"
		}
	}
	util.OK(c, payload)
}

func (h *AdminHandler) persistOAuthAccount(platform string, result *oauth.LoginResult) (*model.UpstreamAccount, error) {
	var requestedGroupIDs *[]int64
	if len(result.Intent.GroupIDs) > 0 {
		requestedGroupIDs = &result.Intent.GroupIDs
	}
	groups, err := h.resolveAccountGroups(normalizeAccountGroupIDs(result.Intent.GroupID, requestedGroupIDs), platform)
	if err != nil {
		return nil, errors.New("target group is unavailable or changed platform")
	}
	identity := oauth.IdentityFromIDToken(result.IDToken)
	email := identity.Email
	if email == "" {
		email = result.Email
	}
	accountID := identity.AccountID
	if accountID == "" {
		accountID = result.AccountID
	}
	if accountID != "" {
		var existing model.UpstreamAccount
		findErr := h.db.Where("platform = ? AND auth_type = ? AND account_id = ?", platform, model.AuthOAuth, accountID).Order("id ASC").First(&existing).Error
		if findErr == nil {
			updates := map[string]any{
				"access_token":   crypto.EncryptedString(result.AccessToken),
				"email":          firstNonEmpty(email, existing.Email),
				"status":         model.StatusActive,
				"error_count":    0,
				"last_error":     "",
				"cooldown_until": nil,
			}
			if result.ExpiresAt != nil {
				updates["expires_at"] = result.ExpiresAt
			}
			if result.RefreshToken != "" {
				updates["refresh_token"] = crypto.EncryptedString(result.RefreshToken)
			}
			if result.Intent.Name != "" {
				updates["name"] = trimAccountName(result.Intent.Name)
			}
			if result.Intent.BaseURL != "" {
				updates["base_url"] = result.Intent.BaseURL
			}
			extra := existing.DecodeExtra()
			if result.IDToken != "" {
				extra["id_token"] = result.IDToken
				if encoded, encodeErr := model.EncodeExtra(extra); encodeErr == nil {
					updates["extra"] = encoded
				}
			}
			if err := h.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&existing).Updates(updates).Error; err != nil {
					return err
				}
				return appendUpstreamAccountGroups(tx, existing.ID, groups)
			}); err != nil {
				return nil, err
			}
			if err := h.db.Preload("Group").Preload("Groups").First(&existing, existing.ID).Error; err != nil {
				return nil, err
			}
			hydrateUpstreamAccountGroups(&existing)
			return &existing, nil
		}
		if findErr != gorm.ErrRecordNotFound {
			return nil, findErr
		}
	}
	name := result.Intent.Name
	if name == "" {
		name = trimAccountName(email)
	}
	if name == "" {
		name = fmt.Sprintf("%s-oauth-%d", platform, time.Now().Unix())
	}
	extra := map[string]any{}
	if result.IDToken != "" {
		extra["id_token"] = result.IDToken
	}
	encodedExtra, err := model.EncodeExtra(extra)
	if err != nil {
		return nil, err
	}
	var maxDisplayOrder int
	_ = h.db.Model(&model.UpstreamAccount{}).Select("COALESCE(MAX(display_order), 0)").Scan(&maxDisplayOrder).Error
	account := model.UpstreamAccount{
		GroupID: groups[0].ID, Name: name, Platform: platform, BaseURL: result.Intent.BaseURL,
		AuthType:    model.AuthOAuth,
		AccessToken: crypto.EncryptedString(result.AccessToken), RefreshToken: crypto.EncryptedString(result.RefreshToken),
		ExpiresAt: result.ExpiresAt, Email: email, AccountID: accountID,
		Extra: encodedExtra, Priority: result.Intent.Priority, Concurrency: result.Intent.Concurrency, DisplayOrder: maxDisplayOrder + 1, Status: model.StatusActive,
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&account).Error; err != nil {
			return err
		}
		return tx.Create(accountGroupBindings(account.ID, groups)).Error
	}); err != nil {
		return nil, err
	}
	account.Group = &groups[0]
	account.Groups = groups
	account.GroupIDs = normalizeAccountGroupIDs(result.Intent.GroupID, requestedGroupIDs)
	return &account, nil
}

// FinishOAuthLogin completes providers whose registered callback cannot return
// directly to a remote console. It accepts either a code or the full callback
// URL copied from the browser address bar.
func (h *AdminHandler) FinishOAuthLogin(c *gin.Context) {
	platform := c.Param("platform")
	var req struct {
		State string `json:"state"`
		Code  string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.State) == "" || strings.TrimSpace(req.Code) == "" {
		util.Fail(c, http.StatusBadRequest, "oauth state and authorization code are required")
		return
	}
	result, err := h.oauth.CompleteLogin(c.Request.Context(), platform, strings.TrimSpace(req.State), strings.TrimSpace(req.Code))
	if err != nil {
		log.Printf("[oauth] complete %s login failed: %v", platform, err)
		util.Fail(c, http.StatusBadRequest, "OAuth 登录未完成，请重新发起授权并粘贴新的回调地址或授权码")
		return
	}
	account, err := h.persistOAuthAccount(platform, result)
	if err != nil {
		log.Printf("[oauth] persist %s account failed: %v", platform, err)
		util.Fail(c, http.StatusInternalServerError, "创建 OAuth 上游账号失败")
		return
	}
	util.OK(c, gin.H{"account_id": account.ID, "name": account.Name, "email": account.Email})
}

// CompleteOAuthLogin is intentionally unauthenticated: it is invoked by the
// provider redirect. The unguessable, one-time PKCE state proves that an admin
// previously initiated this exact flow.
func (h *AdminHandler) CompleteOAuthLogin(c *gin.Context) {
	platform := c.Param("platform")
	state := c.Query("state")
	if providerErr := c.Query("error"); providerErr != "" {
		origin := h.oauth.CancelLogin(platform, state)
		h.oauthCallbackPage(c, http.StatusBadRequest, "上游已取消或拒绝本次 OAuth 登录，请关闭此窗口后重试。", "error", origin)
		return
	}
	code := c.Query("code")
	if state == "" || code == "" {
		h.oauthCallbackPage(c, http.StatusBadRequest, "OAuth 回调缺少必要参数，请关闭此窗口后重试。", "error", "")
		return
	}
	result, err := h.oauth.CompleteLogin(c.Request.Context(), platform, state, code)
	if err != nil {
		h.oauthCallbackPage(c, http.StatusBadRequest, "OAuth 登录未完成，请关闭此窗口后重新发起登录。", "error", "")
		return
	}

	if _, err := h.persistOAuthAccount(platform, result); err != nil {
		h.oauthCallbackPage(c, http.StatusBadRequest, "目标分组不存在或平台已变更，请关闭此窗口后重试。", "error", result.Origin)
		return
	}
	h.oauthCallbackPage(c, http.StatusOK, "OAuth 登录成功，账号已添加。现在可以关闭此窗口。", "success", result.Origin)
}

func trimAccountName(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) <= 64 {
		return name
	}
	return string(runes[:64])
}

func (h *AdminHandler) oauthCallbackPage(c *gin.Context, status int, message, result, origin string) {
	payload, _ := json.Marshal(gin.H{"type": "dengdeng:oauth", "result": result, "message": message, "at": time.Now().UnixNano()})
	originJSON, _ := json.Marshal(origin)
	// This page is deliberately a tiny popup hand-off. It overrides the console
	// CSP/COOP so a cross-origin provider redirect can post its result back to
	// the opener; no user-controlled text is interpolated into executable JS.
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	c.Header("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	c.Header("Cache-Control", "no-store")
	script := fmt.Sprintf("<script>try{localStorage.setItem('dengdeng:oauth',JSON.stringify(%s))}catch(_){}</script>", payload)
	if origin != "" {
		script += fmt.Sprintf("<script>if(window.opener){window.opener.postMessage(%s,%s);setTimeout(function(){window.close()},120)}</script>", payload, originJSON)
	}
	body := fmt.Sprintf(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>OAuth 登录</title><style>body{margin:0;background:#0b1220;color:#e2e8f0;font:16px system-ui;display:grid;min-height:100vh;place-items:center}.box{max-width:360px;padding:28px;text-align:center}.ok{color:#34d399}.err{color:#fb7185}p{line-height:1.6;color:#94a3b8}</style><main class="box"><h1 class="%s">%s</h1><p>%s</p></main>%s</html>`,
		map[string]string{"success": "ok", "error": "err"}[result], map[string]string{"success": "登录完成", "error": "登录失败"}[result], html.EscapeString(message), script)
	c.Data(status, "text/html; charset=utf-8", []byte(body))
}

func (h *AdminHandler) DeleteAccount(c *gin.Context) {
	var account model.UpstreamAccount
	if err := h.db.First(&account, c.Param("id")).Error; err == nil {
		h.db.Where("upstream_account_id = ?", account.ID).Delete(&model.UpstreamAccountGroup{})
		h.db.Where("account_id = ?", account.ID).Delete(&model.AccountProbe{})
		h.db.Where("upstream_account_id = ?", account.ID).Delete(&model.AccountQuotaSnapshot{})
		h.db.Where("upstream_account_id = ?", account.ID).Delete(&model.CodexQuotaSnapshot{})
		h.db.Delete(&account)
	}
	util.OK(c, gin.H{"deleted": true})
}

// ---- model prices ----

func (h *AdminHandler) ListPrices(c *gin.Context) {
	var prices []model.ModelPrice
	h.db.Order("platform, match").Find(&prices)
	util.OK(c, prices)
}

type priceReq struct {
	Match               string  `json:"match" binding:"required,max=128"`
	Platform            string  `json:"platform"`
	InputPrice          float64 `json:"input_price"`
	OutputPrice         float64 `json:"output_price"`
	CacheReadPrice      float64 `json:"cache_read_price"`
	CacheWritePrice     float64 `json:"cache_write_price"`
	CacheWrite5mPrice   float64 `json:"cache_write_5m_price"`
	CacheWrite1hPrice   float64 `json:"cache_write_1h_price"`
	ImageInputPrice     float64 `json:"image_input_price"`
	ImageOutputPrice    float64 `json:"image_output_price"`
	ImageCacheReadPrice float64 `json:"image_cache_read_price"`
	ImagePricePerImage  float64 `json:"image_price_per_image"`
}

func (h *AdminHandler) UpsertPrice(c *gin.Context) {
	var req priceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "match is required")
		return
	}
	var price model.ModelPrice
	err := h.db.Where("match = ?", req.Match).First(&price).Error
	if err != nil {
		price = model.ModelPrice{Match: req.Match}
	}
	price.Platform = req.Platform
	price.InputPrice = req.InputPrice
	price.OutputPrice = req.OutputPrice
	price.CacheReadPrice = req.CacheReadPrice
	price.CacheWritePrice = req.CacheWritePrice
	price.CacheWrite5mPrice = req.CacheWrite5mPrice
	price.CacheWrite1hPrice = req.CacheWrite1hPrice
	price.ImageInputPrice = req.ImageInputPrice
	price.ImageOutputPrice = req.ImageOutputPrice
	price.ImageCacheReadPrice = req.ImageCacheReadPrice
	price.ImagePricePerImage = req.ImagePricePerImage
	if err := h.db.Save(&price).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "save price failed")
		return
	}
	h.pricing.Invalidate()
	util.OK(c, price)
}

func (h *AdminHandler) DeletePrice(c *gin.Context) {
	h.db.Delete(&model.ModelPrice{}, c.Param("id"))
	h.pricing.Invalidate()
	util.OK(c, gin.H{"deleted": true})
}

// ---- model aliases / configuration ----

func (h *AdminHandler) ListModels(c *gin.Context) {
	var configs []model.ModelConfig
	h.db.Order("platform, kind, name").Find(&configs)
	util.OK(c, configs)
}

type modelConfigReq struct {
	Name              string `json:"name" binding:"required,max=128"`
	Platform          string `json:"platform" binding:"required"`
	Kind              string `json:"kind"`
	UpstreamModel     string `json:"upstream_model"`
	ContextWindow     int64  `json:"context_window"`
	MaxOutputTokens   int64  `json:"max_output_tokens"`
	SupportsVision    bool   `json:"supports_vision"`
	SupportsTools     bool   `json:"supports_tools"`
	SupportsReasoning bool   `json:"supports_reasoning"`
	ImageGroupID      int64  `json:"image_group_id"`
	Description       string `json:"description"`
	Status            string `json:"status"`
}

func (h *AdminHandler) UpsertModel(c *gin.Context) {
	var req modelConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "name and platform are required")
		return
	}
	if !validPlatform(req.Platform) {
		util.Fail(c, http.StatusBadRequest, "invalid platform")
		return
	}
	if req.Kind == "" {
		req.Kind = "chat"
	}
	if req.Kind != "chat" && req.Kind != "image" {
		util.Fail(c, http.StatusBadRequest, "kind must be chat or image")
		return
	}
	if req.ContextWindow < 0 || req.MaxOutputTokens < 0 {
		util.Fail(c, http.StatusBadRequest, "model limits cannot be negative")
		return
	}
	if req.Status == "" {
		req.Status = model.StatusActive
	}
	if req.Status != model.StatusActive && req.Status != model.StatusDisabled {
		util.Fail(c, http.StatusBadRequest, "invalid status")
		return
	}
	if req.ImageGroupID > 0 {
		if req.Kind != "image" {
			util.Fail(c, http.StatusBadRequest, "image_group_id is only available for image models")
			return
		}
		var imageGroup model.Group
		if err := h.db.First(&imageGroup, req.ImageGroupID).Error; err != nil {
			util.Fail(c, http.StatusBadRequest, "image upstream group not found")
			return
		}
		if imageGroup.Platform != req.Platform {
			util.Fail(c, http.StatusBadRequest, "image upstream group platform must match model platform")
			return
		}
	}
	var cfg model.ModelConfig
	if err := h.db.Where("name = ?", req.Name).First(&cfg).Error; err != nil {
		cfg = model.ModelConfig{Name: req.Name}
	}
	cfg.Platform, cfg.Kind, cfg.UpstreamModel = req.Platform, req.Kind, strings.TrimSpace(req.UpstreamModel)
	cfg.ContextWindow, cfg.MaxOutputTokens = req.ContextWindow, req.MaxOutputTokens
	cfg.SupportsVision, cfg.SupportsTools, cfg.SupportsReasoning = req.SupportsVision, req.SupportsTools, req.SupportsReasoning
	cfg.ImageGroupID = req.ImageGroupID
	cfg.Description, cfg.Status = strings.TrimSpace(req.Description), req.Status
	if err := h.db.Save(&cfg).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "save model configuration failed")
		return
	}
	util.OK(c, cfg)
}

func (h *AdminHandler) DeleteModel(c *gin.Context) {
	h.db.Delete(&model.ModelConfig{}, c.Param("id"))
	util.OK(c, gin.H{"deleted": true})
}

// ---- redeem codes ----

func (h *AdminHandler) ListRedeemCodes(c *gin.Context) {
	codes := make([]model.RedeemCode, 0)
	h.db.Order("id DESC").Limit(500).Find(&codes)

	userIDs := map[int64]bool{}
	for _, cd := range codes {
		if cd.UsedBy != nil {
			userIDs[*cd.UsedBy] = true
		}
	}
	emails := map[int64]string{}
	if len(userIDs) > 0 {
		var us []model.User
		h.db.Where("id IN ?", keys(userIDs)).Find(&us)
		for _, u := range us {
			emails[u.ID] = u.Email
		}
	}
	for i := range codes {
		if codes[i].UsedBy != nil {
			codes[i].UsedByEmail = emails[*codes[i].UsedBy]
		}
	}
	util.OK(c, codes)
}

type genCodesReq struct {
	Count       int    `json:"count" binding:"required,min=1,max=200"`
	Kind        string `json:"kind"`
	AmountMicro int64  `json:"amount_micro"`
	Value       int64  `json:"value"`
}

func (h *AdminHandler) GenerateRedeemCodes(c *gin.Context) {
	var req genCodesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "count must be between 1 and 200")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = model.RedeemKindAmount
	}
	switch kind {
	case model.RedeemKindAmount:
		if req.AmountMicro < 1 {
			util.Fail(c, http.StatusBadRequest, "amount_micro must be greater than 0")
			return
		}
	case model.RedeemKindDays:
		if req.Value < 1 || req.Value > 3660 {
			util.Fail(c, http.StatusBadRequest, "days must be between 1 and 3660")
			return
		}
	case model.RedeemKindRequests:
		if req.Value < 1 || req.Value > 10000000 {
			util.Fail(c, http.StatusBadRequest, "requests must be between 1 and 10000000")
			return
		}
	default:
		util.Fail(c, http.StatusBadRequest, "kind must be amount, days, or requests")
		return
	}
	batch := time.Now().Format("20060102-150405")
	codes := make([]model.RedeemCode, req.Count)
	plains := make([]string, req.Count)
	for i := range codes {
		plain := "dd-gift-" + util.RandomToken(24)
		codes[i] = model.RedeemCode{
			Code: plain, Kind: kind, AmountMicro: req.AmountMicro, Value: req.Value, Batch: batch,
		}
		plains[i] = plain
	}
	if err := h.db.Create(&codes).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "generate failed")
		return
	}
	util.OK(c, gin.H{"batch": batch, "codes": plains})
}

func (h *AdminHandler) DeleteRedeemCode(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.db.Where("id = ? AND used_by IS NULL", id).Delete(&model.RedeemCode{})
	util.OK(c, gin.H{"deleted": true})
}
