package service

import (
	"context"
	"testing"
	"time"

	"dengdeng/internal/config"
	appcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/payment"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func referralCashTestService(t *testing.T) (*ReferralCashService, *gorm.DB, model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{NowFunc: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.ReferralCommission{}, &model.ReferralCashAccount{}, &model.ReferralPayoutAccount{}, &model.ReferralPayoutConfig{}, &model.ReferralPayout{}, &model.PaymentConfig{}, &model.PaymentLedgerEntry{}, &model.PaymentProviderInstance{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: t.Name() + "@example.test", PasswordHash: "x", Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PaymentConfig{ID: 1, Currency: "CNY", CreditMicroPerUnit: 1_000_000, MinAmountMinor: 100, MaxAmountMinor: 1_000_000, OrderExpiryMinutes: 30, MaxPendingOrders: 3, LoadBalanceStrategy: "round_robin"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ReferralPayoutConfig{ID: 1, Enabled: true, Currency: "CNY", SettlementDays: 7, MinPayoutMinor: 100, MaxPayoutMinor: 200_000, DailyPayoutMinor: 500_000, RequireReview: true, WxProviderID: 99, WxTransferSceneID: "1000", SceneReportInfoType: "佣金类型", SceneReportInfoContent: "推广佣金", TransferRemark: "推广佣金"}).Error; err != nil {
		t.Fatal(err)
	}
	account := model.ReferralPayoutAccount{UserID: user.ID, Channel: model.PaymentProviderWxPay, OpenID: appcrypto.EncryptedString("openid-test-123456"), OpenIDHash: openIDHash("openid-test-123456"), OpenIDHint: openIDHint("openid-test-123456"), Status: model.ReferralPayoutAccountVerified}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	return NewReferralCashService(db, &config.Config{Site: config.SiteConfig{PublicURL: "https://example.test"}}), db, user
}

func TestReferralPayoutLocksCashWithoutTouchingAPIBalance(t *testing.T) {
	service, db, user := referralCashTestService(t)
	if err := db.Create(&model.ReferralCashAccount{UserID: user.ID, AvailableMicro: 1_500_000}).Error; err != nil {
		t.Fatal(err)
	}
	payout, err := service.RequestPayout(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if payout.Status != model.ReferralPayoutReviewPending || payout.AmountMinor != 150 || payout.CommissionMicro != 1_500_000 {
		t.Fatalf("unexpected payout: %#v", payout)
	}
	var cash model.ReferralCashAccount
	db.First(&cash, user.ID)
	if cash.AvailableMicro != 0 || cash.LockedMicro != 1_500_000 || cash.PaidMicro != 0 {
		t.Fatalf("unexpected cash account: %#v", cash)
	}
	db.First(&user, user.ID)
	if user.BalanceMicro != 0 {
		t.Fatalf("API balance changed by cash payout: %d", user.BalanceMicro)
	}
}

func TestReferralPayoutRejectUnlocksExactlyOnce(t *testing.T) {
	service, db, user := referralCashTestService(t)
	db.Create(&model.ReferralCashAccount{UserID: user.ID, AvailableMicro: 2_000_000})
	payout, err := service.RequestPayout(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RejectPayout(payout.ID, "risk review"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RejectPayout(payout.ID, "repeat"); err == nil {
		t.Fatal("repeated rejection should fail")
	}
	var cash model.ReferralCashAccount
	db.First(&cash, user.ID)
	if cash.AvailableMicro != 2_000_000 || cash.LockedMicro != 0 {
		t.Fatalf("cash unlocked incorrectly: %#v", cash)
	}
}

func TestReferralPayoutSuccessIsIdempotentAndWritesExpenseLedger(t *testing.T) {
	service, db, user := referralCashTestService(t)
	db.Create(&model.ReferralCashAccount{UserID: user.ID, AvailableMicro: 3_000_000})
	payout, err := service.RequestPayout(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := &payment.MerchantTransferResponse{OutBillNo: payout.OutBillNo, ProviderBillNo: "wx-bill-1", State: "SUCCESS", AmountMinor: payout.AmountMinor, OpenID: "openid-test-123456"}
	if err := service.applyTransferResult(payout.ID, result); err != nil {
		t.Fatal(err)
	}
	if err := service.applyTransferResult(payout.ID, result); err != nil {
		t.Fatal(err)
	}
	var cash model.ReferralCashAccount
	db.First(&cash, user.ID)
	if cash.LockedMicro != 0 || cash.PaidMicro != 3_000_000 {
		t.Fatalf("cash success settlement not idempotent: %#v", cash)
	}
	var entries []model.PaymentLedgerEntry
	db.Find(&entries)
	if len(entries) != 1 || entries[0].Category != "referral_payout" || entries[0].AmountMinor != 300 {
		t.Fatalf("unexpected cash expense ledger: %#v", entries)
	}
}

func TestReleaseMaturedReferralCommissionMovesCash(t *testing.T) {
	service, db, user := referralCashTestService(t)
	now := time.Now().UTC().Add(-time.Minute)
	db.Create(&model.ReferralCashAccount{UserID: user.ID, PendingMicro: 88_000})
	db.Create(&model.ReferralCommission{UsageLogID: 1, ReferralCodeID: 1, ReferrerUserID: user.ID, ReferredUserID: user.ID + 1, BaseCostMicro: 1_000_000, CommissionBps: 880, AmountMicro: 88_000, Status: model.ReferralCommissionPending, AvailableAt: &now})
	if err := service.ReleaseMatured(user.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ReleaseMatured(user.ID); err != nil {
		t.Fatal(err)
	}
	var cash model.ReferralCashAccount
	db.First(&cash, user.ID)
	if cash.PendingMicro != 0 || cash.AvailableMicro != 88_000 {
		t.Fatalf("maturity move not idempotent: %#v", cash)
	}
}

func TestReferralCashSnapshotUsesPayoutCurrencyForEveryBucket(t *testing.T) {
	service, db, user := referralCashTestService(t)
	db.Create(&model.ReferralCashAccount{
		UserID: user.ID, PendingMicro: 1_000_000, AvailableMicro: 2_000_000,
		LockedMicro: 3_000_000, PaidMicro: 4_000_000,
	})

	snapshot, err := service.Snapshot(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Currency != "CNY" || snapshot.PendingMinor != 100 || snapshot.AvailableMinor != 200 ||
		snapshot.LockedMinor != 300 || snapshot.PaidMinor != 400 || snapshot.TotalMinor != 1000 {
		t.Fatalf("unexpected cash snapshot: %#v", snapshot)
	}
}
