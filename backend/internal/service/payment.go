package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/payment"
	"dengdeng/internal/payment/provider"
	"dengdeng/internal/util"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPaymentDisabled = errors.New("online payment is not enabled")
	ErrOrderNotFound   = errors.New("payment order not found")
)

const paymentLease = 5 * time.Minute

const (
	paymentReconcileInterval = 15 * time.Second
	paymentQueryTimeout      = 10 * time.Second
	pendingReconcileBatch    = 100
)

// PaymentService ports the Sub2API lifecycle into this application's GORM
// schema. Provider adapters only speak to merchants; this service is the sole
// writer for a user's balance.
type PaymentService struct {
	db                  *gorm.DB
	publicURL, siteName string
}

func NewPaymentService(db *gorm.DB, cfg *config.Config) *PaymentService {
	return &PaymentService{db: db, publicURL: strings.TrimRight(strings.TrimSpace(cfg.Site.PublicURL), "/"), siteName: cfg.Site.Name}
}

func (s *PaymentService) Config() (model.PaymentConfig, error) {
	var cfg model.PaymentConfig
	err := s.db.First(&cfg, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cfg = model.PaymentConfig{ID: 1, Currency: "CNY", MinAmountMinor: 100, MaxAmountMinor: 1_000_000, OrderExpiryMinutes: 30, MaxPendingOrders: 3, LoadBalanceStrategy: "round_robin", ProductName: s.siteName + " 账户充值"}
		err = s.db.Create(&cfg).Error
	}
	return cfg, err
}

func (s *PaymentService) UpdateConfig(next model.PaymentConfig) (model.PaymentConfig, error) {
	if _, err := payment.NormalizeCurrency(next.Currency); err != nil {
		return model.PaymentConfig{}, err
	}
	if next.MinAmountMinor <= 0 || next.MaxAmountMinor < next.MinAmountMinor {
		return model.PaymentConfig{}, errors.New("invalid payment amount limits")
	}
	if next.OrderExpiryMinutes < 5 || next.OrderExpiryMinutes > 24*60 {
		return model.PaymentConfig{}, errors.New("order expiry must be between 5 and 1440 minutes")
	}
	if next.MaxPendingOrders < 1 || next.MaxPendingOrders > 50 {
		return model.PaymentConfig{}, errors.New("max pending orders must be between 1 and 50")
	}
	if next.LoadBalanceStrategy != "round_robin" && next.LoadBalanceStrategy != "least_amount" {
		return model.PaymentConfig{}, errors.New("unknown load balance strategy")
	}
	if next.Enabled && (next.CreditMicroPerUnit <= 0 || s.publicURL == "") {
		return model.PaymentConfig{}, errors.New("enable payment requires a positive credit rate and site.public_url")
	}
	next.ID = 1
	if err := s.db.Save(&next).Error; err != nil {
		return model.PaymentConfig{}, err
	}
	if err := s.db.First(&next, 1).Error; err != nil {
		return model.PaymentConfig{}, err
	}
	return next, nil
}

