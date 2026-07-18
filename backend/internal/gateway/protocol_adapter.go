package gateway

// This file contains the intentionally small protocol bridge used when a
// caller's wire protocol differs from the account group's upstream protocol.
// It is not a model emulator: it preserves the provider model selected by the
// API-key group, and only translates the Messages / Responses envelopes.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/service"

	"github.com/gin-gonic/gin"
)

type responseAdapter uint8

const (
	adapterNone responseAdapter = iota
	adapterOpenAIResponsesToAnthropic
	adapterAnthropicToOpenAIResponses
	adapterAnthropicToOpenAIChat
	adapterGeminiToOpenAIChat
)

// pipeAdapted keeps accounting tied to the real upstream protocol while
// presenting the protocol the client asked for. Plain same-protocol calls keep
// the zero-copy relay path in pipe.
func (g *Gateway) pipeAdapted(c *gin.Context, resp *http.Response, platform string, image bool, adapter responseAdapter, requestedModel string) (service.Usage, bool) {
	if adapter == adapterNone {
		return g.pipe(c, resp, platform, image)
	}

	isStream := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if isStream {
		c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.WriteHeader(resp.StatusCode)
		switch adapter {
		case adapterOpenAIResponsesToAnthropic:
			return streamOpenAIResponsesAsAnthropic(c, resp.Body, platform, requestedModel), true
		case adapterAnthropicToOpenAIResponses:
			return streamAnthropicAsOpenAIResponses(c, resp.Body, platform, requestedModel), true
		case adapterAnthropicToOpenAIChat:
			return streamAnthropicAsOpenAIChat(c, resp.Body, platform, requestedModel), true
		case adapterGeminiToOpenAIChat:
			return streamGeminiAsOpenAIChat(c, resp.Body, platform, requestedModel), true
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	extractor := newUsageExtractor(platform, false, image)
	if err == nil {
		extractor.feedJSON(body)
	}
	if err != nil {
		writeAdapterJSON(c, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "read upstream response failed"}})
		return extractor.usage(), false
	}

	var source map[string]any
	if err := json.Unmarshal(body, &source); err != nil {
		writeAdapterJSON(c, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "upstream returned invalid JSON"}})
		return extractor.usage(), false
	}
	var target any
	switch adapter {
	case adapterOpenAIResponsesToAnthropic:
		target = openAIResponseAsAnthropic(source, requestedModel)
	case adapterAnthropicToOpenAIResponses:
		target = anthropicMessageAsOpenAIResponse(source, requestedModel)
	case adapterAnthropicToOpenAIChat:
		target = anthropicMessageAsOpenAIChat(source, requestedModel)
	case adapterGeminiToOpenAIChat:
		target = geminiMessageAsOpenAIChat(source, requestedModel)
	}
	writeAdapterJSON(c, resp.StatusCode, target)
	return extractor.usage(), false
}

func writeAdapterJSON(c *gin.Context, status int, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":{"message":"response conversion failed"}}`)
		status = http.StatusBadGateway
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(encoded)
}

func writeAdapterSSE(c *gin.Context, event string, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
		return false
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func writeOpenAISSE(c *gin.Context, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", encoded); err != nil {
		return false
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func readSSE(body io.Reader, fn func(map[string]any) bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 32<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(payload), &event) == nil && !fn(event) {
			return
		}
	}
}

