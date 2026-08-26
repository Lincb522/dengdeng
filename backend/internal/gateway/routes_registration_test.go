package gateway

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterIncludesCompatiblePublicRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Gateway{}).Register(router)

	want := map[string]bool{
		"POST /v1/v1/messages":              false,
		"POST /v1/v1/messages/count_tokens": false,
		"POST /messages":                    false,
		"POST /messages/count_tokens":       false,
		"POST /chat/completions":            false,
		"POST /responses":                   false,
		"POST /responses/compact":           false,
		"POST /responses/input_tokens":      false,
		"POST /images/generations":          false,
		"POST /images/generations/async":    false,
		"GET /images/tasks/:task_id":        false,
		"POST /images/edits":                false,
		"GET /v1/models":                    false,
		"GET /v1/creation-library":          false,
		"GET /v1/usage":                     false,
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
	for _, route := range router.Routes() {
		if route.Method == "GET" && (route.Path == "/models" || route.Path == "/usage") {
			t.Fatalf("frontend route %s must not be occupied by a legacy API alias", route.Path)
		}
	}
}
