package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"dengdeng/internal/config"
	appcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/payment"
	"dengdeng/internal/payment/provider"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrReferralCashDisabled   = errors.New("cash referral payouts are not enabled")
	ErrReferralPayoutNotFound = errors.New("referral payout not found")
)

type ReferralCashService struct {
	db        *gorm.DB
	publicURL string
}

const referralPayoutReconcileInterval = 30 * time.Second

func (s *ReferralCashService) StartReconciler() {
	go func() {
		reconcile := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.ReconcileOpenPayouts(ctx)
		}
		reconcile()
		ticker := time.NewTicker(referralPayoutReconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			reconcile()
		}
	}()
}

// ReconcileOpenPayouts closes the callback gap. QUEUED is safe to submit
// because review-pending payouts have a distinct state; all submitted states
// are queried with the original out_bill_no and are never blindly re-created.
func (s *ReferralCashService) ReconcileOpenPayouts(ctx context.Context) error {
	var payouts []model.ReferralPayout
	states := []string{model.ReferralPayoutQueued, model.ReferralPayoutSubmitting, model.ReferralPayoutAwaitingConfirm, model.ReferralPayoutProcessing, model.ReferralPayoutStatusUncertain}
	if err := s.db.Where("status IN ?", states).Order("id ASC").Limit(100).Find(&payouts).Error; err != nil {
		return err
	}
	for _, payout := range payouts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if payout.Status == model.ReferralPayoutQueued {
			_, _ = s.dispatchPayout(opCtx, payout.ID)
		} else {
			_, _ = s.QueryPayout(opCtx, payout.ID)
		}
		cancel()
	}
	return nil
}

func NewReferralCashService(db *gorm.DB, cfg *config.Config) *ReferralCashService {
	publicURL := ""
	if cfg != nil {
		publicURL = strings.TrimRight(strings.TrimSpace(cfg.Site.PublicURL), "/")
	}
	return &ReferralCashService{db: db, publicURL: publicURL}
}

func defaultReferralPayoutConfig() model.ReferralPayoutConfig {
	return model.ReferralPayoutConfig{
		ID: 1, Currency: "CNY", SettlementDays: 7, MinPayoutMinor: 100,
		MaxPayoutMinor: 200_000, DailyPayoutMinor: 500_000, RequireReview: true,
		TransferRemark: "推广佣金",
	}
}

func (s *ReferralCashService) Config() (model.ReferralPayoutConfig, error) {
	var cfg model.ReferralPayoutConfig
	err := s.db.First(&cfg, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cfg = defaultReferralPayoutConfig()
		err = s.db.Create(&cfg).Error
	}
	return cfg, err
}

func (s *ReferralCashService) UpdateConfig(next model.ReferralPayoutConfig) (model.ReferralPayoutConfig, error) {
	next.Currency = strings.ToUpper(strings.TrimSpace(next.Currency))
	next.WxTransferSceneID = strings.TrimSpace(next.WxTransferSceneID)
	next.SceneReportInfoType = strings.TrimSpace(next.SceneReportInfoType)
	next.SceneReportInfoContent = strings.TrimSpace(next.SceneReportInfoContent)
	next.TransferRemark = strings.TrimSpace(next.TransferRemark)
	if next.Currency != "CNY" {
		return model.ReferralPayoutConfig{}, errors.New("WeChat cash payouts require CNY")
	}
	if next.SettlementDays < 0 || next.SettlementDays > 90 {
		return model.ReferralPayoutConfig{}, errors.New("settlement days must be between 0 and 90")
	}
	if next.MinPayoutMinor <= 0 || next.MaxPayoutMinor < next.MinPayoutMinor || next.DailyPayoutMinor < next.MaxPayoutMinor {
		return model.ReferralPayoutConfig{}, errors.New("invalid payout amount limits")
	}
	if len([]rune(next.TransferRemark)) == 0 || len([]rune(next.TransferRemark)) > 32 {
		return model.ReferralPayoutConfig{}, errors.New("transfer remark must be 1-32 characters")
	}
	if len([]rune(next.SceneReportInfoType)) > 15 || len([]rune(next.SceneReportInfoContent)) > 32 {
		return model.ReferralPayoutConfig{}, errors.New("scene report info exceeds WeChat limits")
	}
	if next.Enabled {
		if s.publicURL == "" || next.WxProviderID <= 0 || next.WxTransferSceneID == "" || next.SceneReportInfoType == "" || next.SceneReportInfoContent == "" {
			return model.ReferralPayoutConfig{}, errors.New("enable cash payout requires public URL, WeChat provider, transfer scene and report info")
		}
		if _, err := s.merchantTransferProvider(next.WxProviderID); err != nil {
			return model.ReferralPayoutConfig{}, err
		}
	}
	next.ID = 1
	if err := s.db.Save(&next).Error; err != nil {
		return model.ReferralPayoutConfig{}, err
	}
	return next, nil
}

