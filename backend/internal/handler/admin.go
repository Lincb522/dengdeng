package handler

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
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
	db      *gorm.DB
	pricing *service.PricingService
	rates   *service.UserGroupRateResolver
	oauth   *oauth.Manager
	monitor *service.AccountMonitor
	quota   *service.AccountQuotaService
	runtime *service.RuntimeMetrics
	// codexQuotaHTTPClient carries the deployment-wide outbound route. Account
	// specific proxies still take precedence for individual quota lookups.
	codexQuotaHTTPClient *http.Client
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
	h.db.Model(&model.UpstreamAccount{}).
		Select("group_id AS group_id, COUNT(*) AS total, SUM(CASE WHEN status = 'active' AND (cooldown_until IS NULL OR cooldown_until < ?) THEN 1 ELSE 0 END) AS alive", time.Now()).
		Group("group_id").Scan(&rows)
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
	Name                   string  `json:"name" binding:"required,max=64"`
	Platform               string  `json:"platform" binding:"required"`
	Description            string  `json:"description"`
	RateMultiplier         float64 `json:"rate_multiplier"`
	CacheReadMultiplier    float64 `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier float64 `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier float64 `json:"cache_write_1h_multiplier"`
	ImageRateIndependent   *bool   `json:"image_rate_independent"`
	ImageRateMultiplier    float64 `json:"image_rate_multiplier"`
	IsPublic               *bool   `json:"is_public"`
	Status                 string  `json:"status"`
}

// groupUpdateReq intentionally uses pointers throughout. A group may be made
// private with `is_public: false`; a value-type boolean would make that change
// indistinguishable from an omitted field in partial updates.
type groupUpdateReq struct {
	Name                   *string  `json:"name"`
	Description            *string  `json:"description"`
	RateMultiplier         *float64 `json:"rate_multiplier"`
	CacheReadMultiplier    *float64 `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier *float64 `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier *float64 `json:"cache_write_1h_multiplier"`
	ImageRateIndependent   *bool    `json:"image_rate_independent"`
	ImageRateMultiplier    *float64 `json:"image_rate_multiplier"`
	IsPublic               *bool    `json:"is_public"`
	Status                 *string  `json:"status"`
}

func validPlatform(p string) bool {
	for _, x := range model.AllPlatforms {
		if x == p {
			return true
		}
	}
	return false
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
		ImageRateMultiplier: 1, IsPublic: true, Status: model.StatusActive,
	}
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

