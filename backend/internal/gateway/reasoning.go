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

// upstreamReasoningEffort maps DengDeng's UI shortcut to a documented OpenAI
// effort. It never changes the selected model or service tier.
func upstreamReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast":
		return "low"
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

// applyOpenAIReasoningDefault only fills a missing client field. This lets a
// key set a sensible default without blocking a request that deliberately
// chooses another effort level.
func applyOpenAIReasoningDefault(fields map[string]json.RawMessage, body []byte, defaultEffort string, wire openAIReasoningWire) []byte {
	effort := upstreamReasoningEffort(defaultEffort)
	if effort == "" {
		return body
	}

	if wire == openAIReasoningChatCompletions {
		if _, exists := fields["reasoning_effort"]; exists {
			return body
		}
		encoded, err := json.Marshal(effort)
		if err != nil {
			return body
		}
		fields["reasoning_effort"] = encoded
		if patched, err := json.Marshal(fields); err == nil {
			return patched
		}
		return body
	}

	reasoning := map[string]json.RawMessage{}
	if raw, exists := fields["reasoning"]; exists && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &reasoning); err != nil {
			// Leave malformed client JSON untouched; the upstream can return its
			// normal validation error instead of the gateway silently rewriting it.
			return body
		}
		if _, exists := reasoning["effort"]; exists {
			return body
		}
	}
	encoded, err := json.Marshal(effort)
	if err != nil {
		return body
	}
	reasoning["effort"] = encoded
	encoded, err = json.Marshal(reasoning)
	if err != nil {
		return body
	}
	fields["reasoning"] = encoded
	if patched, err := json.Marshal(fields); err == nil {
		return patched
	}
	return body
}

// applyOpenAIResponsesReasoningDefault is the same policy for the Claude
// Code compatibility path, after its Messages payload is converted to an
// OpenAI Responses request.
func applyOpenAIResponsesReasoningDefault(request map[string]any, defaultEffort string) {
	effort := upstreamReasoningEffort(defaultEffort)
	if effort == "" {
		return
	}
	reasoning, _ := request["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
	}
	if _, exists := reasoning["effort"]; exists {
		return
	}
	reasoning["effort"] = effort
	request["reasoning"] = reasoning
}
