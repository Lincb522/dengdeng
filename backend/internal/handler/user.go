package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type UserHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewUserHandler(db *gorm.DB, cfg *config.Config) *UserHandler {
	return &UserHandler{db: db, cfg: cfg}
}

func (h *UserHandler) Me(c *gin.Context) {
	util.OK(c, middleware.CurrentUser(c))
}

type changePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8,max=72"`
}

func (h *UserHandler) ChangePassword(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if !util.CheckPassword(user.PasswordHash, req.OldPassword) {
		util.Fail(c, http.StatusBadRequest, "old password incorrect")
		return
	}
	hash, err := util.HashPassword(req.NewPassword)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "hash failed")
		return
	}
	// Bump TokenVersion to invalidate other sessions, then re-issue a token
	// for this one so the current user stays signed in.
	newVer := user.TokenVersion + 1
	h.db.Model(user).Updates(map[string]any{"password_hash": hash, "token_version": newVer})
	claims := middleware.CurrentClaims(c)
	mfa := user.TOTPEnabled && claims != nil && claims.MFA
	token, err := h.signBoundToken(c, user, newVer, mfa)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "sign token failed")
		return
	}
	util.OK(c, gin.H{"changed": true, "token": token})
}

type totpPasswordReq struct {
	Password string `json:"password" binding:"required"`
}

type totpEnableReq struct {
	Password string `json:"password" binding:"required"`
	Secret   string `json:"secret" binding:"required"`
	Code     string `json:"code" binding:"required"`
}

type totpDisableReq struct {
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required"`
}

func (h *UserHandler) SetupTOTP(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req totpPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil || !util.CheckPassword(user.PasswordHash, req.Password) {
		util.Fail(c, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	secret, err := util.NewTOTPSecret()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "generate authenticator secret failed")
		return
	}
	issuer := "DengDeng AI"
	if h.cfg != nil && strings.TrimSpace(h.cfg.Site.Name) != "" {
		issuer = strings.TrimSpace(h.cfg.Site.Name)
	}
	util.OK(c, gin.H{"secret": secret, "otpauth_url": util.TOTPUri(issuer, user.Email, secret)})
}

func (h *UserHandler) EnableTOTP(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req totpEnableReq
	if err := c.ShouldBindJSON(&req); err != nil || !util.CheckPassword(user.PasswordHash, req.Password) {
		util.Fail(c, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if !util.ValidateTOTP(req.Secret, req.Code, time.Now()) {
		util.Fail(c, http.StatusBadRequest, "authenticator code is invalid")
		return
	}
	newVer := user.TokenVersion + 1
	if err := h.db.Model(user).Updates(map[string]any{
		"totp_enabled": true, "totp_secret": crypto.EncryptedString(strings.ToUpper(strings.TrimSpace(req.Secret))), "token_version": newVer,
	}).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "enable authenticator failed")
		return
	}
	token, err := h.signBoundToken(c, user, newVer, true)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "sign token failed")
		return
	}
	util.OK(c, gin.H{"enabled": true, "token": token})
}

func (h *UserHandler) DisableTOTP(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req totpDisableReq
	if err := c.ShouldBindJSON(&req); err != nil || !util.CheckPassword(user.PasswordHash, req.Password) {
		util.Fail(c, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if !user.TOTPEnabled || !util.ValidateTOTP(string(user.TOTPSecret), req.Code, time.Now()) {
		util.Fail(c, http.StatusBadRequest, "authenticator code is invalid")
		return
	}
	newVer := user.TokenVersion + 1
	if err := h.db.Model(user).Updates(map[string]any{"totp_enabled": false, "totp_secret": "", "token_version": newVer}).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "disable authenticator failed")
		return
	}
	token, err := h.signBoundToken(c, user, newVer, false)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "sign token failed")
		return
	}
	util.OK(c, gin.H{"enabled": false, "token": token})
}

func (h *UserHandler) signBoundToken(c *gin.Context, user *model.User, version int, mfa bool) (string, error) {
	return util.SignJWTBound(
		h.cfg.JWT.Secret, user.ID, user.Role, version,
		time.Duration(h.cfg.JWT.ExpireHour)*time.Hour,
		util.SessionFingerprint(h.cfg.JWT.Secret, c.Request.UserAgent()), mfa,
	)
}

