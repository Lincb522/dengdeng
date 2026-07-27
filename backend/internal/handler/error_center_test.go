package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestErrorCenterSummaryKeepsSiteAndAPIErrorsSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:error-center?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.OpsSystemLog{}, &model.OpsErrorLog{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.OpsSystemLog{
		Level: "error", Category: "frontend", Component: "frontend.vue",
		Message: "render failed", CreatedAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.OpsErrorLog{
		UsageLogID: 1, ErrorType: "upstream_error", ErrorSource: "provider",
		Severity: "P1", Retryable: true, ErrorMessage: "upstream failed",
		StatusCode: 502, CreatedAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	admin := &AdminHandler{db: db}
	router := gin.New()
	router.GET("/summary", admin.ErrorCenterSummary)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/summary?range=1h", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Site errorCenterScope `json:"site"`
			API  errorCenterScope `json:"api"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Site.Total != 1 || body.Data.Site.Critical != 1 || body.Data.API.Total != 1 ||
		body.Data.API.Critical != 1 || body.Data.API.Retryable != 1 {
		t.Fatalf("unexpected summary: %#v", body.Data)
	}
	if len(body.Data.Site.Categories) != 1 || body.Data.Site.Categories[0].Name != "frontend" {
		t.Fatalf("unexpected site categories: %#v", body.Data.Site.Categories)
	}
	if len(body.Data.API.Categories) != 1 || body.Data.API.Categories[0].Name != "upstream_error" {
		t.Fatalf("unexpected API categories: %#v", body.Data.API.Categories)
	}
}

func TestBatchResolveErrorsKeepsSiteAndAPITablesSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:error-center-batch?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.OpsSystemLog{}, &model.OpsErrorLog{}); err != nil {
		t.Fatal(err)
	}
	siteRows := []model.OpsSystemLog{
		{Level: "error", Category: "frontend", Message: "site one"},
		{Level: "warning", Category: "authentication", Message: "site two"},
	}
	if err := db.Create(&siteRows).Error; err != nil {
		t.Fatal(err)
	}
	apiRows := []model.OpsErrorLog{
		{UsageLogID: 101, ErrorType: "upstream_error", ErrorMessage: "api one"},
		{UsageLogID: 102, ErrorType: "rate_limit", ErrorMessage: "api two"},
	}
	if err := db.Create(&apiRows).Error; err != nil {
		t.Fatal(err)
	}
	admin := &AdminHandler{db: db}
	user := &model.User{Email: "admin@example.com", Role: model.RoleAdmin}

	requestBatch := func(target string, handler gin.HandlerFunc, ids []int64) {
		t.Helper()
		payload, _ := json.Marshal(gin.H{"ids": ids})
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set(middleware.CtxUser, user)
		handler(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", target, recorder.Code, recorder.Body.String())
		}
	}

	requestBatch("/errors/site/resolve-batch", admin.ResolveSiteErrorsBatch, []int64{siteRows[0].ID, siteRows[1].ID, siteRows[1].ID})
	requestBatch("/errors/api/resolve-batch", admin.ResolveAPIErrorsBatch, []int64{apiRows[0].ID})

	var resolvedSite, resolvedAPI int64
	if err := db.Model(&model.OpsSystemLog{}).Where("resolved_at IS NOT NULL").Count(&resolvedSite).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.OpsErrorLog{}).Where("resolved_at IS NOT NULL").Count(&resolvedAPI).Error; err != nil {
		t.Fatal(err)
	}
	if resolvedSite != 2 || resolvedAPI != 1 {
		t.Fatalf("resolved site=%d api=%d", resolvedSite, resolvedAPI)
	}
	var untouchedAPI model.OpsErrorLog
	if err := db.First(&untouchedAPI, apiRows[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if untouchedAPI.ResolvedAt != nil {
		t.Fatal("batch API resolution touched an unselected row")
	}
}
