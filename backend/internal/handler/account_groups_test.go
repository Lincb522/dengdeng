package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAccountCanMoveAndServeMultipleSamePlatformGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := crypto.Init("", "account-groups-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:account-groups-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}, &model.UpstreamAccountGroup{}, &model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}); err != nil {
		t.Fatal(err)
	}
	groups := []model.Group{
		{Name: "openai-a", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "openai-b", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "claude", Platform: model.PlatformAnthropic, Status: model.StatusActive},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRecorder()
	createCtx, _ := gin.CreateTestContext(create)
	createBody := fmt.Sprintf(`{"name":"shared","group_ids":[%d,%d],"auth_type":"api_key","api_key":"sk-test","base_url":"https://relay.example/v1","quota_url":"/v1/usage"}`, groups[0].ID, groups[1].ID)
	createCtx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts", strings.NewReader(createBody))
	createCtx.Request.Header.Set("Content-Type", "application/json")
	(&AdminHandler{db: db}).CreateAccount(createCtx)
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	var account model.UpstreamAccount
	if err := db.First(&account).Error; err != nil {
		t.Fatal(err)
	}
	var bindings []model.UpstreamAccountGroup
	if err := db.Where("upstream_account_id = ?", account.ID).Order("group_id ASC").Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if account.GroupID != groups[0].ID || len(bindings) != 2 || bindings[0].GroupID != groups[0].ID || bindings[1].GroupID != groups[1].ID {
		t.Fatalf("created account=%#v bindings=%#v", account, bindings)
	}

	list := httptest.NewRecorder()
	listCtx, _ := gin.CreateTestContext(list)
	listCtx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/admin/accounts?page=1&size=24&group_id=%d", groups[1].ID), nil)
	(&AdminHandler{db: db}).ListAccounts(listCtx)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"shared"`) || !strings.Contains(list.Body.String(), fmt.Sprintf(`"group_ids":[%d,%d]`, groups[0].ID, groups[1].ID)) {
		t.Fatalf("secondary group list status=%d body=%s", list.Code, list.Body.String())
	}

	update := httptest.NewRecorder()
	updateCtx, _ := gin.CreateTestContext(update)
	updateCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/accounts/"+strconv.FormatInt(account.ID, 10), strings.NewReader(fmt.Sprintf(`{"group_ids":[%d]}`, groups[1].ID)))
	updateCtx.Request.Header.Set("Content-Type", "application/json")
	updateCtx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(account.ID, 10)}}
	(&AdminHandler{db: db}).UpdateAccount(updateCtx)
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
	if err := db.First(&account, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if account.GroupID != groups[1].ID || account.BaseURL != "https://relay.example/v1" || account.QuotaURL != "/v1/usage" {
		t.Fatalf("updated account=%#v", account)
	}
	if err := db.Where("upstream_account_id = ?", account.ID).Find(&bindings).Error; err != nil || len(bindings) != 1 || bindings[0].GroupID != groups[1].ID {
		t.Fatalf("moved bindings=%#v err=%v", bindings, err)
	}

	crossPlatform := httptest.NewRecorder()
	crossCtx, _ := gin.CreateTestContext(crossPlatform)
	crossCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/accounts/"+strconv.FormatInt(account.ID, 10), strings.NewReader(fmt.Sprintf(`{"group_ids":[%d,%d]}`, groups[1].ID, groups[2].ID)))
	crossCtx.Request.Header.Set("Content-Type", "application/json")
	crossCtx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(account.ID, 10)}}
	(&AdminHandler{db: db}).UpdateAccount(crossCtx)
	if crossPlatform.Code != http.StatusBadRequest {
		t.Fatalf("cross-platform status=%d body=%s", crossPlatform.Code, crossPlatform.Body.String())
	}
}
