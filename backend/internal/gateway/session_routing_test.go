package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func relaySessionTestContext(headerName, headerValue string) *gin.Context {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	if headerName != "" {
		context.Request.Header.Set(headerName, headerValue)
	}
	return context
}

func TestRelaySessionIDPrefersExplicitHeader(t *testing.T) {
	context := relaySessionTestContext("X-Session-ID", "conversation-a")
	got := relaySessionID(context, 42, []byte(`{"conversation_id":"body-value"}`))
	if got != "42:conversation-a" {
		t.Fatalf("session id = %q", got)
	}
}

func TestRelaySessionIDReadsSupportedJSONFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "conversation", body: `{"conversation":{"id":"conversation-a"}}`, want: "7:conversation-a"},
		{name: "anthropic metadata", body: `{"metadata":{"user_id":"user-a"}}`, want: "7:user-a"},
		{name: "prompt cache", body: `{"prompt_cache_key":"cache-a"}`, want: "7:cache-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := relaySessionID(relaySessionTestContext("", ""), 7, []byte(test.body)); got != test.want {
				t.Fatalf("session id = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRelaySessionIDDoesNotDeriveFromPrompt(t *testing.T) {
	context := relaySessionTestContext("", "")
	if got := relaySessionID(context, 7, []byte(`{"input":"hello"}`)); got != "" {
		t.Fatalf("session id = %q", got)
	}
}
