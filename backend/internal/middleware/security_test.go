package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimitReturnsStructuredRetryInformation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RateLimit(1, time.Minute))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	var body struct {
		ErrorCode         string `json:"error_code"`
		RetryAfterSeconds int64  `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != http.StatusTooManyRequests || body.ErrorCode != "request.rate_limited" || body.RetryAfterSeconds <= 0 {
		t.Fatalf("unexpected response status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header is missing")
	}
}

func TestSecurityHeadersKeepScriptPolicyFreeOfUnsafeInline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/admin/redeem", func(c *gin.Context) { c.Status(http.StatusOK) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/redeem", nil))
	csp := response.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	scriptDirective := strings.Split(strings.Split(csp, "script-src ")[1], ";")[0]
	if strings.Contains(scriptDirective, "'unsafe-inline'") {
		t.Fatalf("script-src must not permit inline scripts: %s", scriptDirective)
	}
	if !strings.Contains(scriptDirective, "'self'") {
		t.Fatalf("script-src must permit same-origin assets: %s", scriptDirective)
	}
	connectDirective := strings.Split(strings.Split(csp, "connect-src ")[1], ";")[0]
	if !strings.Contains(connectDirective, "https://raw.githubusercontent.com") {
		t.Fatalf("connect-src must permit the curated prompt registry: %s", connectDirective)
	}
}
