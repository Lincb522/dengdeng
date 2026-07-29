package service

import (
	"log"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BillingService turns extracted usage into a ledger entry and balance
// deduction. Balance may go negative on an in-flight request; the gateway
// blocks new requests once balance <= 0.
type BillingService struct {
	db       *gorm.DB
	pricing  *PricingService
	geo      *IPGeoResolver
	settings *SystemSettingsService
}

func NewBillingService(db *gorm.DB, pricing *PricingService) *BillingService {
	return &BillingService{db: db, pricing: pricing}
}

func (s *BillingService) SetIPGeoResolver(resolver *IPGeoResolver)          { s.geo = resolver }
func (s *BillingService) SetSystemSettings(settings *SystemSettingsService) { s.settings = settings }

type BillContext struct {
	RequestID       string
	ClientRequestID string
	UserID          int64
	APIKeyID        int64
	AccountID       int64
	GroupID         int64
	Model           string
	Platform        string
	RequestPath     string
	ClientIP        string
	UserAgent       string
	Stream          bool
	// Effort is recorded for auditability: the per-effort multiplier is
	// already folded into Rates by the gateway before Record is called.
	Effort       string
	ServiceTier  string
	BillingMode  string
	Usage        Usage
	Rates        RatePlan
	FirstTokenMs int64
	DurationMs   int64
	QueueMs      int64
	ScheduleMs   int64
	UpstreamMs   int64
	AttemptCount int
	StatusCode   int
	ErrorMessage string
	// SkipBalance is true for a valid day pass or a request quota that was
	// reserved by the gateway. Usage is still logged at its normal cost.
	SkipBalance bool
	// Reservations are created atomically before the upstream request. Billing
	// releases them and records the actual charge in the same transaction.
	ReservedBalanceMicro  int64
	ReservedKeyQuotaMicro int64
	ReservedDailyMicro    int64
}

// EstimateMaximum reserves a conservative upper bound before a request starts.
// Counting every request byte as an input token intentionally overestimates
// UTF-8/JSON prompts; the margin also covers cache-write premiums.
func (s *BillingService) EstimateMaximum(modelName string, bodyBytes int, maxOutputTokens, imageCount int64, rates RatePlan) int64 {
	if imageCount > 0 {
		estimate := s.pricing.Cost(modelName, Usage{ImageCount: imageCount}, rates)
		return estimate + estimate/10
	}
	if maxOutputTokens <= 0 {
		maxOutputTokens = 8_192
	}
	inputTokens := int64(bodyBytes)
	if inputTokens < 1 {
		inputTokens = 1
	}
	estimate := s.pricing.Cost(modelName, Usage{
		InputTokens:  inputTokens,
		OutputTokens: maxOutputTokens,
	}, rates)
	return estimate + estimate/5
}

func (s *BillingService) Record(bc BillContext) error {
	referralsEnabled := true
	if s.settings != nil {
		settings, err := s.settings.Get()
		referralsEnabled = err == nil && settings.Features.ReferralEnabled
	}
	breakdown := s.pricing.Breakdown(bc.Model, bc.Usage, bc.Rates)
	cost := breakdown.TotalMicro
	entry := model.UsageLog{
		RequestID:             bc.RequestID,
		ClientRequestID:       bc.ClientRequestID,
		UserID:                bc.UserID,
		APIKeyID:              bc.APIKeyID,
		AccountID:             bc.AccountID,
		GroupID:               bc.GroupID,
		Model:                 bc.Model,
		RequestPath:           bc.RequestPath,
		ClientIP:              bc.ClientIP,
		UserAgent:             bc.UserAgent,
		Stream:                bc.Stream,
		ReasoningEffort:       bc.Effort,
		InputTokens:           bc.Usage.InputTokens,
		OutputTokens:          bc.Usage.OutputTokens,
		CacheReadTokens:       bc.Usage.CacheReadTokens,
		CacheWriteTokens:      bc.Usage.CacheWriteTokens,
		CacheWrite5mTokens:    bc.Usage.CacheWrite5mTokens,
		CacheWrite1hTokens:    bc.Usage.CacheWrite1hTokens,
		ImageCount:            bc.Usage.ImageCount,
		CostMicro:             cost,
		InputCostMicro:        breakdown.InputMicro,
		OutputCostMicro:       breakdown.OutputMicro,
		CacheReadCostMicro:    breakdown.CacheReadMicro,
		CacheWriteCostMicro:   breakdown.CacheWriteMicro,
		ImageCostMicro:        breakdown.ImageMicro,
		RawCostMicro:          breakdown.RawMicro,
		EffectiveMultiplier:   breakdown.EffectiveMultiplier,
		InputUnitPrice:        breakdown.InputUnitPrice,
		OutputUnitPrice:       breakdown.OutputUnitPrice,
		CacheReadUnitPrice:    breakdown.CacheReadUnitPrice,
		CacheWriteUnitPrice:   breakdown.CacheWriteUnitPrice,
		CacheWrite5mUnitPrice: breakdown.CacheWrite5mPrice,
		CacheWrite1hUnitPrice: breakdown.CacheWrite1hPrice,
		ImageUnitPrice:        breakdown.ImageUnitPrice,
		ServiceTier:           bc.ServiceTier,
		BillingMode:           bc.BillingMode,
		FirstTokenMs:          bc.FirstTokenMs,
		DurationMs:            bc.DurationMs,
		QueueMs:               bc.QueueMs,
		ScheduleMs:            bc.ScheduleMs,
		UpstreamMs:            bc.UpstreamMs,
		AttemptCount:          bc.AttemptCount,
		StatusCode:            bc.StatusCode,
		ErrorMessage:          bc.ErrorMessage,
		// Usage windows and monitoring filters are UTC. Persisting a local-zone
		// timestamp into SQLite makes lexical range comparisons silently exclude
		// recent rows on non-UTC hosts.
		CreatedAt: time.Now().UTC(),
	}
	var errorLogID int64
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		if entry.StatusCode < http.StatusOK || entry.StatusCode >= http.StatusBadRequest {
			errorLog := opsErrorFromUsage(entry, bc.Platform)
			if err := tx.Create(&errorLog).Error; err == nil {
				errorLogID = errorLog.ID
			} else {
				// Usage and balance settlement are authoritative. A monitoring
				// sidecar must not roll them back during a rolling schema upgrade.
				log.Printf("[billing] ops error sidecar unavailable for request %s: %v", entry.RequestID, err)
			}
		}
		if bc.ReservedBalanceMicro > 0 || (cost > 0 && !bc.SkipBalance) {
			if err := tx.Model(&model.User{}).Where("id = ?", bc.UserID).Updates(map[string]any{
				"balance_held_micro": gorm.Expr(
					"CASE WHEN balance_held_micro >= ? THEN balance_held_micro - ? ELSE 0 END",
					bc.ReservedBalanceMicro, bc.ReservedBalanceMicro,
				),
				"balance_micro": gorm.Expr(
					"CASE WHEN balance_micro >= ? THEN balance_micro - ? ELSE 0 END",
					cost, cost,
				),
			}).Error; err != nil {
				return err
			}
		}
		if bc.APIKeyID > 0 && (cost > 0 || bc.ReservedKeyQuotaMicro > 0 || bc.ReservedDailyMicro > 0) {
			if err := tx.Model(&model.APIKey{}).Where("id = ?", bc.APIKeyID).Updates(map[string]any{
				"quota_held_micro": gorm.Expr(
					"CASE WHEN quota_held_micro >= ? THEN quota_held_micro - ? ELSE 0 END",
					bc.ReservedKeyQuotaMicro, bc.ReservedKeyQuotaMicro,
				),
				"daily_quota_held_micro": gorm.Expr(
					"CASE WHEN daily_quota_held_micro >= ? THEN daily_quota_held_micro - ? ELSE 0 END",
					bc.ReservedDailyMicro, bc.ReservedDailyMicro,
				),
				"quota_used_micro": gorm.Expr(
					"CASE WHEN quota_micro > 0 AND quota_used_micro + ? > quota_micro THEN quota_micro ELSE quota_used_micro + ? END",
					cost, cost,
				),
			}).Error; err != nil {
				return err
			}
		}
		// Commission follows real paid usage only. Day passes, request cards and
		// administrators do not create commission because no cash balance was
		// deducted for those calls.
		if referralsEnabled && cost > 0 && !bc.SkipBalance {
			if err := settleReferralCommission(tx, entry.ID, bc.UserID, cost); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Printf("[billing] failed to settle usage for user %d: %v", bc.UserID, err)
		return err
	}
	if s.geo != nil && entry.ClientIP != "" {
		s.geo.Enrich(entry.ID, errorLogID, entry.ClientIP)
	}
	return nil
}

func opsErrorFromUsage(entry model.UsageLog, platform string) model.OpsErrorLog {
	phase, errorType, source := "response", "upstream", "provider"
	businessLimited := entry.StatusCode == http.StatusTooManyRequests
	retryable := businessLimited || entry.StatusCode == http.StatusBadGateway || entry.StatusCode == http.StatusServiceUnavailable || entry.StatusCode == 529
	switch {
	case entry.StatusCode == http.StatusUnauthorized || entry.StatusCode == http.StatusForbidden:
		phase, errorType = "authentication", "authentication"
	case entry.StatusCode == http.StatusBadRequest || entry.StatusCode == http.StatusRequestEntityTooLarge:
		phase, errorType, source = "request", "invalid_request", "client"
	case entry.StatusCode == http.StatusTooManyRequests:
		phase, errorType = "routing", "rate_limit"
	case entry.StatusCode == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(entry.ErrorMessage), "no available upstream"):
		phase, errorType, source = "routing", "no_available_account", "scheduler"
	case entry.StatusCode >= http.StatusInternalServerError && entry.StatusCode < 600:
		phase, errorType = "upstream", "upstream_error"
	}
	severity := "P2"
	if entry.StatusCode >= 500 {
		severity = "P1"
	}
	return model.OpsErrorLog{
		UsageLogID: entry.ID, RequestID: entry.RequestID, ClientRequestID: entry.ClientRequestID,
		UserID: entry.UserID, APIKeyID: entry.APIKeyID, AccountID: entry.AccountID, GroupID: entry.GroupID,
		Platform: platform, Model: entry.Model, RequestPath: entry.RequestPath, ClientIP: entry.ClientIP,
		UserAgent: entry.UserAgent, StatusCode: entry.StatusCode, ErrorPhase: phase, ErrorType: errorType,
		ErrorSource: source, Severity: severity, BusinessLimited: businessLimited, Retryable: retryable,
		ErrorMessage: entry.ErrorMessage, UpstreamErrorChain: entry.ErrorMessage, DurationMs: entry.DurationMs, FirstTokenMs: entry.FirstTokenMs,
		CreatedAt: entry.CreatedAt,
	}
}