// ---- API keys ----

func (h *UserHandler) ListKeys(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var keys []model.APIKey
	h.db.Preload("Group").Preload("Groups").Where("user_id = ?", user.ID).Order("id DESC").Find(&keys)
	for i := range keys {
		hydrateAPIKeyGroups(&keys[i])
	}
	util.OK(c, keys)
}

type createKeyReq struct {
	Name            string     `json:"name" binding:"required,max=64"`
	GroupID         int64      `json:"group_id"`
	GroupIDs        []int64    `json:"group_ids"`
	ReasoningEffort string     `json:"reasoning_effort"`
	QuotaMicro      int64      `json:"quota_micro"`
	DailyQuotaMicro int64      `json:"daily_quota_micro"`
	RPM             int64      `json:"rpm"`
	Concurrency     int        `json:"concurrency"`
	AllowedIPs      string     `json:"allowed_ips"`
	BlockedIPs      string     `json:"blocked_ips"`
	ExpiresAt       *time.Time `json:"expires_at"`
}

func (h *UserHandler) CreateKey(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req createKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "name and group_ids are required")
		return
	}
	groupIDs := normalizeKeyGroupIDs(req.GroupID, req.GroupIDs)
	if len(groupIDs) == 0 {
		util.Fail(c, http.StatusBadRequest, "at least one group is required")
		return
	}
	if req.QuotaMicro < 0 || req.DailyQuotaMicro < 0 {
		util.Fail(c, http.StatusBadRequest, "key quotas cannot be negative")
		return
	}
	if req.Concurrency < 0 || req.Concurrency > 10000 {
		util.Fail(c, http.StatusBadRequest, "key concurrency must be between 0 and 10000")
		return
	}
	reasoningEffort, err := normalizeReasoningEffort(req.ReasoningEffort)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := normalizeKeyPolicy(req.RPM, req.AllowedIPs, req.BlockedIPs, req.ExpiresAt)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	groups, status, message := h.resolveKeyGroups(user, groupIDs)
	if status != 0 {
		util.Fail(c, status, message)
		return
	}
	plain, hash, preview := util.NewAPIKey()
	key := model.APIKey{
		UserID:          user.ID,
		GroupID:         groups[0].ID,
		KeyHash:         hash,
		KeyPreview:      preview,
		Name:            req.Name,
		Status:          model.StatusActive,
		ReasoningEffort: reasoningEffort,
		QuotaMicro:      req.QuotaMicro, DailyQuotaMicro: req.DailyQuotaMicro,
		RPM: policy.rpm, Concurrency: req.Concurrency, AllowedIPs: policy.allowedIPs, BlockedIPs: policy.blockedIPs, ExpiresAt: policy.expiresAt,
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&key).Error; err != nil {
			return err
		}
		return tx.Create(keyGroupBindings(key.ID, groupIDs)).Error
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "create key failed")
		return
	}
	key.Group = &groups[0]
	key.Groups = groups
	key.GroupIDs = groupIDs
	// plaintext is returned exactly once, at creation time
	util.OK(c, gin.H{"key": key, "plain": plain})
}