// anthropicMessagesToOpenAIResponses translates a public Anthropic request to
// an OpenAI Responses request. Claude Code primarily uses text, tool_use and
// tool_result blocks; unsupported media blocks are skipped rather than being
// accidentally relayed as malformed JSON.
func anthropicMessagesToOpenAIResponses(body []byte) (map[string]any, string, bool, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", false, err
	}
	modelName := strings.TrimSpace(stringValue(request["model"]))
	if modelName == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}

	input := make([]any, 0)
	if system := anthropicSystemText(request["system"]); system != "" {
		input = append(input, responseMessage("developer", system))
	}
	if messages, ok := request["messages"].([]any); ok {
		for _, raw := range messages {
			message, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(stringValue(message["role"])))
			if role != "assistant" {
				role = "user"
			}
			input = append(input, anthropicMessageToResponsesInput(role, message["content"])...)
		}
	}
	if len(input) == 0 {
		return nil, "", false, fmt.Errorf("messages is required")
	}

	converted := map[string]any{
		"model":  modelName,
		"input":  input,
		"stream": boolValue(request["stream"]),
		"store":  false,
	}
	if max, ok := request["max_tokens"]; ok {
		converted["max_output_tokens"] = max
	}
	if temperature, ok := request["temperature"]; ok {
		converted["temperature"] = temperature
	}
	if topP, ok := request["top_p"]; ok {
		converted["top_p"] = topP
	}
	if tools := anthropicToolsToResponses(request["tools"]); len(tools) > 0 {
		converted["tools"] = tools
	}
	if choice := anthropicToolChoiceToResponses(request["tool_choice"]); choice != nil {
		converted["tool_choice"] = choice
	}
	return converted, modelName, boolValue(request["stream"]), nil
}

func anthropicSystemText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var text strings.Builder
		for _, raw := range v {
			part, _ := raw.(map[string]any)
			if strings.EqualFold(stringValue(part["type"]), "text") {
				text.WriteString(contentText(part["text"]))
			}
		}
		return text.String()
	default:
		return ""
	}
}

func anthropicMessageToResponsesInput(role string, content any) []any {
	parts, ok := content.([]any)
	if !ok {
		if text := contentText(content); text != "" {
			textType := "input_text"
			if role == "assistant" {
				textType = "output_text"
			}
			return []any{map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": textType, "text": text}}}}
		}
		return nil
	}

	messageParts := make([]any, 0, len(parts))
	items := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.ToLower(stringValue(part["type"])) {
		case "text":
			textType := "input_text"
			if role == "assistant" {
				textType = "output_text"
			}
			messageParts = append(messageParts, map[string]any{"type": textType, "text": contentText(part["text"])})
		case "tool_use":
			arguments, _ := json.Marshal(part["input"])
			items = append(items, map[string]any{
				"type": "function_call", "call_id": firstNonEmpty(stringValue(part["id"]), stringValue(part["tool_use_id"])),
				"name": stringValue(part["name"]), "arguments": string(arguments),
			})
		case "tool_result":
			items = append(items, map[string]any{
				"type": "function_call_output", "call_id": firstNonEmpty(stringValue(part["tool_use_id"]), stringValue(part["id"])),
				"output": anthropicToolResultText(part["content"]),
			})
		}
	}
	if len(messageParts) > 0 {
		items = append([]any{map[string]any{"type": "message", "role": role, "content": messageParts}}, items...)
	}
	return items
}

func anthropicToolResultText(value any) string {
	if text := contentText(value); text != "" {
		return text
	}
	if parts, ok := value.([]any); ok {
		var text strings.Builder
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			text.WriteString(contentText(part["text"]))
		}
		return text.String()
	}
	return ""
}

func anthropicToolsToResponses(raw any) []any {
	tools, ok := raw.([]any)
	if !ok {
		return nil
	}
	converted := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || stringValue(tool["name"]) == "" {
			continue
		}
		item := map[string]any{"type": "function", "name": tool["name"]}
		for _, key := range []string{"description", "input_schema"} {
			if value, exists := tool[key]; exists {
				if key == "input_schema" {
					item["parameters"] = value
				} else {
					item[key] = value
				}
			}
		}
		converted = append(converted, item)
	}
	return converted
}

func anthropicToolChoiceToResponses(raw any) any {
	choice, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	switch strings.ToLower(stringValue(choice["type"])) {
	case "tool":
		if name := stringValue(choice["name"]); name != "" {
			return map[string]any{"type": "function", "name": name}
		}
		return "required"
	case "any":
		return "required"
	case "none":
		return "none"
	default:
		return "auto"
	}
}

