package gateway

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterIncludesLegacyAnthropicDoubleV1Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Gateway{}).Register(router)

	want := map[string]bool{
		"POST /v1/v1/messages":              false,
		"POST /v1/v1/messages/count_tokens": false,
		"POST /v1/responses/compact":        false,
		"POST /v1/responses/input_tokens":   false,
		"GET /backend-api/codex/models":     false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Fatalf("route %s is not registered", route)
		}
	}
}
