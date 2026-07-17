package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/store"
	"dengdeng/internal/util"
)

func TestOAuthCallbackCreatesUpstreamAccount(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "provider-code" || r.Form.Get("code_verifier") == "" {
			t.Fatalf("unexpected token request: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "refresh_token": "refresh", "expires_in": 3600,
		})
	}))
	defer provider.Close()

	cfg := config.Default()
	cfg.JWT.Secret = "router-oauth-test-secret"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	cfg.OAuth.OpenAI = config.OAuthProviderConfig{
		AuthorizeURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
	}
	if err := crypto.Init("", cfg.JWT.Secret); err != nil {
		t.Fatalf("crypto.Init: %v", err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	hash, err := util.HashPassword("admin12345")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.Create(&model.User{Email: "admin@example.test", PasswordHash: hash, Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	router := NewRouter(cfg, db)

	settings, err := service.NewSystemSettingsService(db, cfg).Get()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	login := callJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{"email": "admin@example.test", "password": "admin12345", "terms_revision": settings.LoginAgreement.Revision()}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var loginBody struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil || loginBody.Data.Token == "" {
		t.Fatalf("decode login: %v, body=%s", err, login.Body.String())
	}

	group := callJSON(t, router, http.MethodPost, "/api/admin/groups", map[string]any{"name": "openai", "platform": "openai"}, loginBody.Data.Token)
	if group.Code != http.StatusOK {
		t.Fatalf("group status=%d body=%s", group.Code, group.Body.String())
	}
	var groupBody struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(group.Body.Bytes(), &groupBody); err != nil {
		t.Fatalf("decode group: %v", err)
	}

	start := callJSON(t, router, http.MethodPost, "/api/admin/oauth/openai/start", map[string]any{"group_id": groupBody.Data.ID, "name": "browser-login", "priority": 42}, loginBody.Data.Token)
	if start.Code != http.StatusOK {
		t.Fatalf("oauth start status=%d body=%s", start.Code, start.Body.String())
	}
	var startBody struct {
		Data struct {
			AuthorizeURL string `json:"authorize_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	authorizeURL, err := url.Parse(startBody.Data.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state in authorize URL: %s", startBody.Data.AuthorizeURL)
	}
	redirectURI, err := url.Parse(authorizeURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parse redirect URI: %v", err)
	}
	if (redirectURI.Host != "localhost:1455" && redirectURI.Host != "localhost:1457") || redirectURI.Path != "/auth/callback" {
		t.Fatalf("redirect URI = %q, want OpenAI local callback", redirectURI)
	}

	bridgeQuery := redirectURI.Query()
	bridgeQuery.Set("state", state)
	bridgeQuery.Set("code", "provider-code")
	redirectURI.RawQuery = bridgeQuery.Encode()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	bridgeResponse, err := client.Get(redirectURI.String())
	if err != nil {
		t.Fatalf("call local bridge: %v", err)
	}
	defer bridgeResponse.Body.Close()
	if bridgeResponse.StatusCode != http.StatusFound {
		t.Fatalf("local bridge status=%d", bridgeResponse.StatusCode)
	}
	forwarded, err := url.Parse(bridgeResponse.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse forwarded callback: %v", err)
	}
	callback := httptest.NewRequest(http.MethodGet, forwarded.RequestURI(), nil)
	callbackRecorder := httptest.NewRecorder()
	router.ServeHTTP(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusOK || !bytes.Contains(callbackRecorder.Body.Bytes(), []byte("OAuth 登录成功")) {
		t.Fatalf("callback status=%d body=%s", callbackRecorder.Code, callbackRecorder.Body.String())
	}
	var account model.UpstreamAccount
	if err := db.Where("group_id = ?", groupBody.Data.ID).First(&account).Error; err != nil {
		t.Fatalf("created account missing: %v", err)
	}
	if account.Name != "browser-login" || account.AuthType != model.AuthOAuth || string(account.AccessToken) != "access" || string(account.RefreshToken) != "refresh" || account.Priority != 42 {
		t.Fatalf("unexpected account: %#v", account)
	}
}

func callJSON(t *testing.T, router http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Host = "127.0.0.1:9100"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}
