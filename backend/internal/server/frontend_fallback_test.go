package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFrontendFallbackRejectsUnknownAPIPathAsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	mountFrontend(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/not-a-real-endpoint", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "API endpoint not found") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}
