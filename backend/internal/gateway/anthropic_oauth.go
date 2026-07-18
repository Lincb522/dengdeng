package gateway

// This file adapts outbound Anthropic requests made with a Claude
// subscription (Pro/Max) OAuth credential so they match what the official
// Claude Code CLI sends. claude.ai OAuth tokens are only authorized for Claude
// Code: the upstream rejects a request whose first system block is not the
// Claude Code identity, and a non-CLI User-Agent/header set is an obvious
// signal for account review. Injecting the identity here keeps imported OAuth
// accounts working and makes relayed traffic indistinguishable from the CLI.

import (
	"encoding/json"
	"net/http"
	"strings"
)

const (
	// claudeCodeSystemPrompt must be the exact text of the first system block
	// for a claude.ai OAuth credential; the upstream matches on it verbatim.
	claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

	// Identity of the official CLI. Keep the User-Agent and stainless headers
	// aligned with a real Claude Code build.
	claudeCodeVersion   = "2.1.7"
	claudeCodeUserAgent = "claude-cli/2.1.7 (external, cli)"

	// anthropicOAuthBeta is merged with the caller's beta flags. oauth is
	// mandatory for the bearer flow; claude-code marks the CLI client.
	anthropicOAuthBeta = "oauth-2025-04-20,claude-code-20250219"
)

// applyAnthropicOAuthIdentityHeaders makes an Anthropic OAuth request look like
// the official Claude Code CLI. It is only used on the OAuth bearer path; the
// x-api-key path for API-key accounts is left untouched.
func applyAnthropicOAuthIdentityHeaders(header http.Header) {
	header.Set("User-Agent", claudeCodeUserAgent)
	header.Set("x-app", "cli")
	header.Set("anthropic-dangerous-direct-browser-access", "true")
	if header.Get("anthropic-version") == "" {
		header.Set("anthropic-version", "2023-06-01")
	}
	// Stainless SDK metadata the CLI attaches. Only set when the caller did
	// not already provide its own, so a genuine Claude Code client passes
	// through unchanged.
	stainless := map[string]string{
		"x-stainless-lang":            "js",
		"x-stainless-package-version": "0.65.0",
		"x-stainless-runtime":         "node",
		"x-stainless-runtime-version": "v22.14.0",
		"x-stainless-os":              "MacOS",
		"x-stainless-arch":            "arm64",
		"x-stainless-retry-count":     "0",
	}
	for key, value := range stainless {
		if header.Get(key) == "" {
			header.Set(key, value)
		}
	}
}

// injectClaudeCodeSystemPrompt guarantees the Claude Code identity is the first
// system block. It preserves any system prompt the caller supplied by keeping
// it as a subsequent block, and is a no-op when the identity is already first
// (a real Claude Code request), so it is safe to apply unconditionally on the
// OAuth path. Non-JSON or non-object bodies are returned unchanged.
func injectClaudeCodeSystemPrompt(body []byte) []byte {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return body
	}
	identity := map[string]any{"type": "text", "text": claudeCodeSystemPrompt}

	switch system := request["system"].(type) {
	case nil:
		request["system"] = []any{identity}
	case string:
		if strings.TrimSpace(system) == "" {
			request["system"] = []any{identity}
		} else if system == claudeCodeSystemPrompt {
			request["system"] = []any{identity}
		} else {
			request["system"] = []any{identity, map[string]any{"type": "text", "text": system}}
		}
	case []any:
		if claudeCodeIdentityFirst(system) {
			return body
		}
		request["system"] = append([]any{identity}, system...)
	default:
		return body
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return body
	}
	return encoded
}

// claudeCodeIdentityFirst reports whether the first text block already carries
// the Claude Code identity, so a genuine CLI request is not double-prefixed.
func claudeCodeIdentityFirst(system []any) bool {
	if len(system) == 0 {
		return false
	}
	first, ok := system[0].(map[string]any)
	if !ok {
		return false
	}
	return stringValue(first["text"]) == claudeCodeSystemPrompt
}
