package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"

	"github.com/gin-gonic/gin"
)

func TestApplyGrokOAuthIdentityHeaders(t *testing.T) {
	headers := make(http.Header)
	applyGrokOAuthIdentityHeaders(headers)
	if got := headers.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
		t.Fatalf("X-XAI-Token-Auth = %q", got)
	}
	if got := headers.Get("X-Grok-Client-Version"); got != grokOAuthClientVersion {
		t.Fatalf("X-Grok-Client-Version = %q", got)
	}
	if got := headers.Get("User-Agent"); got != grokOAuthUserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestForwardGrokOAuthChatUsesResponsesBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var received map[string]any
	var receivedHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		receivedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.output_item.done`,
			`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from grok"}]}}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_grok","created_at":123,"model":"grok-4.5","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":3}}}`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()

	gateway := &Gateway{
		oauth:  oauth.NewManager(nil, config.OAuthConfig{}, upstream.Client()),
		client: upstream.Client(),
	}
	account := &model.UpstreamAccount{
		ID: 7, Platform: model.PlatformGrok, AuthType: model.AuthOAuth,
		BaseURL: upstream.URL, AccessToken: "grok-access-token",
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	response, err := gateway.forwardGrokOAuthChat(c, account, relayRequest{
		Path: "/v1/chat/completions", SessionID: "key:conversation",
		Body: []byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"hello"}],"stream":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	responseBody, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(responseBody), `"content":"hello from grok"`) {
		t.Fatalf("response = %s", responseBody)
	}
	if received["stream"] != true || received["model"] != "grok-4.5" {
		t.Fatalf("upstream body = %#v", received)
	}
	if _, exists := received["messages"]; exists {
		t.Fatalf("chat messages leaked to Responses upstream: %#v", received)
	}
	if receivedHeaders.Get("Authorization") != "Bearer grok-access-token" ||
		receivedHeaders.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
		receivedHeaders.Get("X-Grok-Client-Version") != grokOAuthClientVersion ||
		receivedHeaders.Get("X-Grok-Conv-Id") == "" {
		t.Fatalf("headers = %#v", receivedHeaders)
	}
}
