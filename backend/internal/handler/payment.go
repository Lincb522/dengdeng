package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/payment"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

const maxPaymentWebhookBody = 1 << 20

type PaymentHandler struct{ payments *service.PaymentService }

func NewPaymentHandler(payments *service.PaymentService) *PaymentHandler {
	return &PaymentHandler{payments: payments}
}

func (h *PaymentHandler) Config(c *gin.Context) {
	info, err := h.payments.CheckoutInfo()
	if err != nil {
		internalError(c, err)
		return
	}
	util.OK(c, info)
}
func (h *PaymentHandler) CreateOrder(c *gin.Context) {
	var req struct {
		AmountMinor   int64  `json:"amount_minor" binding:"required,gt=0"`
		PaymentMethod string `json:"payment_method" binding:"required,max=32"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "amount_minor and payment_method are required")
		return
	}
	user := middleware.CurrentUser(c)
	order, err := h.payments.CreateOrder(c.Request.Context(), user.ID, req.AmountMinor, req.PaymentMethod, c.ClientIP(), isMobileRequest(c.Request.UserAgent()))
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}
func (h *PaymentHandler) ListMyOrders(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.payments.ListUserOrders(middleware.CurrentUser(c).ID, limit)
	if err != nil {
		internalError(c, err)
		return
	}
	util.OK(c, items)
}
func (h *PaymentHandler) GetOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.GetUserOrder(middleware.CurrentUser(c).ID, id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}
func (h *PaymentHandler) VerifyOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.VerifyOrder(c.Request.Context(), middleware.CurrentUser(c).ID, id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}
func (h *PaymentHandler) CancelOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.CancelOrder(c.Request.Context(), middleware.CurrentUser(c).ID, id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}
func (h *PaymentHandler) RequestRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.RequestRefund(middleware.CurrentUser(c).ID, id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}

func (h *PaymentHandler) Webhook(c *gin.Context) {
	key := strings.TrimSpace(c.Param("provider"))
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPaymentWebhookBody))
	if err != nil {
		c.String(http.StatusBadRequest, "invalid body")
		return
	}
	headers := map[string]string{}
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	err = h.payments.HandleWebhook(c.Request.Context(), key, raw, headers)
	if err != nil && !errors.Is(err, service.ErrOrderNotFound) {
		c.String(http.StatusBadRequest, "verify failed")
		return
	}
	switch key {
	case payment.ProviderWxPay:
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "成功"})
	case payment.ProviderStripe, payment.ProviderAirwallex:
		c.Status(http.StatusOK)
	default:
		c.String(http.StatusOK, "success")
	}
}

type AdminPaymentHandler struct{ payments *service.PaymentService }

func NewAdminPaymentHandler(payments *service.PaymentService) *AdminPaymentHandler {
	return &AdminPaymentHandler{payments: payments}
}
func (h *AdminPaymentHandler) GetConfig(c *gin.Context) {
	cfg, err := h.payments.Config()
	if err != nil {
		internalError(c, err)
		return
	}
	util.OK(c, cfg)
}
func (h *AdminPaymentHandler) UpdateConfig(c *gin.Context) {
	var req struct {
		Enabled             bool   `json:"enabled"`
		Currency            string `json:"currency"`
		CreditMicroPerUnit  int64  `json:"credit_micro_per_unit"`
		MinAmountMinor      int64  `json:"min_amount_minor"`
		MaxAmountMinor      int64  `json:"max_amount_minor"`
		DailyLimitMinor     int64  `json:"daily_limit_minor"`
		OrderExpiryMinutes  int    `json:"order_expiry_minutes"`
		MaxPendingOrders    int    `json:"max_pending_orders"`
		LoadBalanceStrategy string `json:"load_balance_strategy"`
		ProductName         string `json:"product_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid payment config")
		return
	}
	cfg := model.PaymentConfig{ID: 1, Enabled: req.Enabled, Currency: req.Currency, CreditMicroPerUnit: req.CreditMicroPerUnit, MinAmountMinor: req.MinAmountMinor, MaxAmountMinor: req.MaxAmountMinor, DailyLimitMinor: req.DailyLimitMinor, OrderExpiryMinutes: req.OrderExpiryMinutes, MaxPendingOrders: req.MaxPendingOrders, LoadBalanceStrategy: req.LoadBalanceStrategy, ProductName: req.ProductName}
	saved, err := h.payments.UpdateConfig(cfg)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, saved)
}
func (h *AdminPaymentHandler) ListProviders(c *gin.Context) {
	items, err := h.payments.ListProviders()
	if err != nil {
		internalError(c, err)
		return
	}
	util.OK(c, items)
}