// ProviderView keeps encrypted JSON out of every list and detail response.
type ProviderView struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	ProviderKey      string     `json:"provider_key"`
	Currency         string     `json:"currency"`
	SupportedMethods string     `json:"supported_methods"`
	PaymentMode      string     `json:"payment_mode"`
	Status           string     `json:"status"`
	MinAmountMinor   int64      `json:"min_amount_minor"`
	MaxAmountMinor   int64      `json:"max_amount_minor"`
	DailyLimitMinor  int64      `json:"daily_limit_minor"`
	Priority         int        `json:"priority"`
	LastSelectedAt   *time.Time `json:"last_selected_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func providerView(p model.PaymentProviderInstance) ProviderView {
	return ProviderView{ID: p.ID, Name: p.Name, ProviderKey: p.ProviderKey, Currency: p.Currency, SupportedMethods: p.SupportedMethods, PaymentMode: p.PaymentMode, Status: p.Status, MinAmountMinor: p.MinAmountMinor, MaxAmountMinor: p.MaxAmountMinor, DailyLimitMinor: p.DailyLimitMinor, Priority: p.Priority, LastSelectedAt: p.LastSelectedAt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

type ProviderInput struct {
	Name, ProviderKey, Currency, SupportedMethods, PaymentMode, Status string `json:"-"`
	Config                                                             map[string]string
	MinAmountMinor, MaxAmountMinor, DailyLimitMinor                    int64
	Priority                                                           int
}

func (s *PaymentService) ListProviders() ([]ProviderView, error) {
	var items []model.PaymentProviderInstance
	if err := s.db.Order("priority DESC, id").Find(&items).Error; err != nil {
		return nil, err
	}
	result := make([]ProviderView, len(items))
	for i := range items {
		result[i] = providerView(items[i])
	}
	return result, nil
}
func (s *PaymentService) SaveProvider(id int64, input ProviderInput) (ProviderView, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.ProviderKey = strings.TrimSpace(input.ProviderKey)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Name == "" {
		return ProviderView{}, errors.New("provider name is required")
	}
	if _, ok := payment.SupportedProviders[input.ProviderKey]; !ok {
		return ProviderView{}, errors.New("unsupported provider")
	}
	if _, err := payment.NormalizeCurrency(input.Currency); err != nil {
		return ProviderView{}, err
	}
	if input.PaymentMode == "" {
		input.PaymentMode = "qrcode"
	}
	if input.Status == "" {
		input.Status = model.StatusActive
	}
	if input.Status != model.StatusActive && input.Status != model.StatusDisabled {
		return ProviderView{}, errors.New("invalid provider status")
	}
	if input.Config == nil {
		return ProviderView{}, errors.New("provider config is required")
	}
	if _, err := provider.New(input.ProviderKey, input.Config); err != nil {
		return ProviderView{}, err
	}
	raw, err := json.Marshal(input.Config)
	if err != nil {
		return ProviderView{}, err
	}
	item := model.PaymentProviderInstance{Name: input.Name, ProviderKey: input.ProviderKey, Currency: input.Currency, SupportedMethods: normalizeMethods(input.SupportedMethods), PaymentMode: input.PaymentMode, Config: crypto.EncryptedString(raw), Status: input.Status, MinAmountMinor: input.MinAmountMinor, MaxAmountMinor: input.MaxAmountMinor, DailyLimitMinor: input.DailyLimitMinor, Priority: input.Priority}
	if id > 0 {
		var old model.PaymentProviderInstance
		if err := s.db.First(&old, id).Error; err != nil {
			return ProviderView{}, ErrOrderNotFound
		}
		item.ID = id
		if err := s.db.Model(&old).Select("name", "provider_key", "currency", "supported_methods", "payment_mode", "config", "status", "min_amount_minor", "max_amount_minor", "daily_limit_minor", "priority").Updates(&item).Error; err != nil {
			return ProviderView{}, err
		}
		if err := s.db.First(&item, id).Error; err != nil {
			return ProviderView{}, err
		}
	} else if err := s.db.Create(&item).Error; err != nil {
		return ProviderView{}, err
	}
	return providerView(item), nil
}
func (s *PaymentService) DeleteProvider(id int64) error {
	var refs int64
	if err := s.db.Model(&model.PaymentOrder{}).Where("provider_id = ?", id).Count(&refs).Error; err != nil {
		return err
	}
	if refs > 0 {
		return errors.New("provider has payment orders and cannot be deleted")
	}
	result := s.db.Delete(&model.PaymentProviderInstance{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOrderNotFound
	}
	return nil
}

type CheckoutInfo struct {
	Enabled            bool     `json:"enabled"`
	Currency           string   `json:"currency"`
	CreditMicroPerUnit int64    `json:"credit_micro_per_unit"`
	MinAmountMinor     int64    `json:"min_amount_minor"`
	MaxAmountMinor     int64    `json:"max_amount_minor"`
	DailyLimitMinor    int64    `json:"daily_limit_minor"`
	OrderExpiryMinutes int      `json:"order_expiry_minutes"`
	MaxPendingOrders   int      `json:"max_pending_orders"`
	ProductName        string   `json:"product_name"`
	Methods            []string `json:"methods"`
}

func (s *PaymentService) CheckoutInfo() (CheckoutInfo, error) {
	cfg, err := s.Config()
	if err != nil {
		return CheckoutInfo{}, err
	}
	methods, err := s.availableMethods(cfg.Currency)
	if err != nil {
		return CheckoutInfo{}, err
	}
	if methods == nil {
		methods = []string{}
	}
	return CheckoutInfo{Enabled: cfg.Enabled && cfg.CreditMicroPerUnit > 0 && s.publicURL != "", Currency: cfg.Currency, CreditMicroPerUnit: cfg.CreditMicroPerUnit, MinAmountMinor: cfg.MinAmountMinor, MaxAmountMinor: cfg.MaxAmountMinor, DailyLimitMinor: cfg.DailyLimitMinor, OrderExpiryMinutes: cfg.OrderExpiryMinutes, MaxPendingOrders: cfg.MaxPendingOrders, ProductName: cfg.ProductName, Methods: methods}, nil
}

type OrderResult struct {
	ID            int64                   `json:"id"`
	UserID        int64                   `json:"user_id"`
	UserEmail     string                  `json:"user_email,omitempty"`
	OutTradeNo    string                  `json:"out_trade_no"`
	ProviderKey   string                  `json:"provider_key"`
	PaymentMethod string                  `json:"payment_method"`
	Status        string                  `json:"status"`
	Currency      string                  `json:"currency"`
	AmountMinor   int64                   `json:"amount_minor"`
	CreditMicro   int64                   `json:"credit_micro"`
	ExpiresAt     time.Time               `json:"expires_at"`
	PaidAt        *time.Time              `json:"paid_at,omitempty"`
	CompletedAt   *time.Time              `json:"completed_at,omitempty"`
	CancelledAt   *time.Time              `json:"cancelled_at,omitempty"`
	Checkout      *payment.CreateResponse `json:"checkout,omitempty"`
	FailureReason string                  `json:"failure_reason,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
}

