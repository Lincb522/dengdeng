package util

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestFailIncludesStableErrorCodeAndRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		c.Set(requestIDContextKey, "ddr_test")
		Fail(c, http.StatusUnauthorized, "incorrect email or password")
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != http.StatusUnauthorized || body.ErrorCode != "auth.invalid_credentials" {
		t.Fatalf("unexpected error response: %#v", body)
	}
	if body.RequestID != "ddr_test" {
		t.Fatalf("request id = %q", body.RequestID)
	}
}

func TestFailRetryIncludesCountdownAndHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		FailRetry(c, http.StatusTooManyRequests, "auth.too_many_attempts", "locked", 2500*time.Millisecond)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RetryAfterSeconds != 3 || response.Header().Get("Retry-After") != "3" {
		t.Fatalf("retry response = %#v, header=%q", body, response.Header().Get("Retry-After"))
	}
}

func TestErrorCodeFallbackCoversEveryHTTPClass(t *testing.T) {
	tests := []struct {
		status  int
		message string
		want    string
	}{
		{http.StatusBadRequest, "unmapped validation", "request.invalid"},
		{http.StatusNotFound, "missing record", "resource.not_found"},
		{http.StatusConflict, "state changed", "resource.conflict"},
		{http.StatusServiceUnavailable, "temporarily offline", "service.unavailable"},
		{http.StatusInternalServerError, "unexpected failure", "server.internal"},
	}
	for _, test := range tests {
		if got := errorCodeFor(test.status, test.message); got != test.want {
			t.Fatalf("errorCodeFor(%d, %q) = %q, want %q", test.status, test.message, got, test.want)
		}
	}
}
