package handler

import (
	"errors"
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

const (
	minReferralCommissionBps = 500
	maxReferralCommissionBps = 1000
	defaultReferralBps       = 500
)

type referralCodeStats struct {
	ID              int64     `json:"id"`
	Code            string    `json:"code"`
	OwnerUserID     int64     `json:"owner_user_id"`
	OwnerEmail      string    `json:"owner_email"`
	CommissionBps   int       `json:"commission_bps"`
	Status          string    `json:"status"`
	ReferredUsers   int64     `json:"referred_users"`
	CommissionMicro int64     `json:"commission_micro"`
	CreatedAt       time.Time `json:"created_at"`
}

func normalizeReferralCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func validReferralCode(value string) bool {
	if len(value) < 4 || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func createReferralCode(db *gorm.DB, ownerUserID int64, requested string, commissionBps int) (*model.ReferralCode, error) {
	if commissionBps < minReferralCommissionBps || commissionBps > maxReferralCommissionBps {
		return nil, errors.New("commission must be between 5% and 10%")
	}
	var existing int64
	if err := db.Model(&model.ReferralCode{}).Where("owner_user_id = ?", ownerUserID).Count(&existing).Error; err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, errors.New("this user already has a referral code")
	}
	code := normalizeReferralCode(requested)
	if code != "" && !validReferralCode(code) {
		return nil, errors.New("referral code must be 4-32 letters, numbers or hyphens")
	}
	for attempt := 0; attempt < 6; attempt++ {
		if code == "" {
			code = "DD-" + strings.ToUpper(util.RandomToken(10))
		}
		item := model.ReferralCode{
			Code: code, OwnerUserID: ownerUserID, CommissionBps: commissionBps, Status: model.StatusActive,
		}
		if err := db.Create(&item).Error; err == nil {
			return &item, nil
		} else if requested != "" || !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, err
		}
		code = ""
	}
	return nil, errors.New("generate referral code failed")
}

func referralCodeWithStats(db *gorm.DB, code model.ReferralCode) referralCodeStats {
	var referredUsers, commissionMicro int64
	db.Model(&model.ReferralBinding{}).Where("referral_code_id = ?", code.ID).Count(&referredUsers)
	db.Model(&model.ReferralCommission{}).Where("referral_code_id = ?", code.ID).
		Select("COALESCE(SUM(amount_micro), 0)").Scan(&commissionMicro)
	ownerEmail := ""
	if code.Owner != nil {
		ownerEmail = code.Owner.Email
	} else {
		var owner model.User
		if db.Select("email").First(&owner, code.OwnerUserID).Error == nil {
			ownerEmail = owner.Email
		}
	}
	return referralCodeStats{
		ID: code.ID, Code: code.Code, OwnerUserID: code.OwnerUserID, OwnerEmail: ownerEmail,
		CommissionBps: code.CommissionBps, Status: code.Status, ReferredUsers: referredUsers,
		CommissionMicro: commissionMicro, CreatedAt: code.CreatedAt,
	}
}

// ReferralDashboard returns both sides of the current user's referral state:
// the code they were invited with and the code/stats they can share.
func (h *UserHandler) ReferralDashboard(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var codes []model.ReferralCode
	h.db.Preload("Owner").Where("owner_user_id = ?", user.ID).Order("id DESC").Find(&codes)
	codeStats := make([]referralCodeStats, 0, len(codes))
	for _, code := range codes {
		codeStats = append(codeStats, referralCodeWithStats(h.db, code))
	}

	var binding model.ReferralBinding
	var bindingPayload any
	if h.db.Where("referred_user_id = ?", user.ID).First(&binding).Error == nil {
		var code model.ReferralCode
		var referrer model.User
		h.db.First(&code, binding.ReferralCodeID)
		h.db.Select("id", "email").First(&referrer, binding.ReferrerUserID)
		bindingPayload = gin.H{
			"code": code.Code, "referrer_email": referrer.Email, "bound_at": binding.CreatedAt,
		}
	}

	type commissionRow struct {
		model.ReferralCommission
		ReferredEmail string `json:"referred_email"`
		Code          string `json:"code"`
	}
	var commissions []commissionRow
	h.db.Table("referral_commissions").
		Select("referral_commissions.*, users.email AS referred_email, referral_codes.code").
		Joins("LEFT JOIN users ON users.id = referral_commissions.referred_user_id").
		Joins("LEFT JOIN referral_codes ON referral_codes.id = referral_commissions.referral_code_id").
		Where("referral_commissions.referrer_user_id = ?", user.ID).
		Order("referral_commissions.id DESC").Limit(100).Scan(&commissions)
	var totalMicro int64
	h.db.Model(&model.ReferralCommission{}).Where("referrer_user_id = ?", user.ID).
		Select("COALESCE(SUM(amount_micro), 0)").Scan(&totalMicro)

	util.OK(c, gin.H{
		"binding": bindingPayload, "codes": codeStats, "commissions": commissions,
		"total_commission_micro": totalMicro,
	})
}