func openAIResponsesToAnthropic(body []byte) (map[string]any, string, bool, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", false, err
	}
	modelName := strings.TrimSpace(stringValue(request["model"]))
	if modelName == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}
	messages, system := responsesInputToAnthropicMessages(request["input"])
	if instructions := strings.TrimSpace(stringValue(request["instructions"])); instructions != "" {
		system = strings.TrimSpace(system + "\n" + instructions)
	}
	if len(messages) == 0 {
		return nil, "", false, fmt.Errorf("input is required")
	}
	converted := map[string]any{
		"model":      modelName,
		"messages":   messages,
		"stream":     boolValue(request["stream"]),
		"max_tokens": firstPositive(request["max_output_tokens"], request["max_tokens"], 4096),
	}
	if system != "" {
		converted["system"] = system
	}
	if temperature, ok := request["temperature"]; ok {
		converted["temperature"] = temperature
	}
	if topP, ok := request["top_p"]; ok {
		converted["top_p"] = topP
	}
	if tools := responsesToolsToAnthropic(request["tools"]); len(tools) > 0 {
		converted["tools"] = tools
	}
	if choice := responsesToolChoiceToAnthropic(request["tool_choice"]); choice != nil {
		converted["tool_choice"] = choice
	}
	return converted, modelName, boolValue(request["stream"]), nil
}

func openAIChatToAnthropic(body []byte) (map[string]any, string, bool, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", false, err
	}
	modelName := strings.TrimSpace(stringValue(request["model"]))
	if modelName == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}
	messages, _ := request["messages"].([]any)
	if len(messages) == 0 {
		return nil, "", false, fmt.Errorf("messages is required")
	}
	responsesLike := map[string]any{
		"model":             modelName,
		"input":             chatMessagesToResponsesInput(messages),
		"stream":            boolValue(request["stream"]),
		"max_output_tokens": firstPositive(request["max_completion_tokens"], request["max_tokens"], 4096),
		"tools":             chatToolsToResponses(request["tools"]),
		"tool_choice":       chatToolChoiceToResponses(request["tool_choice"]),
	}
	if value, ok := request["temperature"]; ok {
		responsesLike["temperature"] = value
	}
	if value, ok := request["top_p"]; ok {
		responsesLike["top_p"] = value
	}
	encoded, _ := json.Marshal(responsesLike)
	return openAIResponsesToAnthropic(encoded)
}

func responsesInputToAnthropicMessages(raw any) ([]any, string) {
	if text, ok := raw.(string); ok {
		return []any{anthropicMessage("user", []any{map[string]any{"type": "text", "text": text}})}, ""
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, ""
	}
	var messages []any
	var system strings.Builder
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		typ := strings.ToLower(stringValue(item["type"]))
		role := strings.ToLower(stringValue(item["role"]))
		// Responses clients such as Chatbox/AI SDK emit easy-input message
		// objects with role + content but omit the optional outer
		// `type:"message"` discriminator.
		if typ == "" && role != "" {
			typ = "message"
		}
		switch typ {
		case "function_call_output":
			appendAnthropicMessage(&messages, "user", map[string]any{"type": "tool_result", "tool_use_id": stringValue(item["call_id"]), "content": contentText(item["output"])})
		case "function_call":
			arguments := decodeJSONValue(stringValue(item["arguments"]))
			appendAnthropicMessage(&messages, "assistant", map[string]any{"type": "tool_use", "id": firstNonEmpty(stringValue(item["call_id"]), stringValue(item["id"])), "name": stringValue(item["name"]), "input": arguments})
		case "message":
			if role == "developer" || role == "system" {
				system.WriteString(responsesContentText(item["content"]))
				continue
			}
			if role != "assistant" {
				role = "user"
			}
			blocks := responsesContentToAnthropic(item["content"])
			for _, block := range blocks {
				appendAnthropicMessage(&messages, role, block)
			}
		}
	}
	return messages, system.String()
}

