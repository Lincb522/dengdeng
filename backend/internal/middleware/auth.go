package middleware

import (
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	CtxUser   = "ctx_user"
	CtxClaims = "ctx_claims"
)

// JWTAuth validates the bearer token and loads the current user.
func JWTAuth(db *gorm.DB, secret string, settings *service.SystemSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			util.Fail(c, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		claims, err := util.ParseJWT(secret, strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			util.Fail(c, http.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}
		var user model.User
		if err := db.First(&user, claims.UserID).Error; err != nil {
			util.Fail(c, http.StatusUnauthorized, "user not found")
			c.Abort()
			return
		}
		// Reject tokens issued before the last password/ban/role change.
		if claims.Ver != user.TokenVersion {
			util.Fail(c, http.StatusUnauthorized, "session expired, please sign in again")
			c.Abort()
			return
		}
		bindSession := false
		if settings != nil {
			if current, settingsErr := settings.Get(); settingsErr == nil {
				bindSession = current.Security.SessionBindingEnabled
			}
		}
		fingerprint := util.SessionFingerprint(secret, c.ClientIP()+"\x00"+c.Request.UserAgent())
		if bindSession && (claims.Fingerprint == "" || claims.Fingerprint != fingerprint) {
			util.Fail(c, http.StatusUnauthorized, "session device changed, please sign in again")
			c.Abort()
			return
		}
		if user.Status != model.StatusActive {
			util.Fail(c, http.StatusForbidden, "account disabled")
			c.Abort()
			return
		}
		c.Set(CtxUser, &user)
		c.Set(CtxClaims, claims)
		c.Next()
	}
}

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		if user.Role != model.RoleAdmin {
			util.Fail(c, http.StatusForbidden, "admin only")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireRecentMFA protects credential disclosure and other sensitive
// operations independently from the optional site-wide policy switch. Site
// administrators are exempt because their access is already protected by the
// administrator role and may intentionally run without TOTP.
func RequireRecentMFA() gin.HandlerFunc {
	return func(c *gin.Context) {
		if user := CurrentUser(c); user != nil && user.Role == model.RoleAdmin {
			c.Next()
			return
		}
		claims := CurrentClaims(c)
		if claims == nil || !claims.MFA || claims.IssuedAt == nil || time.Since(claims.IssuedAt.Time) > 15*time.Minute {
			util.FailCode(c, http.StatusForbidden, "permission.step_up_required", "recent identity verification is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

func RequireStepUp(settings *service.SystemSettingsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if user := CurrentUser(c); user != nil && user.Role == model.RoleAdmin {
			c.Next()
			return
		}
		if settings == nil {
			c.Next()
			return
		}
		current, err := settings.Get()
		if err != nil {
			util.Fail(c, http.StatusInternalServerError, "load step-up policy failed")
			c.Abort()
			return
		}
		if !current.Security.StepUpEnabled {
			c.Next()
			return
		}
		claims := CurrentClaims(c)
		if claims == nil || !claims.MFA || claims.IssuedAt == nil || time.Since(claims.IssuedAt.Time) > 15*time.Minute {
			util.Fail(c, http.StatusForbidden, "recent two-factor verification is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

func CurrentClaims(c *gin.Context) *util.Claims {
	claims, _ := c.Get(CtxClaims)
	value, _ := claims.(*util.Claims)
	return value
}

func CurrentUser(c *gin.Context) *model.User {
	return c.MustGet(CtxUser).(*model.User)
}
