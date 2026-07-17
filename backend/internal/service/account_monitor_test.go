package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/model"

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
