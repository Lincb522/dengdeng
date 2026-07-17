package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newMonitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AccountProbe{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAccountMonitorProbeAPIKeyPersistsHealthyResult(t *testing.T) {
	db := newMonitorTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer monitor-key" {
			t.Fatalf("missing probe credential")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	monitor := NewAccountMonitor(db, nil)
	probe, err := monitor.Probe(context.Background(), &model.UpstreamAccount{
		ID: 11, Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, APIKey: "monitor-key", BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if probe.State != "healthy" || probe.Mode != "api" || probe.StatusCode != http.StatusOK {
		t.Fatalf("probe = %#v", probe)
	}
	var saved model.AccountProbe
	if err := db.First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.AccountID != 11 || saved.State != "healthy" {
		t.Fatalf("saved probe = %#v", saved)
	}
}

func TestAccountMonitorExpiredOAuthDoesNotCallUpstream(t *testing.T) {
	db := newMonitorTestDB(t)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	expired := time.Now().UTC().Add(-time.Minute)

	monitor := NewAccountMonitor(db, nil)
	probe, err := monitor.Probe(context.Background(), &model.UpstreamAccount{
		ID: 12, Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, AccessToken: "expired", ExpiresAt: &expired, BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || probe.Mode != "transport" || probe.State != "expired" {
		t.Fatalf("probe = %#v called=%v", probe, called)
	}
}

func TestAccountMonitorProactivelyRefreshesOAuthBeforeProbe(t *testing.T) {
	if err := crypto.Init("", "account-monitor-refresh-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.AccountProbe{}); err != nil {
		t.Fatal(err)
	}

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "renewed", "refresh_token": "rotated", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.Header.Get("Authorization") != "Bearer renewed" {
			t.Fatalf("probe method/header = %s %q", r.Method, r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer probeServer.Close()

	expired := time.Now().UTC().Add(-time.Minute)
	account := model.UpstreamAccount{
		GroupID: 1, Name: "claude", Platform: model.PlatformAnthropic, AuthType: model.AuthOAuth,
		AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: &expired, BaseURL: probeServer.URL, Status: model.StatusActive,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	manager := oauth.NewManager(db, config.OAuthConfig{Anthropic: config.OAuthProviderConfig{ClientID: "test-client", TokenURL: tokenServer.URL}}, tokenServer.Client())
	monitor := NewAccountMonitor(db, nil)
	monitor.SetOAuthManager(manager)
	probe, err := monitor.Probe(context.Background(), &account)
	if err != nil {
		t.Fatal(err)
	}
	if probe.State != "healthy" {
		t.Fatalf("probe = %#v", probe)
	}
	var stored model.UpstreamAccount
	if err := db.First(&stored, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "renewed" || stored.RefreshToken != "rotated" || stored.ExpiresAt == nil || !stored.ExpiresAt.After(time.Now()) {
		t.Fatalf("stored account = %#v", stored)
	}
}
