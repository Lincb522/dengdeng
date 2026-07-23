package service

import (
	"context"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func paymentTestService(t *testing.T) (*PaymentService, *gorm.DB) {
	t.Helper()
	if err := crypto.Init("", "payment-test-secret"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:payment-service-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.PaymentConfig{}, &model.PaymentProviderInstance{}, &model.PaymentOrder{}, &model.PaymentAuditLog{}); err != nil {
		t.Fatal(err)
	}
	return NewPaymentService(db, &config.Config{Site: config.SiteConfig{Name: "DengDeng", PublicURL: "https://pay.example.test"}}), db
}

func TestPaymentConfirmIsIdempotent(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "pay@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	order := model.PaymentOrder{OutTradeNo: "ddp_idempotent", UserID: user.ID, ProviderID: 1, ProviderKey: "easypay", PaymentMethod: "alipay", Status: model.PaymentStatusPending, Currency: "CNY", AmountMinor: 1200, CreditMicro: 3_000_000, ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.confirmPayment(order, "trade_1", 1200, "CNY", "test"); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	if err := svc.confirmPayment(order, "trade_1", 1200, "CNY", "test"); err != nil {
		t.Fatalf("repeated confirm must be harmless: %v", err)
	}
	var gotUser model.User
	var gotOrder model.PaymentOrder
	if err := db.First(&gotUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotUser.BalanceMicro != 3_000_000 {
		t.Fatalf("balance=%d, want 3000000", gotUser.BalanceMicro)
	}
	if gotOrder.Status != model.PaymentStatusCompleted {
		t.Fatalf("status=%s", gotOrder.Status)
	}
	var audits int64
	db.Model(&model.PaymentAuditLog{}).Where("order_id = ?", order.ID).Count(&audits)
	if audits != 1 {
		t.Fatalf("audit records=%d, want 1", audits)
	}
}

func TestPaymentConfirmRejectsAmountMismatch(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "mismatch@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	order := model.PaymentOrder{OutTradeNo: "ddp_mismatch", UserID: user.ID, ProviderID: 1, ProviderKey: "easypay", PaymentMethod: "alipay", Status: model.PaymentStatusPending, Currency: "CNY", AmountMinor: 1200, CreditMicro: 3_000_000, ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.confirmPayment(order, "trade_2", 1199, "CNY", "test"); err == nil {
		t.Fatal("expected amount mismatch")
	}
	var gotUser model.User
	var gotOrder model.PaymentOrder
	db.First(&gotUser, user.ID)
	db.First(&gotOrder, order.ID)
	if gotUser.BalanceMicro != 0 || gotOrder.Status != model.PaymentStatusPending {
		t.Fatalf("mismatch changed state: balance=%d status=%s", gotUser.BalanceMicro, gotOrder.Status)
	}
}

func TestPaymentConfigRequiresPublicURLAndRate(t *testing.T) {
	svc, _ := paymentTestService(t)
	svc.publicURL = ""
	_, err := svc.UpdateConfig(model.PaymentConfig{Enabled: true, Currency: "CNY", CreditMicroPerUnit: 1_000_000, MinAmountMinor: 100, MaxAmountMinor: 1000, OrderExpiryMinutes: 30, MaxPendingOrders: 3, LoadBalanceStrategy: "round_robin"})
	if err == nil {
		t.Fatal("enabled config without public URL should fail")
	}
}

func TestExpirePendingUsesUTCForSQLiteTimestampComparison(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "expiry-timezone@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	order := model.PaymentOrder{
		OutTradeNo:    "ddp_expiry_timezone",
		UserID:        user.ID,
		ProviderID:    1,
		ProviderKey:   "wxpay",
		PaymentMethod: "wxpay",
		Status:        model.PaymentStatusPending,
		Currency:      "CNY",
		AmountMinor:   100,
		CreditMicro:   1_000_000,
		// Payment orders are persisted as UTC. SQLite compares the textual
		// representation, so the service must query with UTC as well.
		ExpiresAt: time.Now().UTC().Add(20 * time.Minute),
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ExpirePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got model.PaymentOrder
	if err := db.First(&got, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != model.PaymentStatusPending {
		t.Fatalf("future UTC order status=%s, want %s", got.Status, model.PaymentStatusPending)
	}
}

func TestNewPaymentOrderNoFitsWxPayLimit(t *testing.T) {
	for i := 0; i < 10; i++ {
		orderNo := newPaymentOrderNo(model.PaymentProviderWxPay)
		if len(orderNo) != 32 {
			t.Fatalf("wxpay order number length=%d, want 32", len(orderNo))
		}
		if orderNo[:4] != "ddp_" {
			t.Fatalf("wxpay order number prefix=%q", orderNo[:4])
		}
	}
	if got := newPaymentOrderNo(model.PaymentProviderEasyPay); len(got) != 36 {
		t.Fatalf("other provider order number length=%d, want 36", len(got))
	}
}

func TestListPaymentOrdersIncludesRechargeUser(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "order-owner@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	order := model.PaymentOrder{
		OutTradeNo:    "ddp_order_owner",
		UserID:        user.ID,
		ProviderID:    1,
		ProviderKey:   "wxpay",
		PaymentMethod: "wxpay",
		Status:        model.PaymentStatusCompleted,
		Currency:      "CNY",
		AmountMinor:   100,
		CreditMicro:   1_000_000,
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}

	orders, err := svc.ListOrders(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range orders {
		if result.ID != order.ID {
			continue
		}
		if result.UserID != user.ID || result.UserEmail != user.Email {
			t.Fatalf("order user = %d %q, want %d %q", result.UserID, result.UserEmail, user.ID, user.Email)
		}
		return
	}
	t.Fatalf("order %d not returned", order.ID)
}
