package middleware

import (
	"net/http"
	"strings"

	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	CtxUser   = "ctx_user"
	CtxClaims = "ctx_claims"
)

// JWTAuth validates the bearer token and loads the current user.
func JWTAuth(db *gorm.DB, secret string) gin.HandlerFunc {
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
		if claims.Fingerprint != "" && claims.Fingerprint != util.SessionFingerprint(secret, c.Request.UserAgent()) {
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
		claims := CurrentClaims(c)
		if user.TOTPEnabled && (claims == nil || !claims.MFA) {
			util.Fail(c, http.StatusUnauthorized, "TOTP verification is required for administrator actions")
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