func appendAnthropicMessage(messages *[]any, role string, block any) {
	if len(*messages) > 0 {
		last, _ := (*messages)[len(*messages)-1].(map[string]any)
		if last != nil && stringValue(last["role"]) == role {
			last["content"] = append(last["content"].([]any), block)
			return
		}
	}
	*messages = append(*messages, anthropicMessage(role, []any{block}))
}

func anthropicMessage(role string, content []any) map[string]any {
	return map[string]any{"role": role, "content": content}
}

func responsesContentToAnthropic(content any) []any {
	parts, ok := content.([]any)
	if !ok {
		if text := contentText(content); text != "" {
			return []any{map[string]any{"type": "text", "text": text}}
		}
		return nil
	}
	blocks := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		switch strings.ToLower(stringValue(part["type"])) {
		case "input_text", "output_text", "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": contentText(part["text"])})
		}
	}
	return blocks
}

func responsesContentText(content any) string {
	var text strings.Builder
	for _, block := range responsesContentToAnthropic(content) {
		part, _ := block.(map[string]any)
		text.WriteString(contentText(part["text"]))
	}
	return text.String()
}

func responsesToolsToAnthropic(raw any) []any {
	tools, ok := raw.([]any)
	if !ok {
		return nil
	}
	converted := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		if strings.ToLower(stringValue(tool["type"])) != "function" || stringValue(tool["name"]) == "" {
			continue
		}
		item := map[string]any{"name": tool["name"]}
		if value, ok := tool["description"]; ok {
			item["description"] = value
		}
		if value, ok := tool["parameters"]; ok {
			item["input_schema"] = value
		}
		converted = append(converted, item)
	}
	return converted
}

func responsesToolChoiceToAnthropic(raw any) any {
	switch value := raw.(type) {
	case string:
		switch strings.ToLower(value) {
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		if strings.ToLower(stringValue(value["type"])) == "function" && stringValue(value["name"]) != "" {
			return map[string]any{"type": "tool", "name": value["name"]}
		}
	}
	return nil
}

func firstPositive(values ...any) int64 {
	for _, value := range values {
		if n := intValue(value); n > 0 {
			return int64(n)
		}
	}
	return 4096
}

func decodeJSONValue(raw string) any {
	if raw == "" {
		return map[string]any{}
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		return value
	}
	return map[string]any{}
}

func openAIResponseAsAnthropic(response map[string]any, requestedModel string) map[string]any {
	content, toolUsed := openAIResponseContent(response)
	usage := openAIUsageAsAnthropic(response["usage"])
	return map[string]any{
		"id":            firstNonEmpty(stringValue(response["id"]), "msg_dengdeng"),
		"type":          "message",
		"role":          "assistant",
		"model":         firstNonEmpty(requestedModel, stringValue(response["model"])),
		"content":       content,
		"stop_reason":   openAIStopReason(response, toolUsed),
		"stop_sequence": nil,
		"usage":         usage,
	}
}

func openAIResponseContent(response map[string]any) ([]any, bool) {
	var content []any
	toolUsed := false
	output, _ := response["output"].([]any)
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		switch strings.ToLower(stringValue(item["type"])) {
		case "message":
			for _, block := range responsesContentToAnthropic(item["content"]) {
				content = append(content, block)
			}
		case "function_call":
			toolUsed = true
			content = append(content, map[string]any{
				"type": "tool_use", "id": firstNonEmpty(stringValue(item["call_id"]), stringValue(item["id"])),
				"name": stringValue(item["name"]), "input": decodeJSONValue(stringValue(item["arguments"])),
			})
		}
	}
	return content, toolUsed
}

