package service

import (
	"log"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// BillingService turns extracted usage into a ledger entry and balance
// deduction. Balance may go negative on an in-flight request; the gateway
// blocks new requests once balance <= 0.
type BillingService struct {
	db      *gorm.DB
	pricing *PricingService
}

func NewBillingService(db *gorm.DB, pricing *PricingService) *BillingService {
	return &BillingService{db: db, pricing: pricing}
}

type BillContext struct {
	RequestID string
	UserID    int64
	APIKeyID  int64
	AccountID int64
	GroupID   int64
	Model     string
	Stream    bool
	// Effort is recorded for auditability; its multiplier is already folded
	// into Rates by the gateway before Record is called.
	Effort       string
	Usage        Usage
	Rates        RatePlan
	DurationMs   int64
	StatusCode   int
	ErrorMessage string
	// SkipBalance is true for a valid day pass or a request quota that was
	// reserved by the gateway. Usage is still logged at its normal cost.
	SkipBalance bool
}

func (s *BillingService) Record(bc BillContext) {
	cost := s.pricing.Cost(bc.Model, bc.Usage, bc.Rates)
	entry := model.UsageLog{
		RequestID:          bc.RequestID,
		UserID:             bc.UserID,
		APIKeyID:           bc.APIKeyID,
		AccountID:          bc.AccountID,
		GroupID:            bc.GroupID,
		Model:              bc.Model,
		Stream:             bc.Stream,
		ReasoningEffort:    bc.Effort,
		InputTokens:        bc.Usage.InputTokens,
		OutputTokens:       bc.Usage.OutputTokens,
		CacheReadTokens:    bc.Usage.CacheReadTokens,
		CacheWriteTokens:   bc.Usage.CacheWriteTokens,
		CacheWrite5mTokens: bc.Usage.CacheWrite5mTokens,
		CacheWrite1hTokens: bc.Usage.CacheWrite1hTokens,
		ImageCount:         bc.Usage.ImageCount,
		CostMicro:          cost,
		DurationMs:         bc.DurationMs,
		StatusCode:         bc.StatusCode,
		ErrorMessage:       bc.ErrorMessage,
		// Usage windows and monitoring filters are UTC. Persisting a local-zone
		// timestamp into SQLite makes lexical range comparisons silently exclude
		// recent rows on non-UTC hosts.
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}
		if cost > 0 && !bc.SkipBalance {
			if err := tx.Model(&model.User{}).Where("id = ?", bc.UserID).
				Update("balance_micro", gorm.Expr("balance_micro - ?", cost)).Error; err != nil {
				return err
			}
		}
		if cost > 0 && bc.APIKeyID > 0 {
			if err := tx.Model(&model.APIKey{}).Where("id = ?", bc.APIKeyID).
				Update("quota_used_micro", gorm.Expr("quota_used_micro + ?", cost)).Error; err != nil {
				return err
			}
		}
		// Commission follows real paid usage only. Day passes, request cards and
		// administrators do not create commission because no cash balance was
		// deducted for those calls.
		if cost > 0 && !bc.SkipBalance {
			if err := settleReferralCommission(tx, entry.ID, bc.UserID, cost); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Printf("[billing] failed to settle usage for user %d: %v", bc.UserID, err)
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
	// Split the multiplication to avoid overflowing if an administrator ever
	// credits an unusually large balance.
	amount := (costMicro/10_000)*bps + (costMicro%10_000)*bps/10_000
	if amount <= 0 {
		return nil
	}
	commission := model.ReferralCommission{
		UsageLogID:     usageLogID,
		ReferralCodeID: settlement.ReferralCodeID,
		ReferrerUserID: settlement.ReferrerUserID,
		ReferredUserID: referredUserID,
		BaseCostMicro:  costMicro,
		CommissionBps:  settlement.CommissionBps,
		AmountMicro:    amount,
		CreatedAt:      time.Now().UTC(),
	}
	if err := tx.Create(&commission).Error; err != nil {
		return err
	}
	return tx.Model(&model.User{}).Where("id = ?", settlement.ReferrerUserID).
		Update("balance_micro", gorm.Expr("balance_micro + ?", amount)).Error
}
