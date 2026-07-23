package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
)

func TestOpenAIOAuthResponsesURL(t *testing.T) {
	if got := openAIOAuthResponsesURL(""); got != defaultOpenAIOAuthResponses {
		t.Fatalf("default URL = %q", got)
	}
	if got := openAIOAuthResponsesURL("https://relay.example"); got != "https://relay.example/backend-api/codex/responses" {
		t.Fatalf("base URL = %q", got)
	}
	if got := openAIOAuthResponsesURL("https://relay.example/custom/responses"); got != "https://relay.example/custom/responses" {
		t.Fatalf("endpoint URL = %q", got)
	}
}

func TestApplyOpenAIOAuthIdentityHeaders(t *testing.T) {
	headers := make(http.Header)
	applyOpenAIOAuthIdentityHeaders(headers)

	if got := headers.Get("Originator"); got != openAIOAuthOriginator {
		t.Fatalf("Originator = %q", got)
	}
	if got := headers.Get("Version"); got != openAIOAuthVersion {
		t.Fatalf("Version = %q", got)
	}
	if got := headers.Get("User-Agent"); got != openAIOAuthUserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestOpenAIOAuthRequestAppliesAgentIdentityFedRAMPHeader(t *testing.T) {
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	extra, err := model.EncodeExtra(map[string]any{"chatgpt_account_is_fedramp": true})
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{
		Platform:  model.PlatformOpenAI,
		AuthType:  model.AuthAgentIdentity,
		BaseURL:   upstream.URL + "/v1/responses",
		AccountID: "account-fedramp",
		Extra:     extra,
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	gateway := &Gateway{client: upstream.Client()}
	response, err := gateway.doOpenAIOAuthRequest(c, account, "AgentAssertion test", []byte(`{}`), "session")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := received.Get("x-openai-fedramp"); got != "true" {
		t.Fatalf("x-openai-fedramp = %q", got)
	}
	if got := received.Get("chatgpt-account-id"); got != account.AccountID {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
}

func TestRequireOAuthSSERejectsHTMLChallenge(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("<!doctype html>challenge")),
	}

	response, err := requireOAuthSSE(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "non-stream response") {
		t.Fatalf("body = %s", body)
	}
}

func TestRequireOAuthSSENormalizesHTMLAccessDenied(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("<!doctype html>access denied")),
	}

	response, err := requireOAuthSSE(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "denied this server request") {
		t.Fatalf("body = %s", body)
	}
}

func TestRequireOAuthSSEKeepsEventStream(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("data:{\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")),
	}

	response, err := requireOAuthSSE(upstream, nil)
	if err != nil || response != upstream {
		t.Fatalf("response=%p upstream=%p err=%v", response, upstream, err)
	}
}

func TestRequireOAuthSSEAcceptsHeaderlessSSE(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("event:response.created\ndata:{\"type\":\"response.created\"}\n\ndata:{\"type\":\"response.completed\",\"response\":{}}\n\n")),
	}
	response, err := requireOAuthSSE(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response != upstream {
		t.Fatal("headerless SSE should be forwarded")
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("headerless SSE content type = %q, want text/event-stream", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "event:response.created") {
		t.Fatalf("SSE bytes were not preserved: %q", body)
	}
}

func TestRequireOAuthSSERejectsEmptySuccessfulStream(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data:{\"type\":\"response.created\"}\n\ndata:[DONE]\n\n")),
	}
	response, err := requireOAuthSSE(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "missing_terminal_event") {
		t.Fatalf("body = %s", body)
	}
}

