package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func isPublicRelayPath(path string) bool {
	if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v1beta/") {
		return true
	}
	for _, prefix := range []string{
		"/messages",
		"/chat/completions",
		"/responses",
		"/images/",
		"/models",
		"/usage",
		"/backend-api/codex/",
	} {
		if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

// PublicCORS enables browser-based OpenAI/Anthropic/Gemini-compatible clients
// (including Chatbox) to call the public relay. It deliberately excludes the
// console's /api routes, which use administrator/user JWTs.
func PublicCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !isPublicRelayPath(path) {
			c.Next()
			return
		}

		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		// OpenAI-compatible browser SDKs add runtime-specific request headers.
		// Echoing the browser-declared list lets these clients pass preflight
		// without broadly enabling CORS for the private /api console routes.
		allowHeaders := c.GetHeader("Access-Control-Request-Headers")
		if strings.TrimSpace(allowHeaders) == "" {
			allowHeaders = "Authorization, Content-Type, Accept, OpenAI-Beta, anthropic-version, anthropic-beta, x-api-key, x-goog-api-key"
		}
		h.Set("Access-Control-Allow-Headers", allowHeaders)
		// Let browser SDKs and web clients surface retry timing and support
		// correlation IDs without exposing any credential-bearing header.
		h.Set("Access-Control-Expose-Headers", "Content-Type, Retry-After, X-Request-ID, X-DengDeng-Trace-ID, X-DengDeng-Applied-Rules, X-DengDeng-Applied-Skills, X-DengDeng-Skill-Runs")
		h.Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		c.Next()
	}
}
