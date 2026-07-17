package gateway

// This file adapts the public OpenAI-compatible endpoints to the OpenAI
// OAuth/Codex Responses channel. API keys continue to use api.openai.com
// unchanged; OAuth credentials must never be sent to that API Platform route.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
)

const (
	defaultOpenAIOAuthResponses = "https://chatgpt.com/backend-api/codex/responses"

	// OpenAI OAuth credentials are issued to the Codex client and are accepted
	// by its dedicated ChatGPT Responses endpoint. That endpoint requires the
	// Codex protocol identity headers; a relay-specific User-Agent results in an
	// HTML challenge instead of a Responses stream.
	openAIOAuthOriginator = "codex_cli_rs"
	openAIOAuthVersion    = "0.144.1"
	openAIOAuthUserAgent  = "codex_cli_rs/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color"
)

var openAIOAuthUnsupportedFields = []string{
	"max_output_tokens", "max_completion_tokens", "max_tokens",
	"temperature", "top_p", "frequency_penalty", "presence_penalty",
	"user", "metadata", "prompt_cache_retention", "safety_identifier",
	"stream_options",
}

// forwardOpenAIOAuth is intentionally kept separate from forward: OAuth uses
// the Codex Responses protocol while the public gateway exposes Chat
// Completions, Responses and Images protocol shapes.
func (g *Gateway) forwardOpenAIOAuth(c *gin.Context, acc *model.UpstreamAccount, req relayRequest) (*http.Response, error) {
	switch req.Path {
	case "/v1/responses":
		body, clientStream, err := normalizeOAuthResponsesRequest(req.Body)
		if err != nil {
			return nil, err
		}
		upstream, err := g.doOpenAIOAuth(c, acc, body)
		if upstream, err = requireOAuthSSE(upstream, err); err != nil || upstream.StatusCode >= http.StatusBadRequest {
			return upstream, err
		}
		if clientStream {
			return upstream, nil
		}
		return bufferOAuthResponses(upstream)

	case "/v1/chat/completions":
		body, clientStream, requestedModel, err := chatCompletionsToOAuthResponses(req.Body)
		if err != nil {
			return nil, err
		}
		upstream, err := g.doOpenAIOAuth(c, acc, body)
		if upstream, err = requireOAuthSSE(upstream, err); err != nil || upstream.StatusCode >= http.StatusBadRequest {
			return upstream, err
		}
		if clientStream {
			return streamOAuthResponsesAsChat(upstream, requestedModel), nil
		}
		return bufferOAuthResponsesAsChat(upstream, requestedModel)

	case "/v1/images/generations":
		body, responseFormat, err := imageGenerationToOAuthResponses(req.Body)
		if err != nil {
			return nil, err
		}
		upstream, err := g.doOpenAIOAuth(c, acc, body)
		if upstream, err = requireOAuthSSE(upstream, err); err != nil || upstream.StatusCode >= http.StatusBadRequest {
			return upstream, err
		}
		return bufferOAuthResponsesAsImages(upstream, responseFormat)

	case "/v1/images/edits":
		// The public edits endpoint is multipart. Keeping binary uploads out of
		// the JSON Responses channel avoids silently dropping masks/images.
		return oauthJSONResponse(http.StatusBadRequest, `{"error":{"message":"OpenAI OAuth image edits are not supported; use an API key account for /v1/images/edits"}}`), nil
	default:
		return nil, fmt.Errorf("OpenAI OAuth does not support upstream path %s", req.Path)
	}
}

func (g *Gateway) doOpenAIOAuth(c *gin.Context, acc *model.UpstreamAccount, body []byte) (*http.Response, error) {
	token, err := g.oauth.AccessToken(c.Request.Context(), acc)
	if err != nil {
		return nil, fmt.Errorf("oauth token: %w", err)
	}
	upstream, err := g.doOpenAIOAuthRequest(c, acc, token, body)
	if err != nil || upstream == nil || upstream.StatusCode != http.StatusUnauthorized {
		return upstream, err
	}

	// A valid-looking JWT may be revoked before its recorded expiry. Refresh
	// once and replay this idempotent relay attempt with the new bearer token.
	// Do not retry repeatedly: refresh grants are commonly one-time tokens.
	upstream.Body.Close()
	token, err = g.oauth.Refresh(c.Request.Context(), acc)
	if err != nil {
		return oauthJSONResponse(http.StatusUnauthorized, `{"error":{"message":"OpenAI OAuth session was rejected and could not be refreshed. Sign in again or import a fresh OAuth session."}}`), nil
	}
	return g.doOpenAIOAuthRequest(c, acc, token, body)
}

