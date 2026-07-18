package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOpenAIChatToGeminiBasic(t *testing.T) {
	body := []byte(`{
		"model":"gemini-2.5-pro","stream":true,
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"hello"},
			{"role":"user","content":"bye"}
		]
	}`)
	converted, modelName, stream, err := openAIChatToGemini(body)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if modelName != "gemini-2.5-pro" || !stream {
		t.Fatalf("model/stream = %q/%v", modelName, stream)
	}
	system, _ := converted["systemInstruction"].(map[string]any)
	if system == nil {
		t.Fatalf("systemInstruction missing")
	}
	contents, _ := converted["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("want 3 contents, got %d: %#v", len(contents), contents)
	}
	first, _ := contents[0].(map[string]any)
	if stringValue(first["role"]) != "user" {
		t.Fatalf("first role = %v", first["role"])
	}
	second, _ := contents[1].(map[string]any)
	if stringValue(second["role"]) != "model" {
		t.Fatalf("assistant should map to model role: %v", second["role"])
	}
}

func TestOpenAIChatToGeminiToolCallAndResult(t *testing.T) {
	body := []byte(`{
		"model":"gemini-2.5-flash",
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
			{"role":"tool","tool_call_id":"c1","name":"get_weather","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"required"
	}`)
	converted, _, _, err := openAIChatToGemini(body)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	contents, _ := converted["contents"].([]any)
	// user, model(functionCall), user(functionResponse)
	if len(contents) != 3 {
		t.Fatalf("want 3 contents, got %d", len(contents))
	}
	modelTurn, _ := contents[1].(map[string]any)
	parts, _ := modelTurn["parts"].([]any)
	fcPart, _ := parts[0].(map[string]any)
	if _, ok := fcPart["functionCall"]; !ok {
		t.Fatalf("expected functionCall part: %#v", fcPart)
	}
	toolTurn, _ := contents[2].(map[string]any)
	tparts, _ := toolTurn["parts"].([]any)
	frPart, _ := tparts[0].(map[string]any)
	if _, ok := frPart["functionResponse"]; !ok {
		t.Fatalf("expected functionResponse part: %#v", frPart)
	}
	tools, _ := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected tools declarations")
	}
	config, _ := converted["toolConfig"].(map[string]any)
	fcc, _ := config["functionCallingConfig"].(map[string]any)
	if stringValue(fcc["mode"]) != "ANY" {
		t.Fatalf("tool_choice required -> ANY, got %v", fcc["mode"])
	}
}

func TestGeminiMessageAsOpenAIChat(t *testing.T) {
	src := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hi there"}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": float64(10), "candidatesTokenCount": float64(5)},
	}
	out := geminiMessageAsOpenAIChat(src, "gemini-2.5-pro")
	choices, _ := out["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if stringValue(message["content"]) != "hi there" {
		t.Fatalf("content = %v", message["content"])
	}
	if stringValue(choice["finish_reason"]) != "stop" {
		t.Fatalf("finish = %v", choice["finish_reason"])
	}
	usage, _ := out["usage"].(map[string]any)
	if intValue(usage["total_tokens"]) != 15 {
		t.Fatalf("total tokens = %v", usage["total_tokens"])
	}
}

func TestStreamGeminiAsOpenAIChat(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`,
		"",
	}, "\n")

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	usage := streamGeminiAsOpenAIChat(c, strings.NewReader(stream), "gemini", "gemini-2.5-pro")

	out := rec.Body.String()
	if !strings.Contains(out, `"content":"Hel"`) || !strings.Contains(out, `"content":"lo"`) {
		t.Fatalf("missing text deltas: %s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing DONE: %s", out)
	}
	if usage.OutputTokens != 2 || usage.InputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestOpenAIChatToGeminiInlineImage(t *testing.T) {
	body := []byte(`{
		"model":"gemini-2.5-pro",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
		]}]
	}`)
	converted, _, _, err := openAIChatToGemini(body)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	contents, _ := converted["contents"].([]any)
	first, _ := contents[0].(map[string]any)
	parts, _ := first["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("want text + image parts, got %d: %#v", len(parts), parts)
	}
	image, _ := parts[1].(map[string]any)
	inline, _ := image["inlineData"].(map[string]any)
	if stringValue(inline["mimeType"]) != "image/png" || stringValue(inline["data"]) != "AAAA" {
		t.Fatalf("inline image not parsed: %#v", inline)
	}
}
