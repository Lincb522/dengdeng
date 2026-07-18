package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestReferralDashboardSerializesEmptyCollectionsAsArrays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:referral-dashboard-empty?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.ReferralCode{}, &model.ReferralBinding{}, &model.ReferralCommission{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "referral-empty@example.test", PasswordHash: "x", Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/referrals", nil)
	ctx.Set(middleware.CtxUser, &user)
	NewUserHandler(db, &config.Config{}).ReferralDashboard(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Codes       json.RawMessage `json:"codes"`
			Commissions json.RawMessage `json:"commissions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.Data.Codes) != "[]" || string(response.Data.Commissions) != "[]" {
		t.Fatalf("empty collections must be arrays: %s", recorder.Body.String())
	}
}