func openAIUsageAsAnthropic(raw any) map[string]any {
	usage, _ := raw.(map[string]any)
	input := intValue(usage["input_tokens"])
	if input == 0 {
		input = intValue(usage["prompt_tokens"])
	}
	output := intValue(usage["output_tokens"])
	if output == 0 {
		output = intValue(usage["completion_tokens"])
	}
	result := map[string]any{"input_tokens": input, "output_tokens": output}
	if details, _ := usage["input_tokens_details"].(map[string]any); details != nil && intValue(details["cached_tokens"]) > 0 {
		result["cache_read_input_tokens"] = intValue(details["cached_tokens"])
	}
	return result
}

func openAIStopReason(response map[string]any, toolUsed bool) string {
	if toolUsed {
		return "tool_use"
	}
	if strings.EqualFold(stringValue(response["status"]), "incomplete") {
		if details, _ := response["incomplete_details"].(map[string]any); strings.Contains(stringValue(details["reason"]), "max") {
			return "max_tokens"
		}
	}
	return "end_turn"
}

func anthropicMessageAsOpenAIResponse(message map[string]any, requestedModel string) map[string]any {
	output := make([]any, 0, 2)
	messageContent := make([]any, 0)
	for _, raw := range anthropicContent(message["content"]) {
		block, _ := raw.(map[string]any)
		switch strings.ToLower(stringValue(block["type"])) {
		case "text":
			messageContent = append(messageContent, map[string]any{"type": "output_text", "text": contentText(block["text"])})
		case "tool_use":
			arguments, _ := json.Marshal(block["input"])
			output = append(output, map[string]any{"type": "function_call", "id": stringValue(block["id"]), "call_id": stringValue(block["id"]), "name": stringValue(block["name"]), "arguments": string(arguments)})
		}
	}
	if len(messageContent) > 0 {
		output = append([]any{map[string]any{"type": "message", "id": firstNonEmpty(stringValue(message["id"]), "msg_dengdeng"), "role": "assistant", "content": messageContent}}, output...)
	}
	return map[string]any{
		"id":         "resp_" + firstNonEmpty(stringValue(message["id"]), "dengdeng"),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"model":      firstNonEmpty(requestedModel, stringValue(message["model"])),
		"status":     "completed",
		"output":     output,
		"usage":      anthropicUsageAsOpenAI(message["usage"]),
	}
}

