package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRedeemGrantsEachEntitlementKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:redeem-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.RedeemCode{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "redeem@example.test", PasswordHash: "x", Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	codes := []model.RedeemCode{
		{Code: "dd-gift-days", Kind: model.RedeemKindDays, Value: 3},
		{Code: "dd-gift-requests", Kind: model.RedeemKindRequests, Value: 7},
		// Empty Kind verifies backward compatibility with existing money codes.
		{Code: "dd-gift-legacy", AmountMicro: 2_500_000},
	}
	if err := db.Create(&codes).Error; err != nil {
		t.Fatal(err)
	}
	h := NewUserHandler(db, &config.Config{})

	for _, code := range []string{"dd-gift-days", "dd-gift-requests", "dd-gift-legacy"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/redeem", bytes.NewBufferString(`{"code":"`+code+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(middleware.CtxUser, &user)
		h.Redeem(c)
		if w.Code != http.StatusOK {
			t.Fatalf("redeem %s status = %d, body = %s", code, w.Code, w.Body.String())
		}
	}

	var got model.User
	if err := db.First(&got, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.AccessExpiresAt == nil || got.AccessExpiresAt.Before(time.Now().AddDate(0, 0, 2)) {
		t.Fatalf("day entitlement was not applied: %#v", got.AccessExpiresAt)
	}
	if got.RemainingRequests != 7 {
		t.Fatalf("remaining requests = %d, want 7", got.RemainingRequests)
	}
	if got.BalanceMicro != 2_500_000 {
		t.Fatalf("balance = %d, want 2500000", got.BalanceMicro)
	}
}
