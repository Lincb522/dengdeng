package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func runAuthMiddleware(t *testing.T, method string, user *model.User, claims *util.Claims, handlers ...gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if user != nil {
			c.Set(CtxUser, user)
		}
		if claims != nil {
			c.Set(CtxClaims, claims)
		}
		c.Next()
	})
	handlers = append(handlers, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.Handle(method, "/", handlers...)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/", nil)
	router.ServeHTTP(response, request)
	return response
}

func TestAdminOnlyRequiresTOTPAndMFASession(t *testing.T) {
	admin := &model.User{Role: model.RoleAdmin}
	response := runAuthMiddleware(t, http.MethodGet, admin, &util.Claims{MFA: true}, AdminOnly())
	if response.Code != http.StatusForbidden {
		t.Fatalf("admin without TOTP status=%d, want 403", response.Code)
	}

	admin.TOTPEnabled = true
	response = runAuthMiddleware(t, http.MethodGet, admin, &util.Claims{}, AdminOnly())
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("admin without MFA session status=%d, want 401", response.Code)
	}

	response = runAuthMiddleware(t, http.MethodGet, admin, &util.Claims{MFA: true}, AdminOnly())
	if response.Code != http.StatusNoContent {
		t.Fatalf("verified admin status=%d, want 204", response.Code)
	}
}

func TestAdminMutationRequiresRecentMFA(t *testing.T) {
	oldClaims := &util.Claims{
		MFA: true,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-16 * time.Minute)),
		},
	}
	response := runAuthMiddleware(t, http.MethodPost, nil, oldClaims, RequireAdminMutationMFA())
	if response.Code != http.StatusForbidden {
		t.Fatalf("stale mutation status=%d, want 403", response.Code)
	}

	response = runAuthMiddleware(t, http.MethodGet, nil, oldClaims, RequireAdminMutationMFA())
	if response.Code != http.StatusNoContent {
		t.Fatalf("read-only request status=%d, want 204", response.Code)
	}

	recentClaims := &util.Claims{
		MFA: true,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
	response = runAuthMiddleware(t, http.MethodPost, nil, recentClaims, RequireAdminMutationMFA())
	if response.Code != http.StatusNoContent {
		t.Fatalf("recent mutation status=%d, want 204", response.Code)
	}
}