func (h *AdminHandler) DeleteGroup(c *gin.Context) {
	id := c.Param("id")
	var keyCount int64
	h.db.Model(&model.APIKey{}).Where("group_id = ?", id).Count(&keyCount)
	if keyCount > 0 {
		util.Fail(c, http.StatusBadRequest, "group still has API keys bound to it")
		return
	}
	h.db.Where("group_id = ?", id).Delete(&model.UpstreamAccount{})
	h.db.Where("group_id = ?", id).Delete(&model.UserGroupRate{})
	h.db.Delete(&model.Group{}, id)
	util.OK(c, gin.H{"deleted": true})
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
	if query.AuthType != "" && query.AuthType != model.AuthAPIKey && query.AuthType != model.AuthOAuth {
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
		q = q.Where("upstream_accounts.group_id = ?", query.GroupID)
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

	column := ""
	switch query.Sort {
	case "name":
		column = "upstream_accounts.name"
	case "platform":
		column = "upstream_accounts.platform"
	case "group":
		q = q.Joins("LEFT JOIN groups account_groups ON account_groups.id = upstream_accounts.group_id").Select("upstream_accounts.*")
		column = "account_groups.name"
	case "priority":
		column = "upstream_accounts.priority"
	case "availability":
		column = `CASE
			WHEN upstream_accounts.status <> 'active' THEN 0
			WHEN upstream_accounts.auth_type = 'oauth' AND upstream_accounts.expires_at IS NOT NULL AND upstream_accounts.expires_at <= CURRENT_TIMESTAMP THEN 0
			WHEN upstream_accounts.cooldown_until IS NOT NULL AND upstream_accounts.cooldown_until > CURRENT_TIMESTAMP THEN 10
			WHEN upstream_accounts.error_count >= 4 THEN 45
			WHEN upstream_accounts.error_count > 0 THEN 75
			ELSE 100 END`
	case "last_used":
		// Put never-used accounts last in either direction; a NULL position must
		// not depend on the database engine's default NULL ordering.
		q = q.Order("CASE WHEN upstream_accounts.last_used_at IS NULL THEN 1 ELSE 0 END ASC")
		column = "upstream_accounts.last_used_at"
	}
	return q.Order(column + " " + strings.ToUpper(query.Order)).Order("upstream_accounts.id DESC")
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
		list := applyAccountListFilters(h.db.Model(&model.UpstreamAccount{}).Preload("Group").Preload("Proxy").Preload("Quota").Preload("CodexQuota"), query)
		if err := applyAccountListOrder(list, query).Offset((query.Page - 1) * query.Size).Limit(query.Size).Find(&accounts).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "query accounts failed")
			return
		}
		util.OK(c, gin.H{"items": accounts, "total": total, "page": query.Page, "size": query.Size})
		return
	}

	var accounts []model.UpstreamAccount
	q := h.db.Preload("Group").Preload("Proxy").Preload("Quota").Preload("CodexQuota")
	if gid := c.Query("group_id"); gid != "" {
		q = q.Where("group_id = ?", gid)
	}
	q.Order("CASE WHEN display_order = 0 THEN 1 ELSE 0 END ASC").Order("display_order ASC").Order("id DESC").Find(&accounts)
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
	ProxyID      *int64     `json:"proxy_id"`
	Name         string     `json:"name"`
	BaseURL      string     `json:"base_url"`
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

func (h *AdminHandler) CreateAccount(c *gin.Context) {
	var req accountReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || req.Name == "" {
		util.Fail(c, http.StatusBadRequest, "group_id and name are required")
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
	if req.Concurrency != nil && (*req.Concurrency < 0 || *req.Concurrency > 10000) {
		util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
		return
	}
	var group model.Group
	if err := h.db.First(&group, req.GroupID).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
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
		GroupID: group.ID, ProxyID: proxyID, Name: req.Name, Platform: group.Platform,
		BaseURL: req.BaseURL, AuthType: authType,
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
	if err := h.db.Create(&acc).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "create account failed")
		return
	}
	acc.Group = &group
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
	updates := map[string]any{"base_url": req.BaseURL}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.AuthType == model.AuthAPIKey || req.AuthType == model.AuthOAuth {
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
	h.db.Model(&acc).Updates(updates)
	h.db.Preload("Group").Preload("Proxy").First(&acc, acc.ID)
	util.OK(c, acc)
}

type importReq struct {
	GroupID     int64  `json:"group_id"`
	Format      string `json:"format"` // sub2api | cpa | auto
	Data        string `json:"data"`   // raw export JSON
	BaseURL     string `json:"base_url"`
	Priority    *int   `json:"priority"`
	Concurrency *int   `json:"concurrency"`
	SkipExpired bool   `json:"skip_expired"`
}

