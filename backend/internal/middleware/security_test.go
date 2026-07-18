package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

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
}
