package service

import (
	"testing"
	"time"

	"dengdeng/internal/model"
)

func TestCompleteRefundWritesOneExpenseEntry(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "refund-ledger@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	order := model.PaymentOrder{
		OutTradeNo:    "ddp_refund_ledger",
		UserID:        user.ID,
		ProviderID:    1,
		ProviderKey:   "wxpay",
		PaymentMethod: "wxpay",
		Status:        model.PaymentStatusRefunding,
		Currency:      "CNY",
		AmountMinor:   2500,
		CreditMicro:   25_000_000,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.completeRefund(order, "refund_1", "test"); err != nil {
		t.Fatalf("complete refund: %v", err)
	}
	if err := svc.completeRefund(order, "refund_1", "test"); err != nil {
		t.Fatalf("repeat refund completion: %v", err)
	}
	var got model.PaymentOrder
	if err := db.First(&got, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != model.PaymentStatusRefunded || got.RefundedAt == nil || got.RefundedMicro != order.CreditMicro {
		t.Fatalf("unexpected refunded order: %+v", got)
	}
	var entries []model.PaymentLedgerEntry
	if err := db.Where("order_id = ? AND kind = ?", order.ID, model.PaymentLedgerExpense).Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].AmountMinor != order.AmountMinor || entries[0].CreditMicro != order.CreditMicro {
		t.Fatalf("unexpected refund ledger: %+v", entries)
	}
}

func TestListLedgerReturnsFilteredStatsAndUser(t *testing.T) {
	svc, db := paymentTestService(t)
	user := model.User{Email: "ledger-filter@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	orders := []model.PaymentOrder{
		{OutTradeNo: "ddp_ledger_income", UserID: user.ID, ProviderID: 1, ProviderKey: "stripe", PaymentMethod: "card", Status: model.PaymentStatusCompleted, Currency: "HKD", AmountMinor: 5000, CreditMicro: 5_000_000, ExpiresAt: now.Add(time.Hour)},
		{OutTradeNo: "ddp_ledger_refund", UserID: user.ID, ProviderID: 1, ProviderKey: "stripe", PaymentMethod: "card", Status: model.PaymentStatusRefunded, Currency: "HKD", AmountMinor: 1200, CreditMicro: 1_200_000, RefundedMicro: 1_200_000, ExpiresAt: now.Add(time.Hour)},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	entries := []model.PaymentLedgerEntry{
		{EventKey: "test-income:" + orders[0].OutTradeNo, OrderID: orders[0].ID, UserID: user.ID, Kind: model.PaymentLedgerIncome, Currency: "HKD", AmountMinor: 5000, CreditMicro: 5_000_000, ProviderKey: "stripe", PaymentMethod: "card", OccurredAt: now.Add(-2 * time.Hour)},
		{EventKey: "test-income:" + orders[1].OutTradeNo, OrderID: orders[1].ID, UserID: user.ID, Kind: model.PaymentLedgerIncome, Currency: "HKD", AmountMinor: 1200, CreditMicro: 1_200_000, ProviderKey: "stripe", PaymentMethod: "card", OccurredAt: now.Add(-time.Hour)},
		{EventKey: "test-expense:" + orders[1].OutTradeNo, OrderID: orders[1].ID, UserID: user.ID, Kind: model.PaymentLedgerExpense, Currency: "HKD", AmountMinor: 1200, CreditMicro: 1_200_000, ProviderKey: "stripe", PaymentMethod: "card", OccurredAt: now},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.ListLedger(PaymentLedgerFilter{Page: 1, Size: 10, Period: "7d", Kind: model.PaymentLedgerIncome, Currency: "HKD", ProviderKey: "stripe", User: "ledger-filter"})
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("income page total=%d items=%d, want 2", result.Total, len(result.Items))
	}
	if result.Items[0].UserEmail != user.Email || result.Items[0].OrderNo == "" {
		t.Fatalf("missing joined ledger context: %+v", result.Items[0])
	}
	if result.Summary.IncomeMinor != 6200 || result.Summary.ExpenseMinor != 1200 || result.Summary.NetMinor != 5000 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if result.Summary.IncomeCount != 2 || result.Summary.ExpenseCount != 1 || len(result.Trend) == 0 {
		t.Fatalf("unexpected ledger counts/trend: summary=%+v trend=%+v", result.Summary, result.Trend)
	}
}