// ImportAccounts bulk-creates upstream accounts from a sub2api or cpa export.
// Accounts whose platform differs from the target group are skipped.
func (h *AdminHandler) ImportAccounts(c *gin.Context) {
	var req importReq
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 || req.Data == "" {
		util.Fail(c, http.StatusBadRequest, "group_id and data are required")
		return
	}
	if req.Concurrency != nil && (*req.Concurrency < 0 || *req.Concurrency > 10000) {
		util.Fail(c, http.StatusBadRequest, "account concurrency must be between 0 and 10000")
		return
	}
	var group model.Group
	if err := h.db.First(&group, req.GroupID).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
		return
	}

	parsed, err := importer.Parse(req.Format, []byte(req.Data))
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "parse failed: "+err.Error())
		return
	}

	imported := make([]string, 0, len(parsed))
	skipped := make([]gin.H, 0)
	now := time.Now()
	var maxDisplayOrder int
	_ = h.db.Model(&model.UpstreamAccount{}).Select("COALESCE(MAX(display_order), 0)").Scan(&maxDisplayOrder).Error
	for _, p := range parsed {
		if p.Concurrency != nil && (*p.Concurrency < 0 || *p.Concurrency > 10000) {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "invalid concurrency"})
			continue
		}
		if p.Platform != "" && p.Platform != group.Platform {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "platform " + p.Platform + " != group " + group.Platform})
			continue
		}
		if req.SkipExpired && p.ExpiresAt != nil && p.ExpiresAt.Before(now) {
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
		extra, _ := model.EncodeExtra(p.Extra)
		maxDisplayOrder++
		acc := model.UpstreamAccount{
			GroupID: group.ID, Name: p.Name, Platform: group.Platform,
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
		if err := h.db.Create(&acc).Error; err != nil {
			skipped = append(skipped, gin.H{"name": p.Name, "reason": "db error"})
			continue
		}
		imported = append(imported, acc.Name)
	}

	util.OK(c, gin.H{
		"imported":       len(imported),
		"skipped":        len(skipped),
		"imported_names": imported,
		"skipped_detail": skipped,
	})
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
	GroupID     int64  `json:"group_id"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	Priority    *int   `json:"priority"`
	Concurrency *int   `json:"concurrency"`
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
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID == 0 {
		util.Fail(c, http.StatusBadRequest, "group_id is required")
		return
	}
	var group model.Group
	if err := h.db.First(&group, req.GroupID).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "group not found")
		return
	}
	if group.Platform != platform {
		util.Fail(c, http.StatusBadRequest, "selected group does not match OAuth platform")
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
		GroupID: group.ID, Name: trimAccountName(req.Name), BaseURL: strings.TrimSpace(req.BaseURL), Priority: priority, Concurrency: concurrency,
	})
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "start oauth login failed")
		return
	}
	util.OK(c, gin.H{"authorize_url": authorizeURL})
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

	var group model.Group
	if err := h.db.First(&group, result.Intent.GroupID).Error; err != nil || group.Platform != platform {
		h.oauthCallbackPage(c, http.StatusBadRequest, "目标分组不存在或平台已变更，请关闭此窗口后重试。", "error", result.Origin)
		return
	}
	identity := oauth.IdentityFromIDToken(result.IDToken)
	name := result.Intent.Name
	if name == "" {
		name = trimAccountName(identity.Email)
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
		h.oauthCallbackPage(c, http.StatusInternalServerError, "保存 OAuth 凭据失败，请关闭此窗口后重试。", "error", result.Origin)
		return
	}
	var maxDisplayOrder int
	_ = h.db.Model(&model.UpstreamAccount{}).Select("COALESCE(MAX(display_order), 0)").Scan(&maxDisplayOrder).Error
	account := model.UpstreamAccount{
		GroupID: group.ID, Name: name, Platform: platform, BaseURL: result.Intent.BaseURL,
		AuthType:    model.AuthOAuth,
		AccessToken: crypto.EncryptedString(result.AccessToken), RefreshToken: crypto.EncryptedString(result.RefreshToken),
		ExpiresAt: result.ExpiresAt, Email: identity.Email, AccountID: identity.AccountID,
		Extra: encodedExtra, Priority: result.Intent.Priority, Concurrency: result.Intent.Concurrency, DisplayOrder: maxDisplayOrder + 1, Status: model.StatusActive,
	}
	if err := h.db.Create(&account).Error; err != nil {
		h.oauthCallbackPage(c, http.StatusInternalServerError, "创建上游账号失败，请关闭此窗口后重试。", "error", result.Origin)
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
