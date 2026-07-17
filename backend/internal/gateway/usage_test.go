package gateway

import (
	"testing"

	"dengdeng/internal/model"
)

func TestAnthropicUsageExtractorKeepsCacheTTLBreakdown(t *testing.T) {
	e := newUsageExtractor(model.PlatformAnthropic, false)
	e.feedJSON([]byte(`{
		"usage": {
			"input_tokens": 12,
			"output_tokens": 7,
			"cache_creation_input_tokens": 10,
			"cache_read_input_tokens": 3,
			"cache_creation": {
				"ephemeral_5m_input_tokens": 4,
				"ephemeral_1h_input_tokens": 6
			}
		}
	}`))

	u := e.usage()
	if u.CacheWriteTokens != 10 || u.CacheWrite5mTokens != 4 || u.CacheWrite1hTokens != 6 {
		t.Fatalf("cache usage = %+v, want total=10 5m=4 1h=6", u)
	}
}

func TestOpenAIImageUsageCountsReturnedImages(t *testing.T) {
	e := newUsageExtractor(model.PlatformOpenAI, false, true)
	e.feedJSON([]byte(`{"data":[{"b64_json":"first"},{"url":"https://example.test/image.png"},{"revised_prompt":"not an image payload"}]}`))

	if got, want := e.usage().ImageCount, int64(2); got != want {
		t.Fatalf("ImageCount = %d, want %d", got, want)
	}
}

func TestOpenAIUsageMarksCachedTokensAsIncludedInInput(t *testing.T) {
	e := newUsageExtractor(model.PlatformOpenAI, false)
	e.feedJSON([]byte(`{"usage":{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":80}}}`))

	u := e.usage()
	if !u.InputIncludesCacheRead || u.InputTokens != 100 || u.CacheReadTokens != 80 {
		t.Fatalf("OpenAI usage = %+v, want input=100 cached=80 included=true", u)
	}
}
