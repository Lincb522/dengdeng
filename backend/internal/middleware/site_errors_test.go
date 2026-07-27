package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSiteErrorCaptureSeparatesConsoleFromRelayErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:site-errors?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.OpsSystemLog{}); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(RequestID(), SiteErrorCapture(db))
	router.GET("/api/example", func(c *gin.Context) {
		util.FailCode(c, http.StatusServiceUnavailable, "service.unavailable", "service unavailable")
	})
	router.GET("/v1/example", func(c *gin.Context) {
		util.FailCode(c, http.StatusServiceUnavailable, "upstream.unavailable", "upstream unavailable")
	})

	console := httptest.NewRecorder()
	router.ServeHTTP(console, httptest.NewRequest(http.MethodGet, "/api/example", nil))
	if console.Code != http.StatusServiceUnavailable {
		t.Fatalf("console status = %d", console.Code)
	}
	var item model.OpsSystemLog
	if err := db.First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Category != "public_site" || item.ErrorCode != "service.unavailable" ||
		item.StatusCode != http.StatusServiceUnavailable || item.Path != "/api/example" ||
		item.RequestID == "" {
		t.Fatalf("unexpected site error: %#v", item)
	}

	relay := httptest.NewRecorder()
	router.ServeHTTP(relay, httptest.NewRequest(http.MethodGet, "/v1/example", nil))
	var count int64
	if err := db.Model(&model.OpsSystemLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("relay error should not enter site errors, count = %d", count)
	}
}
