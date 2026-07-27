package middleware

import (
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	ctxErrorCode    = "ctx_error_code"
	ctxErrorMessage = "ctx_error_message"
)

// SiteErrorCapture persists failed console and website requests separately
// from relay usage failures. Relay endpoints keep using OpsErrorLog so the
// error center can present the two scopes without mixing their semantics.
func SiteErrorCapture(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if db == nil || !strings.HasPrefix(c.Request.URL.Path, "/api/") ||
			c.Request.URL.Path == "/api/site-errors" {
			return
		}
		status := c.Writer.Status()
		if status < http.StatusBadRequest {
			return
		}
		code, _ := c.Get(ctxErrorCode)
		message, _ := c.Get(ctxErrorMessage)
		errorCode, _ := code.(string)
		errorMessage, _ := message.(string)
		if strings.TrimSpace(errorMessage) == "" {
			errorMessage = http.StatusText(status)
		}
		userID := int64(0)
		if value, exists := c.Get(CtxUser); exists {
			if user, ok := value.(*model.User); ok && user != nil {
				userID = user.ID
			}
		}
		level := "notice"
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
			level = "warning"
		}
		if status >= http.StatusInternalServerError {
			level = "error"
		}
		_ = db.Create(&model.OpsSystemLog{
			Level:      level,
			Category:   siteErrorCategory(c.Request.URL.Path),
			Component:  siteErrorComponent(c.Request.URL.Path),
			ErrorCode:  trimSiteError(errorCode, 96),
			Message:    trimSiteError(errorMessage, 2048),
			Method:     trimSiteError(c.Request.Method, 16),
			Path:       trimSiteError(c.Request.URL.Path, 512),
			StatusCode: status,
			RequestID:  RequestIDFromContext(c),
			ClientIP:   trimSiteError(c.ClientIP(), 64),
			UserAgent:  trimSiteError(c.Request.UserAgent(), 512),
			UserID:     userID,
			CreatedAt:  time.Now().UTC(),
		}).Error
	}
}

func siteErrorCategory(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/auth/"):
		return "authentication"
	case strings.HasPrefix(path, "/api/admin/payment") || strings.HasPrefix(path, "/api/payment/"):
		return "payment"
	case strings.HasPrefix(path, "/api/admin/"):
		return "administration"
	case strings.HasPrefix(path, "/api/user/"):
		return "user_console"
	case strings.HasPrefix(path, "/api/referrals/"):
		return "referral"
	default:
		return "public_site"
	}
}

func siteErrorComponent(path string) string {
	category := siteErrorCategory(path)
	return "site." + category
}

func trimSiteError(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
