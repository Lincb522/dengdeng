package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeResponsesRootUnionAndParallelTools(t *testing.T) {
	body, err := normalizeOpenAIResponsesRequest([]byte(`{
		"model":"gpt-test",
		"parallel_tool_calls":true,
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","anyOf":[{"required":["q"]},{"type":"object","properties":{}}]}}],
		"input":"hello"
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	tool := request["tools"].([]any)[0].(map[string]any)
	parameters := tool["parameters"].(map[string]any)
	branches := parameters["anyOf"].([]any)
	if got := stringValue(branches[0].(map[string]any)["type"]); got != "object" {
		t.Fatalf("untyped root branch type = %q", got)
	}
	if enabled, _ := request["parallel_tool_calls"].(bool); !enabled {
		t.Fatal("parallel_tool_calls was not preserved when tools exist")
	}
}

func TestNormalizeResponsesLiteForcesParallelFalse(t *testing.T) {
	headers := make(http.Header)
	headers.Set(codexResponsesLiteHeader, "true")
	body, err := normalizeOpenAIResponsesRequest([]byte(`{"model":"gpt-test","parallel_tool_calls":true,"input":"hello"}`), headers)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	if enabled, exists := request["parallel_tool_calls"].(bool); !exists || enabled {
		t.Fatalf("parallel_tool_calls = %#v", request["parallel_tool_calls"])
	}
}

func TestNormalizeResponsesDropsParallelWithoutTools(t *testing.T) {
	body, err := normalizeOpenAIResponsesRequest([]byte(`{"model":"gpt-test","parallel_tool_calls":true,"input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	if _, exists := request["parallel_tool_calls"]; exists {
		t.Fatalf("parallel_tool_calls was retained without tools: %s", body)
	}
}

func TestNormalizeResponsesShortensInputItemIDsDeterministically(t *testing.T) {
	longA := strings.Repeat("工具调用-", 20)
	longB := strings.Repeat("工具结果-", 20)
	raw := []byte(`{"model":"gpt-test","input":[{"type":"function_call","id":"` + longA + `","call_id":"call-1"},{"type":"function_call_output","id":"` + longB + `","call_id":"call-1"},{"type":"message","id":"msg-1"}]}`)
	first, err := normalizeOpenAIResponsesRequest(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeOpenAIResponsesRequest(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var one, two map[string]any
	_ = json.Unmarshal(first, &one)
	_ = json.Unmarshal(second, &two)
	oneItems := one["input"].([]any)
	twoItems := two["input"].([]any)
	firstID := stringValue(oneItems[0].(map[string]any)["id"])
	secondID := stringValue(oneItems[1].(map[string]any)["id"])
	if len([]rune(firstID)) > codexInputItemIDLimit || firstID == longA || firstID == secondID {
		t.Fatalf("invalid shortened IDs: %q %q", firstID, secondID)
	}
	if firstID != stringValue(twoItems[0].(map[string]any)["id"]) {
		t.Fatal("input item ID shortening is not deterministic")
	}
	if got := stringValue(oneItems[0].(map[string]any)["call_id"]); got != "call-1" {
		t.Fatalf("call_id changed to %q", got)
	}
	if got := stringValue(oneItems[2].(map[string]any)["id"]); got != "msg-1" {
		t.Fatalf("short ID changed to %q", got)
	}
}

func TestNormalizeChatFunctionRootUnion(t *testing.T) {
	body, err := normalizeOpenAIChatRequest([]byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","oneOf":[{"required":["q"]}]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	tool := request["tools"].([]any)[0].(map[string]any)
	function := tool["function"].(map[string]any)
	branch := function["parameters"].(map[string]any)["oneOf"].([]any)[0].(map[string]any)
	if got := stringValue(branch["type"]); got != "object" {
		t.Fatalf("branch type = %q", got)
	}
}
