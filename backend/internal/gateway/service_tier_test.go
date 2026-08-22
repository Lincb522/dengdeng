package gateway

import (
	"testing"

	"dengdeng/internal/model"
)

func TestRequestServiceTierReadsOpenAIAndAnthropicFastModes(t *testing.T) {
	if got := requestServiceTier([]byte(`{"service_tier":"flex"}`), model.PlatformOpenAI, "gpt-5.6"); got != "flex" {
		t.Fatalf("OpenAI service tier = %q, want flex", got)
	}
	if got := requestServiceTier([]byte(`{"speed":"fast"}`), model.PlatformAnthropic, "claude-opus-4-8"); got != "fast" {
		t.Fatalf("Anthropic fast tier = %q, want fast", got)
	}
}

func TestRequestServiceTierDoesNotChargeUnsupportedAnthropicFast(t *testing.T) {
	for _, modelName := range []string{"claude-opus-4-7", "claude-sonnet-5", ""} {
		if got := requestServiceTier([]byte(`{"speed":"fast"}`), model.PlatformAnthropic, modelName); got != "" {
			t.Fatalf("unsupported model %q service tier = %q, want empty", modelName, got)
		}
	}
	if got := requestServiceTier([]byte(`{"speed":"fast"}`), model.PlatformOpenAI, "claude-opus-5"); got != "" {
		t.Fatalf("OpenAI speed field service tier = %q, want empty", got)
	}
}
