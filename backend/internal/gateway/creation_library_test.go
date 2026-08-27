package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"dengdeng/internal/service"
)

func testCreationLibrary() service.CreationLibrarySettings {
	return service.CreationLibrarySettings{
		Enabled:        true,
		CatalogVersion: 2,
		Capabilities: service.CreationCapabilitySettings{
			Prompts: true, Rules: true, Skills: true, Chat: true, Image: true, Video: true, Audio: true,
		},
		Rules: []service.CreationLibraryEntry{
			{ID: "auto", Content: "automatic rule", Scope: service.CreationScopeAll, Enabled: true, AutoApply: true},
			{ID: "manual", Content: "manual rule", Scope: service.CreationScopeChat, Enabled: true},
		},
		Skills: []service.CreationLibraryEntry{
			{ID: "review", Content: "review skill", Scope: service.CreationScopeChat, Enabled: true},
			{ID: "image", Content: "image skill", Scope: service.CreationScopeImage, Enabled: true},
		},
	}
}

func TestSelectCreationGuidance(t *testing.T) {
	guidance, rules, skills, err := selectCreationGuidance(testCreationLibrary(), service.CreationScopeChat, []string{"manual"}, []string{"review"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(guidance) != 3 || len(rules) != 2 || len(skills) != 1 {
		t.Fatalf("guidance=%#v rules=%#v skills=%#v", guidance, rules, skills)
	}
	outOfScope, _, _, err := selectCreationGuidance(testCreationLibrary(), service.CreationScopeChat, nil, []string{"image"})
	if err != nil || len(outOfScope) != 1 {
		t.Fatalf("out-of-scope skill should be skipped while the automatic rule remains: guidance=%#v err=%v", outOfScope, err)
	}
	if _, _, _, err := selectCreationGuidance(testCreationLibrary(), service.CreationScopeChat, nil, []string{"missing"}); err == nil {
		t.Fatal("unknown skill was accepted")
	}
}

func TestStoredCreationSelectionUsesPublishedCapabilities(t *testing.T) {
	library := testCreationLibrary()
	storedSkills := availableStoredCreationIDs(library, "skill", []string{"review", "missing"})
	guidance, _, skills, err := selectCreationGuidance(library, service.CreationScopeChat, nil, mergeCreationIDs(storedSkills))
	if err != nil || len(skills) != 1 || skills[0] != "review" || len(guidance) != 2 {
		t.Fatalf("stored selection guidance=%#v skills=%#v err=%v", guidance, skills, err)
	}
	library.Capabilities.Skills = false
	if got := availableStoredCreationIDs(library, "skill", []string{"review"}); len(got) != 0 {
		t.Fatalf("disabled skill remained available: %#v", got)
	}
}

func TestResolveCreationSkillRunsUsesCompletePackage(t *testing.T) {
	library := service.CreationLibrarySettings{
		Enabled:      true,
		Capabilities: service.CreationCapabilitySettings{Rules: true, Skills: true, Chat: true},
		Rules:        []service.CreationLibraryEntry{{ID: "auto", Content: "automatic rule", Scope: service.CreationScopeChat, Enabled: true, AutoApply: true}},
		Skills:       []service.CreationLibraryEntry{{ID: "backend-engineer", Content: "catalog summary", Scope: service.CreationScopeChat, Enabled: true, SourceType: "builtin"}},
	}
	guidance, rules, skills, err := selectCreationGuidance(library, service.CreationScopeChat, nil, []string{"backend-engineer"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, runs, err := resolveCreationSkillRuns(library, guidance, rules, skills)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Mode != "package" || len(resolved) != 2 {
		t.Fatalf("resolved=%#v runs=%#v", resolved, runs)
	}
	if !strings.Contains(resolved[1], "# 后端工程") || !strings.Contains(resolved[1], "references/service-contracts.md") || strings.Contains(resolved[1], "catalog summary") {
		t.Fatalf("runtime guidance did not use the complete package: %q", resolved[1])
	}
}

func decodeCreationBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return value
}

func TestApplyCreationGuidanceJSON(t *testing.T) {
	guidance := []string{"automatic rule", "review skill"}

	chat, err := applyCreationGuidanceJSON([]byte(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`), creationWireOpenAIChat, guidance)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	messages := decodeCreationBody(t, chat)["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" {
		t.Fatalf("chat messages = %#v", messages)
	}

	responses, err := applyCreationGuidanceJSON([]byte(`{"instructions":"existing","input":"hello"}`), creationWireOpenAIResponses, guidance)
	if err != nil {
		t.Fatalf("responses: %v", err)
	}
	if got := decodeCreationBody(t, responses)["instructions"]; got != "existing\n\nautomatic rule\n\nreview skill" {
		t.Fatalf("responses instructions = %#v", got)
	}

	anthropic, err := applyCreationGuidanceJSON([]byte(`{"system":[{"type":"text","text":"existing"}],"messages":[]}`), creationWireAnthropic, guidance)
	if err != nil {
		t.Fatalf("anthropic: %v", err)
	}
	blocks := decodeCreationBody(t, anthropic)["system"].([]any)
	if len(blocks) != 2 || blocks[1].(map[string]any)["text"] != "automatic rule\n\nreview skill" {
		t.Fatalf("anthropic system = %#v", blocks)
	}

	gemini, err := applyCreationGuidanceJSON([]byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`), creationWireGemini, guidance)
	if err != nil {
		t.Fatalf("gemini: %v", err)
	}
	instruction := decodeCreationBody(t, gemini)["systemInstruction"].(map[string]any)
	if len(instruction["parts"].([]any)) != 1 {
		t.Fatalf("gemini instruction = %#v", instruction)
	}
}

func TestCompleteSkillPackageAppliesAcrossWireFormats(t *testing.T) {
	run, err := service.ResolveCreationSkillRun(service.CreationLibraryEntry{ID: "backend-engineer", Content: "summary", SourceType: "builtin"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire creationWire
		body string
	}{
		{name: "openai-chat", wire: creationWireOpenAIChat, body: `{"model":"test","messages":[{"role":"user","content":"hello"}]}`},
		{name: "openai-responses", wire: creationWireOpenAIResponses, body: `{"model":"test","input":"hello"}`},
		{name: "anthropic", wire: creationWireAnthropic, body: `{"model":"test","messages":[{"role":"user","content":"hello"}]}`},
		{name: "gemini", wire: creationWireGemini, body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			patched, err := applyCreationGuidanceJSON([]byte(test.body), test.wire, []string{run.Guidance})
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"# 后端工程", "references/service-contracts.md", "## 数据与事务"} {
				if !strings.Contains(string(patched), marker) {
					t.Fatalf("patched request is missing %q", marker)
				}
			}
		})
	}
}

func TestApplyCreationGuidanceMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "edit this image"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	patched, contentType, err := applyCreationGuidanceMultipart(writer.FormDataContentType(), body.Bytes(), []string{"keep text readable"})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(patched), params["boundary"])
	found := ""
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if part.FormName() == "prompt" {
			value, _ := io.ReadAll(part)
			found = string(value)
		}
	}
	if found != "edit this image\n\nkeep text readable" {
		t.Fatalf("prompt = %q", found)
	}
}

func TestCompleteImageSkillPackageAppliesToJSONAndMultipart(t *testing.T) {
	run, err := service.ResolveCreationSkillRun(service.CreationLibraryEntry{ID: "visual-director", Content: "summary", SourceType: "builtin"})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := applyCreationGuidanceJSON([]byte(`{"model":"gpt-image-2","prompt":"make an image"}`), creationWirePrompt, []string{run.Guidance})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "# 视觉导演") || !strings.Contains(string(generated), "references/visual-direction.md") {
		t.Fatal("JSON image request did not receive the complete visual skill package")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "edit this image"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	patched, _, err := applyCreationGuidanceMultipart(writer.FormDataContentType(), body.Bytes(), []string{run.Guidance})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patched), "# 视觉导演") || !strings.Contains(string(patched), "references/visual-direction.md") {
		t.Fatal("multipart image request did not receive the complete visual skill package")
	}
}