func orderResult(o model.PaymentOrder) OrderResult {
	result := OrderResult{ID: o.ID, UserID: o.UserID, OutTradeNo: o.OutTradeNo, ProviderKey: o.ProviderKey, PaymentMethod: o.PaymentMethod, Status: o.Status, Currency: o.Currency, AmountMinor: o.AmountMinor, CreditMicro: o.CreditMicro, ExpiresAt: o.ExpiresAt, PaidAt: o.PaidAt, CompletedAt: o.CompletedAt, CancelledAt: o.CancelledAt, FailureReason: o.FailureReason, CreatedAt: o.CreatedAt}
	if o.User != nil {
		result.UserEmail = o.User.Email
	}
	if o.CheckoutData != "" {
		var checkout payment.CreateResponse
		if json.Unmarshal([]byte(o.CheckoutData), &checkout) == nil {
			result.Checkout = &checkout
		}
	}
	return result
}

func (s *PaymentService) CreateOrder(ctx context.Context, userID, amountMinor int64, method, clientIP string, mobile bool) (OrderResult, error) {
	cfg, err := s.Config()
	if err != nil {
		return OrderResult{}, err
	}
	if !cfg.Enabled || cfg.CreditMicroPerUnit <= 0 || s.publicURL == "" {
		return OrderResult{}, ErrPaymentDisabled
	}
	if amountMinor < cfg.MinAmountMinor || amountMinor > cfg.MaxAmountMinor {
		return OrderResult{}, errors.New("amount is outside the allowed range")
	}
	if _, err := payment.NormalizeCurrency(cfg.Currency); err != nil {
		return OrderResult{}, err
	}
	credit, err := creditFor(amountMinor, cfg.CreditMicroPerUnit, cfg.Currency)
	if err != nil {
		return OrderResult{}, err
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return OrderResult{}, errors.New("payment method is required")
	}
	selected, err := s.selectProvider(userID, amountMinor, method, cfg)
	if err != nil {
		return OrderResult{}, err
	}
	p, err := s.providerFor(selected)
	if err != nil {
		return OrderResult{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(time.Duration(cfg.OrderExpiryMinutes) * time.Minute)
	order := model.PaymentOrder{OutTradeNo: newPaymentOrderNo(selected.ProviderKey), UserID: userID, ProviderID: selected.ID, ProviderKey: selected.ProviderKey, PaymentMethod: method, Status: model.PaymentStatusPending, Currency: cfg.Currency, AmountMinor: amountMinor, CreditMicro: credit, ExpiresAt: expires, ProviderSnapshot: providerSnapshot(selected)}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var pending int64
		if err := tx.Model(&model.PaymentOrder{}).Where("user_id = ? AND status = ?", userID, model.PaymentStatusPending).Count(&pending).Error; err != nil {
			return err
		}
		if int(pending) >= cfg.MaxPendingOrders {
			return errors.New("too many pending payment orders")
		}
		if cfg.DailyLimitMinor > 0 {
			var total int64
			start := now.Truncate(24 * time.Hour)
			if err := tx.Model(&model.PaymentOrder{}).Where("user_id = ? AND created_at >= ? AND status IN ?", userID, start, []string{model.PaymentStatusPaid, model.PaymentStatusCompleted}).Select("COALESCE(SUM(amount_minor), 0)").Scan(&total).Error; err != nil {
				return err
			}
			if total+amountMinor > cfg.DailyLimitMinor {
				return errors.New("daily payment limit reached")
			}
		}
		return tx.Create(&order).Error
	}); err != nil {
		return OrderResult{}, err
	}
	checkout, err := p.Create(ctx, payment.CreateRequest{OrderID: order.OutTradeNo, AmountMinor: amountMinor, Currency: cfg.Currency, PaymentMethod: method, Subject: cfg.ProductName, NotifyURL: s.publicURL + "/api/payment/webhook/" + selected.ProviderKey, ReturnURL: s.publicURL + "/wallet", ClientIP: clientIP, IsMobile: mobile})
	if err != nil {
		_ = s.db.Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusFailed, "failure_reason": safeFailure(err.Error())}).Error
		return OrderResult{}, fmt.Errorf("create payment: %w", err)
	}
	raw, _ := json.Marshal(checkout)
	updates := map[string]any{"checkout_data": crypto.EncryptedString(raw), "provider_trade_no": checkout.TradeNo}
	if err := s.db.Model(&model.PaymentOrder{}).Where("id = ?", order.ID).Updates(updates).Error; err != nil {
		return OrderResult{}, err
	}
	_ = s.db.Model(&model.PaymentProviderInstance{}).Where("id = ?", selected.ID).Update("last_selected_at", now).Error
	order.ProviderTradeNo = checkout.TradeNo
	order.CheckoutData = crypto.EncryptedString(raw)
	s.audit(order.ID, "ORDER_CREATED", "user", fmt.Sprintf("provider=%s", selected.ProviderKey))
	return orderResult(order), nil
}