func anthropicMessageAsOpenAIChat(message map[string]any, requestedModel string) map[string]any {
	response := anthropicMessageAsOpenAIResponse(message, requestedModel)
	content := ""
	var calls []any
	for _, item := range response["output"].([]any) {
		part, _ := item.(map[string]any)
		switch stringValue(part["type"]) {
		case "message":
			content = oauthResponseText(map[string]any{"output": []any{part}})
		case "function_call":
			calls = append(calls, map[string]any{"id": firstNonEmpty(stringValue(part["call_id"]), stringValue(part["id"])), "type": "function", "function": map[string]any{"name": stringValue(part["name"]), "arguments": stringValue(part["arguments"])}})
		}
	}
	messageOut := map[string]any{"role": "assistant", "content": content}
	if len(calls) > 0 {
		messageOut["tool_calls"] = calls
	}
	return map[string]any{
		"id":      "chatcmpl_" + firstNonEmpty(stringValue(message["id"]), "dengdeng"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   response["model"],
		"choices": []any{map[string]any{"index": 0, "message": messageOut, "finish_reason": anthropicStopReasonAsOpenAI(stringValue(message["stop_reason"]))}},
		"usage":   anthropicUsageAsOpenAI(message["usage"]),
	}
}

func anthropicContent(raw any) []any {
	if items, ok := raw.([]any); ok {
		return items
	}
	if text := contentText(raw); text != "" {
		return []any{map[string]any{"type": "text", "text": text}}
	}
	return nil
}

func anthropicUsageAsOpenAI(raw any) map[string]any {
	usage, _ := raw.(map[string]any)
	input, output := intValue(usage["input_tokens"]), intValue(usage["output_tokens"])
	result := map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output, "input_tokens": input, "output_tokens": output}
	if cached := intValue(usage["cache_read_input_tokens"]); cached > 0 {
		result["input_tokens_details"] = map[string]any{"cached_tokens": cached}
		result["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
	}
	return result
}

func anthropicStopReasonAsOpenAI(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func streamOpenAIResponsesAsAnthropic(c *gin.Context, body io.Reader, platform, requestedModel string) service.Usage {
	extractor := newUsageExtractor(platform, true)
	started, blockOpen := false, false
	// Anthropic content block indexes are zero-based. Start below zero so the
	// first generated text or tool block is emitted as index 0.
	blockIndex := -1
	activeTools := map[string]int{}
	// Some Responses upstreams only put completed function arguments on
	// response.output_item.done instead of emitting argument-delta events.
	// Keep the streamed fragment so the final item can supply only what is
	// missing without duplicating a JSON object.
	toolArguments := map[string]string{}
	var lastResponse map[string]any
	start := func(response map[string]any) bool {
		if started {
			return true
		}
		started = true
		lastResponse = response
		return writeAdapterSSE(c, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": firstNonEmpty(mapString(response, "id"), "msg_dengdeng"), "type": "message", "role": "assistant",
			"model": firstNonEmpty(requestedModel, mapString(response, "model")), "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": openAIUsageAsAnthropic(response["usage"]),
		}})
	}
	closeBlock := func() bool {
		if !blockOpen {
			return true
		}
		blockOpen = false
		return writeAdapterSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
	}
	readSSE(body, func(event map[string]any) bool {
		encoded, _ := json.Marshal(event)
		extractor.feedJSON(encoded)
		response, _ := event["response"].(map[string]any)
		if response != nil {
			lastResponse = response
		}
		typ := stringValue(event["type"])
		switch typ {
		case "response.created":
			return start(response)
		case "response.output_item.added":
			item, _ := event["item"].(map[string]any)
			if !start(lastResponse) || item == nil {
				return false
			}
			if stringValue(item["type"]) == "function_call" {
				if !closeBlock() {
					return false
				}
				blockIndex++
				callID := firstNonEmpty(stringValue(item["call_id"]), stringValue(item["id"]))
				activeTools[callID] = blockIndex
				toolArguments[callID] = ""
				blockOpen = true
				return writeAdapterSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "tool_use", "id": callID, "name": stringValue(item["name"]), "input": map[string]any{}}})
			}
		case "response.output_text.delta":
			if !start(lastResponse) {
				return false
			}
			if !blockOpen {
				blockIndex++
				blockOpen = true
				if !writeAdapterSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "text", "text": ""}}) {
					return false
				}
			}
			return writeAdapterSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "text_delta", "text": stringValue(event["delta"])}})
		case "response.function_call_arguments.delta":
			callID := firstNonEmpty(stringValue(event["call_id"]), stringValue(event["item_id"]))
			index, ok := activeTools[callID]
			if !ok {
				return true
			}
			fragment := stringValue(event["delta"])
			toolArguments[callID] += fragment
			return writeAdapterSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": fragment}})
		case "response.output_item.done":
			item, _ := event["item"].(map[string]any)
			if item != nil && stringValue(item["type"]) == "function_call" {
				callID := firstNonEmpty(stringValue(item["call_id"]), stringValue(item["id"]))
				if index, ok := activeTools[callID]; ok {
					complete := stringValue(item["arguments"])
					emitted := toolArguments[callID]
					if complete != "" && complete != emitted {
						// The normal case is no deltas (or a strict prefix). If an
						// upstream sends a non-prefix final payload, retain the
						// already valid streamed JSON rather than corrupting it by
						// appending a second object.
						if emitted == "" || strings.HasPrefix(complete, emitted) {
							fragment := strings.TrimPrefix(complete, emitted)
							if fragment != "" && !writeAdapterSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": fragment}}) {
								return false
							}
						}
					}
					blockOpen = false
					return writeAdapterSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
				}
			}
		case "response.completed":
			if !start(response) || !closeBlock() {
				return false
			}
			if response == nil {
				response = lastResponse
			}
			content, toolUsed := openAIResponseContent(response)
			_ = content
			if !writeAdapterSSE(c, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": openAIStopReason(response, toolUsed), "stop_sequence": nil}, "usage": map[string]any{"output_tokens": intValue(openAIUsageAsAnthropic(response["usage"])["output_tokens"])}}) {
				return false
			}
			return writeAdapterSSE(c, "message_stop", map[string]any{"type": "message_stop"})
		}
		return true
	})
	extractor.finish()
	return extractor.usage()
}

