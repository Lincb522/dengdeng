package util

import "testing"

func TestJoinUpstreamURLAvoidsDuplicateVersionPrefixes(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		target string
		want   string
	}{
		{"host", "https://relay.example", "/v1/chat/completions", "https://relay.example/v1/chat/completions"},
		{"openai sdk base", "https://relay.example/v1", "/v1/chat/completions", "https://relay.example/v1/chat/completions"},
		{"anthropic sdk base", "https://relay.example/v1/", "/v1/messages", "https://relay.example/v1/messages"},
		{"path prefix", "https://relay.example/api/openai/v1", "/v1/responses", "https://relay.example/api/openai/v1/responses"},
		{"gemini sdk base", "https://relay.example/v1beta", "/v1beta/models/gemini:test?alt=sse", "https://relay.example/v1beta/models/gemini:test?alt=sse"},
		{"full chat endpoint", "https://relay.example/api/v1/chat/completions", "/v1/chat/completions", "https://relay.example/api/v1/chat/completions"},
		{"full messages endpoint", "https://relay.example/v1/messages?legacy=true", "/v1/messages", "https://relay.example/v1/messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JoinUpstreamURL(tt.base, tt.target)
			if err != nil {
				t.Fatalf("JoinUpstreamURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("JoinUpstreamURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeUpstreamBaseURLRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"relay.example/v1", "ftp://relay.example/v1", "://bad"} {
		if _, err := NormalizeUpstreamBaseURL(value); err == nil {
			t.Fatalf("NormalizeUpstreamBaseURL(%q) unexpectedly succeeded", value)
		}
	}
}
