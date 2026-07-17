package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
)

func TestAnthropicMessagesToOpenAIResponses(t *testing.T) {
	converted, name, stream, err := anthropicMessagesToOpenAIResponses([]byte(`{
		"model":"gpt-5.4", "system":"Be concise", "max_tokens":120, "stream":true,
		"tools":[{"name":"weather","description":"lookup","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"weather"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Shanghai weather"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Shanghai"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("convert Anthropic request: %v", err)
	}
	if name != "gpt-5.4" || !stream || converted["max_output_tokens"] != float64(120) {
		t.Fatalf("unexpected request envelope: %#v, model=%q, stream=%v", converted, name, stream)
	}
	input := converted["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input item count = %d, want 4: %#v", len(input), input)
	}
	if role := stringValue(input[0].(map[string]any)["role"]); role != "developer" {
		t.Fatalf("system role = %q, want developer", role)
	}
	if typ := stringValue(input[2].(map[string]any)["type"]); typ != "function_call" {
		t.Fatalf("tool use type = %q, want function_call", typ)
	}
	if typ := stringValue(input[3].(map[string]any)["type"]); typ != "function_call_output" {
		t.Fatalf("tool result type = %q, want function_call_output", typ)
	}
	tools := converted["tools"].([]any)
	if len(tools) != 1 || stringValue(tools[0].(map[string]any)["name"]) != "weather" {
		t.Fatalf("tool conversion failed: %#v", tools)
	}
}

func TestOpenAIResponsesToAnthropic(t *testing.T) {
	converted, name, stream, err := openAIResponsesToAnthropic([]byte(`{
		"model":"claude-sonnet-4-6", "instructions":"Be concise", "max_output_tokens":512, "stream":true,
		"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"weather"},
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"Follow policy"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Shanghai weather"}]},
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		]
	}`))
	if err != nil {
		t.Fatalf("convert OpenAI request: %v", err)
	}
	if name != "claude-sonnet-4-6" || !stream || converted["max_tokens"] != int64(512) {
		t.Fatalf("unexpected request envelope: %#v, model=%q, stream=%v", converted, name, stream)
	}
	if !strings.Contains(stringValue(converted["system"]), "Follow policy") || !strings.Contains(stringValue(converted["system"]), "Be concise") {
		t.Fatalf("system conversion failed: %#v", converted["system"])
	}
	messages := converted["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3: %#v", len(messages), messages)
	}
	assistant := messages[1].(map[string]any)
	if stringValue(assistant["role"]) != "assistant" || stringValue(assistant["content"].([]any)[0].(map[string]any)["type"]) != "tool_use" {
		t.Fatalf("function call was not converted to an assistant tool block: %#v", assistant)
	}
	result := messages[2].(map[string]any)
	if stringValue(result["content"].([]any)[0].(map[string]any)["type"]) != "tool_result" {
		t.Fatalf("function output was not converted to a user tool result: %#v", result)
	}
	if tools := converted["tools"].([]any); len(tools) != 1 || stringValue(tools[0].(map[string]any)["name"]) != "weather" {
		t.Fatalf("tool definition conversion failed: %#v", tools)
	}
}

func TestOpenAIResponsesToAnthropicAcceptsEasyInputMessages(t *testing.T) {
	converted, name, stream, err := openAIResponsesToAnthropic([]byte(`{
		"model":"claude-opus-4-8",
		"stream":true,
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("convert easy-input Responses request: %v", err)
	}
	if name != "claude-opus-4-8" || !stream {
		t.Fatalf("model=%q stream=%v", name, stream)
	}
	messages, _ := converted["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", converted["messages"])
	}
	message := messages[0].(map[string]any)
	if stringValue(message["role"]) != "user" {
		t.Fatalf("role = %q", stringValue(message["role"]))
	}
	content := message["content"].([]any)
	if len(content) != 1 || stringValue(content[0].(map[string]any)["text"]) != "hello" {
		t.Fatalf("content = %#v", content)
	}
}

func TestStreamOpenAIResponsesAsAnthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	streamOpenAIResponsesAsAnthropic(c, strings.NewReader(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","usage":{"input_tokens":7}}}

data: {"type":"response.output_text.delta","delta":"Hello"}

data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":7,"output_tokens":1}}}

`), model.PlatformOpenAI, "gpt-5.4")

	body := w.Body.String()
	for _, want := range []string{"event: message_start", `"index":0`, "event: content_block_delta", "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Anthropic stream missing %q:\n%s", want, body)
		}
	}
}

func TestStreamOpenAIResponsesAsAnthropicUsesFinalToolArguments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	streamOpenAIResponsesAsAnthropic(c, strings.NewReader(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress"}}

data: {"type":"response.output_item.added","item":{"type":"function_call","id":"call_1","call_id":"call_1","name":"echo_value","arguments":""}}

data: {"type":"response.output_item.done","item":{"type":"function_call","id":"call_1","call_id":"call_1","name":"echo_value","arguments":"{\"value\":\"hello\"}"}}

data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","status":"completed","output":[{"type":"function_call","id":"call_1","call_id":"call_1","name":"echo_value","arguments":"{\"value\":\"hello\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}}

`), model.PlatformOpenAI, "gpt-5.4")

	body := w.Body.String()
	if !strings.Contains(body, `"partial_json":"{\"value\":\"hello\"}"`) {
		t.Fatalf("final function arguments were not emitted as Anthropic input_json_delta:\n%s", body)
	}
}

func TestStreamAnthropicAsOpenAIResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	streamAnthropicAsOpenAIResponses(c, strings.NewReader(`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-6","usage":{"input_tokens":7,"output_tokens":0}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":7,"output_tokens":1}}

data: {"type":"message_stop"}

`), model.PlatformAnthropic, "claude-sonnet-4-6")

	body := w.Body.String()
	for _, want := range []string{"response.created", "response.output_text.delta", "response.completed", `"model":"claude-sonnet-4-6"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("OpenAI Responses stream missing %q:\n%s", want, body)
		}
	}
}
