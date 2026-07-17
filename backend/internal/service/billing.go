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
	RequestID    string
	UserID       int64
	APIKeyID     int64
	AccountID    int64
	GroupID      int64
	Model        string
	Stream       bool
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
	if err := s.db.Create(&entry).Error; err != nil {
		log.Printf("[billing] failed to write usage log: %v", err)
		return
	}
	if cost > 0 && !bc.SkipBalance {
		if err := s.db.Model(&model.User{}).Where("id = ?", bc.UserID).
			Update("balance_micro", gorm.Expr("balance_micro - ?", cost)).Error; err != nil {
			log.Printf("[billing] failed to deduct balance for user %d: %v", bc.UserID, err)
		}
	}
	if cost > 0 && bc.APIKeyID > 0 {
		if err := s.db.Model(&model.APIKey{}).Where("id = ?", bc.APIKeyID).
			Update("quota_used_micro", gorm.Expr("quota_used_micro + ?", cost)).Error; err != nil {
			log.Printf("[billing] failed to update quota for API key %d: %v", bc.APIKeyID, err)
		}
	}
}
