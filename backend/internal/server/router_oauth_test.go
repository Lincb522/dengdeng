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

func TestOpenAIOAuthPastedCallbackCreatesUpstreamAccount(t *testing.T) {
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
			AuthorizeURL   string `json:"authorize_url"`
			State          string `json:"state"`
			CompletionMode string `json:"completion_mode"`
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
	if startBody.Data.State != state || startBody.Data.CompletionMode != "code" {
		t.Fatalf("unexpected OAuth completion payload: %#v", startBody.Data)
	}
	redirectURI, err := url.Parse(authorizeURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parse redirect URI: %v", err)
	}
	if redirectURI.Host != "localhost:1455" || redirectURI.Path != "/auth/callback" {
		t.Fatalf("redirect URI = %q, want OpenAI local callback", redirectURI)
	}

	callbackQuery := redirectURI.Query()
	callbackQuery.Set("state", state)
	callbackQuery.Set("code", "provider-code")
	redirectURI.RawQuery = callbackQuery.Encode()
	complete := callJSON(t, router, http.MethodPost, "/api/admin/oauth/openai/complete", map[string]any{
		"state": state,
		"code":  redirectURI.String(),
	}, loginBody.Data.Token)
	if complete.Code != http.StatusOK {
		t.Fatalf("oauth complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	var account model.UpstreamAccount
	if err := db.Where("group_id = ?", groupBody.Data.ID).First(&account).Error; err != nil {
		t.Fatalf("created account missing: %v", err)
	}
	if account.Name != "browser-login" || account.AuthType != model.AuthOAuth || string(account.AccessToken) != "access" || string(account.RefreshToken) != "refresh" || account.Priority != 42 {
		t.Fatalf("unexpected account: %#v", account)
	}
}

func TestClaudeOAuthPastedCodeCreatesUpstreamAccount(t *testing.T) {
	var tokenRequest map[string]string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "claude-access", "refresh_token": "claude-refresh", "expires_in": 3600,
			"account": map[string]any{"uuid": "account-123", "email_address": "claude@example.test"},
		})
	}))
	defer provider.Close()

	cfg := config.Default()
	cfg.JWT.Secret = "router-claude-oauth-test-secret"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	cfg.OAuth.Anthropic = config.OAuthProviderConfig{
		AuthorizeURL: provider.URL + "/authorize", TokenURL: provider.URL,
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
	group := model.Group{Name: "claude", Platform: model.PlatformAnthropic, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	secondaryGroup := model.Group{Name: "claude-secondary", Platform: model.PlatformAnthropic, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&secondaryGroup).Error; err != nil {
		t.Fatalf("create secondary group: %v", err)
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

	start := callJSON(t, router, http.MethodPost, "/api/admin/oauth/anthropic/start", map[string]any{"group_ids": []int64{group.ID, secondaryGroup.ID}, "name": "claude-login"}, loginBody.Data.Token)
	if start.Code != http.StatusOK {
		t.Fatalf("oauth start status=%d body=%s", start.Code, start.Body.String())
	}
	var startBody struct {
		Data struct {
			AuthorizeURL   string `json:"authorize_url"`
			State          string `json:"state"`
			CompletionMode string `json:"completion_mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	authorizeURL, err := url.Parse(startBody.Data.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if startBody.Data.CompletionMode != "code" || startBody.Data.State == "" || authorizeURL.Query().Get("redirect_uri") != "https://platform.claude.com/oauth/code/callback" || authorizeURL.Query().Get("code") != "true" {
		t.Fatalf("unexpected start response: %+v URL=%s", startBody.Data, authorizeURL)
	}

	complete := callJSON(t, router, http.MethodPost, "/api/admin/oauth/anthropic/complete", map[string]any{
		"state": startBody.Data.State,
		"code":  "provider-code#" + startBody.Data.State,
	}, loginBody.Data.Token)
	if complete.Code != http.StatusOK {
		t.Fatalf("oauth complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	if tokenRequest["code"] != "provider-code" || tokenRequest["state"] != startBody.Data.State || tokenRequest["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
		t.Fatalf("unexpected token request: %v", tokenRequest)
	}
	var account model.UpstreamAccount
	if err := db.Where("group_id = ?", group.ID).First(&account).Error; err != nil {
		t.Fatalf("created account missing: %v", err)
	}
	if account.Name != "claude-login" || account.AuthType != model.AuthOAuth || account.Email != "claude@example.test" || account.AccountID != "account-123" || string(account.AccessToken) != "claude-access" || string(account.RefreshToken) != "claude-refresh" {
		t.Fatalf("unexpected account: %#v", account)
	}
	var bindings []model.UpstreamAccountGroup
	if err := db.Where("upstream_account_id = ?", account.ID).Order("group_id ASC").Find(&bindings).Error; err != nil || len(bindings) != 2 || bindings[0].GroupID != group.ID || bindings[1].GroupID != secondaryGroup.ID {
		t.Fatalf("oauth group bindings=%#v err=%v", bindings, err)
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
