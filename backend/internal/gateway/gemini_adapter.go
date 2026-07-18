package gateway

// This file bridges the public OpenAI Chat Completions contract to a Gemini
// upstream group. Like protocol_adapter.go it is a wire translator, not a model
// emulator: the Gemini model chosen by the API-key group is preserved for
// routing and billing while only the request/response envelopes are converted.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/service"

	"github.com/gin-gonic/gin"
)

// openAIChatToGemini converts an OpenAI Chat Completions request into a Gemini
// generateContent request. It returns the Gemini body, the requested model and
// whether the caller asked for a stream.
func openAIChatToGemini(body []byte) (map[string]any, string, bool, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", false, err
	}
	modelName := strings.TrimSpace(stringValue(request["model"]))
	if modelName == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, "", false, fmt.Errorf("messages is required")
	}

	contents := make([]any, 0, len(messages))
	var systemParts []any
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringValue(message["role"])))
		switch role {
		case "system", "developer":
			if part := geminiTextPart(message["content"]); part != nil {
				systemParts = append(systemParts, part)
			}
		case "tool":
			// A tool result maps to a functionResponse part in a user turn.
			name := firstNonEmpty(stringValue(message["name"]), stringValue(message["tool_call_id"]))
			contents = appendGeminiContent(contents, "user", map[string]any{
				"functionResponse": map[string]any{
					"name":     name,
					"response": map[string]any{"result": contentText(message["content"])},
				},
			})
		case "assistant":
			parts := geminiContentParts(message["content"], false)
			if calls, ok := message["tool_calls"].([]any); ok {
				for _, rawCall := range calls {
					call, ok := rawCall.(map[string]any)
					if !ok {
						continue
					}
					function, _ := call["function"].(map[string]any)
					parts = append(parts, map[string]any{
						"functionCall": map[string]any{
							"name": stringValue(function["name"]),
							"args": decodeJSONValue(stringValue(function["arguments"])),
						},
					})
				}
			}
			if len(parts) > 0 {
				contents = appendGeminiContent(contents, "model", parts...)
			}
		default:
			parts := geminiContentParts(message["content"], true)
			if len(parts) > 0 {
				contents = appendGeminiContent(contents, "user", parts...)
			}
		}
	}
	if len(contents) == 0 {
		return nil, "", false, fmt.Errorf("messages is required")
	}

	converted := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		converted["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	generation := map[string]any{}
	if value, ok := request["temperature"]; ok {
		generation["temperature"] = value
	}
	if value, ok := request["top_p"]; ok {
		generation["topP"] = value
	}
	if max := firstPositive(request["max_completion_tokens"], request["max_tokens"]); max > 0 {
		generation["maxOutputTokens"] = max
	}
	if len(generation) > 0 {
		converted["generationConfig"] = generation
	}
	if tools := openAIToolsToGemini(request["tools"]); tools != nil {
		converted["tools"] = tools
	}
	if config := openAIToolChoiceToGemini(request["tool_choice"]); config != nil {
		converted["toolConfig"] = config
	}
	return converted, modelName, boolValue(request["stream"]), nil
}

func geminiTextPart(content any) map[string]any {
	text := geminiPlainText(content)
	if text == "" {
		return nil
	}
	return map[string]any{"text": text}
}

// geminiContentParts turns OpenAI message content into Gemini parts. It keeps
// text and, for user turns, inline image data URLs; other block types are
// skipped rather than relayed as malformed parts.
func geminiContentParts(content any, allowImages bool) []any {
	switch v := content.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []any{map[string]any{"text": v}}
	case []any:
		parts := make([]any, 0, len(v))
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch strings.ToLower(stringValue(part["type"])) {
			case "text", "input_text", "output_text":
				if text := contentText(part["text"]); text != "" {
					parts = append(parts, map[string]any{"text": text})
				}
			case "image_url", "input_image":
				if !allowImages {
					continue
				}
				imageURL := part["image_url"]
				if nested, ok := imageURL.(map[string]any); ok {
					imageURL = nested["url"]
				}
				if inline := geminiInlineImage(stringValue(imageURL)); inline != nil {
					parts = append(parts, inline)
				}
			}
		}
		return parts
	default:
		if text := contentText(content); text != "" {
			return []any{map[string]any{"text": text}}
		}
		return nil
	}
}

// geminiInlineImage converts a data: URL into a Gemini inlineData part. Remote
// URLs are skipped because Gemini generateContent expects inline bytes.
func geminiInlineImage(url string) map[string]any {
	if !strings.HasPrefix(url, "data:") {
		return nil
	}
	comma := strings.Index(url, ",")
	semicolon := strings.Index(url, ";")
	if comma < 0 || semicolon < 0 || semicolon > comma {
		return nil
	}
	mimeType := strings.TrimPrefix(url[:semicolon], "data:")
	data := url[comma+1:]
	if mimeType == "" || data == "" {
		return nil
	}
	return map[string]any{"inlineData": map[string]any{"mimeType": mimeType, "data": data}}
}

func geminiPlainText(content any) string {
	var text strings.Builder
	for _, raw := range geminiContentParts(content, false) {
		part, _ := raw.(map[string]any)
		text.WriteString(stringValue(part["text"]))
	}
	return text.String()
}

// appendGeminiContent merges consecutive same-role turns, which Gemini expects
// to be coalesced into a single content entry.
func appendGeminiContent(contents []any, role string, parts ...any) []any {
	if len(parts) == 0 {
		return contents
	}
	if len(contents) > 0 {
		last, _ := contents[len(contents)-1].(map[string]any)
		if last != nil && stringValue(last["role"]) == role {
			existing, _ := last["parts"].([]any)
			last["parts"] = append(existing, parts...)
			return contents
		}
	}
	return append(contents, map[string]any{"role": role, "parts": parts})
}

