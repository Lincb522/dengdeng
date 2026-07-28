package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
)

func TestForwardAcceptsSDKStyleBaseURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		platform string
		basePath string
		reqPath  string
		wantPath string
	}{
		{"openai", model.PlatformOpenAI, "/v1", "/v1/chat/completions", "/v1/chat/completions"},
		{"anthropic", model.PlatformAnthropic, "/v1", "/v1/messages", "/v1/messages"},
		{"gemini", model.PlatformGemini, "/v1beta", "/v1beta/models/gemini:test", "/v1beta/models/gemini:test"},
		{"grok", model.PlatformGrok, "/v1", "/v1/chat/completions", "/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("upstream path = %q, want %q", r.URL.Path, tt.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			gateway := &Gateway{client: upstream.Client()}
			account := &model.UpstreamAccount{
				Platform: tt.platform, AuthType: model.AuthAPIKey,
				BaseURL: upstream.URL + tt.basePath, APIKey: "test-key",
			}
			response, err := gateway.forward(ctx, account, relayRequest{
				Platform: tt.platform, Path: tt.reqPath, Body: []byte(`{}`),
			})
			if err != nil {
				t.Fatalf("forward() error = %v", err)
			}
			response.Body.Close()
		})
	}
}
