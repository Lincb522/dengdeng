package gateway

import (
	"bytes"
	"encoding/json"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
)

// usageExtractor accumulates token usage from response bodies. For SSE it
// keeps a line buffer across chunk boundaries and parses each `data:` event.
type usageExtractor struct {
	platform string
	stream   bool
	image    bool
	buf      []byte
	u        service.Usage
}

func newUsageExtractor(platform string, stream bool, image ...bool) *usageExtractor {
	isImage := len(image) > 0 && image[0]
	return &usageExtractor{platform: platform, stream: stream, image: isImage}
}

func (e *usageExtractor) usage() service.Usage { return e.u }

func (e *usageExtractor) feedChunk(p []byte) {
	e.buf = append(e.buf, p...)
	for {
		idx := bytes.IndexByte(e.buf, '\n')
		if idx < 0 {
			return
		}
		line := bytes.TrimRight(e.buf[:idx], "\r")
		e.buf = e.buf[idx+1:]
		e.feedLine(line)
	}
}

func (e *usageExtractor) finish() {
	if len(e.buf) > 0 {
		e.feedLine(bytes.TrimRight(e.buf, "\r\n"))
		e.buf = nil
	}
}

func (e *usageExtractor) feedLine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[5:])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	e.feedJSON(payload)
}

// feedJSON merges usage from one JSON document (full body or one SSE event).
func (e *usageExtractor) feedJSON(doc []byte) {
	switch e.platform {
	case model.PlatformAnthropic:
		e.feedAnthropic(doc)
	case model.PlatformOpenAI, model.PlatformGrok:
		// xAI returns OpenAI-shaped usage on both Chat and Responses wires.
		e.feedOpenAI(doc)
	case model.PlatformGemini:
		e.feedGemini(doc)
	}
}

type anthropicUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5mInputTokens *int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (e *usageExtractor) feedAnthropic(doc []byte) {
	var evt struct {
		Usage   *anthropicUsage `json:"usage"`
		Message *struct {
			Usage *anthropicUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(doc, &evt); err != nil {
		return
	}
	apply := func(u *anthropicUsage) {
		if u == nil {
			return
		}
		if u.InputTokens != nil {
			e.u.InputTokens = *u.InputTokens
		}
		// output_tokens in message_delta is cumulative: keep the max.
		if u.OutputTokens != nil && *u.OutputTokens > e.u.OutputTokens {
			e.u.OutputTokens = *u.OutputTokens
		}
		if u.CacheCreationInputTokens != nil {
			e.u.CacheWriteTokens = *u.CacheCreationInputTokens
		}
		if u.CacheCreation != nil {
			if u.CacheCreation.Ephemeral5mInputTokens != nil {
				e.u.CacheWrite5mTokens = *u.CacheCreation.Ephemeral5mInputTokens
			}
			if u.CacheCreation.Ephemeral1hInputTokens != nil {
				e.u.CacheWrite1hTokens = *u.CacheCreation.Ephemeral1hInputTokens
			}
			if e.u.CacheWriteTokens == 0 {
				e.u.CacheWriteTokens = e.u.CacheWrite5mTokens + e.u.CacheWrite1hTokens
			}
		}
		if u.CacheReadInputTokens != nil {
			e.u.CacheReadTokens = *u.CacheReadInputTokens
		}
	}
	apply(evt.Usage)
	if evt.Message != nil {
		apply(evt.Message.Usage)
	}
}

func (e *usageExtractor) feedOpenAI(doc []byte) {
	var evt struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			InputTokens         int64 `json:"input_tokens"`  // Responses API naming
			OutputTokens        int64 `json:"output_tokens"` // Responses API naming
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputTokensDetails *struct {
				CachedTokens     int64 `json:"cached_tokens"`
				CacheWriteTokens int64 `json:"cache_write_tokens"`
				ImageTokens      int64 `json:"image_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails *struct {
				ImageTokens int64 `json:"image_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Response *struct {
			Usage *struct {
				InputTokens        int64 `json:"input_tokens"`
				OutputTokens       int64 `json:"output_tokens"`
				InputTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		} `json:"response"` // Responses API stream: response.completed event
	}
	if err := json.Unmarshal(doc, &evt); err != nil {
		return
	}
	if u := evt.Usage; u != nil {
		if u.PromptTokens > 0 {
			e.u.InputTokens = u.PromptTokens
		}
		if u.InputTokens > 0 {
			e.u.InputTokens = u.InputTokens
		}
		if u.CompletionTokens > 0 {
			e.u.OutputTokens = u.CompletionTokens
		}
		if u.OutputTokens > 0 {
			e.u.OutputTokens = u.OutputTokens
		}
		if u.PromptTokensDetails != nil {
			e.u.CacheReadTokens = u.PromptTokensDetails.CachedTokens
			e.u.InputIncludesCacheRead = true
		}
		if u.InputTokensDetails != nil {
			e.u.CacheReadTokens = u.InputTokensDetails.CachedTokens
			e.u.InputIncludesCacheRead = true
			e.u.CacheWriteTokens = u.InputTokensDetails.CacheWriteTokens
			e.u.ImageInputTokens = u.InputTokensDetails.ImageTokens
		}
		if u.OutputTokensDetails != nil {
			e.u.ImageOutputTokens = u.OutputTokensDetails.ImageTokens
		}
	}
	if r := evt.Response; r != nil && r.Usage != nil {
		e.u.InputTokens = r.Usage.InputTokens
		e.u.OutputTokens = r.Usage.OutputTokens
		if r.Usage.InputTokensDetails != nil {
			e.u.CacheReadTokens = r.Usage.InputTokensDetails.CachedTokens
			e.u.InputIncludesCacheRead = true
		}
	}
	if e.image {
		count := int64(0)
		for _, item := range evt.Data {
			if item.URL != "" || item.B64JSON != "" {
				count++
			}
		}
		if count > e.u.ImageCount {
			e.u.ImageCount = count
		}
	}
}

func (e *usageExtractor) feedGemini(doc []byte) {
	var evt struct {
		UsageMetadata *struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(doc, &evt); err != nil {
		return
	}
	if m := evt.UsageMetadata; m != nil {
		// Every Gemini chunk repeats cumulative usage: last one wins.
		e.u.InputTokens = m.PromptTokenCount
		e.u.OutputTokens = m.CandidatesTokenCount + m.ThoughtsTokenCount
		e.u.CacheReadTokens = m.CachedContentTokenCount
		e.u.InputIncludesCacheRead = true
	}
}