func streamAnthropicAsOpenAIResponses(c *gin.Context, body io.Reader, platform, requestedModel string) service.Usage {
	extractor := newUsageExtractor(platform, true)
	started := false
	responseID := "resp_dengdeng"
	messageID := "msg_dengdeng"
	blocks := map[int]map[string]any{}
	output := make([]any, 0)
	usage := map[string]any{}
	start := func() bool {
		if started {
			return true
		}
		started = true
		return writeOpenAISSE(c, map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "object": "response", "created_at": time.Now().Unix(), "model": requestedModel, "status": "in_progress", "output": []any{}}})
	}
	readSSE(body, func(event map[string]any) bool {
		encoded, _ := json.Marshal(event)
		extractor.feedJSON(encoded)
		typ := stringValue(event["type"])
		switch typ {
		case "message_start":
			message, _ := event["message"].(map[string]any)
			responseID = "resp_" + firstNonEmpty(stringValue(message["id"]), "dengdeng")
			messageID = firstNonEmpty(stringValue(message["id"]), messageID)
			if message != nil {
				usage = anthropicUsageAsOpenAI(message["usage"])
			}
			return start()
		case "content_block_start":
			if !start() {
				return false
			}
			index := int(intValue(event["index"]))
			block, _ := event["content_block"].(map[string]any)
			if stringValue(block["type"]) == "tool_use" {
				item := map[string]any{"type": "function_call", "id": stringValue(block["id"]), "call_id": stringValue(block["id"]), "name": stringValue(block["name"]), "arguments": ""}
				blocks[index] = item
				output = append(output, item)
				return writeOpenAISSE(c, map[string]any{"type": "response.output_item.added", "output_index": len(output) - 1, "item": item})
			}
			item := map[string]any{"type": "message", "id": messageID, "role": "assistant", "content": []any{}}
			part := map[string]any{"type": "output_text", "text": ""}
			item["content"] = append(item["content"].([]any), part)
			blocks[index] = map[string]any{"item": item, "part": part, "output_index": len(output)}
			output = append(output, item)
			if !writeOpenAISSE(c, map[string]any{"type": "response.output_item.added", "output_index": len(output) - 1, "item": item}) {
				return false
			}
			return writeOpenAISSE(c, map[string]any{"type": "response.content_part.added", "item_id": messageID, "output_index": len(output) - 1, "content_index": 0, "part": part})
		case "content_block_delta":
			index := int(intValue(event["index"]))
			delta, _ := event["delta"].(map[string]any)
			state := blocks[index]
			if state == nil {
				return true
			}
			if stringValue(state["type"]) == "function_call" {
				chunk := stringValue(delta["partial_json"])
				state["arguments"] = stringValue(state["arguments"]) + chunk
				return writeOpenAISSE(c, map[string]any{"type": "response.function_call_arguments.delta", "item_id": stringValue(state["id"]), "output_index": outputIndex(output, state), "delta": chunk})
			}
			part, _ := state["part"].(map[string]any)
			chunk := stringValue(delta["text"])
			part["text"] = stringValue(part["text"]) + chunk
			return writeOpenAISSE(c, map[string]any{"type": "response.output_text.delta", "item_id": messageID, "output_index": intValue(state["output_index"]), "content_index": 0, "delta": chunk})
		case "content_block_stop":
			index := int(intValue(event["index"]))
			state := blocks[index]
			if state == nil {
				return true
			}
			if stringValue(state["type"]) == "function_call" {
				return writeOpenAISSE(c, map[string]any{"type": "response.output_item.done", "output_index": outputIndex(output, state), "item": state})
			}
			part, _ := state["part"].(map[string]any)
			if !writeOpenAISSE(c, map[string]any{"type": "response.output_text.done", "item_id": messageID, "output_index": intValue(state["output_index"]), "content_index": 0, "text": stringValue(part["text"])}) {
				return false
			}
			item, _ := state["item"].(map[string]any)
			return writeOpenAISSE(c, map[string]any{"type": "response.output_item.done", "output_index": intValue(state["output_index"]), "item": item})
		case "message_delta":
			usage = anthropicUsageAsOpenAI(event["usage"])
		case "message_stop":
			if !start() {
				return false
			}
			return writeOpenAISSE(c, map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "object": "response", "created_at": time.Now().Unix(), "model": requestedModel, "status": "completed", "output": output, "usage": usage}})
		}
		return true
	})
	extractor.finish()
	return extractor.usage()
}