func (g *Gateway) doOpenAIOAuthRequest(c *gin.Context, acc *model.UpstreamAccount, token string, body []byte) (*http.Response, error) {
	upReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, openAIOAuthResponsesURL(acc.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Accept", "text/event-stream")
	upReq.Header.Set("OpenAI-Beta", "responses=experimental")
	applyOpenAIOAuthIdentityHeaders(upReq.Header)
	if language := c.GetHeader("Accept-Language"); language != "" {
		upReq.Header.Set("Accept-Language", language)
	}
	if acc.AccountID != "" {
		upReq.Header.Set("chatgpt-account-id", acc.AccountID)
	}
	client, err := g.clientFor(acc)
	if err != nil {
		return nil, err
	}
	return client.Do(upReq)
}

func applyOpenAIOAuthIdentityHeaders(headers http.Header) {
	headers.Set("Originator", openAIOAuthOriginator)
	headers.Set("Version", openAIOAuthVersion)
	headers.Set("User-Agent", openAIOAuthUserAgent)
}

// requireOAuthSSE turns successful-looking HTML/JSON challenge pages into a
// clear gateway error. The Codex Responses endpoint always streams SSE for the
// request shapes above; treating an HTML interstitial as a successful stream
// otherwise leaves clients with an empty reply and hides the actual issue.
func requireOAuthSSE(upstream *http.Response, err error) (*http.Response, error) {
	if err != nil || upstream == nil {
		return upstream, err
	}
	contentType := strings.ToLower(upstream.Header.Get("Content-Type"))
	if upstream.StatusCode >= http.StatusBadRequest {
		// An HTML page cannot be interpreted by OpenAI-compatible clients and
		// usually represents an edge/access page rather than an API error.
		if strings.Contains(contentType, "text/html") {
			upstream.Body.Close()
			return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OpenAI OAuth upstream denied this server request and returned an HTML access page. Verify that the server has authorized access to ChatGPT."}}`), nil
		}
		return upstream, nil
	}
	if strings.Contains(contentType, "text/event-stream") {
		return upstream, nil
	}
	// Some HTTPS proxies preserve the streaming body but strip Content-Type.
	// Do not turn a healthy Codex stream into a 502 solely because of that
	// missing header. Peek only the first frame, accept the canonical SSE
	// prefixes, and put the buffered bytes back in front of the caller.
	if contentType == "" && upstream.Body != nil {
		body := upstream.Body
		reader := bufio.NewReader(body)
		prefix, _ := reader.Peek(256)
		if looksLikeOAuthSSE(prefix) {
			upstream.Body = &oauthBufferedBody{Reader: reader, Closer: body}
			// The body has been verified as an SSE stream. Preserve that fact for
			// downstream protocol adapters; otherwise they treat the stream as a
			// JSON response when a proxy stripped Content-Type.
			upstream.Header.Set("Content-Type", "text/event-stream")
			return upstream, nil
		}
	}
	upstream.Body.Close()
	return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OpenAI OAuth upstream returned a non-stream response. Verify the upstream account and that this server has authorized network access to ChatGPT."}}`), nil
}

type oauthBufferedBody struct {
	*bufio.Reader
	io.Closer
}

func looksLikeOAuthSSE(prefix []byte) bool {
	prefix = bytes.TrimSpace(prefix)
	return bytes.HasPrefix(prefix, []byte("event:")) || bytes.HasPrefix(prefix, []byte("data:"))
}

func openAIOAuthResponsesURL(base string) string {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		return defaultOpenAIOAuthResponses
	}
	if strings.Contains(base, "/backend-api/codex/responses") || strings.HasSuffix(base, "/responses") {
		return base
	}
	return base + "/backend-api/codex/responses"
}

