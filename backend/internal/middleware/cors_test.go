package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicCORSPreflightForRelayOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(PublicCORS())
	router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/user/me", func(c *gin.Context) { c.Status(http.StatusOK) })

	preflight := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	preflight.Header.Set("Origin", "https://app.chatboxai.app")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type, x-stainless-lang")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "authorization, content-type, x-stainless-lang" {
		t.Fatalf("allow headers = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "Content-Type, X-Request-ID" {
		t.Fatalf("expose headers = %q", got)
	}

	console := httptest.NewRequest(http.MethodGet, "/api/user/me", nil)
	consoleRecorder := httptest.NewRecorder()
	router.ServeHTTP(consoleRecorder, console)
	if got := consoleRecorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("console CORS should be absent, got %q", got)
	}
}