func TestRequireOAuthSSEConvertsPreOutputRateLimitToRetryableStatus(t *testing.T) {
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data:{\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"limited\"}}\n\n")),
	}
	response, err := requireOAuthSSE(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestNormalizeOAuthResponsesRequest(t *testing.T) {
	body, stream, err := normalizeOAuthResponsesRequest([]byte(`{
        "model":"gpt-5.6-sol", "input":"hello", "stream":false,
        "temperature":0.2, "max_output_tokens":10, "store":true
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if stream {
		t.Fatal("client stream should remain false")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["stream"] != true || got["store"] != false {
		t.Fatalf("OAuth flags = %#v", got)
	}
	if _, exists := got["temperature"]; exists {
		t.Fatal("unsupported temperature was retained")
	}
	input := got["input"].([]any)
	message := input[0].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("input = %#v", input)
	}
	content := message["content"].([]any)
	if content[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("user content = %#v", content)
	}
}

func TestChatCompletionsToOAuthResponses(t *testing.T) {
	body, stream, modelName, err := chatCompletionsToOAuthResponses([]byte(`{
        "model":"gpt-5.6-sol", "stream":true,
        "messages":[
          {"role":"system","content":"be concise"},
          {"role":"user","content":"hello"},
          {"role":"assistant","content":"previous reply"}
        ],
        "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if !stream || modelName != "gpt-5.6-sol" {
		t.Fatalf("stream=%v model=%q", stream, modelName)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["stream"] != true || got["store"] != false {
		t.Fatalf("OAuth flags = %#v", got)
	}
	input := got["input"].([]any)
	if input[0].(map[string]any)["role"] != "developer" {
		t.Fatalf("system role not converted: %#v", input[0])
	}
	if got := input[0].(map[string]any)["content"].([]any)[0].(map[string]any)["type"]; got != "input_text" {
		t.Fatalf("developer content type = %#v", got)
	}
	assistant := input[2].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant input = %#v", assistant)
	}
	if got := assistant["content"].([]any)[0].(map[string]any)["type"]; got != "output_text" {
		t.Fatalf("assistant content type = %#v", got)
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "lookup" || tool["type"] != "function" {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestNormalizeOAuthResponsesAssistantContent(t *testing.T) {
	body, _, err := normalizeOAuthResponsesRequest([]byte(`{
        "model":"gpt-5.6-sol",
        "input":[
          {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
          {"type":"message","role":"assistant","content":[{"type":"input_text","text":"hi"}]}
        ]
    }`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	input := got["input"].([]any)
	if contentType := input[1].(map[string]any)["content"].([]any)[0].(map[string]any)["type"]; contentType != "output_text" {
		t.Fatalf("assistant content type = %#v", contentType)
	}
}

func TestImageGenerationToOAuthResponsesOmitsUnsupportedImageModel(t *testing.T) {
	body, responseFormat, err := imageGenerationToOAuthResponses([]byte(`{
        "model":"gpt-image-2", "prompt":"draw a lantern", "size":"1024x1024", "quality":"high", "n":1, "response_format":"b64_json"
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if responseFormat != "b64_json" {
		t.Fatalf("response format = %q", responseFormat)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt-5.4-mini" {
		t.Fatalf("responses model = %#v", got["model"])
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if _, exists := tool["model"]; exists {
		t.Fatalf("OAuth image tool must not carry a public image model: %#v", tool)
	}
	if _, exists := tool["n"]; exists {
		t.Fatalf("OAuth image tool must not carry Images API n: %#v", tool)
	}
	if tool["size"] != "1024x1024" || tool["quality"] != "high" {
		t.Fatalf("image controls = %#v", tool)
	}
}

func TestBufferOAuthResponsesAsChat(t *testing.T) {
	sse := `event: response.output_item.done
	data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"hello back"}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","created_at":123,"model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2}}}

`
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	response, err := bufferOAuthResponsesAsChat(upstream, "public-model")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["object"] != "chat.completion" || got["model"] != "gpt-5.6-sol" {
		t.Fatalf("completion = %#v", got)
	}
	choice := got["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	if message["content"] != "hello back" {
		t.Fatalf("message = %#v", message)
	}
	usage := got["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(3) || usage["completion_tokens"] != float64(2) {
		t.Fatalf("usage = %#v", usage)
	}
}