// newPaymentOrderNo creates identifiers accepted by every bundled provider.
// WeChat Pay's out_trade_no is capped at 32 ASCII characters; the historical
// 36-character value ("ddp_" + 32 chars) was valid for some aggregators but
// was rejected by the official API before checkout creation.
func newPaymentOrderNo(providerKey string) string {
	tokenLength := 32
	if providerKey == model.PaymentProviderWxPay {
		tokenLength = 28 // ddp_ + 28 = WeChat Pay's 32-character maximum.
	}
	return "ddp_" + util.RandomToken(tokenLength)
}

func (s *PaymentService) ListUserOrders(userID int64, limit int) ([]OrderResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var orders []model.PaymentOrder
	if err := s.db.Where("user_id = ?", userID).Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	results := make([]OrderResult, len(orders))
	for i := range orders {
		results[i] = orderResult(orders[i])
	}
	return results, nil
}
func (s *PaymentService) GetUserOrder(userID, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Where("id=? AND user_id=?", id, userID).First(&order).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	return orderResult(order), nil
}
func (s *PaymentService) VerifyOrder(ctx context.Context, userID, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Where("id=? AND user_id=?", id, userID).First(&order).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	if order.Status != model.PaymentStatusPending {
		return orderResult(order), nil
	}
	if order.ExpiresAt.Before(time.Now().UTC()) {
		_ = s.expireOrder(order.ID)
		_ = s.db.First(&order, id).Error
		return orderResult(order), nil
	}
	p, err := s.providerByID(order.ProviderID)
	if err != nil {
		return OrderResult{}, err
	}
	query, err := p.Query(ctx, order.ProviderTradeNo)
	if err != nil {
		return OrderResult{}, err
	}
	if query.Status == payment.StatusPaid {
		if err := s.confirmPayment(order, query.TradeNo, query.AmountMinor, query.Currency, "verify"); err != nil {
			return OrderResult{}, err
		}
	} else if query.Status == payment.StatusFailed {
		_ = s.db.Model(&model.PaymentOrder{}).Where("id=? AND status=?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusFailed, "failure_reason": "payment rejected by provider"}).Error
	}
	_ = s.db.First(&order, id).Error
	return orderResult(order), nil
}
func (s *PaymentService) CancelOrder(ctx context.Context, userID, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Where("id=? AND user_id=?", id, userID).First(&order).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	if order.Status != model.PaymentStatusPending {
		return orderResult(order), nil
	}
	p, err := s.providerByID(order.ProviderID)
	if err == nil && order.ProviderTradeNo != "" {
		_ = p.Cancel(ctx, order.ProviderTradeNo)
	}
	now := time.Now().UTC()
	_ = s.db.Model(&model.PaymentOrder{}).Where("id=? AND status=?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusCancelled, "cancelled_at": now}).Error
	_ = s.db.First(&order, id).Error
	s.audit(order.ID, "ORDER_CANCELLED", "user", "")
	return orderResult(order), nil
}

func (s *PaymentService) RequestRefund(userID, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Where("id = ? AND user_id = ?", id, userID).First(&order).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	if order.Status != model.PaymentStatusCompleted {
		return OrderResult{}, errors.New("only completed orders can be submitted for refund")
	}
	res := s.db.Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", id, model.PaymentStatusCompleted).Update("status", model.PaymentStatusRefundRequested)
	if res.Error != nil || res.RowsAffected == 0 {
		if res.Error != nil {
			return OrderResult{}, res.Error
		}
		return OrderResult{}, errors.New("order status changed, refresh and retry")
	}
	_ = s.db.First(&order, id).Error
	s.audit(id, "REFUND_REQUESTED", "user", "")
	return orderResult(order), nil
}

func (s *PaymentService) ListOrders(limit int) ([]OrderResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var orders []model.PaymentOrder
	if err := s.db.Preload("User").Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	result := make([]OrderResult, len(orders))
	for i := range orders {
		result[i] = orderResult(orders[i])
	}
	return result, nil
}

// ProcessRefund holds back the credited balance before calling the provider.
// If the provider rejects the refund synchronously, the hold is returned in
// the same recovery transaction. Pending provider refunds remain held until
// an administrator resolves them; this avoids refunding money after its API
// credit has been spent.
func (s *PaymentService) ProcessRefund(ctx context.Context, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Preload("User").First(&order, id).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	if order.Status != model.PaymentStatusRefundRequested && order.Status != model.PaymentStatusCompleted {
		return OrderResult{}, errors.New("order is not eligible for refund")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.PaymentOrder{}).Where("id = ? AND status IN ?", id, []string{model.PaymentStatusRefundRequested, model.PaymentStatusCompleted}).Update("status", model.PaymentStatusRefunding)
		if res.Error != nil || res.RowsAffected != 1 {
			if res.Error != nil {
				return res.Error
			}
			return errors.New("order status changed")
		}
		res = tx.Model(&model.User{}).Where("id = ? AND balance_micro >= ?", order.UserID, order.CreditMicro).Update("balance_micro", gorm.Expr("balance_micro - ?", order.CreditMicro))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("current balance is lower than the credited amount")
		}
		return tx.Create(&model.PaymentAuditLog{OrderID: id, Action: "REFUND_BALANCE_HELD", Actor: "admin", Detail: fmt.Sprintf("credit_micro=%d", order.CreditMicro)}).Error
	}); err != nil {
		return OrderResult{}, err
	}
	p, err := s.providerByID(order.ProviderID)
	if err != nil {
		_ = s.restoreRefundHold(order, "provider unavailable")
		return OrderResult{}, err
	}
	response, err := p.Refund(ctx, order.ProviderTradeNo, order.OutTradeNo, order.AmountMinor, order.Currency, "requested_by_customer")
	if err != nil || response.Status == payment.StatusFailed {
		reason := "provider refund failed"
		if err != nil {
			reason = safeFailure(err.Error())
		}
		_ = s.restoreRefundHold(order, reason)
		if err != nil {
			return OrderResult{}, err
		}
		return OrderResult{}, errors.New(reason)
	}
	if response.Status == payment.StatusRefunded {
		if err := s.completeRefund(order, response.RefundID, ""); err != nil {
			return OrderResult{}, err
		}
	} else {
		_ = s.db.Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", id, model.PaymentStatusRefunding).Update("refund_trade_no", response.RefundID).Error
		s.audit(id, "REFUND_PENDING", "admin", "")
	}
	_ = s.db.Preload("User").First(&order, id).Error
	return orderResult(order), nil
}