type providerReq struct {
	Name             string            `json:"name"`
	ProviderKey      string            `json:"provider_key"`
	Currency         string            `json:"currency"`
	SupportedMethods string            `json:"supported_methods"`
	PaymentMode      string            `json:"payment_mode"`
	Status           string            `json:"status"`
	Config           map[string]string `json:"config"`
	MinAmountMinor   int64             `json:"min_amount_minor"`
	MaxAmountMinor   int64             `json:"max_amount_minor"`
	DailyLimitMinor  int64             `json:"daily_limit_minor"`
	Priority         int               `json:"priority"`
}

func inputFromProviderReq(req providerReq) service.ProviderInput {
	return service.ProviderInput{Name: req.Name, ProviderKey: req.ProviderKey, Currency: req.Currency, SupportedMethods: req.SupportedMethods, PaymentMode: req.PaymentMode, Status: req.Status, Config: req.Config, MinAmountMinor: req.MinAmountMinor, MaxAmountMinor: req.MaxAmountMinor, DailyLimitMinor: req.DailyLimitMinor, Priority: req.Priority}
}
func (h *AdminPaymentHandler) CreateProvider(c *gin.Context) {
	var req providerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid provider data")
		return
	}
	item, err := h.payments.SaveProvider(0, inputFromProviderReq(req))
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, item)
}
func (h *AdminPaymentHandler) UpdateProvider(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid provider id")
		return
	}
	var req providerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid provider data")
		return
	}
	item, err := h.payments.SaveProvider(id, inputFromProviderReq(req))
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, item)
}
func (h *AdminPaymentHandler) DeleteProvider(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid provider id")
		return
	}
	if err := h.payments.DeleteProvider(id); err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, gin.H{"deleted": true})
}
func (h *AdminPaymentHandler) ListOrders(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := h.payments.ListOrders(limit)
	if err != nil {
		internalError(c, err)
		return
	}
	util.OK(c, items)
}
func (h *AdminPaymentHandler) ListLedger(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	result, err := h.payments.ListLedger(service.PaymentLedgerFilter{
		Page:        page,
		Size:        size,
		Period:      c.DefaultQuery("period", "30d"),
		Kind:        c.Query("kind"),
		Currency:    c.Query("currency"),
		ProviderKey: c.Query("provider"),
		User:        c.Query("user"),
	})
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, result)
}
func (h *AdminPaymentHandler) ProcessRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.ProcessRefund(c.Request.Context(), id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}
func (h *AdminPaymentHandler) FinalizeRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := h.payments.FinalizeRefund(c.Request.Context(), id)
	if err != nil {
		paymentError(c, err)
		return
	}
	util.OK(c, order)
}

func paymentError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrOrderNotFound) {
		util.Fail(c, http.StatusNotFound, "payment order not found")
		return
	}
	if errors.Is(err, service.ErrPaymentDisabled) {
		util.Fail(c, http.StatusForbidden, "online payment is not enabled")
		return
	}
	util.Fail(c, http.StatusBadRequest, err.Error())
}
func internalError(c *gin.Context, err error) {
	util.Fail(c, http.StatusInternalServerError, "payment service error")
}
func isMobileRequest(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	return strings.Contains(ua, "mobile") || strings.Contains(ua, "android") || strings.Contains(ua, "iphone")
}