func (h *UserHandler) UpdateKey(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var key model.APIKey
	if err := h.db.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&key).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "key not found")
		return
	}
	var req struct {
		Name            *string          `json:"name"`
		Status          *string          `json:"status"`
		GroupID         *int64           `json:"group_id"`
		GroupIDs        *[]int64         `json:"group_ids"`
		ReasoningEffort *string          `json:"reasoning_effort"`
		QuotaMicro      *int64           `json:"quota_micro"`
		DailyQuotaMicro *int64           `json:"daily_quota_micro"`
		RPM             *int64           `json:"rpm"`
		Concurrency     *int             `json:"concurrency"`
		AllowedIPs      *string          `json:"allowed_ips"`
		BlockedIPs      *string          `json:"blocked_ips"`
		ExpiresAt       *json.RawMessage `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	updates := map[string]any{}
	var selectedGroupIDs []int64
	groupsChanged := false
	if req.Name != nil && *req.Name != "" {
		updates["name"] = *req.Name
	}
	if req.Status != nil && (*req.Status == model.StatusActive || *req.Status == model.StatusDisabled) {
		updates["status"] = *req.Status
	}
	if req.ReasoningEffort != nil {
		reasoningEffort, err := normalizeReasoningEffort(*req.ReasoningEffort)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updates["reasoning_effort"] = reasoningEffort
	}
	if req.GroupIDs != nil {
		selectedGroupIDs = normalizeKeyGroupIDs(0, *req.GroupIDs)
		groupsChanged = true
	} else if req.GroupID != nil {
		selectedGroupIDs = normalizeKeyGroupIDs(*req.GroupID, nil)
		groupsChanged = true
	}
	if groupsChanged {
		if len(selectedGroupIDs) == 0 {
			util.Fail(c, http.StatusBadRequest, "at least one group is required")
			return
		}
		_, status, message := h.resolveKeyGroups(user, selectedGroupIDs)
		if status != 0 {
			util.Fail(c, status, message)
			return
		}
		updates["group_id"] = selectedGroupIDs[0]
	}
	if req.QuotaMicro != nil {
		if *req.QuotaMicro < 0 {
			util.Fail(c, http.StatusBadRequest, "key quota cannot be negative")
			return
		}
		updates["quota_micro"] = *req.QuotaMicro
	}
	if req.DailyQuotaMicro != nil {
		if *req.DailyQuotaMicro < 0 {
			util.Fail(c, http.StatusBadRequest, "key daily quota cannot be negative")
			return
		}
		updates["daily_quota_micro"] = *req.DailyQuotaMicro
	}
	if req.Concurrency != nil {
		if *req.Concurrency < 0 || *req.Concurrency > 10000 {
			util.Fail(c, http.StatusBadRequest, "key concurrency must be between 0 and 10000")
			return
		}
		updates["concurrency"] = *req.Concurrency
	}
	if req.RPM != nil || req.AllowedIPs != nil || req.BlockedIPs != nil || req.ExpiresAt != nil {
		rpm, allowed, blocked, expiresAt := int64(key.RPM), key.AllowedIPs, key.BlockedIPs, key.ExpiresAt
		if req.RPM != nil {
			rpm = *req.RPM
		}
		if req.AllowedIPs != nil {
			allowed = *req.AllowedIPs
		}
		if req.BlockedIPs != nil {
			blocked = *req.BlockedIPs
		}
		if req.ExpiresAt != nil {
			raw := strings.TrimSpace(string(*req.ExpiresAt))
			expiresAt = nil
			if raw != "" && raw != "null" && raw != `\"\"` {
				var parsed time.Time
				if err := json.Unmarshal(*req.ExpiresAt, &parsed); err != nil {
					util.Fail(c, http.StatusBadRequest, "invalid key expiry")
					return
				}
				expiresAt = &parsed
			}
		}
		policy, policyErr := normalizeKeyPolicy(rpm, allowed, blocked, expiresAt)
		if policyErr != nil {
			util.Fail(c, http.StatusBadRequest, policyErr.Error())
			return
		}
		updates["rpm"], updates["allowed_ips"], updates["blocked_ips"], updates["expires_at"] = policy.rpm, policy.allowedIPs, policy.blockedIPs, policy.expiresAt
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&key).Updates(updates).Error; err != nil {
				return err
			}
		}
		if groupsChanged {
			if err := tx.Where("api_key_id = ?", key.ID).Delete(&model.APIKeyGroup{}).Error; err != nil {
				return err
			}
			if err := tx.Create(keyGroupBindings(key.ID, selectedGroupIDs)).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "update key failed")
		return
	}
	h.db.Preload("Group").Preload("Groups").First(&key, key.ID)
	hydrateAPIKeyGroups(&key)
	util.OK(c, key)
}

const maxKeyGroups = 32