// FinalizeRefund reconciles an asynchronous refund. The original balance was
// already held when the refund began, so only the terminal order state moves
// here; no second debit or credit is possible.
func (s *PaymentService) FinalizeRefund(ctx context.Context, id int64) (OrderResult, error) {
	var order model.PaymentOrder
	if err := s.db.Preload("User").First(&order, id).Error; err != nil {
		return OrderResult{}, ErrOrderNotFound
	}
	if order.Status != model.PaymentStatusRefunding || order.RefundTradeNo == "" {
		return OrderResult{}, errors.New("refund is not awaiting reconciliation")
	}
	p, err := s.providerByID(order.ProviderID)
	if err != nil {
		return OrderResult{}, err
	}
	query, ok := p.(payment.RefundQueryProvider)
	if !ok {
		return OrderResult{}, errors.New("this provider does not support refund status queries")
	}
	result, err := query.QueryRefund(ctx, order.ProviderTradeNo, order.OutTradeNo, order.RefundTradeNo)
	if err != nil {
		return OrderResult{}, err
	}
	if result.Status == payment.StatusFailed {
		if err := s.restoreRefundHold(order, "provider reported refund failure"); err != nil {
			return OrderResult{}, err
		}
		_ = s.db.Preload("User").First(&order, id).Error
		return orderResult(order), nil
	}
	if result.Status == payment.StatusRefunded {
		if err := s.completeRefund(order, result.RefundID, "reconciled"); err != nil {
			return OrderResult{}, err
		}
		_ = s.db.Preload("User").First(&order, id).Error
		return orderResult(order), nil
	}
	return orderResult(order), nil
}

func (s *PaymentService) completeRefund(order model.PaymentOrder, refundID, detail string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		res := tx.Model(&model.PaymentOrder{}).
			Where("id = ? AND status = ?", order.ID, model.PaymentStatusRefunding).
			Updates(map[string]any{
				"status":          model.PaymentStatusRefunded,
				"refund_trade_no": refundID,
				"refunded_micro":  order.CreditMicro,
				"refunded_at":     now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var current model.PaymentOrder
			if err := tx.First(&current, order.ID).Error; err != nil {
				return err
			}
			if current.Status == model.PaymentStatusRefunded {
				return nil
			}
			return errors.New("refund status changed")
		}
		if err := recordPaymentLedger(tx, order, model.PaymentLedgerExpense, now); err != nil {
			return err
		}
		return tx.Create(&model.PaymentAuditLog{OrderID: order.ID, Action: "ORDER_REFUNDED", Actor: "admin", Detail: detail}).Error
	})
}