type ReferralCashSnapshot struct {
	PendingMicro   int64  `json:"pending_micro"`
	AvailableMicro int64  `json:"available_micro"`
	LockedMicro    int64  `json:"locked_micro"`
	PaidMicro      int64  `json:"paid_micro"`
	Currency       string `json:"currency"`
	TotalMinor     int64  `json:"total_minor"`
	PendingMinor   int64  `json:"pending_minor"`
	AvailableMinor int64  `json:"available_minor"`
	LockedMinor    int64  `json:"locked_minor"`
	PaidMinor      int64  `json:"paid_minor"`
	MinPayoutMinor int64  `json:"min_payout_minor"`
	Enabled        bool   `json:"enabled"`
}

func referralMicroToMinor(micro, rate, divisor int64) int64 {
	if micro <= 0 || rate <= 0 || divisor <= 0 || micro > math.MaxInt64/divisor {
		return 0
	}
	return micro * divisor / rate
}

func referralMicroTotal(values ...int64) int64 {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0
		}
		total += value
	}
	return total
}

func ensureReferralCashAccount(tx *gorm.DB, userID int64, now time.Time) error {
	account := model.ReferralCashAccount{UserID: userID, CreatedAt: now, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error
}

func releaseMaturedReferralCommissions(tx *gorm.DB, userID int64, now time.Time) error {
	var items []model.ReferralCommission
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("referrer_user_id = ? AND status = ? AND available_at IS NOT NULL AND available_at <= ?", userID, model.ReferralCommissionPending, now).
		Order("id ASC")
	if err := query.Find(&items).Error; err != nil || len(items) == 0 {
		return err
	}
	ids := make([]int64, 0, len(items))
	var total int64
	for _, item := range items {
		ids = append(ids, item.ID)
		total += item.AmountMicro
	}
	result := tx.Model(&model.ReferralCommission{}).
		Where("id IN ? AND status = ?", ids, model.ReferralCommissionPending).
		Update("status", model.ReferralCommissionAvailable)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(items)) {
		return errors.New("commission maturity changed concurrently")
	}
	if err := ensureReferralCashAccount(tx, userID, now); err != nil {
		return err
	}
	return tx.Model(&model.ReferralCashAccount{}).Where("user_id = ?", userID).Updates(map[string]any{
		"pending_micro":   gorm.Expr("pending_micro - ?", total),
		"available_micro": gorm.Expr("available_micro + ?", total),
		"updated_at":      now,
	}).Error
}

func (s *ReferralCashService) ReleaseMatured(userID int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		return releaseMaturedReferralCommissions(tx, userID, time.Now().UTC())
	})
}