func normalizeKeyGroupIDs(legacyID int64, values []int64) []int64 {
	if len(values) == 0 && legacyID > 0 {
		values = []int64{legacyID}
	}
	result := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
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

func (h *UserHandler) resolveKeyGroups(user *model.User, ids []int64) ([]model.Group, int, string) {
	if len(ids) == 0 || len(ids) > maxKeyGroups {
		return nil, http.StatusBadRequest, "select between 1 and 32 groups"
	}
	var found []model.Group
	if err := h.db.Where("id IN ?", ids).Find(&found).Error; err != nil {
		return nil, http.StatusInternalServerError, "load groups failed"
	}
	byID := make(map[int64]model.Group, len(found))
	for _, group := range found {
		byID[group.ID] = group
	}
	ordered := make([]model.Group, 0, len(ids))
	for _, id := range ids {
		group, ok := byID[id]
		if !ok {
			return nil, http.StatusNotFound, "group not found"
		}
		if group.Status != model.StatusActive {
			return nil, http.StatusBadRequest, "group is disabled"
		}
		if !group.IsPublic && user.Role != model.RoleAdmin {
			return nil, http.StatusForbidden, "group is not open"
		}
		ordered = append(ordered, group)
	}
	return ordered, 0, ""
}

func keyGroupBindings(keyID int64, groupIDs []int64) []model.APIKeyGroup {
	bindings := make([]model.APIKeyGroup, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		bindings = append(bindings, model.APIKeyGroup{APIKeyID: keyID, GroupID: groupID})
	}
	return bindings
}

func hydrateAPIKeyGroups(key *model.APIKey) {
	if key == nil {
		return
	}
	ordered := make([]model.Group, 0, len(key.Groups)+1)
	seen := make(map[int64]struct{}, len(key.Groups)+1)
	if key.Group != nil && key.Group.ID > 0 {
		ordered = append(ordered, *key.Group)
		seen[key.Group.ID] = struct{}{}
	}
	for _, group := range key.Groups {
		if _, exists := seen[group.ID]; exists {
			continue
		}
		ordered = append(ordered, group)
		seen[group.ID] = struct{}{}
	}
	key.Groups = ordered
	key.GroupIDs = make([]int64, 0, len(ordered))
	for _, group := range ordered {
		key.GroupIDs = append(key.GroupIDs, group.ID)
	}
}

type keyPolicy struct {
	rpm                    int
	allowedIPs, blockedIPs string
	expiresAt              *time.Time
}

// normalizeReasoningEffort stores GPT-5.6's supported effort values plus
// "auto" (= follow the client/model). Legacy fast/minimal values migrate to
// low so saved keys, pricing rules and usage logs share one vocabulary.
func normalizeReasoningEffort(value string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "", "auto":
		return "auto", nil
	case "fast", "minimal":
		return "low", nil
	case "none", "low", "medium", "high", "xhigh", "max":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid reasoning effort")
	}
}

func normalizeKeyPolicy(rpm int64, allowed, blocked string, expiresAt *time.Time) (keyPolicy, error) {
	if rpm < 0 || rpm > 100000 {
		return keyPolicy{}, fmt.Errorf("key rpm must be between 0 and 100000")
	}
	allowed, err := util.NormalizeIPRules(allowed)
	if err != nil {
		return keyPolicy{}, err
	}
	blocked, err = util.NormalizeIPRules(blocked)
	if err != nil {
		return keyPolicy{}, err
	}
	if expiresAt != nil && !expiresAt.After(time.Now().UTC()) {
		return keyPolicy{}, fmt.Errorf("key expiry must be in the future")
	}
	return keyPolicy{rpm: int(rpm), allowedIPs: allowed, blockedIPs: blocked, expiresAt: expiresAt}, nil
}

func (h *UserHandler) DeleteKey(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var key model.APIKey
	if err := h.db.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&key).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "key not found")
		return
	}
	res := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("api_key_id = ?", key.ID).Delete(&model.APIKeyGroup{}).Error; err != nil {
			return err
		}
		return tx.Delete(&key).Error
	})
	if res != nil {
		util.Fail(c, http.StatusInternalServerError, "delete key failed")
		return
	}
	util.OK(c, gin.H{"deleted": true})
}

// RotateKey replaces the credential material without changing its quotas,
// group binding, or historical usage. We retain only a hash by design, so an
// existing key can never be shown again; rotation is the safe way to recover a
// copy for a new CLI installation.
func (h *UserHandler) RotateKey(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var key model.APIKey
	if err := h.db.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&key).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "key not found")
		return
	}
	plain, hash, preview := util.NewAPIKey()
	if err := h.db.Model(&key).Updates(map[string]any{
		"key_hash": hash, "key_preview": preview, "last_used_at": nil,
	}).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "rotate key failed")
		return
	}
	h.db.Preload("Group").Preload("Groups").First(&key, key.ID)
	hydrateAPIKeyGroups(&key)
	util.OK(c, gin.H{"key": key, "plain": plain})
}

