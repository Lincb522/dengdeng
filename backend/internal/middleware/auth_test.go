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

func TestAdminOnlyChecksRoleWithoutMFA(t *testing.T) {
	admin := &model.User{Role: model.RoleAdmin}
	response := runAuthMiddleware(t, http.MethodGet, admin, &util.Claims{}, AdminOnly())
	if response.Code != http.StatusNoContent {
		t.Fatalf("admin status=%d, want 204", response.Code)
	}

	user := &model.User{Role: model.RoleUser}
	response = runAuthMiddleware(t, http.MethodGet, user, &util.Claims{}, AdminOnly())
	if response.Code != http.StatusForbidden {
		t.Fatalf("regular user status=%d, want 403", response.Code)
	}
}

func TestRecentMFAExemptsAdministrators(t *testing.T) {
	admin := &model.User{Role: model.RoleAdmin}
	response := runAuthMiddleware(t, http.MethodGet, admin, &util.Claims{}, RequireRecentMFA())
	if response.Code != http.StatusNoContent {
		t.Fatalf("administrator status=%d, want 204", response.Code)
	}
}

func TestRecentMFAStillProtectsRegularUsers(t *testing.T) {
	user := &model.User{Role: model.RoleUser}
	oldClaims := &util.Claims{
		MFA: true,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-16 * time.Minute)),
		},
	}
	response := runAuthMiddleware(t, http.MethodGet, user, oldClaims, RequireRecentMFA())
	if response.Code != http.StatusForbidden {
		t.Fatalf("stale verification status=%d, want 403", response.Code)
	}

	recentClaims := &util.Claims{
		MFA: true,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
	response = runAuthMiddleware(t, http.MethodGet, user, recentClaims, RequireRecentMFA())
	if response.Code != http.StatusNoContent {
		t.Fatalf("recent verification status=%d, want 204", response.Code)
	}
}
