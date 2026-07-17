package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestReorderAccountsPersistsConsoleOrderWithoutChangingPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:account-order-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	accounts := []model.UpstreamAccount{
		{Name: "first", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 30, Status: model.StatusActive},
		{Name: "second", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 20, Status: model.StatusActive},
		{Name: "third", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
	}
	for index := range accounts {
		if err := db.Create(&accounts[index]).Error; err != nil {
			t.Fatalf("create account: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/accounts/order", strings.NewReader(`{"account_ids":[3,1,2]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&AdminHandler{db: db}).ReorderAccounts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var stored []model.UpstreamAccount
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load accounts: %v", err)
	}
	if stored[0].DisplayOrder != 2 || stored[1].DisplayOrder != 3 || stored[2].DisplayOrder != 1 {
		t.Fatalf("unexpected display order: %#v", stored)
	}
	if stored[0].Priority != 30 || stored[1].Priority != 20 || stored[2].Priority != 10 {
		t.Fatalf("display reordering changed scheduler priorities: %#v", stored)
	}
}

func TestListAccountsPaginatesAndFiltersCredentialType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:account-list-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}, &model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	group := model.Group{Name: "openai", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	accounts := []model.UpstreamAccount{
		{GroupID: group.ID, Name: "omega", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, DisplayOrder: 1, Status: model.StatusActive},
		{GroupID: group.ID, Name: "alpha", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, DisplayOrder: 2, Status: model.StatusActive},
		{GroupID: group.ID, Name: "oauth", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, DisplayOrder: 3, Status: model.StatusActive},
	}
	for index := range accounts {
		if err := db.Create(&accounts[index]).Error; err != nil {
			t.Fatalf("create account: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts?page=1&size=1&platform=openai&auth_type=api_key&sort=name&order=asc", nil)
	(&AdminHandler{db: db}).ListAccounts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Items []model.UpstreamAccount `json:"items"`
			Total int64                   `json:"total"`
			Page  int                     `json:"page"`
			Size  int                     `json:"size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Code != 0 || payload.Data.Total != 2 || payload.Data.Page != 1 || payload.Data.Size != 1 {
		t.Fatalf("unexpected page metadata: %#v", payload)
	}
	if len(payload.Data.Items) != 1 || payload.Data.Items[0].Name != "alpha" || payload.Data.Items[0].AuthType != model.AuthAPIKey {
		t.Fatalf("credential filter/sort returned %#v", payload.Data.Items)
	}
}

func TestReorderAccountByPlacementRenumbersTheWholeSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:account-placement-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	accounts := []model.UpstreamAccount{
		{Name: "one", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 40, DisplayOrder: 1, Status: model.StatusActive},
		{Name: "two", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 30, DisplayOrder: 2, Status: model.StatusActive},
		{Name: "three", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 20, DisplayOrder: 3, Status: model.StatusActive},
		{Name: "four", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, DisplayOrder: 4, Status: model.StatusActive},
	}
	for index := range accounts {
		if err := db.Create(&accounts[index]).Error; err != nil {
			t.Fatalf("create account: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/accounts/order", strings.NewReader(`{"source_id":3,"target_id":1,"placement":"before"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&AdminHandler{db: db}).ReorderAccounts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var stored []model.UpstreamAccount
	if err := db.Order("display_order ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load accounts: %v", err)
	}
	if got := []string{stored[0].Name, stored[1].Name, stored[2].Name, stored[3].Name}; strings.Join(got, ",") != "three,one,two,four" {
		t.Fatalf("placement order = %v", got)
	}
	if stored[0].Priority != 20 || stored[1].Priority != 40 || stored[2].Priority != 30 || stored[3].Priority != 10 {
		t.Fatalf("placement changed scheduler priorities: %#v", stored)
	}
}