func payoutRate(tx *gorm.DB, currency string) (int64, int64, error) {
	var cfg model.PaymentConfig
	if err := tx.First(&cfg, 1).Error; err != nil {
		return 0, 0, errors.New("payment exchange rate is not configured")
	}
	if !strings.EqualFold(cfg.Currency, currency) || cfg.CreditMicroPerUnit <= 0 {
		return 0, 0, errors.New("cash payout currency does not match the payment exchange rate")
	}
	divisor := int64(1)
	if payment.MinorDigits(currency) > 0 {
		divisor = 100
	}
	return cfg.CreditMicroPerUnit, divisor, nil
}

func (s *ReferralCashService) Snapshot(userID int64) (ReferralCashSnapshot, error) {
	if err := s.ReleaseMatured(userID); err != nil {
		return ReferralCashSnapshot{}, err
	}
	cfg, err := s.Config()
	if err != nil {
		return ReferralCashSnapshot{}, err
	}
	var account model.ReferralCashAccount
	if err := s.db.First(&account, userID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		account.UserID = userID
	} else if err != nil {
		return ReferralCashSnapshot{}, err
	}
	snapshot := ReferralCashSnapshot{PendingMicro: account.PendingMicro, AvailableMicro: account.AvailableMicro, LockedMicro: account.LockedMicro, PaidMicro: account.PaidMicro, Currency: cfg.Currency, MinPayoutMinor: cfg.MinPayoutMinor, Enabled: cfg.Enabled}
	if rate, divisor, rateErr := payoutRate(s.db, cfg.Currency); rateErr == nil {
		snapshot.PendingMinor = referralMicroToMinor(account.PendingMicro, rate, divisor)
		snapshot.AvailableMinor = referralMicroToMinor(account.AvailableMicro, rate, divisor)
		snapshot.LockedMinor = referralMicroToMinor(account.LockedMicro, rate, divisor)
		snapshot.PaidMinor = referralMicroToMinor(account.PaidMicro, rate, divisor)
		snapshot.TotalMinor = referralMicroToMinor(referralMicroTotal(account.PendingMicro, account.AvailableMicro, account.LockedMicro, account.PaidMicro), rate, divisor)
	}
	return snapshot, nil
}

