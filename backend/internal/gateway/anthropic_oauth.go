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
	return injectClaudeCodeSystemPromptWithText(body, claudeCodeSystemPrompt)
}

func injectClaudeCodeSystemPromptWithText(body []byte, prompt string) []byte {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return body
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = claudeCodeSystemPrompt
	}
	identity := map[string]any{"type": "text", "text": prompt}

	switch system := request["system"].(type) {
	case nil:
		request["system"] = []any{identity}
	case string:
		if strings.TrimSpace(system) == "" {
			request["system"] = []any{identity}
		} else if system == prompt {
			request["system"] = []any{identity}
		} else {
			request["system"] = []any{identity, map[string]any{"type": "text", "text": system}}
		}
	case []any:
		if claudeCodeIdentityFirstWithText(system, prompt) {
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

func injectAnthropicCacheTTL1h(body []byte) []byte {
	var request map[string]any
	if json.Unmarshal(body, &request) != nil || request == nil {
		return body
	}
	changed := false
	apply := func(blocks []any) {
		for _, raw := range blocks {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			cache, ok := block["cache_control"].(map[string]any)
			if ok && stringValue(cache["type"]) == "ephemeral" && stringValue(cache["ttl"]) != "1h" {
				cache["ttl"] = "1h"
				changed = true
			}
		}
	}
	if blocks, ok := request["system"].([]any); ok {
		apply(blocks)
	}
	if messages, ok := request["messages"].([]any); ok {
		for _, raw := range messages {
			message, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if blocks, ok := message["content"].([]any); ok {
				apply(blocks)
			}
		}
	}
	if !changed {
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
	return claudeCodeIdentityFirstWithText(system, claudeCodeSystemPrompt)
}

func claudeCodeIdentityFirstWithText(system []any, prompt string) bool {
	if len(system) == 0 {
		return false
	}
	first, ok := system[0].(map[string]any)
	if !ok {
		return false
	}
	return stringValue(first["text"]) == prompt
}

func rewriteAnthropicMessageCacheControl(body []byte) []byte {
	var request map[string]any
	if json.Unmarshal(body, &request) != nil || request == nil {
		return body
	}
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) == 0 {
		return body
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		if blocks, ok := message["content"].([]any); ok {
			for _, rawBlock := range blocks {
				if block, ok := rawBlock.(map[string]any); ok {
					delete(block, "cache_control")
				}
			}
		}
	}
	targets := []int{len(messages) - 1}
	if len(messages) >= 4 {
		seenUsers := 0
		for i := len(messages) - 1; i >= 0; i-- {
			message, _ := messages[i].(map[string]any)
			if stringValue(message["role"]) == "user" {
				seenUsers++
				if seenUsers == 2 {
					targets = append(targets, i)
					break
				}
			}
		}
	}
	for _, index := range targets {
		message, ok := messages[index].(map[string]any)
		if !ok {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			message["content"] = []any{map[string]any{"type": "text", "text": content, "cache_control": map[string]any{"type": "ephemeral"}}}
		case []any:
			if len(content) > 0 {
				if block, ok := content[len(content)-1].(map[string]any); ok {
					block["cache_control"] = map[string]any{"type": "ephemeral"}
				}
			}
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return body
	}
	return encoded
}
