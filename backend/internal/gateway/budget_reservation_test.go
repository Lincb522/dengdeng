package gateway

import (
	"errors"
	"testing"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUsageBudgetReservationBlocksConcurrentOverspend(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:budget-reservation-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.APIKey{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "budget@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, BalanceMicro: 100}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{
		UserID: user.ID, GroupID: 1, KeyHash: "budget-key", KeyPreview: "dd-budget",
		Name: "budget", Status: model.StatusActive, QuotaMicro: 100,
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{db: db}
	first := &authedKey{User: user, Key: key}
	reserved, err := gw.reserveUsageBudget(first, 80)
	if err != nil {
		t.Fatalf("first reservation: %v", err)
	}
	first.Budget = reserved

	second := &authedKey{User: user, Key: key}
	if _, err := gw.reserveUsageBudget(second, 80); !errors.Is(err, errInsufficientUsageBudget) {
		t.Fatalf("second reservation error=%v, want insufficient budget", err)
	}

	var storedUser model.User
	var storedKey model.APIKey
	db.First(&storedUser, user.ID)
	db.First(&storedKey, key.ID)
	if storedUser.BalanceHeldMicro != 80 || storedKey.QuotaHeldMicro != 80 {
		t.Fatalf("holds balance=%d key=%d, want 80/80", storedUser.BalanceHeldMicro, storedKey.QuotaHeldMicro)
	}

	gw.releaseUsageBudget(first)
	db.First(&storedUser, user.ID)
	db.First(&storedKey, key.ID)
	if storedUser.BalanceHeldMicro != 0 || storedKey.QuotaHeldMicro != 0 {
		t.Fatalf("released holds balance=%d key=%d", storedUser.BalanceHeldMicro, storedKey.QuotaHeldMicro)
	}
}
