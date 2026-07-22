package gateway

import (
	"encoding/json"
	"strings"

	"dengdeng/internal/model"
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

var reasoningEffortRank = map[string]int{
	"none": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5,
}

// groupReasoningEffort first applies the group's exact mapping and then its
// ceiling. It returns ok=false for unknown client spellings so upstream keeps
// responsibility for its normal validation error.
func groupReasoningEffort(value string, group model.Group) (effort string, ok bool) {
	effort = upstreamReasoningEffort(value)
	if effort == "" {
		return billableEffort(value), false
	}
	if mapped := upstreamReasoningEffort(group.ReasoningEffortMappings[effort]); mapped != "" {
		effort = mapped
	}
	ceiling := upstreamReasoningEffort(group.MaxReasoningEffort)
	if ceiling != "" && reasoningEffortRank[effort] > reasoningEffortRank[ceiling] {
		effort = ceiling
	}
	return effort, true
}

// applyOpenAIReasoningDefault only fills a missing client field. This lets a
// key set a sensible default without blocking a request that deliberately
// chooses another effort level. The second return value is the effective
// effort of the outgoing request ("" when the model default applies), which
// the gateway records for per-effort billing.
func applyOpenAIReasoningDefault(fields map[string]json.RawMessage, body []byte, defaultEffort string, wire openAIReasoningWire) ([]byte, string) {
	return applyOpenAIReasoningPolicy(fields, body, defaultEffort, model.Group{}, wire)
}

// applyOpenAIReasoningPolicy resolves the client choice (or key default),
// applies group mapping and ceiling, rewrites the outgoing request when the
// policy changes it, and returns the final effort used for billing.
func applyOpenAIReasoningPolicy(fields map[string]json.RawMessage, body []byte, defaultEffort string, group model.Group, wire openAIReasoningWire) ([]byte, string) {
	requested := defaultEffort
	explicit := false

	if wire == openAIReasoningChatCompletions {
		if raw, exists := fields["reasoning_effort"]; exists {
			requested = jsonString(raw)
			explicit = true
		}
		effort, known := groupReasoningEffort(requested, group)
		if !known {
			if explicit {
				return body, effort
			}
			return body, ""
		}
		if explicit && billableEffort(requested) == effort {
			return body, effort
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
			requested = jsonString(raw)
			explicit = true
		}
	}
	effort, known := groupReasoningEffort(requested, group)
	if !known {
		if explicit {
			return body, effort
		}
		return body, ""
	}
	if explicit && billableEffort(requested) == effort {
		return body, effort
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
	return applyOpenAIResponsesReasoningPolicy(request, defaultEffort, model.Group{})
}

func applyOpenAIResponsesReasoningPolicy(request map[string]any, defaultEffort string, group model.Group) string {
	requested := defaultEffort
	explicit := false
	reasoning, _ := request["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
	}
	if existing, exists := reasoning["effort"]; exists {
		if text, ok := existing.(string); ok {
			requested = text
			explicit = true
		} else {
			return ""
		}
	}
	effort, known := groupReasoningEffort(requested, group)
	if !known {
		if explicit {
			return effort
		}
		return ""
	}
	if explicit && billableEffort(requested) == effort {
		return effort
	}
	reasoning["effort"] = effort
	request["reasoning"] = reasoning
	return effort
}