// normalizeOAuthResponsesRequest makes a public Responses request acceptable
// to the OAuth upstream. It always requests upstream SSE; non-stream clients
// are buffered and returned as an ordinary JSON response below.
func normalizeOAuthResponsesRequest(body []byte) ([]byte, bool, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, false, err
	}
	clientStream := boolValue(request["stream"])
	for _, field := range openAIOAuthUnsupportedFields {
		delete(request, field)
	}
	if input, ok := request["input"].(string); ok {
		request["input"] = []any{responseMessage("user", input)}
	}
	if input, ok := request["input"].([]any); ok {
		request["input"] = normalizeOAuthInput(input)
	}
	request["store"] = false
	request["stream"] = true
	encoded, err := json.Marshal(request)
	return encoded, clientStream, err
}

func chatCompletionsToOAuthResponses(body []byte) ([]byte, bool, string, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, false, "", err
	}
	modelName, _ := request["model"].(string)
	if strings.TrimSpace(modelName) == "" {
		return nil, false, "", fmt.Errorf("model is required")
	}
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, false, "", fmt.Errorf("messages is required")
	}

	responses := map[string]any{
		"model":  modelName,
		"input":  chatMessagesToResponsesInput(messages),
		"store":  false,
		"stream": true,
	}
	if tools := chatToolsToResponses(request["tools"]); len(tools) > 0 {
		responses["tools"] = tools
	}
	if choice := chatToolChoiceToResponses(request["tool_choice"]); choice != nil {
		responses["tool_choice"] = choice
	}
	if responseFormat, ok := request["response_format"].(map[string]any); ok {
		if kind, _ := responseFormat["type"].(string); kind == "json_object" {
			responses["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
		}
	}
	encoded, err := json.Marshal(responses)
	return encoded, boolValue(request["stream"]), modelName, err
}

func chatMessagesToResponsesInput(messages []any) []any {
	input := make([]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := message["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "system" {
			role = "developer"
		}
		if role == "tool" {
			callID, _ := message["tool_call_id"].(string)
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID,
				"output": contentText(message["content"]),
			})
			continue
		}
		if role == "" {
			role = "user"
		}
		if role == "assistant" {
			// In Responses conversation history, assistant turns are output
			// items. The Codex OAuth endpoint rejects input_text in an
			// assistant message and accepts output_text (or refusal) instead.
			// Do not manufacture an empty assistant message for a tool-only
			// turn; its function_call items below fully represent that turn.
			if hasChatContent(message["content"]) {
				input = append(input, map[string]any{
					"type": "message", "role": role, "content": chatAssistantContentToResponses(message["content"]),
				})
			}
			if calls, ok := message["tool_calls"].([]any); ok {
				for _, rawCall := range calls {
					call, ok := rawCall.(map[string]any)
					if !ok {
						continue
					}
					function, _ := call["function"].(map[string]any)
					name, _ := function["name"].(string)
					arguments, _ := function["arguments"].(string)
					callID, _ := call["id"].(string)
					input = append(input, map[string]any{"type": "function_call", "call_id": callID, "name": name, "arguments": arguments})
				}
			}
			continue
		}
		input = append(input, map[string]any{
			"type": "message", "role": role, "content": chatContentToResponses(message["content"]),
		})
	}
	return input
}

func chatContentToResponses(content any) []any {
	return chatContentToResponsesWithType(content, "input_text")
}

func chatAssistantContentToResponses(content any) []any {
	return chatContentToResponsesWithType(content, "output_text")
}

func chatContentToResponsesWithType(content any, textType string) []any {
	switch v := content.(type) {
	case string:
		return []any{map[string]any{"type": textType, "text": v}}
	case []any:
		parts := make([]any, 0, len(v))
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch strings.ToLower(stringValue(part["type"])) {
			case "text", "input_text", "output_text":
				parts = append(parts, map[string]any{"type": textType, "text": contentText(part["text"])})
			case "image_url", "input_image":
				if textType != "input_text" {
					continue
				}
				imageURL := part["image_url"]
				if nested, ok := imageURL.(map[string]any); ok {
					imageURL = nested["url"]
				}
				if url := stringValue(imageURL); url != "" {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
				}
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return []any{map[string]any{"type": textType, "text": contentText(content)}}
}

func hasChatContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return false
	case string:
		return value != ""
	case []any:
		return len(value) > 0
	default:
		return contentText(value) != ""
	}
}

