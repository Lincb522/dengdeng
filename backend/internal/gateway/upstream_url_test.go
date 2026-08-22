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

func TestForwardChineseProviderProtocolURLsAndCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, platform, path, baseField, wantHeader string
	}{
		{"kimi-chat", model.PlatformKimi, "/v1/chat/completions", "chat", "Authorization"},
		{"zhipu-anthropic", model.PlatformZhipu, "/v1/messages", "anthropic", "x-api-key"},
		{"deepseek-responses", model.PlatformDeepSeek, "/v1/responses", "responses", "Authorization"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(tt.wantHeader); got == "" {
					t.Errorf("missing %s", tt.wantHeader)
				}
				if r.URL.Path != tt.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tt.path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			account := &model.UpstreamAccount{Platform: tt.platform, AuthType: model.AuthAPIKey, APIKey: "provider-key", APIProtocol: model.APIProtocolAdaptive}
			switch tt.baseField {
			case "chat":
				account.ChatBaseURL = upstream.URL
			case "anthropic":
				account.AnthropicBaseURL = upstream.URL
			case "responses":
				account.ResponsesBaseURL = upstream.URL
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			response, err := (&Gateway{client: upstream.Client()}).forward(ctx, account, relayRequest{Platform: tt.platform, Path: tt.path, Body: []byte(`{}`)})
			if err != nil {
				t.Fatalf("forward() error = %v", err)
			}
			response.Body.Close()
		})
	}
}

func TestAccountRequestPathForZhipuProviderBase(t *testing.T) {
	tests := []struct {
		name, base, path, want string
	}{
		{"china payg", "https://open.bigmodel.cn/api/paas/v4", "/v1/chat/completions", "/chat/completions"},
		{"global payg", "https://api.z.ai/api/paas/v4", "/v1/chat/completions", "/chat/completions"},
		{"china coding", "https://open.bigmodel.cn/api/coding/paas/v4", "/v1/chat/completions", "/chat/completions"},
		{"anthropic", "https://api.z.ai/api/anthropic", "/v1/messages", "/v1/messages"},
		{"relay", "https://relay.example/zhipu", "/v1/chat/completions", "/v1/chat/completions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := accountRequestPathForBase(model.PlatformZhipu, test.base, test.path); got != test.want {
				t.Fatalf("path=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAdaptCompositeNativeProvider(t *testing.T) {
	req := relayRequest{Platform: model.PlatformComposite, Path: "/v1/chat/completions", Body: []byte(`{"model":"moonshot-v1"}`)}
	got, err := (&Gateway{}).adaptCompositeRequest(req, &model.UpstreamAccount{Platform: model.PlatformKimi})
	if err != nil {
		t.Fatalf("adaptCompositeRequest() error = %v", err)
	}
	if got.Platform != model.PlatformKimi || got.Path != req.Path {
		t.Fatalf("adapted request = %#v", got)
	}
}