// ---- groups visible to the user ----

func (h *UserHandler) ListGroups(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var groups []model.Group
	q := h.db.Where("status = ?", model.StatusActive)
	if user.Role != model.RoleAdmin {
		q = q.Where("is_public = ?", true)
	}
	q.Order("id").Find(&groups)
	util.OK(c, groups)
}

type catalogueGroup struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Platform             string  `json:"platform"`
	RateMultiplier       float64 `json:"rate_multiplier"`
	ImageRateIndependent bool    `json:"image_rate_independent"`
	ImageRateMultiplier  float64 `json:"image_rate_multiplier"`
	Ready                bool    `json:"ready"`
}

type catalogueModel struct {
	ID                int64             `json:"id"`
	Name              string            `json:"name"`
	Platform          string            `json:"platform"`
	Kind              string            `json:"kind"`
	ContextWindow     int64             `json:"context_window"`
	MaxOutputTokens   int64             `json:"max_output_tokens"`
	SupportsVision    bool              `json:"supports_vision"`
	SupportsTools     bool              `json:"supports_tools"`
	SupportsReasoning bool              `json:"supports_reasoning"`
	Description       string            `json:"description"`
	Available         bool              `json:"available"`
	Groups            []catalogueGroup  `json:"groups"`
	Pricing           *model.ModelPrice `json:"pricing,omitempty"`
}

// ModelCatalogue is the user-facing model plaza source. It is derived from
// the same active aliases and price rules the gateway uses, while deliberately
// keeping account credentials and private routing data out of the response.
func (h *UserHandler) ModelCatalogue(c *gin.Context) {
	user := middleware.CurrentUser(c)
	h.modelCatalogue(c, user.Role == model.RoleAdmin)
}

// PublicModelCatalogue powers the standalone model plaza. It exposes only
// active public groups and non-secret catalogue metadata, so browsing it does
// not require a console account.
func (h *UserHandler) PublicModelCatalogue(c *gin.Context) {
	h.modelCatalogue(c, false)
}

func (h *UserHandler) modelCatalogue(c *gin.Context, includePrivate bool) {
	var groups []model.Group
	groupQuery := h.db.Where("status = ?", model.StatusActive)
	if !includePrivate {
		groupQuery = groupQuery.Where("is_public = ?", true)
	}
	if err := groupQuery.Order("platform, id").Find(&groups).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load model groups failed")
		return
	}

	var readyGroupIDs []int64
	now := time.Now()
	if err := h.db.Model(&model.UpstreamAccount{}).
		Where("status = ? AND (cooldown_until IS NULL OR cooldown_until <= ?)", model.StatusActive, now).
		Distinct("group_id").Pluck("group_id", &readyGroupIDs).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load upstream status failed")
		return
	}
	ready := make(map[int64]bool, len(readyGroupIDs))
	for _, id := range readyGroupIDs {
		ready[id] = true
	}
	groupsByPlatform := make(map[string][]catalogueGroup)
	for _, group := range groups {
		groupsByPlatform[group.Platform] = append(groupsByPlatform[group.Platform], catalogueGroup{
			ID: group.ID, Name: group.Name, Platform: group.Platform,
			RateMultiplier: group.RateMultiplier, ImageRateIndependent: group.ImageRateIndependent,
			ImageRateMultiplier: group.ImageRateMultiplier, Ready: ready[group.ID],
		})
	}

	var configs []model.ModelConfig
	if err := h.db.Where("status = ?", model.StatusActive).Order("platform, kind, name").Find(&configs).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load model catalogue failed")
		return
	}
	var prices []model.ModelPrice
	if err := h.db.Find(&prices).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load model prices failed")
		return
	}

	items := make([]catalogueModel, 0, len(configs))
	for _, cfg := range configs {
		itemGroups := groupsByPlatform[cfg.Platform]
		if itemGroups == nil {
			// Keep the JSON contract stable: Vue clients may safely render an
			// empty group list even before any public group is configured.
			itemGroups = make([]catalogueGroup, 0)
		}
		available := false
		for _, group := range itemGroups {
			if group.Ready {
				available = true
				break
			}
		}
		items = append(items, catalogueModel{
			ID: cfg.ID, Name: cfg.Name, Platform: cfg.Platform, Kind: cfg.Kind,
			ContextWindow: cfg.ContextWindow, MaxOutputTokens: cfg.MaxOutputTokens,
			SupportsVision: cfg.SupportsVision, SupportsTools: cfg.SupportsTools,
			SupportsReasoning: cfg.SupportsReasoning, Description: cfg.Description,
			Available: available, Groups: itemGroups, Pricing: matchCataloguePrice(cfg.Name, prices),
		})
	}
	util.OK(c, items)
}

