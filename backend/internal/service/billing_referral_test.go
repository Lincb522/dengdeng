package service

import (
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBillingSettlesReservationWhenReferralsAreDisabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:billing-reservation?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.APIKey{}, &model.ModelPrice{}, &model.UsageLog{},
		&model.Setting{}, &model.ReferralCode{}, &model.ReferralBinding{},
		&model.ReferralCommission{}, &model.ReferralCashAccount{}, &model.ReferralPayoutConfig{},
	); err != nil {
		t.Fatal(err)
	}
	user := model.User{
		Email: "reservation@example.test", PasswordHash: "x", Role: model.RoleUser,
		Status: model.StatusActive, BalanceMicro: 2_000_000, BalanceHeldMicro: 1_200_000,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{
		UserID: user.ID, GroupID: 1, KeyHash: "reservation-hash",
		KeyPreview: "reservation", Name: "reservation", Status: model.StatusActive,
		QuotaMicro: 2_000_000, QuotaHeldMicro: 1_200_000,
		DailyQuotaMicro: 2_000_000, DailyQuotaHeldMicro: 1_200_000,
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ModelPrice{Match: "gpt-reservation", InputPrice: 1}).Error; err != nil {
		t.Fatal(err)
	}
	settingsService := NewSystemSettingsService(db, config.Default())
	settings, err := settingsService.Get()
	if err != nil {
		t.Fatal(err)
	}
	settings.Features.ReferralEnabled = false
	if _, err := settingsService.Update(settings); err != nil {
		t.Fatal(err)
	}
	billing := NewBillingService(db, NewPricingService(db))
	billing.SetSystemSettings(settingsService)
	if err := billing.Record(BillContext{
		UserID: user.ID, APIKeyID: key.ID, GroupID: 1, Model: "gpt-reservation",
		Usage: Usage{InputTokens: 1_000_000}, Rates: RatePlan{Base: 1}, StatusCode: 200,
		ReservedBalanceMicro: 1_200_000, ReservedKeyQuotaMicro: 1_200_000, ReservedDailyMicro: 1_200_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&key, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.BalanceMicro != 1_000_000 || user.BalanceHeldMicro != 0 {
		t.Fatalf("balance=%d held=%d, want 1000000/0", user.BalanceMicro, user.BalanceHeldMicro)
	}
	if key.QuotaUsedMicro != 1_000_000 || key.QuotaHeldMicro != 0 || key.DailyQuotaHeldMicro != 0 {
		t.Fatalf("key used=%d held=%d daily-held=%d", key.QuotaUsedMicro, key.QuotaHeldMicro, key.DailyQuotaHeldMicro)
	}
}

func TestBillingSettlesReferralCommissionFromPaidUsage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:billing-referral?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.APIKey{}, &model.ModelPrice{}, &model.UsageLog{},
		&model.ReferralCode{}, &model.ReferralBinding{}, &model.ReferralCommission{}, &model.ReferralCashAccount{}, &model.ReferralPayoutConfig{},
	); err != nil {
		t.Fatal(err)
	}
	referrer := model.User{Email: "promoter@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive}
	referred := model.User{Email: "customer@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, BalanceMicro: 2_000_000}
	if err := db.Create(&referrer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&referred).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{UserID: referred.ID, GroupID: 1, KeyHash: "hash", KeyPreview: "preview", Name: "test", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	code := model.ReferralCode{Code: "DD-TESTCODE", OwnerUserID: referrer.ID, CommissionBps: 750, Status: model.StatusActive}
	if err := db.Create(&code).Error; err != nil {
		t.Fatal(err)
	}
	binding := model.ReferralBinding{ReferralCodeID: code.ID, ReferrerUserID: referrer.ID, ReferredUserID: referred.ID}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ModelPrice{Match: "gpt-test", InputPrice: 1}).Error; err != nil {
		t.Fatal(err)
	}

	billing := NewBillingService(db, NewPricingService(db))
	billing.Record(BillContext{
		UserID: referred.ID, APIKeyID: key.ID, GroupID: 1, Model: "gpt-test",
		Usage: Usage{InputTokens: 1_000_000}, Rates: RatePlan{Base: 1}, StatusCode: 200,
	})

	if err := db.First(&referred, referred.ID).Error; err != nil {
		t.Fatal(err)
	}
	if referred.BalanceMicro != 1_000_000 {
		t.Fatalf("referred balance = %d, want 1000000", referred.BalanceMicro)
	}
	if err := db.First(&referrer, referrer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if referrer.BalanceMicro != 0 {
		t.Fatalf("referrer API balance = %d, want 0", referrer.BalanceMicro)
	}
	var commission model.ReferralCommission
	if err := db.First(&commission).Error; err != nil {
		t.Fatal(err)
	}
	if commission.BaseCostMicro != 1_000_000 || commission.AmountMicro != 75_000 || commission.CommissionBps != 750 {
		t.Fatalf("unexpected commission: %#v", commission)
	}
	if commission.Status != model.ReferralCommissionPending || commission.AvailableAt == nil {
		t.Fatalf("commission cash status = %#v", commission)
	}
	var cash model.ReferralCashAccount
	if err := db.First(&cash, referrer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if cash.PendingMicro != 75_000 || cash.AvailableMicro != 0 {
		t.Fatalf("cash account = %#v", cash)
	}
}

func TestBillingDoesNotCommissionSponsoredUsage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:billing-referral-sponsored?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.ModelPrice{}, &model.UsageLog{},
		&model.ReferralCode{}, &model.ReferralBinding{}, &model.ReferralCommission{}, &model.ReferralCashAccount{}, &model.ReferralPayoutConfig{},
	); err != nil {
		t.Fatal(err)
	}
	referrer := model.User{Email: "promoter2@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive}
	referred := model.User{Email: "customer2@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive}
	db.Create(&referrer)
	db.Create(&referred)
	code := model.ReferralCode{Code: "DD-SPONSORED", OwnerUserID: referrer.ID, CommissionBps: 1000, Status: model.StatusActive}
	db.Create(&code)
	db.Create(&model.ReferralBinding{ReferralCodeID: code.ID, ReferrerUserID: referrer.ID, ReferredUserID: referred.ID})
	db.Create(&model.ModelPrice{Match: "gpt-sponsored", InputPrice: 1})

	NewBillingService(db, NewPricingService(db)).Record(BillContext{
		UserID: referred.ID, GroupID: 1, Model: "gpt-sponsored",
		Usage: Usage{InputTokens: 1_000_000}, Rates: RatePlan{Base: 1}, StatusCode: 200, SkipBalance: true,
	})

	var count int64
	db.Model(&model.ReferralCommission{}).Count(&count)
	if count != 0 {
		t.Fatalf("sponsored usage created %d commissions, want 0", count)
	}
	db.First(&referrer, referrer.ID)
	if referrer.BalanceMicro != 0 {
		t.Fatalf("referrer balance = %d, want 0", referrer.BalanceMicro)
	}
}
