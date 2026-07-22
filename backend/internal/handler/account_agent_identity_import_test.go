package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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

func TestImportAgentIdentityUpdatesSameMemberAndSeparatesTeams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := appcrypto.Init("", "agent-identity-import-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:agent-identity-import?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "openai", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	extra, err := model.EncodeExtra(map[string]any{"chatgpt_user_id": "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	existing := model.UpstreamAccount{
		GroupID: group.ID, Name: "existing", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth,
		AccessToken: appcrypto.EncryptedString("old-access"), RefreshToken: appcrypto.EncryptedString("old-refresh"),
		AccountID: "team-a", Extra: extra, Status: model.StatusActive, Priority: 10,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	entries := []map[string]any{
		testAgentIdentityValue(t, "runtime-new", "team-a", "user-a"),
		testAgentIdentityValue(t, "runtime-team-b", "team-b", "user-a"),
		testAgentIdentityValue(t, "runtime-user-b", "team-a", "user-b"),
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(encoded))
	}
	body, err := json.Marshal(map[string]any{
		"group_id": group.ID,
		"format":   "auto",
		"data":     strings.Join(lines, "\n"),
	})
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
		Data struct {
			Imported int `json:"imported"`
			Updated  int `json:"updated"`
			Skipped  int `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Imported != 2 || response.Data.Updated != 1 || response.Data.Skipped != 0 {
		t.Fatalf("unexpected import result: %s", recorder.Body.String())
	}

	var accounts []model.UpstreamAccount
	if err := db.Order("id ASC").Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("got %d accounts, want 3", len(accounts))
	}
	updated := accounts[0]
	if updated.ID != existing.ID || updated.AuthType != model.AuthAgentIdentity {
		t.Fatalf("existing account was not upgraded: %#v", updated)
	}
	if updated.AccessToken != "" || updated.RefreshToken != "" {
		t.Fatal("OAuth tokens were retained after Agent Identity upgrade")
	}
	if got := stringMapValue(updated.DecodeExtra(), "agent_runtime_id"); got != "runtime-new" {
		t.Fatalf("runtime = %q, want runtime-new", got)
	}
}

func TestImportAgentIdentitySkipsDuplicateMemberInSamePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := appcrypto.Init("", "agent-identity-duplicate-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:agent-identity-duplicate?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "openai", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]map[string]any{
		testAgentIdentityValue(t, "runtime-a", "team-a", "user-a"),
		testAgentIdentityValue(t, "runtime-b", "team-a", "user-a"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"group_id": group.ID, "format": "auto", "data": string(data)})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/import", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&AdminHandler{db: db}).ImportAccounts(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"skipped":1`) {
		t.Fatalf("unexpected response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func testAgentIdentityValue(t *testing.T, runtimeID, accountID, userID string) map[string]any {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"auth_mode": "agentIdentity",
		"agent_identity": map[string]any{
			"agent_runtime_id":  runtimeID,
			"agent_private_key": base64.StdEncoding.EncodeToString(der),
			"account_id":        accountID,
			"chatgpt_user_id":   userID,
		},
	}
}