func openAIToolsToGemini(raw any) []any {
	tools, ok := raw.([]any)
	if !ok || len(tools) == 0 {
		return nil
	}
	declarations := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || stringValue(tool["type"]) != "function" {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok || stringValue(function["name"]) == "" {
			continue
		}
		declaration := map[string]any{"name": function["name"]}
		if value, ok := function["description"]; ok {
			declaration["description"] = value
		}
		if value, ok := function["parameters"]; ok {
			declaration["parameters"] = value
		}
		declarations = append(declarations, declaration)
	}
	if len(declarations) == 0 {
		return nil
	}
	return []any{map[string]any{"functionDeclarations": declarations}}
}

func openAIToolChoiceToGemini(raw any) map[string]any {
	mode := ""
	switch value := raw.(type) {
	case string:
		switch strings.ToLower(value) {
		case "required":
			mode = "ANY"
		case "none":
			mode = "NONE"
		case "auto":
			mode = "AUTO"
		}
	case map[string]any:
		if stringValue(value["type"]) == "function" {
			function, _ := value["function"].(map[string]any)
			if name := stringValue(function["name"]); name != "" {
				return map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []any{name}}}
			}
			mode = "ANY"
		}
	}
	if mode == "" {
		return nil
	}
	return map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
}

// geminiCandidateContent extracts text and function-call tool calls from a
// Gemini candidates array.
func geminiCandidateContent(candidates []any) (string, []any, string) {
	var text strings.Builder
	var calls []any
	finish := ""
	for _, raw := range candidates {
		candidate, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if reason := stringValue(candidate["finishReason"]); reason != "" {
			finish = reason
		}
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			if fc, ok := part["functionCall"].(map[string]any); ok {
				arguments, _ := json.Marshal(fc["args"])
				calls = append(calls, map[string]any{
					"id":       fmt.Sprintf("call_%d", len(calls)),
					"type":     "function",
					"function": map[string]any{"name": stringValue(fc["name"]), "arguments": string(arguments)},
				})
				continue
			}
			text.WriteString(stringValue(part["text"]))
		}
	}
	return text.String(), calls, finish
}

func geminiFinishReasonAsOpenAI(reason string, toolUsed bool) string {
	if toolUsed {
		return "tool_calls"
	}
	switch strings.ToUpper(reason) {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "content_filter"
	default:
		return "stop"
	}
}

func geminiUsageAsOpenAI(raw any) map[string]any {
	usage, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	prompt := intValue(usage["promptTokenCount"])
	completion := intValue(usage["candidatesTokenCount"]) + intValue(usage["thoughtsTokenCount"])
	if prompt == 0 && completion == 0 {
		return nil
	}
	result := map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion}
	if cached := intValue(usage["cachedContentTokenCount"]); cached > 0 {
		result["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
	}
	return result
}

// geminiMessageAsOpenAIChat converts a full Gemini generateContent response to
// an OpenAI chat.completion object.
func geminiMessageAsOpenAIChat(response map[string]any, requestedModel string) map[string]any {
	candidates, _ := response["candidates"].([]any)
	text, calls, finish := geminiCandidateContent(candidates)
	message := map[string]any{"role": "assistant", "content": text}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	result := map[string]any{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   firstNonEmpty(requestedModel, stringValue(response["modelVersion"])),
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": geminiFinishReasonAsOpenAI(finish, len(calls) > 0)}},
	}
	if usage := geminiUsageAsOpenAI(response["usageMetadata"]); usage != nil {
		result["usage"] = usage
	}
	return result
}

// streamGeminiAsOpenAIChat converts a Gemini streamGenerateContent SSE stream
// into OpenAI chat.completion.chunk SSE.
func streamGeminiAsOpenAIChat(c *gin.Context, body io.Reader, platform, requestedModel string) service.Usage {
	extractor := newUsageExtractor(platform, true)
	emittedRole := false
	toolIndex := 0
	var lastUsage map[string]any
	finish := "stop"
	readSSE(body, func(event map[string]any) bool {
		encoded, _ := json.Marshal(event)
		extractor.feedJSON(encoded)
		if usage := geminiUsageAsOpenAI(event["usageMetadata"]); usage != nil {
			lastUsage = usage
		}
		candidates, _ := event["candidates"].([]any)
		text, calls, reason := geminiCandidateContent(candidates)
		if reason != "" {
			finish = geminiFinishReasonAsOpenAI(reason, len(calls) > 0)
		}
		if text != "" {
			delta := map[string]any{"content": text}
			if !emittedRole {
				delta["role"] = "assistant"
				emittedRole = true
			}
			if !writeOpenAISSE(c, geminiChatChunk(requestedModel, delta, nil, nil)) {
				return false
			}
		}
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			function, _ := call["function"].(map[string]any)
			delta := map[string]any{"tool_calls": []any{map[string]any{
				"index": toolIndex, "id": call["id"], "type": "function",
				"function": map[string]any{"name": stringValue(function["name"]), "arguments": stringValue(function["arguments"])},
			}}}
			if !emittedRole {
				delta["role"] = "assistant"
				emittedRole = true
			}
			toolIndex++
			finish = "tool_calls"
			if !writeOpenAISSE(c, geminiChatChunk(requestedModel, delta, nil, nil)) {
				return false
			}
		}
		return true
	})
	writeOpenAISSE(c, geminiChatChunk(requestedModel, map[string]any{}, &finish, lastUsage))
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	extractor.finish()
	return extractor.usage()
}

func geminiChatChunk(requestedModel string, delta map[string]any, finish *string, usage map[string]any) map[string]any {
	chunk := map[string]any{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   requestedModel,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return chunk
}