func outputIndex(output []any, target map[string]any) int {
	for index, raw := range output {
		item, _ := raw.(map[string]any)
		if item != nil && stringValue(item["id"]) != "" && stringValue(item["id"]) == stringValue(target["id"]) {
			return index
		}
	}
	return 0
}

func streamAnthropicAsOpenAIChat(c *gin.Context, body io.Reader, platform, requestedModel string) service.Usage {
	extractor := newUsageExtractor(platform, true)
	chatID := "chatcmpl_dengdeng"
	started := false
	toolIndexes := map[int]int{}
	toolCount := 0
	readSSE(body, func(event map[string]any) bool {
		encoded, _ := json.Marshal(event)
		extractor.feedJSON(encoded)
		typ := stringValue(event["type"])
		if typ == "message_start" {
			message, _ := event["message"].(map[string]any)
			chatID = "chatcmpl_" + firstNonEmpty(stringValue(message["id"]), "dengdeng")
		}
		chunk := func(delta map[string]any, finish any, usage any) map[string]any {
			return map[string]any{"id": chatID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": requestedModel, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": usage}
		}
		switch typ {
		case "message_start":
			started = true
			return writeOpenAISSE(c, chunk(map[string]any{"role": "assistant"}, nil, nil))
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			if stringValue(block["type"]) == "tool_use" {
				toolIndexes[int(intValue(event["index"]))] = toolCount
				toolCount++
				delta := map[string]any{"tool_calls": []any{map[string]any{"index": toolCount - 1, "id": stringValue(block["id"]), "type": "function", "function": map[string]any{"name": stringValue(block["name"]), "arguments": ""}}}}
				return writeOpenAISSE(c, chunk(delta, nil, nil))
			}
		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			if stringValue(delta["type"]) == "input_json_delta" {
				index := toolIndexes[int(intValue(event["index"]))]
				return writeOpenAISSE(c, chunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": stringValue(delta["partial_json"])}}}}, nil, nil))
			}
			return writeOpenAISSE(c, chunk(map[string]any{"content": stringValue(delta["text"])}, nil, nil))
		case "message_delta":
			delta, _ := event["delta"].(map[string]any)
			return writeOpenAISSE(c, chunk(map[string]any{}, anthropicStopReasonAsOpenAI(stringValue(delta["stop_reason"])), anthropicUsageAsOpenAI(event["usage"])))
		case "message_stop":
			if started {
				_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
				if flusher, ok := c.Writer.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
		return true
	})
	extractor.finish()
	return extractor.usage()
}
