package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
