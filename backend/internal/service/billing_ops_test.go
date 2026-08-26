package service

import (
	"net/http"
	"testing"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestOpsErrorFromUsageClassifiesModelCircuitState(t *testing.T) {
	tests := []struct {
		status    int
		message   string
		errorType string
		retryable bool
	}{
		{http.StatusBadGateway, "upstream model is temporarily unavailable", "model_cooldown", true},
		{http.StatusNotFound, "upstream rejected this model or endpoint", "model_unsupported", false},
	}
	for _, test := range tests {
		got := opsErrorFromUsage(model.UsageLog{StatusCode: test.status, ErrorMessage: test.message}, model.PlatformOpenAI)
		if got.ErrorType != test.errorType || got.ErrorSource != "scheduler" || got.Retryable != test.retryable {
			t.Fatalf("classified sidecar = %#v", got)
		}
	}
}

func TestBillingPersistsRequestMetadataAndErrorSidecar(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:billing-ops-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.APIKey{}, &model.ModelPrice{}, &model.UsageLog{}, &model.OpsErrorLog{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "ops-sidecar@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, BalanceMicro: 1_000_000, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	NewBillingService(db, NewPricingService(db)).Record(BillContext{RequestID: "ddr_ops_sidecar", ClientRequestID: "client-42", UserID: user.ID, Platform: model.PlatformOpenAI, Model: "gpt-test", RequestPath: "/v1/responses", ClientIP: "203.0.113.8", UserAgent: "codex-cli/test", StatusCode: 503, ErrorMessage: "no available upstream account", SkipBalance: true})
	var usage model.UsageLog
	if err := db.Where("request_id = ?", "ddr_ops_sidecar").First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if usage.ClientRequestID != "client-42" || usage.ClientIP != "203.0.113.8" || usage.RequestPath != "/v1/responses" {
		t.Fatalf("metadata not persisted: %#v", usage)
	}
	var sidecar model.OpsErrorLog
	if err := db.Where("usage_log_id = ?", usage.ID).First(&sidecar).Error; err != nil {
		t.Fatal(err)
	}
	if sidecar.ErrorType != "no_available_account" || sidecar.ErrorSource != "scheduler" || !sidecar.Retryable {
		t.Fatalf("unexpected sidecar: %#v", sidecar)
	}
}
