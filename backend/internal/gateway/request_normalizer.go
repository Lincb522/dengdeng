package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	codexInputItemIDLimit      = 64
	codexResponsesLiteHeader   = "X-OpenAI-Internal-Codex-Responses-Lite"
	codexResponsesLiteMetadata = "ws_request_header_x_openai_internal_codex_responses_lite"
)

// normalizeOpenAIResponsesRequest applies provider-safe, semantics-preserving
// fixes to a Responses request. The same normalizer is used by direct API-key
// traffic and the Codex OAuth path so compatibility does not depend on which
// upstream credential happened to be selected.
func normalizeOpenAIResponsesRequest(body []byte, headers http.Header) ([]byte, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	normalizeResponsesTools(request["tools"])
	normalizeResponsesParallelToolCalls(request, headers)
	normalizeResponsesInputItemIDs(request["input"])
	return json.Marshal(request)
}

// normalizeOpenAIChatRequest fixes the equivalent function schema location on
// Chat Completions requests. Other client fields remain untouched.
func normalizeOpenAIChatRequest(body []byte) ([]byte, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	tools, _ := request["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		if function != nil {
			if _, exists := function["parameters"]; !exists {
				function["parameters"] = emptyObjectToolSchema()
			}
			normalizeObjectRootUnionBranchTypes(function["parameters"])
		}
	}
	return json.Marshal(request)
}

func normalizeResponsesTools(raw any) {
	tools, _ := raw.([]any)
	for _, value := range tools {
		tool, _ := value.(map[string]any)
		if tool == nil || !strings.EqualFold(strings.TrimSpace(stringValue(tool["type"])), "function") {
			continue
		}
		if _, exists := tool["parameters"]; !exists {
			tool["parameters"] = emptyObjectToolSchema()
		}
		normalizeObjectRootUnionBranchTypes(tool["parameters"])
	}
}

func emptyObjectToolSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// normalizeObjectRootUnionBranchTypes follows the strict-provider rule used
// by current CPA: when the parameter root is explicitly object-only, untyped
// root anyOf/oneOf branches are object branches too. Making that type explicit
// preserves the schema while avoiding upstream "Invalid tool parameters".
func normalizeObjectRootUnionBranchTypes(raw any) {
	parameters, _ := raw.(map[string]any)
	if parameters == nil || !strings.EqualFold(strings.TrimSpace(stringValue(parameters["type"])), "object") {
		return
	}
	for _, unionName := range []string{"anyOf", "oneOf"} {
		branches, _ := parameters[unionName].([]any)
		for _, value := range branches {
			branch, _ := value.(map[string]any)
			if branch == nil {
				continue
			}
			if _, exists := branch["type"]; !exists {
				branch["type"] = "object"
			}
		}
	}
}

func normalizeResponsesParallelToolCalls(request map[string]any, headers http.Header) {
	if isCodexResponsesLiteRequest(request, headers) {
		request["parallel_tool_calls"] = false
		return
	}
	if _, exists := request["parallel_tool_calls"]; !exists {
		return
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) == 0 {
		delete(request, "parallel_tool_calls")
	}
}

func isCodexResponsesLiteRequest(request map[string]any, headers http.Header) bool {
	if strings.EqualFold(strings.TrimSpace(headers.Get(codexResponsesLiteHeader)), "true") {
		return true
	}
	metadata, _ := request["client_metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	value, exists := metadata[codexResponsesLiteMetadata]
	if !exists {
		return false
	}
	if enabled, ok := value.(bool); ok {
		return enabled
	}
	return strings.EqualFold(strings.TrimSpace(stringValue(value)), "true")
}

func normalizeResponsesInputItemIDs(raw any) {
	input, _ := raw.([]any)
	if len(input) == 0 {
		return
	}
	occupied := make(map[string]struct{}, len(input))
	for _, value := range input {
		item, _ := value.(map[string]any)
		id, _ := item["id"].(string)
		if id != "" && len([]rune(id)) <= codexInputItemIDLimit {
			occupied[id] = struct{}{}
		}
	}
	mapped := make(map[string]string)
	for _, value := range input {
		item, _ := value.(map[string]any)
		id, _ := item["id"].(string)
		if id == "" || len([]rune(id)) <= codexInputItemIDLimit {
			continue
		}
		shortened, exists := mapped[id]
		if !exists {
			for attempt := 0; ; attempt++ {
				shortened = shortenResponsesInputItemID(id, attempt)
				if _, collision := occupied[shortened]; !collision {
					break
				}
			}
			mapped[id] = shortened
			occupied[shortened] = struct{}{}
		}
		item["id"] = shortened
	}
}

func shortenResponsesInputItemID(id string, attempt int) string {
	runes := []rune(id)
	if len(runes) <= codexInputItemIDLimit {
		return id
	}
	hashInput := id
	if attempt > 0 {
		hashInput += "\x00" + strconv.Itoa(attempt)
	}
	sum := sha256.Sum256([]byte(hashInput))
	suffix := "_" + hex.EncodeToString(sum[:8])
	return string(runes[:codexInputItemIDLimit-len(suffix)]) + suffix
}
