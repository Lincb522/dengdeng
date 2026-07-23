package service

import (
	"testing"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBillingPersistsCostBreakdownSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:billing-breakdown-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.APIKey{}, &model.ModelPrice{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "breakdown@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, BalanceMicro: 1_000_000, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	price := model.ModelPrice{Match: "snapshot-model", InputPrice: 5, OutputPrice: 30, CacheReadPrice: .5}
	if err := db.Create(&price).Error; err != nil {
		t.Fatal(err)
	}

	NewBillingService(db, NewPricingService(db)).Record(BillContext{
		RequestID:   "req_breakdown",
		UserID:      user.ID,
		Model:       "snapshot-model",
		ServiceTier: "priority",
		Usage: Usage{
			InputTokens:     1_000,
			OutputTokens:    100,
			CacheReadTokens: 200,
		},
		Rates:       RatePlan{Base: .5, CacheRead: .1},
		StatusCode:  200,
		SkipBalance: true,
	})

	var log model.UsageLog
	if err := db.Where("request_id = ?", "req_breakdown").First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.CostMicro <= 0 || log.RawCostMicro <= log.CostMicro {
		t.Fatalf("unexpected totals: charged=%d raw=%d", log.CostMicro, log.RawCostMicro)
	}
	if log.InputCostMicro+log.OutputCostMicro+log.CacheReadCostMicro != log.CostMicro {
		t.Fatalf("components do not add up to charge: %#v", log)
	}
	if log.InputUnitPrice != 5 || log.OutputUnitPrice != 30 || log.CacheReadUnitPrice != .5 {
		t.Fatalf("unit prices were not snapshotted: %#v", log)
	}
	if log.ServiceTier != "priority" || log.EffectiveMultiplier <= 0 {
		t.Fatalf("billing metadata was not snapshotted: %#v", log)
	}
}
