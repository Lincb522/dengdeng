package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/middleware"

	"github.com/gin-gonic/gin"
)

func TestEmbeddedFrontendUsesExternalThemeInitializer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.SecurityHeaders())
	mountFrontend(router)

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin/redeem", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("SPA status = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), `src="/theme-init.js"`) {
		t.Fatalf("SPA does not load the external theme initializer: %s", page.Body.String())
	}
	if strings.Contains(page.Body.String(), "localStorage.getItem('dengdeng.theme')") {
		t.Fatal("SPA still contains an inline theme initializer")
	}

	asset := httptest.NewRecorder()
	router.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/theme-init.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("theme initializer asset = status %d, content-type %q", asset.Code, asset.Header().Get("Content-Type"))
	}
	if !strings.Contains(asset.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("same-origin script policy missing: %q", asset.Header().Get("Content-Security-Policy"))
	}
}
