package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/config"

	"github.com/gin-gonic/gin"
)

func TestDefaultProxySettingsAcceptLocalReverseProxyIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	router := gin.New()
	if err := router.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	router.RemoteIPHeaders = cfg.Server.ForwardedClientIPHeaders
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	request := httptest.NewRequest(http.MethodGet, "/ip", nil)
	request.RemoteAddr = "127.0.0.1:45123"
	request.Header.Set("X-Forwarded-For", "203.0.113.24")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "203.0.113.24" {
		t.Fatalf("local proxy client IP = %q (status %d)", response.Body.String(), response.Code)
	}
}

func TestDefaultProxySettingsRejectExternalForwardedIPSpoofing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	router := gin.New()
	if err := router.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	router.RemoteIPHeaders = cfg.Server.ForwardedClientIPHeaders
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	request := httptest.NewRequest(http.MethodGet, "/ip", nil)
	request.RemoteAddr = "198.51.100.8:45123"
	request.Header.Set("X-Forwarded-For", "203.0.113.24")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "198.51.100.8" {
		t.Fatalf("external caller client IP = %q (status %d)", response.Body.String(), response.Code)
	}
}
