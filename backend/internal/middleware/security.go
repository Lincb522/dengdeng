package middleware

import (
	"net/http"
	"sync"
	"time"

	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets conservative hardening headers on every response.
// The CSP allows same-origin assets plus inline styles (the SPA ships some),
// and blocks framing to prevent clickjacking of the console.
func SecurityHeaders() gin.HandlerFunc {
	// Stripe Elements and Airwallex Checkout are loaded only after a user
	// creates their own authenticated order. Their official origins are kept
	// explicit here; no wildcard script, frame, or connect source is allowed.
	const csp = "default-src 'self'; script-src 'self' https://js.stripe.com https://checkout.airwallex.com https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob: https:; font-src 'self' data:; " +
		"connect-src 'self' https://api.stripe.com https://*.stripe.com https://*.airwallex.com https://challenges.cloudflare.com; " +
		"frame-src https://js.stripe.com https://hooks.stripe.com https://*.airwallex.com https://challenges.cloudflare.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(self), usb=()")
		// Only set CSP for the console/SPA, not the relay API responses.
		if !isPublicRelayPath(c.Request.URL.Path) {
			h.Set("Content-Security-Policy", csp)
		}
		c.Next()
	}
}

// MaxBodyBytes caps the request body for console endpoints (relay endpoints
// set their own, larger limit).
func MaxBodyBytes(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// fixedWindowLimiter is a lightweight per-key request counter. Single-process
// only (matches the default single-instance deployment); a multi-instance
// setup would move this to Redis.
type fixedWindowLimiter struct {
	mu     sync.Mutex
	hits   map[string]*window
	limit  int
	window time.Duration
	lastGC time.Time
}

type window struct {
	count int
	reset time.Time
}

func newFixedWindowLimiter(limit int, w time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{hits: make(map[string]*window), limit: limit, window: w, lastGC: time.Now()}
}

func (l *fixedWindowLimiter) allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > 10*l.window {
		for k, v := range l.hits {
			if now.After(v.reset) {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

	w, ok := l.hits[key]
	if !ok || now.After(w.reset) {
		l.hits[key] = &window{count: 1, reset: now.Add(l.window)}
		return true, 0
	}
	if w.count >= l.limit {
		return false, time.Until(w.reset)
	}
	w.count++
	return true, 0
}

// RateLimit throttles by client IP. Use for unauthenticated endpoints.
func RateLimit(limit int, w time.Duration) gin.HandlerFunc {
	limiter := newFixedWindowLimiter(limit, w)
	return func(c *gin.Context) {
		if allowed, retryAfter := limiter.allow(c.ClientIP()); !allowed {
			util.FailRetry(c, http.StatusTooManyRequests, "request.rate_limited", "too many requests, please slow down", retryAfter)
			c.Abort()
			return
		}
		c.Next()
	}
}