func (s *PaymentService) restoreRefundHold(order model.PaymentOrder, reason string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", order.ID, model.PaymentStatusRefunding).Update("status", model.PaymentStatusCompleted)
		if res.Error != nil || res.RowsAffected != 1 {
			return res.Error
		}
		if err := tx.Model(&model.User{}).Where("id = ?", order.UserID).Update("balance_micro", gorm.Expr("balance_micro + ?", order.CreditMicro)).Error; err != nil {
			return err
		}
		return tx.Create(&model.PaymentAuditLog{OrderID: order.ID, Action: "REFUND_HOLD_RELEASED", Actor: "system", Detail: safeFailure(reason)}).Error
	})
}

// HandleWebhook verifies with the order's merchant instance where possible;
// unresolvable signed payloads are tested against active instances of only the
// requested provider. Amount, currency and order id are then checked again.
func (s *PaymentService) HandleWebhook(ctx context.Context, key string, raw []byte, headers map[string]string) error {
	if _, ok := payment.SupportedProviders[key]; !ok {
		return errors.New("unknown provider")
	}
	candidates, err := s.webhookCandidates(key, raw)
	if err != nil {
		return err
	}
	var last error
	for _, candidate := range candidates {
		p, err := s.providerFor(candidate)
		if err != nil {
			last = err
			continue
		}
		note, err := p.Verify(ctx, raw, headers)
		if err != nil {
			last = err
			continue
		}
		if note == nil {
			return nil
		}
		var order model.PaymentOrder
		if err := s.db.Where("out_trade_no=? AND provider_id=?", note.OrderID, candidate.ID).First(&order).Error; err != nil {
			last = ErrOrderNotFound
			continue
		}
		if note.Status != payment.StatusPaid {
			_ = s.db.Model(&model.PaymentOrder{}).Where("id=? AND status=?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusFailed, "failure_reason": "provider reported failed payment"}).Error
			return nil
		}
		return s.confirmPayment(order, note.TradeNo, note.AmountMinor, note.Currency, key)
	}
	if last != nil {
		return last
	}
	return ErrOrderNotFound
}

func (s *PaymentService) confirmPayment(order model.PaymentOrder, tradeNo string, amount int64, currency, actor string) error {
	normalized, err := payment.NormalizeCurrency(currency)
	if err != nil {
		return err
	}
	if normalized != order.Currency || amount != order.AmountMinor {
		return errors.New("payment callback amount or currency mismatch")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		res := tx.Model(&model.PaymentOrder{}).Where("id=? AND status=?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusPaid, "provider_trade_no": tradeNo, "paid_at": now, "failure_reason": ""})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var current model.PaymentOrder
			if err := tx.First(&current, order.ID).Error; err != nil {
				return err
			}
			if current.Status == model.PaymentStatusCompleted {
				return nil
			}
			return fmt.Errorf("payment order is %s", current.Status)
		}
		if err := tx.Model(&model.User{}).Where("id=?", order.UserID).Update("balance_micro", gorm.Expr("balance_micro + ?", order.CreditMicro)).Error; err != nil {
			return err
		}
		res = tx.Model(&model.PaymentOrder{}).Where("id=? AND status=?", order.ID, model.PaymentStatusPaid).Updates(map[string]any{"status": model.PaymentStatusCompleted, "completed_at": now, "fulfillment_lease": nil})
		if res.Error != nil || res.RowsAffected != 1 {
			if res.Error != nil {
				return res.Error
			}
			return errors.New("payment fulfillment lease lost")
		}
		if err := recordPaymentLedger(tx, order, model.PaymentLedgerIncome, now); err != nil {
			return err
		}
		return tx.Create(&model.PaymentAuditLog{OrderID: order.ID, Action: "ORDER_COMPLETED", Actor: actor, Detail: fmt.Sprintf("credit_micro=%d", order.CreditMicro)}).Error
	})
}

func recordPaymentLedger(tx *gorm.DB, order model.PaymentOrder, kind string, occurredAt time.Time) error {
	credit := order.CreditMicro
	if kind == model.PaymentLedgerExpense && order.RefundedMicro > 0 {
		credit = order.RefundedMicro
	}
	entry := model.PaymentLedgerEntry{
		EventKey:      fmt.Sprintf("%s:%d", kind, order.ID),
		OrderID:       order.ID,
		UserID:        order.UserID,
		Kind:          kind,
		Currency:      order.Currency,
		AmountMinor:   order.AmountMinor,
		CreditMicro:   credit,
		ProviderKey:   order.ProviderKey,
		PaymentMethod: order.PaymentMethod,
		OccurredAt:    occurredAt.UTC(),
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_key"}},
		DoNothing: true,
	}).Create(&entry).Error
}

