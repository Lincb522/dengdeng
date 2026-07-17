package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBrowserLoginUsesPKCEAndExchangesCode(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		received = r.Form
		_ = json.NewEncoder(w).Encode(tokenResp{
			AccessToken: "new-access", RefreshToken: "new-refresh", IDToken: "new-id", ExpiresIn: 3600,
		})
	}))
	defer server.Close()

	manager := NewManager(nil, config.OAuthConfig{OpenAI: config.OAuthProviderConfig{
		ClientID: "test-client", AuthorizeURL: server.URL + "/authorize", TokenURL: server.URL + "/token",
		RedirectURL: "http://localhost:5173/api/admin/oauth/openai/callback",
	}}, nil)
	authorizeURL, err := manager.BeginLogin(model.PlatformOpenAI, "http://localhost:5173/api/admin/oauth/openai/callback", LoginIntent{GroupID: 7, Name: "test", Priority: 22})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE parameters: %s", u.RawQuery)
	}

	result, err := manager.CompleteLogin(context.Background(), model.PlatformOpenAI, q.Get("state"), "authorization-code")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if result.AccessToken != "new-access" || result.RefreshToken != "new-refresh" || result.Intent.GroupID != 7 || result.Intent.Priority != 22 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if received.Get("grant_type") != "authorization_code" || received.Get("code") != "authorization-code" || received.Get("client_id") != "test-client" || received.Get("code_verifier") == "" {
		t.Fatalf("unexpected token request: %v", received)
	}
	if _, err := manager.CompleteLogin(context.Background(), model.PlatformOpenAI, q.Get("state"), "authorization-code"); err == nil {
		t.Fatal("one-time OAuth state was accepted twice")
	}
}

func TestRefreshRenewsRevokedSessionBeforeRecordedExpiry(t *testing.T) {
	if err := crypto.Init("", "oauth-refresh-test"); err != nil {
		t.Fatalf("initialize crypto: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		received = r.Form
		_ = json.NewEncoder(w).Encode(tokenResp{AccessToken: "renewed-access", RefreshToken: "renewed-refresh", ExpiresIn: 3600})
	}))
	defer server.Close()

	future := time.Now().Add(24 * time.Hour)
	account := model.UpstreamAccount{
		GroupID: 1, Name: "test", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth,
		AccessToken: crypto.EncryptedString("revoked-access"), RefreshToken: crypto.EncryptedString("stored-refresh"), ExpiresAt: &future,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	manager := NewManager(db, config.OAuthConfig{OpenAI: config.OAuthProviderConfig{ClientID: "test-client", TokenURL: server.URL}}, nil)
	token, err := manager.Refresh(context.Background(), &account)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if token != "renewed-access" || received.Get("refresh_token") != "stored-refresh" || received.Get("client_id") != "test-client" {
		t.Fatalf("token=%q form=%v", token, received)
	}

	var stored model.UpstreamAccount
	if err := db.First(&stored, account.ID).Error; err != nil {
		t.Fatalf("load account: %v", err)
	}
	if stored.AccessToken != "renewed-access" || stored.RefreshToken != "renewed-refresh" || stored.ExpiresAt == nil || !stored.ExpiresAt.After(time.Now()) {
		t.Fatalf("stored account = %#v", stored)
	}
}

func TestCallbackURLRequiresExplicitProductionURL(t *testing.T) {
	manager := NewManager(nil, config.OAuthConfig{}, nil)
	defer manager.Close()
	if _, err := manager.CallbackURL(model.PlatformOpenAI, "relay.example.com", true); err == nil {
		t.Fatal("production Host header should not determine an OAuth callback URL")
	}
	providerURL, completionURL, err := manager.CallbackURLs(model.PlatformOpenAI, "127.0.0.1:5173", false)
	if err != nil {
		t.Fatalf("localhost callback: %v", err)
	}
	parsedProvider, err := url.Parse(providerURL)
	if err != nil {
		t.Fatalf("parse provider callback: %v", err)
	}
	if parsedProvider.Host != "localhost:1455" && parsedProvider.Host != "localhost:1457" || parsedProvider.Path != "/auth/callback" {
		t.Fatalf("provider callback = %q, want localhost OpenAI callback", providerURL)
	}
	if want := "http://127.0.0.1:5173/api/admin/oauth/openai/callback"; completionURL != want {
		t.Fatalf("completion callback = %q, want %q", completionURL, want)
	}
}

func TestLocalCallbackTargetForwardsToStateBoundCompletion(t *testing.T) {
	manager := NewManager(nil, config.OAuthConfig{}, nil)
	authorizeURL, err := manager.BeginLoginWithCompletion(
		model.PlatformOpenAI,
		"http://localhost:9100/auth/callback",
		"http://127.0.0.1:9100/api/admin/oauth/openai/callback",
		LoginIntent{GroupID: 1},
	)
	if err != nil {
		t.Fatalf("BeginLoginWithCompletion: %v", err)
	}
	u, _ := url.Parse(authorizeURL)
	target, err := manager.LocalCallbackTarget(u.Query().Get("state"), url.Values{"code": {"provider-code"}})
	if err != nil {
		t.Fatalf("LocalCallbackTarget: %v", err)
	}
	forwarded, _ := url.Parse(target)
	if forwarded.Host != "127.0.0.1:9100" || forwarded.Path != "/api/admin/oauth/openai/callback" || forwarded.Query().Get("code") != "provider-code" || forwarded.Query().Get("state") == "" {
		t.Fatalf("unexpected forwarded callback: %s", target)
	}
}
