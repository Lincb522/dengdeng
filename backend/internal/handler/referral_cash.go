package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

type ReferralCashHandler struct {
	cash *service.ReferralCashService
}

func NewReferralCashHandler(cash *service.ReferralCashService) *ReferralCashHandler {
	return &ReferralCashHandler{cash: cash}
}

func (h *ReferralCashHandler) SaveMyPayoutAccount(c *gin.Context) {
	var req struct {
		OpenID string `json:"openid" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "WeChat OpenID is required")
		return
	}
	item, err := h.cash.SavePayoutAccount(middleware.CurrentUser(c).ID, req.OpenID, model.ReferralPayoutAccountPending, "", false)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) RequestPayout(c *gin.Context) {
	item, err := h.cash.RequestPayout(c.Request.Context(), middleware.CurrentUser(c).ID)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) ListMyPayouts(c *gin.Context) {
	items, err := h.cash.ListUserPayouts(middleware.CurrentUser(c).ID)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, items)
}

func (h *ReferralCashHandler) WxPayWebhook(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPaymentWebhookBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "invalid body"})
		return
	}
	headers := map[string]string{}
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	if err := h.cash.HandleWxPayWebhook(c.Request.Context(), raw, headers); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "FAIL", "message": "verify failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
}

func (h *ReferralCashHandler) GetConfig(c *gin.Context) {
	item, err := h.cash.Config()
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) UpdateConfig(c *gin.Context) {
	var req model.ReferralPayoutConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid cash payout config")
		return
	}
	item, err := h.cash.UpdateConfig(req)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) ListAccounts(c *gin.Context) {
	items, err := h.cash.ListPayoutAccounts()
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, items)
}

func (h *ReferralCashHandler) SaveAccount(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid user id")
		return
	}
	var req struct {
		OpenID string `json:"openid"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid payout account")
		return
	}
	var item service.ReferralPayoutAccountView
	if strings.TrimSpace(req.OpenID) != "" {
		item, err = h.cash.SavePayoutAccount(userID, req.OpenID, req.Status, req.Note, true)
	} else {
		item, err = h.cash.ReviewPayoutAccount(userID, req.Status, req.Note)
	}
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) ListPayouts(c *gin.Context) {
	items, err := h.cash.ListPayouts()
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, items)
}

func (h *ReferralCashHandler) ApprovePayout(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid payout id")
		return
	}
	item, err := h.cash.ApprovePayout(c.Request.Context(), id)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) RejectPayout(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid payout id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	item, err := h.cash.RejectPayout(id, req.Reason)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ReferralCashHandler) QueryPayout(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid payout id")
		return
	}
	item, err := h.cash.QueryPayout(c.Request.Context(), id)
	if err != nil {
		referralCashError(c, err)
		return
	}
	util.OK(c, item)
}

func referralCashError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrReferralCashDisabled) {
		util.Fail(c, http.StatusForbidden, err.Error())
		return
	}
	if errors.Is(err, service.ErrReferralPayoutNotFound) {
		util.Fail(c, http.StatusNotFound, err.Error())
		return
	}
	util.Fail(c, http.StatusBadRequest, err.Error())
}