func (s *PaymentService) ExpirePending(ctx context.Context) error {
	now := time.Now().UTC()
	var orders []model.PaymentOrder
	if err := s.db.Where("status=? AND expires_at < ?", model.PaymentStatusPending, now).Limit(200).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		_ = s.expireOrder(order.ID)
	}
	return nil
}
func (s *PaymentService) expireOrder(id int64) error {
	res := s.db.Model(&model.PaymentOrder{}).Where("id=? AND status=? AND expires_at < ?", id, model.PaymentStatusPending, time.Now().UTC()).Update("status", model.PaymentStatusExpired)
	if res.RowsAffected > 0 {
		s.audit(id, "ORDER_EXPIRED", "system", "")
	}
	return res.Error
}
func (s *PaymentService) StartReconciler() {
	go func() {
		reconcile := func() {
			ctx, cancel := context.WithTimeout(context.Background(), paymentQueryTimeout*time.Duration(pendingReconcileBatch))
			defer cancel()
			_ = s.ReconcilePending(ctx)
			_ = s.ExpirePending(ctx)
		}
		reconcile()
		ticker := time.NewTicker(paymentReconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			reconcile()
		}
	}()
}

// ReconcilePending queries the payment provider for open orders. Merchant
// callbacks remain the fastest path, but this closes the gap when a callback
// is delayed or a client leaves the payment page before clicking “核验到账”.
// Provider query failures deliberately leave an order pending for the next
// pass; only an explicit provider rejection marks it failed.
func (s *PaymentService) ReconcilePending(ctx context.Context) error {
	now := time.Now().UTC()
	var orders []model.PaymentOrder
	if err := s.db.Where("status = ? AND expires_at > ? AND provider_trade_no <> ?", model.PaymentStatusPending, now, "").Order("id ASC").Limit(pendingReconcileBatch).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		provider, err := s.providerByID(order.ProviderID)
		if err != nil {
			continue
		}
		queryCtx, cancel := context.WithTimeout(ctx, paymentQueryTimeout)
		result, err := provider.Query(queryCtx, order.ProviderTradeNo)
		cancel()
		if err != nil || result == nil {
			continue
		}
		switch result.Status {
		case payment.StatusPaid:
			_ = s.confirmPayment(order, result.TradeNo, result.AmountMinor, result.Currency, "reconcile")
		case payment.StatusFailed:
			_ = s.db.Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", order.ID, model.PaymentStatusPending).Updates(map[string]any{"status": model.PaymentStatusFailed, "failure_reason": "payment rejected by provider"}).Error
		}
	}
	return nil
}

