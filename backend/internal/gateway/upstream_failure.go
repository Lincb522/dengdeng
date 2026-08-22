package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type upstreamFailureScope uint8

const (
	failureRequest upstreamFailureScope = iota
	failureModel
	failureAccount
)

type upstreamFailureDecision struct {
	Retry bool
	Scope upstreamFailureScope
}

// classifyUpstreamFailure is the single policy point for retry and circuit
// scope. Client payload errors stop immediately; credential failures cool the
// account; model/capability and transient transport failures only cool the
// failing account+model pair.
func classifyUpstreamFailure(status int, body []byte) upstreamFailureDecision {
	marker := strings.ToLower(string(body))
	modelSpecific := containsAny(marker,
		"model_not_found", "unsupported model", "model is not supported",
		"model not supported", "model is not available", "model unavailable",
		"not supported when using codex", "does not support image",
		"does not support this model", "unsupported endpoint",
		"capability is not available", "capability not supported",
	)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusPaymentRequired:
		return upstreamFailureDecision{Retry: true, Scope: failureAccount}
	case status == http.StatusForbidden:
		if modelSpecific {
			return upstreamFailureDecision{Retry: true, Scope: failureModel}
		}
		return upstreamFailureDecision{Retry: true, Scope: failureAccount}
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return upstreamFailureDecision{Retry: modelSpecific, Scope: failureModel}
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed ||
		status == http.StatusRequestTimeout || status == http.StatusConflict ||
		status == http.StatusRequestEntityTooLarge || status == http.StatusTooEarly:
		return upstreamFailureDecision{Retry: true, Scope: failureModel}
	case status == http.StatusTooManyRequests:
		return upstreamFailureDecision{Retry: true, Scope: failureAccount}
	case status >= http.StatusInternalServerError:
		return upstreamFailureDecision{Retry: true, Scope: failureModel}
	default:
		return upstreamFailureDecision{Scope: failureRequest}
	}
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// preflightSuccessfulJSON detects gateways that encode overload/rate-limit
// failures inside HTTP 200 JSON. It buffers only unary JSON responses, restores
// the body for the normal adapter, and converts known errors into retryable
// statuses before any bytes reach the client.
func preflightSuccessfulResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return preflightSuccessfulSSE(resp)
	}
	if !strings.Contains(contentType, "json") && contentType != "" {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"upstream response is too large"}}`), nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", stringInt64(int64(len(body))))
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return resp, nil
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || payload["error"] == nil {
		return resp, nil
	}
	marker := strings.ToLower(string(body))
	status := 0
	switch {
	case containsAny(marker, "rate_limit", "too many requests", "quota exceeded"):
		status = http.StatusTooManyRequests
	case containsAny(marker, "server_is_overloaded", "overloaded", "at capacity", "temporarily unavailable"):
		status = http.StatusServiceUnavailable
	}
	if status == 0 {
		return resp, nil
	}
	resp.StatusCode = status
	resp.Status = http.StatusText(status)
	return resp, nil
}

func preflightSuccessfulJSON(resp *http.Response) (*http.Response, error) {
	return preflightSuccessfulResponse(resp)
}

func preflightSuccessfulSSE(resp *http.Response) (*http.Response, error) {
	body := resp.Body
	reader := bufio.NewReader(body)
	var prefix bytes.Buffer
	for prefix.Len() <= 4<<20 {
		line, readErr := reader.ReadString('\n')
		prefix.WriteString(line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload == "[DONE]" {
				body.Close()
				return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"upstream stream ended before producing output","code":"empty_stream"}}`), nil
			}
			if payload != "" && json.Valid([]byte(payload)) {
				var event map[string]any
				_ = json.Unmarshal([]byte(payload), &event)
				marker := strings.ToLower(payload)
				typ, _ := event["type"].(string)
				if event["error"] != nil || typ == "error" || typ == "response.failed" {
					status := http.StatusBadGateway
					switch {
					case containsAny(marker, "rate_limit", "too many requests", "quota exceeded"):
						status = http.StatusTooManyRequests
					case containsAny(marker, "unauthorized", "invalid_auth"):
						status = http.StatusUnauthorized
					case containsAny(marker, "forbidden", "permission"):
						status = http.StatusForbidden
					case containsAny(marker, "overloaded", "at capacity", "temporarily unavailable"):
						status = http.StatusServiceUnavailable
					}
					body.Close()
					return oauthJSONResponse(status, payload), nil
				}
				if successfulSSEHasOutput(typ, event) {
					resp.Body = &oauthReplayBody{Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader), Closer: body}
					resp.ContentLength = -1
					resp.Header.Del("Content-Length")
					return resp, nil
				}
				if typ == "response.completed" || typ == "message_stop" {
					body.Close()
					return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"upstream completed without producing output","code":"empty_stream"}}`), nil
				}
			}
		}
		if readErr != nil {
			body.Close()
			return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"upstream stream ended before producing output","code":"empty_stream"}}`), nil
		}
	}
	body.Close()
	return oauthJSONResponse(http.StatusBadGateway, `{"error":{"message":"upstream stream prelude is too large","code":"invalid_stream"}}`), nil
}

func successfulSSEHasOutput(typ string, event map[string]any) bool {
	if oauthSSEHasOutput(typ, event) {
		return true
	}
	if typ == "content_block_delta" {
		if delta, _ := event["delta"].(map[string]any); delta != nil {
			return delta["text"] != nil || delta["partial_json"] != nil || delta["thinking"] != nil
		}
	}
	if typ == "response.completed" || typ == "response.incomplete" {
		if response, _ := event["response"].(map[string]any); response != nil {
			if output, ok := response["output"].([]any); ok && len(output) > 0 {
				return true
			}
		}
	}
	// Gemini streaming payloads carry generated content under candidates.
	if candidates, ok := event["candidates"].([]any); ok && len(candidates) > 0 {
		return true
	}
	// OpenAI Chat Completions streaming chunks carry visible text/tool calls in
	// choices[].delta rather than a top-level event type.
	if choices, ok := event["choices"].([]any); ok {
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if delta != nil && (delta["content"] != nil || delta["tool_calls"] != nil || delta["function_call"] != nil) {
				return true
			}
		}
	}
	return false
}

func stringInt64(value int64) string {
	// Avoid fmt on the response hot path.
	return strconv.FormatInt(value, 10)
}
