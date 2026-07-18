package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDAddsStableResponseHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, RequestIDFromContext(c))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))

	id := response.Header().Get("X-Request-ID")
	if !strings.HasPrefix(id, "ddr_") || len(id) != len("ddr_")+24 {
		t.Fatalf("unexpected request id %q", id)
	}
	if response.Body.String() != id {
		t.Fatalf("context id = %q, header id = %q", response.Body.String(), id)
	}
	if traceID := response.Header().Get("X-DengDeng-Trace-ID"); traceID != id {
		t.Fatalf("trace id = %q, request id = %q", traceID, id)
	}
}