func chatToolsToResponses(raw any) []any {
	tools, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || stringValue(tool["type"]) != "function" {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok || stringValue(function["name"]) == "" {
			continue
		}
		converted := map[string]any{"type": "function", "name": function["name"]}
		for _, key := range []string{"description", "parameters", "strict"} {
			if value, exists := function[key]; exists {
				converted[key] = value
			}
		}
		result = append(result, converted)
	}
	return result
}

func chatToolChoiceToResponses(raw any) any {
	choice, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	if stringValue(choice["type"]) != "function" {
		return raw
	}
	function, _ := choice["function"].(map[string]any)
	if name := stringValue(function["name"]); name != "" {
		return map[string]any{"type": "function", "name": name}
	}
	return "auto"
}

func normalizeOAuthInput(input []any) []any {
	for _, raw := range input {
		message, ok := raw.(map[string]any)
		if !ok || stringValue(message["type"]) != "message" {
			continue
		}
		if strings.EqualFold(stringValue(message["role"]), "system") {
			message["role"] = "developer"
		}
		message["content"] = normalizeOAuthMessageContent(message["content"], stringValue(message["role"]))
	}
	return input
}

func normalizeOAuthMessageContent(content any, role string) any {
	textType := "input_text"
	if strings.EqualFold(role, "assistant") {
		textType = "output_text"
	}
	if text, ok := content.(string); ok {
		return []any{map[string]any{"type": textType, "text": text}}
	}
	parts, ok := content.([]any)
	if !ok {
		return content
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.ToLower(stringValue(part["type"])) {
		case "text", "input_text", "output_text":
			part["type"] = textType
		}
	}
	return parts
}

func responseMessage(role, text string) map[string]any {
	return map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": "input_text", "text": text}}}
}

func imageGenerationToOAuthResponses(body []byte) ([]byte, string, error) {
	request, err := decodeJSONObject(body)
	if err != nil {
		return nil, "", err
	}
	prompt := strings.TrimSpace(stringValue(request["prompt"]))
	if prompt == "" {
		return nil, "", fmt.Errorf("prompt is required")
	}
	// ChatGPT OAuth accounts use the Codex Responses host rather than the API
	// Platform Images API. That host rejects public image model names such as
	// gpt-image-2 in the tool payload. Keep the public model for local routing
	// and billing, but let Codex select the image capability available to the
	// signed-in ChatGPT account.
	tool := map[string]any{"type": "image_generation", "action": "generate"}
	for _, field := range []string{"size", "quality", "background", "output_format", "output_compression", "moderation", "style"} {
		if value, exists := request[field]; exists {
			tool[field] = value
		}
	}
	// Responses' image_generation tool always produces one image. Unlike the
	// public Images API it rejects an `n` field (tools[0].n), even when n is 1.
	// Do not forward it or a normal Studio request fails before generation.
	// The Responses host model orchestrates the image_generation tool. The
	// tool deliberately carries no public API Platform image-model override.
	responses := map[string]any{
		"model":       "gpt-5.4-mini",
		"input":       []any{responseMessage("user", prompt)},
		"tools":       []any{tool},
		"tool_choice": map[string]any{"type": "image_generation"},
		"store":       false,
		"stream":      true,
	}
	encoded, err := json.Marshal(responses)
	return encoded, stringValue(request["response_format"]), err
}

func bufferOAuthResponses(upstream *http.Response) (*http.Response, error) {
	body, err := readOAuthSSE(upstream)
	if err != nil {
		return nil, err
	}
	completed := completedOAuthResponse(body)
	if completed == nil {
		return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OAuth upstream did not return response.completed"}}`), nil
	}
	encoded, err := json.Marshal(completed)
	if err != nil {
		return nil, err
	}
	return oauthJSONResponse(http.StatusOK, string(encoded)), nil
}

