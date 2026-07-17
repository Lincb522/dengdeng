package gateway

import (
	"encoding/json"
	"testing"
)

func TestApplyOpenAIReasoningDefaultResponses(t *testing.T) {
	fields := peekJSON([]byte(`{"model":"gpt-5","input":"hello"}`))
	body := applyOpenAIReasoningDefault(fields, []byte(`{"model":"gpt-5","input":"hello"}`), "fast", openAIReasoningResponses)
	var got struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Reasoning.Effort != "low" {
		t.Fatalf("fast effort = %q, want low", got.Reasoning.Effort)
	}
}

func TestApplyOpenAIReasoningDefaultKeepsClientChoice(t *testing.T) {
	body := []byte(`{"model":"gpt-5","reasoning":{"effort":"high"}}`)
	fields := peekJSON(body)
	got := applyOpenAIReasoningDefault(fields, body, "fast", openAIReasoningResponses)
	if string(got) != string(body) {
		t.Fatalf("client reasoning should not be rewritten: %s", got)
	}
}

func TestApplyOpenAIReasoningDefaultChatCompletions(t *testing.T) {
	fields := peekJSON([]byte(`{"model":"gpt-5","messages":[]}`))
	body := applyOpenAIReasoningDefault(fields, []byte(`{"model":"gpt-5","messages":[]}`), "high", openAIReasoningChatCompletions)
	var got struct {
		Effort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Effort != "high" {
		t.Fatalf("chat effort = %q, want high", got.Effort)
	}
}

func TestApplyOpenAIResponsesReasoningDefault(t *testing.T) {
	request := map[string]any{"input": "hello"}
	applyOpenAIResponsesReasoningDefault(request, "none")
	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("converted request missing effort: %#v", request)
	}
}