func (h *UserHandler) CreateMyReferralCode(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var existing model.ReferralCode
	if err := h.db.Preload("Owner").Where("owner_user_id = ?", user.ID).First(&existing).Error; err == nil {
		util.OK(c, referralCodeWithStats(h.db, existing))
		return
	}
	code, err := createReferralCode(h.db, user.ID, "", defaultReferralBps)
	if err != nil {
		util.Fail(c, http.StatusConflict, "create referral code failed")
		return
	}
	code.Owner = user
	util.OK(c, referralCodeWithStats(h.db, *code))
}

type bindReferralReq struct {
	Code string `json:"code" binding:"required"`
}

func (h *UserHandler) BindReferralCode(c *gin.Context) {
	user := middleware.CurrentUser(c)
	var req bindReferralReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "referral code is required")
		return
	}
	codeText := normalizeReferralCode(req.Code)
	var code model.ReferralCode
	if err := h.db.Where("code = ? AND status = ?", codeText, model.StatusActive).First(&code).Error; err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid referral code")
		return
	}
	if code.OwnerUserID == user.ID {
		util.Fail(c, http.StatusBadRequest, "you cannot bind your own referral code")
		return
	}
	var owner model.User
	if err := h.db.First(&owner, code.OwnerUserID).Error; err != nil || owner.Status != model.StatusActive {
		util.Fail(c, http.StatusBadRequest, "referral code is unavailable")
		return
	}
	binding := model.ReferralBinding{
		ReferralCodeID: code.ID, ReferrerUserID: code.OwnerUserID, ReferredUserID: user.ID,
	}
	if err := h.db.Create(&binding).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			util.Fail(c, http.StatusConflict, "a referral code is already bound")
			return
		}
		util.Fail(c, http.StatusInternalServerError, "bind referral code failed")
		return
	}
	util.OK(c, gin.H{"bound": true, "code": code.Code})
}

func (h *AdminHandler) ListReferralCodes(c *gin.Context) {
	var codes []model.ReferralCode
	h.db.Preload("Owner").Order("id DESC").Limit(1000).Find(&codes)
	items := make([]referralCodeStats, 0, len(codes))
	for _, code := range codes {
		items = append(items, referralCodeWithStats(h.db, code))
	}
	util.OK(c, items)
}

type adminCreateReferralReq struct {
	OwnerUserID   int64  `json:"owner_user_id" binding:"required"`
	Code          string `json:"code"`
	CommissionBps int    `json:"commission_bps"`
}

func (h *AdminHandler) CreateReferralCode(c *gin.Context) {
	var req adminCreateReferralReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "owner user is required")
		return
	}
	if req.CommissionBps == 0 {
		req.CommissionBps = defaultReferralBps
	}
	var owner model.User
	if err := h.db.First(&owner, req.OwnerUserID).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "owner user not found")
		return
	}
	code, err := createReferralCode(h.db, owner.ID, req.Code, req.CommissionBps)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	code.Owner = &owner
	util.OK(c, referralCodeWithStats(h.db, *code))
}

type adminUpdateReferralReq struct {
	CommissionBps *int    `json:"commission_bps"`
	Status        *string `json:"status"`
}

func (h *AdminHandler) UpdateReferralCode(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var code model.ReferralCode
	if id <= 0 || h.db.First(&code, id).Error != nil {
		util.Fail(c, http.StatusNotFound, "referral code not found")
		return
	}
	var req adminUpdateReferralReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	updates := map[string]any{}
	if req.CommissionBps != nil {
		if *req.CommissionBps < minReferralCommissionBps || *req.CommissionBps > maxReferralCommissionBps {
			util.Fail(c, http.StatusBadRequest, "commission must be between 5% and 10%")
			return
		}
		updates["commission_bps"] = *req.CommissionBps
	}
	if req.Status != nil {
		if *req.Status != model.StatusActive && *req.Status != model.StatusDisabled {
			util.Fail(c, http.StatusBadRequest, "status must be active or disabled")
			return
		}
		updates["status"] = *req.Status
	}
	if len(updates) > 0 {
		if err := h.db.Model(&code).Updates(updates).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "update referral code failed")
			return
		}
	}
	h.db.Preload("Owner").First(&code, code.ID)
	util.OK(c, referralCodeWithStats(h.db, code))
}

func (h *AdminHandler) DeleteReferralCode(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var count int64
	h.db.Model(&model.ReferralBinding{}).Where("referral_code_id = ?", id).Count(&count)
	if count > 0 {
		util.Fail(c, http.StatusConflict, "used referral codes can only be disabled")
		return
	}
	result := h.db.Delete(&model.ReferralCode{}, id)
	if result.Error != nil {
		util.Fail(c, http.StatusInternalServerError, "delete referral code failed")
		return
	}
	util.OK(c, gin.H{"deleted": result.RowsAffected > 0})
}