func bufferOAuthResponsesAsChat(upstream *http.Response, requestedModel string) (*http.Response, error) {
	body, err := readOAuthSSE(upstream)
	if err != nil {
		return nil, err
	}
	completed := completedOAuthResponse(body)
	if completed == nil {
		return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OAuth upstream did not return response.completed"}}`), nil
	}
	encoded, err := json.Marshal(oauthResponseAsChatCompletion(completed, requestedModel))
	if err != nil {
		return nil, err
	}
	return oauthJSONResponse(http.StatusOK, string(encoded)), nil
}

func bufferOAuthResponsesAsImages(upstream *http.Response, responseFormat string) (*http.Response, error) {
	body, err := readOAuthSSE(upstream)
	if err != nil {
		return nil, err
	}
	completed := completedOAuthResponse(body)
	if completed == nil {
		return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OAuth upstream did not return response.completed"}}`), nil
	}
	data := make([]any, 0, 1)
	created := intValue(completed["created_at"])
	if created == 0 {
		created = time.Now().Unix()
	}
	if output, ok := completed["output"].([]any); ok {
		for _, raw := range output {
			item, ok := raw.(map[string]any)
			if !ok || stringValue(item["type"]) != "image_generation_call" {
				continue
			}
			result := stringValue(item["result"])
			if result == "" {
				continue
			}
			image := map[string]any{"b64_json": result}
			if strings.EqualFold(responseFormat, "url") {
				image["url"] = "data:image/png;base64," + result
			}
			if prompt := stringValue(item["revised_prompt"]); prompt != "" {
				image["revised_prompt"] = prompt
			}
			data = append(data, image)
		}
	}
	if len(data) == 0 {
		return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"OAuth upstream completed without an image result"}}`), nil
	}
	encoded, err := json.Marshal(map[string]any{"created": created, "data": data})
	if err != nil {
		return nil, err
	}
	return oauthJSONResponse(http.StatusOK, string(encoded)), nil
}

func streamOAuthResponsesAsChat(upstream *http.Response, requestedModel string) *http.Response {
	reader, writer := io.Pipe()
	go func() {
		defer upstream.Body.Close()
		defer writer.Close()
		scanner := bufio.NewScanner(upstream.Body)
		scanner.Buffer(make([]byte, 32<<10), 4<<20)
		emittedRole := false
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
			if json.Unmarshal([]byte(payload), &event) != nil {
				continue
			}
			switch stringValue(event["type"]) {
			case "response.output_text.delta":
				delta := stringValue(event["delta"])
				if delta == "" {
					continue
				}
				chunkDelta := map[string]any{"content": delta}
				if !emittedRole {
					chunkDelta["role"] = "assistant"
					emittedRole = true
				}
				if !writeOAuthChatSSE(writer, oauthChatChunk(event, requestedModel, chunkDelta, nil, nil)) {
					return
				}
			case "response.reasoning_text.delta":
				delta := stringValue(event["delta"])
				if delta != "" && !writeOAuthChatSSE(writer, oauthChatChunk(event, requestedModel, map[string]any{"reasoning_content": delta}, nil, nil)) {
					return
				}
			case "response.completed":
				response, _ := event["response"].(map[string]any)
				finish := "stop"
				if response != nil {
					finish = oauthChatFinishReason(response)
				}
				if !writeOAuthChatSSE(writer, oauthChatChunk(event, requestedModel, map[string]any{}, &finish, oauthChatUsage(response))) {
					return
				}
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				return
			}
		}
	}()
	headers := upstream.Header.Clone()
	headers.Set("Content-Type", "text/event-stream")
	headers.Del("Content-Length")
	return &http.Response{StatusCode: upstream.StatusCode, Header: headers, Body: reader}
}

func oauthChatChunk(event map[string]any, requestedModel string, delta map[string]any, finish *string, usage map[string]any) map[string]any {
	response, _ := event["response"].(map[string]any)
	modelName := requestedModel
	if response != nil && stringValue(response["model"]) != "" {
		modelName = stringValue(response["model"])
	}
	chunk := map[string]any{
		"id":      firstNonEmpty(stringValue(event["response_id"]), mapString(response, "id"), "chatcmpl-oauth"),
		"object":  "chat.completion.chunk",
		"created": firstNonZero(intValue(event["created_at"]), mapInt(response, "created_at"), time.Now().Unix()),
		"model":   modelName,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return chunk
}

func writeOAuthChatSSE(writer io.Writer, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	return err == nil
}

func oauthResponseAsChatCompletion(response map[string]any, requestedModel string) map[string]any {
	message := map[string]any{"role": "assistant", "content": oauthResponseText(response)}
	if calls := oauthResponseToolCalls(response); len(calls) > 0 {
		message["tool_calls"] = calls
	}
	result := map[string]any{
		"id":      firstNonEmpty(stringValue(response["id"]), "chatcmpl-oauth"),
		"object":  "chat.completion",
		"created": firstNonZero(intValue(response["created_at"]), time.Now().Unix()),
		"model":   firstNonEmpty(stringValue(response["model"]), requestedModel),
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": oauthChatFinishReason(response)}},
	}
	if usage := oauthChatUsage(response); usage != nil {
		result["usage"] = usage
	}
	return result
}

func oauthResponseText(response map[string]any) string {
	var text strings.Builder
	output, _ := response["output"].([]any)
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok || stringValue(item["type"]) != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if ok && (stringValue(part["type"]) == "output_text" || stringValue(part["type"]) == "text") {
				text.WriteString(contentText(part["text"]))
			}
		}
	}
	return text.String()
}

func oauthResponseToolCalls(response map[string]any) []any {
	var calls []any
	output, _ := response["output"].([]any)
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok || stringValue(item["type"]) != "function_call" {
			continue
		}
		calls = append(calls, map[string]any{
			"id": firstNonEmpty(stringValue(item["call_id"]), stringValue(item["id"])), "type": "function",
			"function": map[string]any{"name": stringValue(item["name"]), "arguments": stringValue(item["arguments"])}},
		)
	}
	return calls
}

func oauthChatFinishReason(response map[string]any) string {
	if response == nil || stringValue(response["status"]) != "incomplete" {
		return "stop"
	}
	if details, ok := response["incomplete_details"].(map[string]any); ok && stringValue(details["reason"]) == "max_output_tokens" {
		return "length"
	}
	return "stop"
}

func oauthChatUsage(response map[string]any) map[string]any {
	if response == nil {
		return nil
	}
	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return nil
	}
	prompt, completion := intValue(usage["input_tokens"]), intValue(usage["output_tokens"])
	if prompt == 0 && completion == 0 {
		return nil
	}
	result := map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion}
	if details, ok := usage["input_tokens_details"].(map[string]any); ok && intValue(details["cached_tokens"]) > 0 {
		result["prompt_tokens_details"] = map[string]any{"cached_tokens": intValue(details["cached_tokens"])}
	}
	return result
}

func readOAuthSSE(upstream *http.Response) ([]byte, error) {
	defer upstream.Body.Close()
	return io.ReadAll(io.LimitReader(upstream.Body, maxBodyBytes))
}

func completedOAuthResponse(body []byte) map[string]any {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 32<<10), 4<<20)
	// The Codex upstream deliberately leaves response.output empty in the
	// terminal event. Completed output items arrive immediately before it, so
	// a non-stream client must reconstruct the final response from that event
	// sequence rather than trusting response.completed on its own.
	output := make([]any, 0, 1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if stringValue(event["type"]) == "response.output_item.done" {
			if item, ok := event["item"].(map[string]any); ok {
				output = append(output, item)
			}
			continue
		}
		if stringValue(event["type"]) != "response.completed" {
			continue
		}
		if response, ok := event["response"].(map[string]any); ok {
			if len(output) > 0 {
				response["output"] = output
			}
			return response
		}
	}
	return nil
}

func oauthJSONResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func decodeJSONObject(body []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil || value == nil {
		return nil, fmt.Errorf("invalid JSON body")
	}
	return value, nil
}

func boolValue(value any) bool { valueBool, _ := value.(bool); return valueBool }

func stringValue(value any) string {
	valueString, _ := value.(string)
	return strings.TrimSpace(valueString)
}

func contentText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func intValue(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	case json.Number:
		parsed, _ := number.Int64()
		return parsed
	}
	return 0
}

func mapString(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	return stringValue(value[key])
}

func mapInt(value map[string]any, key string) int64 {
	if value == nil {
		return 0
	}
	return intValue(value[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
