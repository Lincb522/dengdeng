package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestImportPlatformlessOAuthUsesSelectedGrokGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := appcrypto.Init("", "grok-import-target-group-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:grok-import-target-group?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "grok", Platform: model.PlatformGrok, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	result := importAccountPayload(t, db, group.ID, `{"accounts":[{"name":"grok-oauth","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","client_id":"grok-client"}}]}`)
	if result.Imported != 1 || result.Skipped != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	var account model.UpstreamAccount
	if err := db.First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.GroupID != group.ID || account.Platform != model.PlatformGrok || account.AuthType != model.AuthOAuth {
		t.Fatalf("account was not assigned to selected Grok group: %#v", account)
	}
	if string(account.AccessToken) != "access" || string(account.RefreshToken) != "refresh" {
		t.Fatal("OAuth credentials were not retained")
	}
}

func TestImportDoesNotOverrideExplicitPlatformMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := appcrypto.Init("", "grok-import-explicit-platform-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:grok-import-explicit-platform?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "grok", Platform: model.PlatformGrok, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	result := importAccountPayload(t, db, group.ID, `{"accounts":[{"name":"openai-oauth","platform":"openai","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh"}}]}`)
	if result.Imported != 0 || result.Skipped != 1 || len(result.SkippedDetail) != 1 || result.SkippedDetail[0].Reason != "platform openai != group grok" {
		t.Fatalf("explicit platform mismatch was not preserved: %#v", result)
	}
}

func TestImportGrokOAuthUpdatesExistingAccountIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := appcrypto.Init("", "grok-import-idempotent-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:grok-import-idempotent?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "grok", Platform: model.PlatformGrok, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	first := `{"accounts":[{"name":"grok-oauth","platform":"grok","type":"oauth","credentials":{"access_token":"access-old","refresh_token":"refresh-old","account_id":"grok-account-1"}}]}`
	second := `{"accounts":[{"name":"grok-oauth-new-name","platform":"grok","type":"oauth","credentials":{"access_token":"access-new","refresh_token":"refresh-new","account_id":"grok-account-1"}}]}`
	if result := importAccountPayload(t, db, group.ID, first); result.Imported != 1 || result.Updated != 0 {
		t.Fatalf("first import = %#v", result)
	}
	if result := importAccountPayload(t, db, group.ID, second); result.Imported != 0 || result.Updated != 1 {
		t.Fatalf("second import = %#v", result)
	}

	var accounts []model.UpstreamAccount
	if err := db.Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(accounts))
	}
	if accounts[0].Name != "grok-oauth" || string(accounts[0].AccessToken) != "access-new" || string(accounts[0].RefreshToken) != "refresh-new" {
		t.Fatalf("updated account = %#v", accounts[0])
	}
}

type importAccountTestResult struct {
	Imported      int `json:"imported"`
	Updated       int `json:"updated"`
	Skipped       int `json:"skipped"`
	SkippedDetail []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"skipped_detail"`
}

func importAccountPayload(t *testing.T, db *gorm.DB, groupID int64, payload string) importAccountTestResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"group_id": groupID, "format": "auto", "data": payload})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/import", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&AdminHandler{db: db}).ImportAccounts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data importAccountTestResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}
