package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

func systemBlocks(t *testing.T, body []byte) []any {
	t.Helper()
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	blocks, ok := request["system"].([]any)
	if !ok {
		t.Fatalf("system is not an array: %T", request["system"])
	}
	return blocks
}

func firstBlockText(t *testing.T, body []byte) string {
	t.Helper()
	blocks := systemBlocks(t, body)
	if len(blocks) == 0 {
		t.Fatalf("no system blocks")
	}
	first, _ := blocks[0].(map[string]any)
	return stringValue(first["text"])
}

func TestInjectClaudeCodeSystemPromptWhenMissing(t *testing.T) {
	out := injectClaudeCodeSystemPrompt([]byte(`{"model":"claude","messages":[]}`))
	if got := firstBlockText(t, out); got != claudeCodeSystemPrompt {
		t.Fatalf("first block = %q, want identity", got)
	}
	if blocks := systemBlocks(t, out); len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
}

func TestInjectClaudeCodePreservesStringSystem(t *testing.T) {
	out := injectClaudeCodeSystemPrompt([]byte(`{"system":"be terse","messages":[]}`))
	blocks := systemBlocks(t, out)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if got := firstBlockText(t, out); got != claudeCodeSystemPrompt {
		t.Fatalf("first block = %q", got)
	}
	second, _ := blocks[1].(map[string]any)
	if stringValue(second["text"]) != "be terse" {
		t.Fatalf("original system was not preserved: %#v", second)
	}
}

func TestInjectClaudeCodeIsIdempotent(t *testing.T) {
	in := []byte(`{"system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."},{"type":"text","text":"extra"}],"messages":[]}`)
	out := injectClaudeCodeSystemPrompt(in)
	if len(systemBlocks(t, out)) != 2 {
		t.Fatalf("identity-first request must not be double-prefixed: %s", out)
	}
	if got := firstBlockText(t, out); got != claudeCodeSystemPrompt {
		t.Fatalf("first block = %q", got)
	}
}

func TestInjectClaudeCodePrependsToArraySystem(t *testing.T) {
	in := []byte(`{"system":[{"type":"text","text":"custom rules"}],"messages":[]}`)
	out := injectClaudeCodeSystemPrompt(in)
	blocks := systemBlocks(t, out)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if firstBlockText(t, out) != claudeCodeSystemPrompt {
		t.Fatalf("identity must be first: %s", out)
	}
}

func TestInjectClaudeCodeIgnoresInvalidJSON(t *testing.T) {
	in := []byte(`not json`)
	if out := injectClaudeCodeSystemPrompt(in); string(out) != string(in) {
		t.Fatalf("invalid JSON must be returned unchanged")
	}
}

func TestApplyAnthropicOAuthIdentityHeaders(t *testing.T) {
	header := http.Header{}
	applyAnthropicOAuthIdentityHeaders(header)
	if got := header.Get("User-Agent"); got != claudeCodeUserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := header.Get("x-app"); got != "cli" {
		t.Fatalf("x-app = %q", got)
	}
	if got := header.Get("anthropic-dangerous-direct-browser-access"); got != "true" {
		t.Fatalf("browser-access = %q", got)
	}
	if header.Get("x-stainless-lang") == "" {
		t.Fatalf("stainless metadata missing")
	}
}

func TestOAuthSessionHeaderIsStableForSeed(t *testing.T) {
	a := oauthSessionHeader("42:conv-1")
	b := oauthSessionHeader("42:conv-1")
	if a != b {
		t.Fatalf("same seed must be stable: %q vs %q", a, b)
	}
	if oauthSessionHeader("42:conv-2") == a {
		t.Fatalf("different seeds must differ")
	}
	if len(a) != 36 || a[8] != '-' || a[13] != '-' || a[18] != '-' || a[23] != '-' {
		t.Fatalf("not a UUID shape: %q", a)
	}
	if a[14] != '4' {
		t.Fatalf("version nibble not 4: %q", a)
	}
}

func TestOAuthSessionHeaderRandomWhenNoSeed(t *testing.T) {
	if oauthSessionHeader("") == oauthSessionHeader("") {
		t.Fatalf("empty seed should produce random ids")
	}
}

func TestApplyAnthropicOAuthIdentityKeepsCallerStainless(t *testing.T) {
	header := http.Header{}
	header.Set("x-stainless-os", "Linux")
	applyAnthropicOAuthIdentityHeaders(header)
	if got := header.Get("x-stainless-os"); got != "Linux" {
		t.Fatalf("caller stainless header was overwritten: %q", got)
	}
}
