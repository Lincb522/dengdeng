package gateway

import (
	"encoding/json"
	"strings"
)

type openAIReasoningWire uint8

const (
	openAIReasoningResponses openAIReasoningWire = iota
	openAIReasoningChatCompletions
)

// upstreamReasoningEffort returns a GPT-5.6-supported effort. Earlier
// DengDeng releases stored fast/minimal, so both remain accepted as aliases
// for low during migration.
func upstreamReasoningEffort(value string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "fast", "minimal":
		return "low"
	case "none", "low", "medium", "high", "xhigh", "max":
		return normalized
	default:
		return ""
	}
}

// billableEffort normalizes a client-provided effort for the usage ledger.
// Unknown spellings are kept lowercase as-is: the upstream will reject or
// accept them, and the ledger should reflect what was actually requested.
func billableEffort(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if mapped := upstreamReasoningEffort(normalized); mapped != "" {
		return mapped
	}
	if len(normalized) > 16 {
		normalized = normalized[:16]
	}
	return normalized
}

// applyOpenAIReasoningDefault only fills a missing client field. This lets a
// key set a sensible default without blocking a request that deliberately
// chooses another effort level. The second return value is the effective
// effort of the outgoing request ("" when the model default applies), which
// the gateway records for per-effort billing.
func applyOpenAIReasoningDefault(fields map[string]json.RawMessage, body []byte, defaultEffort string, wire openAIReasoningWire) ([]byte, string) {
	effort := upstreamReasoningEffort(defaultEffort)

	if wire == openAIReasoningChatCompletions {
		if raw, exists := fields["reasoning_effort"]; exists {
			return body, billableEffort(jsonString(raw))
		}
		if effort == "" {
			return body, ""
		}
		encoded, err := json.Marshal(effort)
		if err != nil {
			return body, ""
		}
		fields["reasoning_effort"] = encoded
		if patched, err := json.Marshal(fields); err == nil {
			return patched, effort
		}
		return body, ""
	}

	reasoning := map[string]json.RawMessage{}
	if raw, exists := fields["reasoning"]; exists && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &reasoning); err != nil {
			// Leave malformed client JSON untouched; the upstream can return its
			// normal validation error instead of the gateway silently rewriting it.
			return body, ""
		}
		if raw, exists := reasoning["effort"]; exists {
			return body, billableEffort(jsonString(raw))
		}
	}
	if effort == "" {
		return body, ""
	}
	encoded, err := json.Marshal(effort)
	if err != nil {
		return body, ""
	}
	reasoning["effort"] = encoded
	encoded, err = json.Marshal(reasoning)
	if err != nil {
		return body, ""
	}
	fields["reasoning"] = encoded
	if patched, err := json.Marshal(fields); err == nil {
		return patched, effort
	}
	return body, ""
}

// applyOpenAIResponsesReasoningDefault is the same policy for the Claude
// Code compatibility path, after its Messages payload is converted to an
// OpenAI Responses request. Returns the effective effort for billing.
func applyOpenAIResponsesReasoningDefault(request map[string]any, defaultEffort string) string {
	effort := upstreamReasoningEffort(defaultEffort)
	reasoning, _ := request["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
	}
	if existing, exists := reasoning["effort"]; exists {
		if text, ok := existing.(string); ok {
			return billableEffort(text)
		}
		return ""
	}
	if effort == "" {
		return ""
	}
	reasoning["effort"] = effort
	request["reasoning"] = reasoning
	return effort
}