type referralSettlement struct {
	ReferralCodeID int64
	ReferrerUserID int64
	CommissionBps  int
}

func settleReferralCommission(tx *gorm.DB, usageLogID, referredUserID, costMicro int64) error {
	var settlement referralSettlement
	err := tx.Table("referral_bindings").
		Select("referral_bindings.referral_code_id, referral_bindings.referrer_user_id, referral_codes.commission_bps").
		Joins("JOIN referral_codes ON referral_codes.id = referral_bindings.referral_code_id").
		Where("referral_bindings.referred_user_id = ? AND referral_codes.status = ?", referredUserID, model.StatusActive).
		Take(&settlement).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if settlement.ReferrerUserID == 0 || settlement.ReferrerUserID == referredUserID ||
		settlement.CommissionBps < 500 || settlement.CommissionBps > 1000 {
		return nil
	}
	bps := int64(settlement.CommissionBps)
	amount := (costMicro/10_000)*bps + (costMicro%10_000)*bps/10_000
	if amount <= 0 {
		return nil
	}
	now := time.Now().UTC()
	settlementDays := 7
	var cashConfig model.ReferralPayoutConfig
	if err := tx.First(&cashConfig, 1).Error; err == nil {
		settlementDays = cashConfig.SettlementDays
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	if settlementDays < 0 {
		settlementDays = 0
	}
	availableAt := now.Add(time.Duration(settlementDays) * 24 * time.Hour)
	status := model.ReferralCommissionPending
	accountColumn := "pending_micro"
	if settlementDays == 0 {
		status = model.ReferralCommissionAvailable
		accountColumn = "available_micro"
	}
	commission := model.ReferralCommission{
		UsageLogID: usageLogID, ReferralCodeID: settlement.ReferralCodeID,
		ReferrerUserID: settlement.ReferrerUserID, ReferredUserID: referredUserID,
		BaseCostMicro: costMicro, CommissionBps: settlement.CommissionBps,
		AmountMicro: amount, Status: status, AvailableAt: &availableAt, CreatedAt: now,
	}
	if err := tx.Create(&commission).Error; err != nil {
		return err
	}
	account := model.ReferralCashAccount{UserID: settlement.ReferrerUserID, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
		return err
	}
	return tx.Model(&model.ReferralCashAccount{}).Where("user_id = ?", settlement.ReferrerUserID).
		Updates(map[string]any{accountColumn: gorm.Expr(accountColumn+" + ?", amount), "updated_at": now}).Error
}