func (s *PaymentService) providerByID(id int64) (payment.Provider, error) {
	var item model.PaymentProviderInstance
	if err := s.db.First(&item, id).Error; err != nil {
		return nil, ErrOrderNotFound
	}
	return s.providerFor(item)
}
func (s *PaymentService) providerFor(item model.PaymentProviderInstance) (payment.Provider, error) {
	var cfg map[string]string
	if err := json.Unmarshal([]byte(item.Config), &cfg); err != nil {
		return nil, fmt.Errorf("provider config: %w", err)
	}
	// Display mode is safe instance metadata and is stored outside of the
	// encrypted credentials blob so administrators can change it without
	// exposing or re-entering secrets.
	if _, ok := cfg["paymentMode"]; !ok {
		cfg["paymentMode"] = item.PaymentMode
	}
	if _, ok := cfg["currency"]; !ok {
		cfg["currency"] = item.Currency
	}
	return provider.New(item.ProviderKey, cfg)
}
func (s *PaymentService) selectProvider(userID, amountMinor int64, method string, cfg model.PaymentConfig) (model.PaymentProviderInstance, error) {
	var providers []model.PaymentProviderInstance
	q := s.db.Where("status=? AND currency=?", model.StatusActive, cfg.Currency).Order("priority DESC,last_selected_at ASC,id")
	if err := q.Find(&providers).Error; err != nil {
		return model.PaymentProviderInstance{}, err
	}
	eligible := make([]model.PaymentProviderInstance, 0, len(providers))
	for _, item := range providers {
		if !methodSupported(item, method) {
			continue
		}
		if item.MinAmountMinor > 0 && amountMinor < item.MinAmountMinor {
			continue
		}
		if item.MaxAmountMinor > 0 && amountMinor > item.MaxAmountMinor {
			continue
		}
		if item.DailyLimitMinor > 0 {
			var total int64
			today := time.Now().UTC().Truncate(24 * time.Hour)
			_ = s.db.Model(&model.PaymentOrder{}).
				Where("provider_id = ? AND created_at >= ? AND status IN ?", item.ID, today, []string{model.PaymentStatusPaid, model.PaymentStatusCompleted}).
				Select("COALESCE(SUM(amount_minor),0)").Scan(&total).Error
			if total+amountMinor > item.DailyLimitMinor {
				continue
			}
		}
		eligible = append(eligible, item)
	}
	if len(eligible) == 0 {
		return model.PaymentProviderInstance{}, errors.New("no payment provider accepts this method")
	}
	if cfg.LoadBalanceStrategy == "least_amount" {
		type ranked struct {
			item   model.PaymentProviderInstance
			amount int64
		}
		items := make([]ranked, 0, len(eligible))
		today := time.Now().UTC().Truncate(24 * time.Hour)
		for _, item := range eligible {
			var amount int64
			_ = s.db.Model(&model.PaymentOrder{}).Where("provider_id=? AND created_at>=? AND status IN ?", item.ID, today, []string{model.PaymentStatusPaid, model.PaymentStatusCompleted}).Select("COALESCE(SUM(amount_minor),0)").Scan(&amount)
			items = append(items, ranked{item, amount})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].amount < items[j].amount })
		return items[0].item, nil
	}
	return eligible[0], nil
}
func (s *PaymentService) webhookCandidates(key string, raw []byte) ([]model.PaymentProviderInstance, error) {
	orderID := extractOrderID(key, raw)
	var items []model.PaymentProviderInstance
	if orderID != "" {
		var order model.PaymentOrder
		if err := s.db.Where("out_trade_no=? AND provider_key=?", orderID, key).First(&order).Error; err == nil {
			var item model.PaymentProviderInstance
			if s.db.First(&item, order.ProviderID).Error == nil {
				return []model.PaymentProviderInstance{item}, nil
			}
		}
	}
	if err := s.db.Where("provider_key=? AND status=?", key, model.StatusActive).Order("id").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
func extractOrderID(key string, raw []byte) string {
	if key == payment.ProviderEasyPay || key == payment.ProviderAlipay {
		values, err := url.ParseQuery(string(raw))
		if err == nil {
			return values.Get("out_trade_no")
		}
	}
	if key == payment.ProviderStripe {
		var body struct {
			Data struct {
				Object struct {
					Metadata map[string]string `json:"metadata"`
				} `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &body) == nil {
			return body.Data.Object.Metadata["order_id"]
		}
	}
	if key == payment.ProviderAirwallex {
		var body struct {
			Data struct {
				Object struct {
					MerchantOrderID string `json:"merchant_order_id"`
				} `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &body) == nil {
			return body.Data.Object.MerchantOrderID
		}
	}
	return ""
}
func (s *PaymentService) audit(orderID int64, action, actor, detail string) {
	_ = s.db.Create(&model.PaymentAuditLog{OrderID: orderID, Action: action, Actor: actor, Detail: detail}).Error
}
func creditFor(minor, perUnit int64, currency string) (int64, error) {
	if perUnit <= 0 {
		return 0, errors.New("payment credit rate is not configured")
	}
	divisor := int64(1)
	if payment.MinorDigits(currency) > 0 {
		divisor = 100
	}
	if minor > math.MaxInt64/perUnit {
		return 0, errors.New("payment amount overflow")
	}
	credit := minor * perUnit / divisor
	if credit <= 0 {
		return 0, errors.New("payment credit rate is too small")
	}
	return credit, nil
}
func normalizeMethods(raw string) string {
	fields := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" && !seen[field] {
			seen[field] = true
			out = append(out, field)
		}
	}
	return strings.Join(out, ",")
}
func methodSupported(item model.PaymentProviderInstance, method string) bool {
	methods := normalizeMethods(item.SupportedMethods)
	if methods == "" {
		switch item.ProviderKey {
		case payment.ProviderEasyPay:
			return method == payment.MethodAlipay || method == payment.MethodWxPay
		case payment.ProviderAlipay:
			return method == payment.MethodAlipay
		case payment.ProviderWxPay:
			return method == payment.MethodWxPay
		case payment.ProviderStripe, payment.ProviderAirwallex:
			return method == payment.MethodCard || method == payment.MethodLink
		}
		return false
	}
	for _, candidate := range strings.Split(methods, ",") {
		if candidate == method {
			return true
		}
	}
	return false
}
func (s *PaymentService) availableMethods(currency string) ([]string, error) {
	var items []model.PaymentProviderInstance
	if err := s.db.Where("status=? AND currency=?", model.StatusActive, currency).Find(&items).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var result []string
	for _, item := range items {
		for _, method := range []string{payment.MethodAlipay, payment.MethodWxPay, payment.MethodCard, payment.MethodLink} {
			if methodSupported(item, method) && !seen[method] {
				seen[method] = true
				result = append(result, method)
			}
		}
	}
	sort.Strings(result)
	return result, nil
}
func providerSnapshot(item model.PaymentProviderInstance) string {
	raw, _ := json.Marshal(map[string]any{"provider_id": item.ID, "provider_key": item.ProviderKey, "currency": item.Currency, "method": item.SupportedMethods, "name": item.Name})
	return string(raw)
}
func safeFailure(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 512 {
		return message[:512]
	}
	return message
}
