package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestListUsersIncludesGroupRateOverridesForTokenCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:admin-user-list-rates?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserGroupRate{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "capacity@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UserGroupRate{UserID: user.ID, GroupID: 42, RateMultiplier: .8}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	(&AdminHandler{db: db}).ListUsers(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data []struct {
			ID         int64             `json:"id"`
			GroupRates map[int64]float64 `json:"group_rates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != user.ID || response.Data[0].GroupRates[42] != .8 {
		t.Fatalf("unexpected response: %#v", response.Data)
	}
}
