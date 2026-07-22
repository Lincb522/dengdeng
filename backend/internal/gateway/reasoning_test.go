package gateway

import (
	"encoding/json"
	"testing"

	"dengdeng/internal/model"
)

func TestApplyOpenAIReasoningDefaultResponses(t *testing.T) {
	fields := peekJSON([]byte(`{"model":"gpt-5","input":"hello"}`))
	body, effort := applyOpenAIReasoningDefault(fields, []byte(`{"model":"gpt-5","input":"hello"}`), "fast", openAIReasoningResponses)
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
	if effort != "low" {
		t.Fatalf("effective effort = %q, want low", effort)
	}
}

func TestGroupReasoningMappingAndCeilingRewriteAndBillFinalEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-5","reasoning":{"effort":"max"}}`)
	group := model.Group{MaxReasoningEffort: "medium", ReasoningEffortMappings: map[string]string{"max": "xhigh"}}
	patched, effort := applyOpenAIReasoningPolicy(peekJSON(body), body, "auto", group, openAIReasoningResponses)
	var got struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(patched, &got); err != nil {
		t.Fatal(err)
	}
	if got.Reasoning.Effort != "medium" || effort != "medium" {
		t.Fatalf("outgoing=%q billing=%q, want medium", got.Reasoning.Effort, effort)
	}
}

func TestGroupReasoningPolicyAppliesToClaudeCodeBridge(t *testing.T) {
	request := map[string]any{"reasoning": map[string]any{"effort": "high"}}
	group := model.Group{ReasoningEffortMappings: map[string]string{"high": "low"}}
	effort := applyOpenAIResponsesReasoningPolicy(request, "auto", group)
	got := request["reasoning"].(map[string]any)["effort"]
	if got != "low" || effort != "low" {
		t.Fatalf("outgoing=%v billing=%q, want low", got, effort)
	}
}

func TestApplyOpenAIReasoningDefaultKeepsClientChoice(t *testing.T) {
	body := []byte(`{"model":"gpt-5","reasoning":{"effort":"high"}}`)
	fields := peekJSON(body)
	got, effort := applyOpenAIReasoningDefault(fields, body, "fast", openAIReasoningResponses)
	if string(got) != string(body) {
		t.Fatalf("client reasoning should not be rewritten: %s", got)
	}
	if effort != "high" {
		t.Fatalf("client effort should win for billing, got %q", effort)
	}
}

func TestApplyOpenAIReasoningDefaultChatCompletions(t *testing.T) {
	fields := peekJSON([]byte(`{"model":"gpt-5","messages":[]}`))
	body, effort := applyOpenAIReasoningDefault(fields, []byte(`{"model":"gpt-5","messages":[]}`), "high", openAIReasoningChatCompletions)
	var got struct {
		Effort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Effort != "high" {
		t.Fatalf("chat effort = %q, want high", got.Effort)
	}
	if effort != "high" {
		t.Fatalf("effective effort = %q, want high", effort)
	}
}

func TestApplyOpenAIResponsesReasoningDefault(t *testing.T) {
	request := map[string]any{"input": "hello"}
	effort := applyOpenAIResponsesReasoningDefault(request, "none")
	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("converted request missing effort: %#v", request)
	}
	if effort != "none" {
		t.Fatalf("effective effort = %q, want none", effort)
	}
}

func TestReasoningOfficialValuesAndLegacyAliases(t *testing.T) {
	cases := map[string]string{
		"fast": "low", "minimal": "low",
		"none": "none", "low": "low", "medium": "medium",
		"high": "high", "xhigh": "xhigh", "max": "max",
		"auto": "",
	}
	for input, want := range cases {
		if got := upstreamReasoningEffort(input); got != want {
			t.Fatalf("upstreamReasoningEffort(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestApplyOpenAIReasoningDefaultAutoAndClientChoice(t *testing.T) {
	autoBody := []byte(`{"model":"gpt-5","messages":[]}`)
	got, effort := applyOpenAIReasoningDefault(peekJSON(autoBody), autoBody, "auto", openAIReasoningChatCompletions)
	if string(got) != string(autoBody) || effort != "" {
		t.Fatalf("auto must preserve model default, got body=%s effort=%q", got, effort)
	}

	clientBody := []byte(`{"model":"gpt-5","messages":[],"reasoning_effort":"max"}`)
	got, effort = applyOpenAIReasoningDefault(peekJSON(clientBody), clientBody, "low", openAIReasoningChatCompletions)
	if string(got) != string(clientBody) || effort != "max" {
		t.Fatalf("client max must win, got body=%s effort=%q", got, effort)
	}
}