func matchCataloguePrice(name string, prices []model.ModelPrice) *model.ModelPrice {
	var best *model.ModelPrice
	bestLength := -1
	for i := range prices {
		price := &prices[i]
		if price.Match == name {
			return price
		}
		if strings.HasSuffix(price.Match, "*") {
			prefix := strings.TrimSuffix(price.Match, "*")
			if strings.HasPrefix(name, prefix) && len(prefix) > bestLength {
				best, bestLength = price, len(prefix)
			}
		}
	}
	return best
}

// ---- usage ----

func (h *UserHandler) Usage(c *gin.Context) {
	user := middleware.CurrentUser(c)
	filter, err := parseUsageQuery(c)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	logs, total, err := queryUsage(h.db, filter, &user.ID)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "query usage failed")
		return
	}
	util.OK(c, gin.H{"items": logs, "total": total, "page": filter.Page, "size": filter.Size})
}

// ExportUsage lets a user download their own ledger without exposing a JWT in
// the URL or leaking routing/account information from the operator view.
func (h *UserHandler) ExportUsage(c *gin.Context) {
	user := middleware.CurrentUser(c)
	filter, err := parseUsageQuery(c)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := prepareUsageExport(&filter); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := writeUsageCSV(c, h.db, filter, &user.ID, false); err != nil {
		util.Fail(c, http.StatusInternalServerError, "export usage failed")
	}
}

func (h *UserHandler) UsageSummary(c *gin.Context) {
	user := middleware.CurrentUser(c)
	util.OK(c, usageSummary(h.db, &user.ID))
}

// ---- redeem ----

func (h *UserHandler) Redeem(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "code required")
		return
	}
	var (
		amount  int64
		kind    string
		value   int64
		expires *time.Time
	)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var code model.RedeemCode
		if err := tx.Where("code = ? AND used_by IS NULL", req.Code).First(&code).Error; err != nil {
			return err
		}
		now := time.Now()
		res := tx.Model(&model.RedeemCode{}).
			Where("id = ? AND used_by IS NULL", code.ID).
			Updates(map[string]any{"used_by": user.ID, "used_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		kind = code.Kind
		if kind == "" { // legacy money codes predate the entitlement modes.
			kind = model.RedeemKindAmount
		}
		value = code.Value

		switch kind {
		case model.RedeemKindAmount:
			amount = code.AmountMicro
			return tx.Model(&model.User{}).Where("id = ?", user.ID).
				Update("balance_micro", gorm.Expr("balance_micro + ?", code.AmountMicro)).Error
		case model.RedeemKindDays:
			var recipient model.User
			if err := tx.First(&recipient, user.ID).Error; err != nil {
				return err
			}
			start := now
			if recipient.AccessExpiresAt != nil && recipient.AccessExpiresAt.After(now) {
				start = *recipient.AccessExpiresAt
			}
			until := start.AddDate(0, 0, int(code.Value))
			expires = &until
			return tx.Model(&model.User{}).Where("id = ?", user.ID).
				Update("access_expires_at", until).Error
		case model.RedeemKindRequests:
			return tx.Model(&model.User{}).Where("id = ?", user.ID).
				Update("remaining_requests", gorm.Expr("remaining_requests + ?", code.Value)).Error
		default:
			return gorm.ErrInvalidData
		}
	})
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid or already used code")
		return
	}
	response := gin.H{"kind": kind, "value": value, "amount_micro": amount}
	if expires != nil {
		response["access_expires_at"] = expires
	}
	util.OK(c, response)
}
