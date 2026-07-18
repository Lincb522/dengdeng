package middleware

import (
	"crypto/rand"
	"encoding/hex"
)

import "github.com/gin-gonic/gin"

const CtxRequestID = "ctx_request_id"

// RequestID adds a compact correlation identifier to every response. The
// value is generated server-side rather than trusting a caller supplied value,
// so support, gateway logs and usage records always refer to the same safe ID.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := newRequestID()
		c.Set(CtxRequestID, id)
		c.Header("X-Request-ID", id)
		// A provider-neutral alias makes the identifier's purpose explicit to
		// clients while X-Request-ID remains fully backwards compatible.
		c.Header("X-DengDeng-Trace-ID", id)
		c.Next()
	}
}

func RequestIDFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(CtxRequestID)
	id, _ := value.(string)
	return id
}

func newRequestID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "ddr_" + hex.EncodeToString(raw[:])
	}
	// crypto/rand failure is exceptionally rare. Keep a non-empty, opaque
	// identifier even then; the header remains useful for correlating one
	// response with the ledger entry written in this process.
	return "ddr_unavailable"
}