type ReferralPayoutAccountView struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	UserEmail  string     `json:"user_email,omitempty"`
	Channel    string     `json:"channel"`
	OpenIDHint string     `json:"openid_hint"`
	Status     string     `json:"status"`
	Note       string     `json:"note,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func payoutAccountView(item model.ReferralPayoutAccount, email string) ReferralPayoutAccountView {
	return ReferralPayoutAccountView{ID: item.ID, UserID: item.UserID, UserEmail: email, Channel: item.Channel, OpenIDHint: item.OpenIDHint, Status: item.Status, Note: item.Note, VerifiedAt: item.VerifiedAt, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func openIDHash(openID string) string {
	sum := sha256.Sum256([]byte(openID))
	return hex.EncodeToString(sum[:])
}

func openIDHint(openID string) string {
	runes := []rune(openID)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
}

func (s *ReferralCashService) SavePayoutAccount(userID int64, openID, status, note string, admin bool) (ReferralPayoutAccountView, error) {
	openID = strings.TrimSpace(openID)
	if len(openID) < 8 || len(openID) > 64 {
		return ReferralPayoutAccountView{}, errors.New("invalid WeChat OpenID")
	}
	if !admin {
		status = model.ReferralPayoutAccountPending
	}
	if status != model.ReferralPayoutAccountPending && status != model.ReferralPayoutAccountVerified && status != model.ReferralPayoutAccountDisabled {
		return ReferralPayoutAccountView{}, errors.New("invalid payout account status")
	}
	now := time.Now().UTC()
	var item model.ReferralPayoutAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&model.ReferralPayout{}).Where("user_id = ? AND status NOT IN ?", userID, []string{model.ReferralPayoutSuccess, model.ReferralPayoutFailed, model.ReferralPayoutCancelled}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return errors.New("payout account cannot change while a transfer is open")
		}
		verifiedAt := (*time.Time)(nil)
		if status == model.ReferralPayoutAccountVerified {
			verifiedAt = &now
		}
		values := model.ReferralPayoutAccount{UserID: userID, Channel: model.PaymentProviderWxPay, OpenID: appcrypto.EncryptedString(openID), OpenIDHash: openIDHash(openID), OpenIDHint: openIDHint(openID), Status: status, Note: strings.TrimSpace(note), VerifiedAt: verifiedAt, CreatedAt: now, UpdatedAt: now}
		if err := tx.Where("user_id = ?", userID).First(&item).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			item = values
			return tx.Create(&item).Error
		} else if err != nil {
			return err
		}
		return tx.Model(&item).Updates(map[string]any{"channel": values.Channel, "open_id": values.OpenID, "open_id_hash": values.OpenIDHash, "open_id_hint": values.OpenIDHint, "status": values.Status, "note": values.Note, "verified_at": values.VerifiedAt, "updated_at": now}).Error
	})
	if err != nil {
		return ReferralPayoutAccountView{}, err
	}
	_ = s.db.First(&item, item.ID).Error
	return payoutAccountView(item, ""), nil
}

func (s *ReferralCashService) PayoutAccount(userID int64) (*ReferralPayoutAccountView, error) {
	var item model.ReferralPayoutAccount
	if err := s.db.Where("user_id = ?", userID).First(&item).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	view := payoutAccountView(item, "")
	return &view, nil
}

func (s *ReferralCashService) ReviewPayoutAccount(userID int64, status, note string) (ReferralPayoutAccountView, error) {
	if status != model.ReferralPayoutAccountPending && status != model.ReferralPayoutAccountVerified && status != model.ReferralPayoutAccountDisabled {
		return ReferralPayoutAccountView{}, errors.New("invalid payout account status")
	}
	now := time.Now().UTC()
	verifiedAt := (*time.Time)(nil)
	if status == model.ReferralPayoutAccountVerified {
		verifiedAt = &now
	}
	result := s.db.Model(&model.ReferralPayoutAccount{}).Where("user_id = ?", userID).Updates(map[string]any{"status": status, "note": strings.TrimSpace(note), "verified_at": verifiedAt, "updated_at": now})
	if result.Error != nil {
		return ReferralPayoutAccountView{}, result.Error
	}
	if result.RowsAffected == 0 {
		return ReferralPayoutAccountView{}, errors.New("payout account not found")
	}
	var item model.ReferralPayoutAccount
	if err := s.db.Where("user_id = ?", userID).First(&item).Error; err != nil {
		return ReferralPayoutAccountView{}, err
	}
	return payoutAccountView(item, ""), nil
}

func (s *ReferralCashService) ListPayoutAccounts() ([]ReferralPayoutAccountView, error) {
	var rows []struct {
		model.ReferralPayoutAccount
		UserEmail string
	}
	if err := s.db.Table("referral_payout_accounts").Select("referral_payout_accounts.*, COALESCE(users.email, '') AS user_email").Joins("LEFT JOIN users ON users.id = referral_payout_accounts.user_id").Order("referral_payout_accounts.id DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]ReferralPayoutAccountView, 0, len(rows))
	for _, row := range rows {
		items = append(items, payoutAccountView(row.ReferralPayoutAccount, row.UserEmail))
	}
	return items, nil
}

type ReferralPayoutView struct {
	model.ReferralPayout
	UserEmail  string `json:"user_email,omitempty"`
	OpenIDHint string `json:"openid_hint,omitempty"`
}

func newReferralOutBillNo() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return "DDC" + time.Now().UTC().Format("060102150405") + strings.ToUpper(hex.EncodeToString(raw))
}

func (s *ReferralCashService) RequestPayout(ctx context.Context, userID int64) (ReferralPayoutView, error) {
	var payout model.ReferralPayout
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := releaseMaturedReferralCommissions(tx, userID, now); err != nil {
			return err
		}
		var cfg model.ReferralPayoutConfig
		if err := tx.First(&cfg, 1).Error; err != nil || !cfg.Enabled {
			return ErrReferralCashDisabled
		}
		var recipient model.ReferralPayoutAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND status = ?", userID, model.ReferralPayoutAccountVerified).First(&recipient).Error; err != nil {
			return errors.New("a verified WeChat payout account is required")
		}
		var open int64
		if err := tx.Model(&model.ReferralPayout{}).Where("user_id = ? AND status NOT IN ?", userID, []string{model.ReferralPayoutSuccess, model.ReferralPayoutFailed, model.ReferralPayoutCancelled}).Count(&open).Error; err != nil {
			return err
		}
		if open > 0 {
			return errors.New("an unsettled cash payout already exists")
		}
		var cash model.ReferralCashAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cash, userID).Error; err != nil {
			return errors.New("no cash commission is available")
		}
		rate, divisor, err := payoutRate(tx, cfg.Currency)
		if err != nil {
			return err
		}
		if cash.AvailableMicro <= 0 || cash.AvailableMicro > math.MaxInt64/divisor {
			return errors.New("no cash commission is available")
		}
		amountMinor := cash.AvailableMicro * divisor / rate
		if cfg.MaxPayoutMinor > 0 && amountMinor > cfg.MaxPayoutMinor {
			amountMinor = cfg.MaxPayoutMinor
		}
		if amountMinor < cfg.MinPayoutMinor {
			return errors.New("available commission is below the minimum payout")
		}
		if amountMinor > math.MaxInt64/rate {
			return errors.New("payout amount overflow")
		}
		commissionMicro := amountMinor * rate / divisor
		if commissionMicro <= 0 || commissionMicro > cash.AvailableMicro {
			return errors.New("invalid payout exchange amount")
		}
		dayStart := now.Truncate(24 * time.Hour)
		var daily int64
		if err := tx.Model(&model.ReferralPayout{}).Where("requested_at >= ? AND status NOT IN ?", dayStart, []string{model.ReferralPayoutFailed, model.ReferralPayoutCancelled}).Select("COALESCE(SUM(amount_minor), 0)").Scan(&daily).Error; err != nil {
			return err
		}
		if cfg.DailyPayoutMinor > 0 && daily+amountMinor > cfg.DailyPayoutMinor {
			return errors.New("daily cash payout limit reached")
		}
		status := model.ReferralPayoutQueued
		if cfg.RequireReview {
			status = model.ReferralPayoutReviewPending
		}
		payout = model.ReferralPayout{OutBillNo: newReferralOutBillNo(), UserID: userID, PayoutAccountID: recipient.ID, ProviderID: cfg.WxProviderID, Channel: model.PaymentProviderWxPay, Status: status, Currency: cfg.Currency, AmountMinor: amountMinor, CommissionMicro: commissionMicro, ExchangeMicro: rate, RequestedAt: now}
		if err := tx.Create(&payout).Error; err != nil {
			return err
		}
		return tx.Model(&cash).Updates(map[string]any{"available_micro": gorm.Expr("available_micro - ?", commissionMicro), "locked_micro": gorm.Expr("locked_micro + ?", commissionMicro), "updated_at": now}).Error
	})
	if err != nil {
		return ReferralPayoutView{}, err
	}
	if payout.Status == model.ReferralPayoutQueued {
		return s.dispatchPayout(ctx, payout.ID)
	}
	return s.payoutView(payout), nil
}

func (s *ReferralCashService) merchantTransferProvider(id int64) (payment.MerchantTransferProvider, error) {
	var item model.PaymentProviderInstance
	if err := s.db.Where("id = ? AND provider_key = ? AND status = ?", id, model.PaymentProviderWxPay, model.StatusActive).First(&item).Error; err != nil {
		return nil, errors.New("active WeChat Pay provider not found")
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(item.Config), &cfg); err != nil {
		return nil, fmt.Errorf("WeChat provider config: %w", err)
	}
	channel, err := provider.New(item.ProviderKey, cfg)
	if err != nil {
		return nil, err
	}
	transfer, ok := channel.(payment.MerchantTransferProvider)
	if !ok {
		return nil, errors.New("payment provider does not support merchant transfer")
	}
	return transfer, nil
}

func (s *ReferralCashService) dispatchPayout(ctx context.Context, id int64) (ReferralPayoutView, error) {
	var payout model.ReferralPayout
	var recipient model.ReferralPayoutAccount
	var cfg model.ReferralPayoutConfig
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payout, id).Error; err != nil {
			return ErrReferralPayoutNotFound
		}
		if payout.Status != model.ReferralPayoutQueued {
			return errors.New("payout is not queued")
		}
		if err := tx.First(&recipient, payout.PayoutAccountID).Error; err != nil {
			return err
		}
		if err := tx.First(&cfg, 1).Error; err != nil {
			return err
		}
		return tx.Model(&payout).Updates(map[string]any{"status": model.ReferralPayoutSubmitting, "submitted_at": now, "failure_message": ""}).Error
	})
	if err != nil {
		return ReferralPayoutView{}, err
	}
	transfer, err := s.merchantTransferProvider(payout.ProviderID)
	if err != nil {
		_ = s.markPayoutUncertain(payout.ID, err)
		return ReferralPayoutView{}, err
	}
	result, err := transfer.CreateMerchantTransfer(ctx, payment.MerchantTransferRequest{
		OutBillNo: payout.OutBillNo, OpenID: string(recipient.OpenID), AmountMinor: payout.AmountMinor,
		SceneID: cfg.WxTransferSceneID, Remark: cfg.TransferRemark,
		NotifyURL: s.publicURL + "/api/referrals/payout/webhook/wxpay", UserPerception: "佣金报酬",
		SceneReportInfo: []payment.MerchantTransferSceneInfo{{InfoType: cfg.SceneReportInfoType, InfoContent: cfg.SceneReportInfoContent}},
	})
	if err != nil {
		_ = s.markPayoutUncertain(payout.ID, err)
		return ReferralPayoutView{}, fmt.Errorf("submit cash payout; query the original bill before retrying: %w", err)
	}
	if err := s.applyTransferResult(payout.ID, result); err != nil {
		return ReferralPayoutView{}, err
	}
	return s.GetPayout(payout.ID)
}

func (s *ReferralCashService) markPayoutUncertain(id int64, cause error) error {
	return s.db.Model(&model.ReferralPayout{}).Where("id = ? AND status = ?", id, model.ReferralPayoutSubmitting).Updates(map[string]any{"status": model.ReferralPayoutStatusUncertain, "failure_message": safeFailure(cause.Error())}).Error
}

func referralPayoutTerminal(status string) bool {
	return status == model.ReferralPayoutSuccess || status == model.ReferralPayoutFailed || status == model.ReferralPayoutCancelled
}

func transferState(status string) string {
	switch strings.ToUpper(status) {
	case "WAIT_USER_CONFIRM":
		return model.ReferralPayoutAwaitingConfirm
	case "SUCCESS":
		return model.ReferralPayoutSuccess
	case "FAIL":
		return model.ReferralPayoutFailed
	case "CANCELLED":
		return model.ReferralPayoutCancelled
	case "ACCEPTED", "PROCESSING", "TRANSFERING", "CANCELING":
		return model.ReferralPayoutProcessing
	default:
		return model.ReferralPayoutStatusUncertain
	}
}

func (s *ReferralCashService) applyTransferResult(id int64, result *payment.MerchantTransferResponse) error {
	if result == nil {
		return errors.New("empty merchant transfer result")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var payout model.ReferralPayout
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payout, id).Error; err != nil {
			return ErrReferralPayoutNotFound
		}
		if result.OutBillNo != "" && result.OutBillNo != payout.OutBillNo {
			return errors.New("merchant transfer bill number mismatch")
		}
		if result.AmountMinor > 0 && result.AmountMinor != payout.AmountMinor {
			return errors.New("merchant transfer amount mismatch")
		}
		var recipient model.ReferralPayoutAccount
		if err := tx.First(&recipient, payout.PayoutAccountID).Error; err != nil {
			return err
		}
		if result.OpenID != "" && result.OpenID != string(recipient.OpenID) {
			return errors.New("merchant transfer recipient mismatch")
		}
		next := transferState(result.State)
		if referralPayoutTerminal(payout.Status) {
			return nil
		}
		updates := map[string]any{"status": next, "provider_bill_no": result.ProviderBillNo, "package_info": result.PackageInfo, "failure_message": safeFailure(result.FailureReason), "app_id": result.AppID, "merchant_id": result.MerchantID}
		if referralPayoutTerminal(next) {
			now := time.Now().UTC()
			updates["completed_at"] = now
			if next == model.ReferralPayoutSuccess {
				accountUpdate := tx.Model(&model.ReferralCashAccount{}).Where("user_id = ? AND locked_micro >= ?", payout.UserID, payout.CommissionMicro).Updates(map[string]any{"locked_micro": gorm.Expr("locked_micro - ?", payout.CommissionMicro), "paid_micro": gorm.Expr("paid_micro + ?", payout.CommissionMicro), "updated_at": now})
				if accountUpdate.Error != nil {
					return accountUpdate.Error
				}
				if accountUpdate.RowsAffected != 1 {
					return errors.New("cash account locked balance mismatch")
				}
				entry := model.PaymentLedgerEntry{EventKey: fmt.Sprintf("referral_payout:%d", payout.ID), OrderID: 0, UserID: payout.UserID, Kind: model.PaymentLedgerExpense, Currency: payout.Currency, AmountMinor: payout.AmountMinor, CreditMicro: payout.CommissionMicro, ProviderKey: payout.Channel, PaymentMethod: "referral_payout", Category: "referral_payout", OccurredAt: now}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&entry).Error; err != nil {
					return err
				}
			} else {
				accountUpdate := tx.Model(&model.ReferralCashAccount{}).Where("user_id = ? AND locked_micro >= ?", payout.UserID, payout.CommissionMicro).Updates(map[string]any{"locked_micro": gorm.Expr("locked_micro - ?", payout.CommissionMicro), "available_micro": gorm.Expr("available_micro + ?", payout.CommissionMicro), "updated_at": now})
				if accountUpdate.Error != nil {
					return accountUpdate.Error
				}
				if accountUpdate.RowsAffected != 1 {
					return errors.New("cash account locked balance mismatch")
				}
			}
		}
		return tx.Model(&payout).Updates(updates).Error
	})
}

func (s *ReferralCashService) ApprovePayout(ctx context.Context, id int64) (ReferralPayoutView, error) {
	result := s.db.Model(&model.ReferralPayout{}).Where("id = ? AND status = ?", id, model.ReferralPayoutReviewPending).Update("status", model.ReferralPayoutQueued)
	if result.Error != nil {
		return ReferralPayoutView{}, result.Error
	}
	if result.RowsAffected == 0 {
		return ReferralPayoutView{}, errors.New("payout is not awaiting review")
	}
	return s.dispatchPayout(ctx, id)
}

func (s *ReferralCashService) RejectPayout(id int64, reason string) (ReferralPayoutView, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var payout model.ReferralPayout
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&payout, id).Error; err != nil {
			return ErrReferralPayoutNotFound
		}
		if payout.Status != model.ReferralPayoutReviewPending {
			return errors.New("only a review-pending payout can be rejected")
		}
		now := time.Now().UTC()
		accountUpdate := tx.Model(&model.ReferralCashAccount{}).Where("user_id = ? AND locked_micro >= ?", payout.UserID, payout.CommissionMicro).Updates(map[string]any{"locked_micro": gorm.Expr("locked_micro - ?", payout.CommissionMicro), "available_micro": gorm.Expr("available_micro + ?", payout.CommissionMicro), "updated_at": now})
		if accountUpdate.Error != nil {
			return accountUpdate.Error
		}
		if accountUpdate.RowsAffected != 1 {
			return errors.New("cash account locked balance mismatch")
		}
		return tx.Model(&payout).Updates(map[string]any{"status": model.ReferralPayoutCancelled, "failure_message": safeFailure(reason), "completed_at": now}).Error
	})
	if err != nil {
		return ReferralPayoutView{}, err
	}
	return s.GetPayout(id)
}

func (s *ReferralCashService) QueryPayout(ctx context.Context, id int64) (ReferralPayoutView, error) {
	var payout model.ReferralPayout
	if err := s.db.First(&payout, id).Error; err != nil {
		return ReferralPayoutView{}, ErrReferralPayoutNotFound
	}
	if referralPayoutTerminal(payout.Status) || payout.Status == model.ReferralPayoutReviewPending || payout.Status == model.ReferralPayoutQueued {
		return s.payoutView(payout), nil
	}
	transfer, err := s.merchantTransferProvider(payout.ProviderID)
	if err != nil {
		return ReferralPayoutView{}, err
	}
	result, err := transfer.QueryMerchantTransfer(ctx, payout.OutBillNo)
	if err != nil {
		return ReferralPayoutView{}, err
	}
	if err := s.applyTransferResult(payout.ID, result); err != nil {
		return ReferralPayoutView{}, err
	}
	return s.GetPayout(id)
}

func (s *ReferralCashService) payoutView(item model.ReferralPayout) ReferralPayoutView {
	view := ReferralPayoutView{ReferralPayout: item}
	var user model.User
	if s.db.Select("email").First(&user, item.UserID).Error == nil {
		view.UserEmail = user.Email
	}
	var account model.ReferralPayoutAccount
	if s.db.Select("open_id_hint").First(&account, item.PayoutAccountID).Error == nil {
		view.OpenIDHint = account.OpenIDHint
	}
	return view
}

func (s *ReferralCashService) GetPayout(id int64) (ReferralPayoutView, error) {
	var item model.ReferralPayout
	if err := s.db.First(&item, id).Error; err != nil {
		return ReferralPayoutView{}, ErrReferralPayoutNotFound
	}
	return s.payoutView(item), nil
}

func (s *ReferralCashService) ListUserPayouts(userID int64) ([]ReferralPayoutView, error) {
	return s.listPayouts("user_id = ?", userID)
}

func (s *ReferralCashService) ListPayouts() ([]ReferralPayoutView, error) {
	return s.listPayouts("1 = 1")
}

func (s *ReferralCashService) listPayouts(where string, args ...any) ([]ReferralPayoutView, error) {
	var rows []model.ReferralPayout
	if err := s.db.Where(where, args...).Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]ReferralPayoutView, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.payoutView(row))
	}
	return items, nil
}

func (s *ReferralCashService) HandleWxPayWebhook(ctx context.Context, raw []byte, headers map[string]string) error {
	var providers []model.PaymentProviderInstance
	if err := s.db.Where("provider_key = ? AND status = ?", model.PaymentProviderWxPay, model.StatusActive).Order("id ASC").Find(&providers).Error; err != nil {
		return err
	}
	var lastErr error
	for _, item := range providers {
		transfer, err := s.merchantTransferProvider(item.ID)
		if err != nil {
			lastErr = err
			continue
		}
		result, err := transfer.VerifyMerchantTransfer(ctx, raw, headers)
		if err != nil {
			lastErr = err
			continue
		}
		if result == nil {
			return nil
		}
		var payout model.ReferralPayout
		if err := s.db.Where("out_bill_no = ? AND provider_id = ?", result.OutBillNo, item.ID).First(&payout).Error; err != nil {
			return ErrReferralPayoutNotFound
		}
		return s.applyTransferResult(payout.ID, result)
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no WeChat payout provider accepted webhook")
}
