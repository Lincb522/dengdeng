package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

// Register mounts the public relay endpoints. These paths mirror the three
// official APIs so existing SDKs / CLIs work by only switching base URL + key.
func (g *Gateway) Register(r *gin.Engine) {
	// Anthropic Messages API
	r.POST("/v1/messages", g.handleAnthropicMessages)
	r.POST("/v1/messages/count_tokens", g.handleAnthropicCountTokens)
	// Some clients treat the configured URL as the complete API prefix and
	// append only /messages. Accept a bare-domain base URL as well.
	r.POST("/messages", g.handleAnthropicMessages)
	r.POST("/messages/count_tokens", g.handleAnthropicCountTokens)
	// Older quick-setup snippets and some desktop clients accept a provider
	// URL ending in /v1, then append /v1/messages themselves. Keep these
	// aliases so that configuration mistake returns a real API response
	// instead of the SPA index document.
	r.POST("/v1/v1/messages", g.handleAnthropicMessages)
	r.POST("/v1/v1/messages/count_tokens", g.handleAnthropicCountTokens)

	// OpenAI
	r.POST("/v1/chat/completions", g.handleOpenAIChat)
	r.POST("/v1/responses", g.handleOpenAIResponses)
	r.POST("/v1/responses/compact", g.handleOpenAIResponsesCompact)
	r.POST("/v1/responses/input_tokens", g.handleOpenAIInputTokens)
	r.POST("/chat/completions", g.handleOpenAIChat)
	r.POST("/responses", g.handleOpenAIResponses)
	r.POST("/responses/compact", g.handleOpenAIResponsesCompact)
	r.POST("/responses/input_tokens", g.handleOpenAIInputTokens)
	r.GET("/backend-api/codex/models", g.handleCodexModelsManifest)
	r.POST("/v1/images/generations", g.handleOpenAIImageGeneration)
	r.POST("/v1/images/generations/async", g.handleOpenAIImageGenerationAsync)
	r.GET("/v1/images/tasks/:task_id", g.handleOpenAIImageTask)
	r.POST("/v1/images/edits", g.handleOpenAIImageEdit)
	r.POST("/images/generations", g.handleOpenAIImageGeneration)
	r.POST("/images/generations/async", g.handleOpenAIImageGenerationAsync)
	r.GET("/images/tasks/:task_id", g.handleOpenAIImageTask)
	r.POST("/images/edits", g.handleOpenAIImageEdit)
	// xAI-compatible media surfaces. These preserve the request body and
	// content type so new Grok media models do not require a gateway release.
	r.POST("/v1/videos/generations", g.handleGrokMedia)
	r.GET("/v1/videos/:media_id", g.handleGrokMedia)
	r.POST("/v1/audio/speech", g.handleGrokMedia)
	r.POST("/v1/audio/transcriptions", g.handleGrokMedia)
	r.POST("/v1/search", g.handleGrokMedia)

	// Gemini (native v1beta path style)
	r.POST("/v1beta/models/*action", g.handleGemini)

	// Static-key groups expose the model list returned by their real upstream.
	// OAuth-only groups retain the configured catalogue because those providers
	// do not consistently offer a credential-compatible discovery endpoint.
	r.GET("/v1/models", g.handleListModels)
	// CCSwitch and similar desktop clients use this authenticated endpoint to
	// display the key's remaining balance and configured caps.
	r.GET("/v1/usage", g.handleUsage)
}

func (g *Gateway) handleGrokMedia(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	if !ak.selectGroup(model.PlatformGrok) {
		util.Fail(c, http.StatusBadRequest, "this key has no Grok media group")
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	modelName := ""
	if fields := peekJSON(body); fields != nil {
		modelName = jsonString(fields["model"])
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformGrok, Path: c.Request.URL.Path, Model: modelName,
		Body: body, ContentType: c.GetHeader("Content-Type"), Billable: c.Request.Method != http.MethodGet,
		Image: strings.Contains(c.Request.URL.Path, "/videos/"),
	})
}

func (g *Gateway) handleOpenAIImageGenerationAsync(c *gin.Context) {
	ak, ok := g.authenticateUsage(c)
	if !ok {
		return
	}
	if g.imageStorage == nil {
		writeAdapterJSON(c, http.StatusNotFound, map[string]any{"error": map[string]any{"type": "not_found_error", "message": "asynchronous image tasks are not enabled"}})
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil || strings.TrimSpace(jsonString(fields["prompt"])) == "" {
		util.Fail(c, http.StatusBadRequest, "prompt is required")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok) {
		util.Fail(c, http.StatusBadRequest, "this key has no image-capable group")
		return
	}
	task, err := g.imageStorage.CreateTask(c.Request.Context(), ak.User.ID, ak.Key.ID)
	if err != nil {
		writeAdapterJSON(c, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"type": "service_unavailable", "message": err.Error()}})
		return
	}
	authorization := c.GetHeader("Authorization")
	userAgent := c.Request.UserAgent()
	acceptLanguage := c.GetHeader("Accept-Language")
	go g.runAsyncImageTask(task.ID, authorization, userAgent, acceptLanguage, body)
	c.JSON(http.StatusAccepted, imageTaskResponse(task))
}

func (g *Gateway) handleOpenAIImageTask(c *gin.Context) {
	ak, ok := g.authenticateUsage(c)
	if !ok {
		return
	}
	if g.imageStorage == nil {
		util.Fail(c, http.StatusNotFound, "image task not found")
		return
	}
	task, err := g.imageStorage.GetTask(c.Request.Context(), c.Param("task_id"), ak.User.ID, ak.Key.ID)
	if err != nil {
		util.Fail(c, http.StatusNotFound, "image task not found")
		return
	}
	c.JSON(http.StatusOK, imageTaskResponse(task))
}

func imageTaskResponse(task model.ImageTask) map[string]any {
	result := any(nil)
	if strings.TrimSpace(task.Result) != "" {
		var decoded any
		if json.Unmarshal([]byte(task.Result), &decoded) == nil {
			result = decoded
		}
	}
	taskError := any(nil)
	if strings.TrimSpace(task.Error) != "" {
		var decoded any
		if json.Unmarshal([]byte(task.Error), &decoded) == nil {
			taskError = decoded
		}
	}
	return map[string]any{
		"id": task.ID, "task_id": task.ID, "object": "image.generation.task", "status": task.Status,
		"http_status": task.HTTPStatus, "result": result, "error": taskError,
		"created_at": task.CreatedAt.Unix(), "completed_at": task.CompletedAt, "expires_at": task.ExpiresAt.Unix(),
	}
}

func (g *Gateway) runAsyncImageTask(taskID, authorization, userAgent, acceptLanguage string, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept-Language", acceptLanguage)
	c.Request = request
	g.handleOpenAIImageGeneration(c)
	status := recorder.Code
	if status == 0 {
		status = http.StatusOK
	}
	result := recorder.Body.Bytes()
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		stored, err := g.imageStorage.RewriteImageResult(ctx, taskID, result)
		if err == nil {
			_ = g.imageStorage.FinishTask(context.Background(), taskID, "completed", status, stored, nil)
			return
		}
		result, _ = json.Marshal(map[string]any{"error": map[string]any{"type": "storage_error", "message": "failed to store generated image"}})
		status = http.StatusBadGateway
	}
	_ = g.imageStorage.FinishTask(context.Background(), taskID, "failed", status, nil, result)
}

func (g *Gateway) handleAnthropicMessages(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	modelName := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, modelName, model.PlatformAnthropic, model.PlatformOpenAI, model.PlatformGrok, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek, model.PlatformComposite) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with Anthropic Messages")
		return
	}
	// OpenAI/Codex and Grok groups both speak the OpenAI Responses contract,
	// so Claude Code's Messages request is bridged to Responses upstream.
	if ak.Group.Platform == model.PlatformOpenAI || ak.Group.Platform == model.PlatformGrok {
		g.relayAnthropicViaResponses(c, ak, body, ak.Group.Platform)
		return
	}
	body = stripAnthropicUnsupportedParams(body)
	g.relay(c, ak, relayRequest{
		Platform: ak.Group.Platform,
		Path:     "/v1/messages",
		Model:    modelName,
		Stream:   jsonBool(fields["stream"]),
		Body:     body,
		Billable: true,
	})
}

func (g *Gateway) handleAnthropicCountTokens(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	modelName := ""
	if fields != nil {
		modelName = jsonString(fields["model"])
	}
	if !g.selectGroupForModel(ak, modelName, model.PlatformAnthropic, model.PlatformOpenAI, model.PlatformGrok, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek, model.PlatformComposite) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with Anthropic Messages")
		return
	}
	// Grok has no compatible token-count endpoint, so keep its conservative
	// local estimate. OpenAI exposes /responses/input_tokens; using it here
	// keeps Claude Code's count in sync with the selected upstream tokenizer.
	if ak.Group.Platform == model.PlatformGrok || ak.Group.Platform == model.PlatformComposite {
		c.JSON(http.StatusOK, gin.H{"input_tokens": estimateBridgeTokens(body)})
		return
	}
	if ak.Group.Platform == model.PlatformOpenAI {
		converted, modelName, _, err := anthropicMessagesToOpenAIResponses(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		resolved, err := g.resolveModel(model.PlatformOpenAI, modelName)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		converted["model"] = resolved.UpstreamModel
		for _, field := range []string{"stream", "store", "max_output_tokens", "temperature", "top_p"} {
			delete(converted, field)
		}
		encoded, err := json.Marshal(converted)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, "convert Anthropic token count request failed")
			return
		}
		g.relay(c, ak, relayRequest{
			Platform: model.PlatformOpenAI,
			Path:     "/v1/responses/input_tokens",
			Model:    modelName,
			Body:     encoded,
			Billable: false,
		})
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: ak.Group.Platform,
		Path:     "/v1/messages/count_tokens",
		Model:    "",
		Body:     body,
		Billable: false,
	})
}

func (g *Gateway) handleOpenAIChat(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok, model.PlatformAnthropic, model.PlatformGemini, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek, model.PlatformComposite) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with OpenAI Chat Completions")
		return
	}
	if ak.Group.Platform == model.PlatformAnthropic {
		converted, modelName, stream, err := openAIChatToAnthropic(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		g.relayOpenAIViaAnthropic(c, ak, converted, modelName, stream, adapterAnthropicToOpenAIChat)
		return
	}
	if ak.Group.Platform == model.PlatformGemini {
		converted, modelName, stream, err := openAIChatToGemini(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		g.relayOpenAIViaGemini(c, ak, converted, modelName, stream)
		return
	}
	platform, ok := openAICompatiblePlatform(c, ak) // openai or grok
	if !ok {
		return
	}
	modelName, _, body, ok := g.rewriteJSONModel(c, platform, fields, body, "")
	if !ok {
		return
	}
	body, effort := applyOpenAIReasoningPolicy(fields, body, ak.Key.ReasoningEffort, ak.Group, openAIReasoningChatCompletions)
	stream := jsonBool(fields["stream"])
	// Guarantee a usage chunk on streams so billing never misses tokens.
	if stream {
		if _, has := fields["stream_options"]; !has {
			fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
			if patched, err := json.Marshal(fields); err == nil {
				body = patched
			}
		}
	}
	if _, hasTools := fields["tools"]; hasTools {
		if normalized, err := normalizeOpenAIChatRequest(body); err == nil {
			body = normalized
		} else {
			util.Fail(c, http.StatusBadRequest, "invalid OpenAI tool schema")
			return
		}
	}
	g.relay(c, ak, relayRequest{
		Platform: platform,
		Path:     "/v1/chat/completions",
		Model:    modelName,
		Stream:   stream,
		Effort:   effort,
		Body:     body,
		Billable: true,
	})
}

func (g *Gateway) handleOpenAIResponses(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok, model.PlatformAnthropic, model.PlatformDeepSeek, model.PlatformComposite) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with OpenAI Responses")
		return
	}
	if ak.Group.Platform == model.PlatformAnthropic {
		converted, modelName, stream, err := openAIResponsesToAnthropic(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		g.relayOpenAIViaAnthropic(c, ak, converted, modelName, stream, adapterAnthropicToOpenAIResponses)
		return
	}
	platform, ok := openAICompatiblePlatform(c, ak) // openai or grok
	if !ok {
		return
	}
	modelName, _, body, ok := g.rewriteJSONModel(c, platform, fields, body, "")
	if !ok {
		return
	}
	body, effort := applyOpenAIReasoningPolicy(fields, body, ak.Key.ReasoningEffort, ak.Group, openAIReasoningResponses)
	_, hasTools := fields["tools"]
	_, hasParallel := fields["parallel_tool_calls"]
	_, hasMetadata := fields["client_metadata"]
	inputHasItemID := bytes.Contains(fields["input"], []byte(`"id"`))
	responsesLite := strings.EqualFold(strings.TrimSpace(c.GetHeader(codexResponsesLiteHeader)), "true")
	if hasTools || hasParallel || hasMetadata || inputHasItemID || responsesLite {
		if normalized, err := normalizeOpenAIResponsesRequest(body, c.Request.Header); err == nil {
			body = normalized
		} else {
			util.Fail(c, http.StatusBadRequest, "invalid OpenAI Responses request")
			return
		}
	}
	g.relay(c, ak, relayRequest{
		Platform: platform,
		Path:     "/v1/responses",
		Model:    modelName,
		Stream:   jsonBool(fields["stream"]),
		Effort:   effort,
		Body:     body,
		Billable: true,
	})
}

// handleOpenAIResponsesCompact implements Codex remote compaction as its own
// unary Responses endpoint. Compact requests are deliberately kept separate
// from normal streaming Responses requests because the ChatGPT upstream uses a
// different URL and rejects request-scoped fields such as stream and store.
func (g *Gateway) handleOpenAIResponsesCompact(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI) {
		util.Fail(c, http.StatusBadRequest, "this key has no OpenAI group compatible with Responses compact")
		return
	}
	modelName, _, body, ok := g.rewriteJSONModel(c, model.PlatformOpenAI, fields, body, "")
	if !ok {
		return
	}
	body, effort := applyOpenAIReasoningPolicy(fields, body, ak.Key.ReasoningEffort, ak.Group, openAIReasoningResponses)
	body, err = normalizeOpenAICompactRequest(body)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid OpenAI compact request")
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformOpenAI,
		Path:     "/v1/responses/compact",
		Model:    modelName,
		Effort:   effort,
		Body:     body,
		Billable: true,
	})
}

// handleOpenAIInputTokens exposes the native OpenAI token-count endpoint for
// clients that already speak Responses. It is operational only and never
// consumes balance or request quota.
func (g *Gateway) handleOpenAIInputTokens(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI) {
		util.Fail(c, http.StatusBadRequest, "this key has no OpenAI group compatible with input token counting")
		return
	}
	modelName, _, body, ok := g.rewriteJSONModel(c, model.PlatformOpenAI, fields, body, "")
	if !ok {
		return
	}
	body, err = normalizeOpenAIInputTokensRequest(body)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid OpenAI input token request")
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformOpenAI,
		Path:     "/v1/responses/input_tokens",
		Model:    modelName,
		Body:     body,
		Billable: false,
	})
}

// handleCodexModelsManifest keeps the ChatGPT/Codex capability manifest
// separate from the standard model list because Codex clients consume its
// provider-specific metadata.
func (g *Gateway) handleCodexModelsManifest(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	if !ak.selectGroup(model.PlatformOpenAI) {
		util.Fail(c, http.StatusNotFound, "Codex models manifest is only available for OpenAI groups")
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformOpenAI,
		Path:     "/backend-api/codex/models",
		Billable: false,
	})
}

// openAICompatiblePlatform returns the upstream platform for a request that
// arrived on the OpenAI wire. Only OpenAI-compatible groups (openai, grok)
// pass; Anthropic is bridged before this is called, and anything else (e.g. a
// Gemini group) gets the standard cross-platform rejection instead of having
// an OpenAI-shaped body forwarded to an incompatible upstream.
func openAICompatiblePlatform(c *gin.Context, ak *authedKey) (string, bool) {
	switch ak.Group.Platform {
	case model.PlatformOpenAI, model.PlatformGrok, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek, model.PlatformComposite:
		return ak.Group.Platform, true
	default:
		util.Fail(c, http.StatusBadRequest,
			fmt.Sprintf("this key belongs to a %s group and cannot call %s endpoints", ak.Group.Platform, model.PlatformOpenAI))
		return "", false
	}
}

// adaptCompositeRequest resolves the public wire against the concrete account
// selected from a composite group. This keeps a composite pool honest: the
// account retains its real provider identity for credentials, usage parsing
// and billing, while the caller continues to use one stable endpoint.
func (g *Gateway) adaptCompositeRequest(req relayRequest, acc *model.UpstreamAccount) (relayRequest, error) {
	if req.Platform != model.PlatformComposite || acc == nil {
		return req, nil
	}
	out := req
	out.Platform = acc.Platform
	switch req.Path {
	case "/v1/chat/completions":
		switch acc.Platform {
		case model.PlatformOpenAI, model.PlatformGrok, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek:
			return out, nil
		case model.PlatformAnthropic:
			converted, modelName, stream, err := openAIChatToAnthropic(req.Body)
			if err != nil {
				return out, err
			}
			resolved, err := g.resolveModel(acc.Platform, modelName)
			if err != nil {
				return out, err
			}
			converted["model"] = resolved.UpstreamModel
			out.Body, err = json.Marshal(converted)
			out.Path, out.Stream, out.ResponseAdapter = "/v1/messages", stream, adapterAnthropicToOpenAIChat
			return out, err
		case model.PlatformGemini:
			converted, modelName, stream, err := openAIChatToGemini(req.Body)
			if err != nil {
				return out, err
			}
			resolved, err := g.resolveModel(acc.Platform, modelName)
			if err != nil {
				return out, err
			}
			out.Body, err = json.Marshal(converted)
			method := "generateContent"
			if stream {
				method = "streamGenerateContent"
			}
			out.Path = "/v1beta/models/" + resolved.UpstreamModel + ":" + method
			if stream {
				out.Path += "?alt=sse"
			}
			out.Stream, out.ResponseAdapter = stream, adapterGeminiToOpenAIChat
			return out, err
		}
	case "/v1/messages":
		switch acc.Platform {
		case model.PlatformAnthropic, model.PlatformKimi, model.PlatformZhipu, model.PlatformDeepSeek:
			return out, nil
		case model.PlatformOpenAI, model.PlatformGrok:
			converted, modelName, stream, err := anthropicMessagesToOpenAIResponses(req.Body)
			if err != nil {
				return out, err
			}
			resolved, err := g.resolveModel(acc.Platform, modelName)
			if err != nil {
				return out, err
			}
			converted["model"] = resolved.UpstreamModel
			out.Body, err = json.Marshal(converted)
			out.Path, out.Stream, out.ResponseAdapter = "/v1/responses", stream, adapterOpenAIResponsesToAnthropic
			return out, err
		}
	case "/v1/responses":
		switch acc.Platform {
		case model.PlatformOpenAI, model.PlatformGrok, model.PlatformDeepSeek:
			return out, nil
		case model.PlatformAnthropic:
			converted, modelName, stream, err := openAIResponsesToAnthropic(req.Body)
			if err != nil {
				return out, err
			}
			resolved, err := g.resolveModel(acc.Platform, modelName)
			if err != nil {
				return out, err
			}
			converted["model"] = resolved.UpstreamModel
			out.Body, err = json.Marshal(converted)
			out.Path, out.Stream, out.ResponseAdapter = "/v1/messages", stream, adapterAnthropicToOpenAIResponses
			return out, err
		}
	}
	return out, fmt.Errorf("provider %s cannot serve %s in a composite group", acc.Platform, req.Path)
}

// relayAnthropicViaResponses makes an OpenAI-Responses-compatible group
// (OpenAI/Codex or Grok) usable from Claude Code. The upstream platform stays
// the real one for scheduler selection and usage billing; only the public
// Messages request and response are translated.
func (g *Gateway) relayAnthropicViaResponses(c *gin.Context, ak *authedKey, body []byte, platform string) {
	converted, modelName, stream, err := anthropicMessagesToOpenAIResponses(body)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	resolved, err := g.resolveModel(platform, modelName)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	converted["model"] = resolved.UpstreamModel
	effort := applyOpenAIResponsesReasoningPolicy(converted, ak.Key.ReasoningEffort, ak.Group)
	encoded, err := json.Marshal(converted)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "convert Anthropic request failed")
		return
	}
	encoded, err = normalizeOpenAIResponsesRequest(encoded, c.Request.Header)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "normalize Anthropic tools failed")
		return
	}
	g.relay(c, ak, relayRequest{
		Platform:        platform,
		Path:            "/v1/responses",
		Model:           modelName,
		Stream:          stream,
		Effort:          effort,
		ResponseAdapter: adapterOpenAIResponsesToAnthropic,
		Body:            encoded,
		Billable:        true,
	})
}

// relayOpenAIViaAnthropic makes an Anthropic group available through both the
// OpenAI Chat Completions and Responses contracts. This is also what lets the
// Codex CLI use a Claude account group without changing the caller's model
// gateway URL.
func (g *Gateway) relayOpenAIViaAnthropic(c *gin.Context, ak *authedKey, converted map[string]any, modelName string, stream bool, adapter responseAdapter) {
	resolved, err := g.resolveModel(model.PlatformAnthropic, modelName)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	converted["model"] = resolved.UpstreamModel
	encoded, err := json.Marshal(converted)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "convert OpenAI request failed")
		return
	}
	g.relay(c, ak, relayRequest{
		Platform:        model.PlatformAnthropic,
		Path:            "/v1/messages",
		Model:           modelName,
		Stream:          stream,
		ResponseAdapter: adapter,
		Body:            encoded,
		Billable:        true,
	})
}

// relayOpenAIViaGemini makes a Gemini group available through the OpenAI Chat
// Completions contract. The public model name is preserved for billing while
// the upstream generateContent path uses the resolved provider model.
func (g *Gateway) relayOpenAIViaGemini(c *gin.Context, ak *authedKey, converted map[string]any, modelName string, stream bool) {
	resolved, err := g.resolveModel(model.PlatformGemini, modelName)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "convert OpenAI request failed")
		return
	}
	method := "generateContent"
	path := "/v1beta/models/" + resolved.UpstreamModel + ":" + method
	if stream {
		method = "streamGenerateContent"
		path = "/v1beta/models/" + resolved.UpstreamModel + ":" + method + "?alt=sse"
	}
	g.relay(c, ak, relayRequest{
		Platform:        model.PlatformGemini,
		Path:            path,
		Model:           modelName,
		Stream:          stream,
		ResponseAdapter: adapterGeminiToOpenAIChat,
		Body:            encoded,
		Billable:        true,
	})
}

func estimateBridgeTokens(body []byte) int {
	// This is deliberately only used for the compatibility-only count_tokens
	// endpoint. The actual call is billed from provider-reported usage.
	characters := len([]rune(string(body)))
	if characters < 4 {
		return 1
	}
	return (characters + 3) / 4
}

// handleOpenAIImageGeneration mirrors the Images API. A configured image
// model can select a dedicated OpenAI account pool while retaining the normal
// retry, OAuth, and accounting behavior.
func (g *Gateway) handleOpenAIImageGeneration(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	fields := peekJSON(body)
	if fields == nil {
		util.Fail(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	requestedModel := jsonString(fields["model"])
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok) {
		util.Fail(c, http.StatusBadRequest, "this key has no image-capable group")
		return
	}
	platform := ak.Group.Platform
	defaultModel := "gpt-image-2"
	if platform == model.PlatformGrok {
		defaultModel = "grok-imagine-image"
	}
	modelName, imageGroupID, body, ok := g.rewriteJSONModel(c, platform, fields, body, defaultModel)
	if !ok {
		return
	}
	g.relay(c, ak, relayRequest{Platform: platform, Path: "/v1/images/generations", Model: modelName, Body: body, Billable: true, Image: true, UpstreamGroupID: imageGroupID})
}

// handleOpenAIImageEdit preserves multipart image uploads while rewriting a
// configured public model alias before the request reaches the provider.
func (g *Gateway) handleOpenAIImageEdit(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	if !ak.selectGroup(model.PlatformOpenAI) {
		util.Fail(c, http.StatusBadRequest, "this key has no OpenAI image group")
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	modelName, imageGroupID, rewritten, contentType, err := g.rewriteMultipartModel(c.GetHeader("Content-Type"), body, "gpt-image-2")
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	g.relay(c, ak, relayRequest{Platform: model.PlatformOpenAI, Path: "/v1/images/edits", Model: modelName, Body: rewritten, ContentType: contentType, Billable: true, Image: true, UpstreamGroupID: imageGroupID})
}

func (g *Gateway) rewriteJSONModel(c *gin.Context, platform string, fields map[string]json.RawMessage, body []byte, fallback string) (string, int64, []byte, bool) {
	name := jsonString(fields["model"])
	if name == "" {
		name = fallback
	}
	if name == "" {
		util.Fail(c, http.StatusBadRequest, "model is required")
		return "", 0, nil, false
	}
	resolved, err := g.resolveModel(platform, name)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return "", 0, nil, false
	}
	if jsonString(fields["model"]) != resolved.UpstreamModel {
		encoded, _ := json.Marshal(resolved.UpstreamModel)
		fields["model"] = encoded
		if patched, err := json.Marshal(fields); err == nil {
			body = patched
		}
	}
	return name, resolved.ImageGroupID, body, true
}

func (g *Gateway) rewriteMultipartModel(contentType string, body []byte, fallback string) (string, int64, []byte, string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return "", 0, nil, "", fmt.Errorf("image edits require multipart/form-data")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var modelName string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, nil, "", fmt.Errorf("invalid multipart body")
		}
		if part.FormName() == "model" {
			value, _ := io.ReadAll(part)
			modelName = string(value)
		}
	}
	if modelName == "" {
		modelName = fallback
	}
	resolved, err := g.resolveModel(model.PlatformOpenAI, modelName)
	if err != nil {
		return "", 0, nil, "", err
	}
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	hadModel := false
	// Re-read the raw body so binary upload parts are copied byte-for-byte.
	reader = multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, nil, "", fmt.Errorf("invalid multipart body")
		}
		outPart, err := writer.CreatePart(part.Header)
		if err != nil {
			return "", 0, nil, "", err
		}
		if part.FormName() == "model" {
			hadModel = true
			_, err = outPart.Write([]byte(resolved.UpstreamModel))
		} else {
			_, err = io.Copy(outPart, part)
		}
		if err != nil {
			return "", 0, nil, "", err
		}
	}
	if !hadModel {
		if err := writer.WriteField("model", resolved.UpstreamModel); err != nil {
			return "", 0, nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", 0, nil, "", err
	}
	return modelName, resolved.ImageGroupID, out.Bytes(), writer.FormDataContentType(), nil
}

// handleGemini serves /v1beta/models/{model}:{method} with optional ?alt=sse.
func (g *Gateway) handleGemini(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	if !ak.selectGroup(model.PlatformGemini) {
		util.Fail(c, http.StatusBadRequest, "this key has no Gemini group")
		return
	}
	body, err := readBody(c)
	if err != nil {
		writeReadBodyError(c, err)
		return
	}
	action := strings.TrimPrefix(c.Param("action"), "/") // "gemini-2.5-pro:streamGenerateContent"
	modelName := action
	if i := strings.LastIndex(action, ":"); i >= 0 {
		modelName = action[:i]
	}
	resolved, err := g.resolveModel(model.PlatformGemini, modelName)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if resolved.UpstreamModel != modelName {
		action = strings.Replace(action, modelName, resolved.UpstreamModel, 1)
	}
	path := "/v1beta/models/" + action
	if q := c.Request.URL.RawQuery; q != "" {
		// Never leak the client's key to upstream via query string.
		values := c.Request.URL.Query()
		values.Del("key")
		if enc := values.Encode(); enc != "" {
			path += "?" + enc
		}
	}
	stream := strings.Contains(action, ":streamGenerateContent")
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformGemini,
		Path:     path,
		Model:    modelName,
		Stream:   stream,
		Body:     body,
		Billable: true,
	})
}

func (g *Gateway) handleListModels(c *gin.Context) {
	ak, ok := g.authenticate(c)
	if !ok {
		return
	}
	groupSet := make(map[int64]model.Group, len(ak.Groups)+1)
	if ak.Group.ID > 0 {
		groupSet[ak.Group.ID] = ak.Group
	}
	for _, group := range ak.Groups {
		groupSet[group.ID] = group
	}
	selectedGroups := make([]model.Group, 0, len(groupSet))
	requestedPlatform := strings.TrimSpace(c.Query("platform"))
	if requestedPlatform != "" {
		for _, group := range groupSet {
			if group.Platform == requestedPlatform {
				selectedGroups = append(selectedGroups, group)
			}
		}
		if len(selectedGroups) == 0 {
			util.Fail(c, http.StatusForbidden, "platform is not available to this key")
			return
		}
	} else {
		for _, group := range groupSet {
			selectedGroups = append(selectedGroups, group)
		}
	}
	items, err := g.modelsForGroups(c, selectedGroups)
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "upstream model list is unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": items})
}

type upstreamModelItem struct {
	ID      string
	Owner   string
	Created int64
}

func (g *Gateway) modelsForGroups(c *gin.Context, groups []model.Group) ([]gin.H, error) {
	if len(groups) == 0 {
		return []gin.H{}, nil
	}
	groupIDs := make([]int64, 0, len(groups))
	groupByID := make(map[int64]model.Group, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
		groupByID[group.ID] = group
	}

	var accounts []model.UpstreamAccount
	accountIDs := g.db.Model(&model.UpstreamAccountGroup{}).
		Select("upstream_account_id").Where("group_id IN ?", groupIDs)
	if err := g.db.Preload("Proxy").
		Where("auth_type IN ? AND (group_id IN ? OR id IN (?))", []string{"", model.AuthAPIKey}, groupIDs, accountIDs).
		Find(&accounts).Error; err != nil {
		return nil, err
	}

	accountGroups := make(map[int64]map[int64]struct{}, len(accounts))
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		ids = append(ids, account.ID)
		accountGroups[account.ID] = map[int64]struct{}{}
		if _, selected := groupByID[account.GroupID]; selected {
			accountGroups[account.ID][account.GroupID] = struct{}{}
		}
	}
	if len(ids) > 0 {
		var links []model.UpstreamAccountGroup
		if err := g.db.Where("upstream_account_id IN ? AND group_id IN ?", ids, groupIDs).Find(&links).Error; err != nil {
			return nil, err
		}
		for _, link := range links {
			accountGroups[link.UpstreamAccountID][link.GroupID] = struct{}{}
		}
	}

	accountsByGroup := make(map[int64][]*model.UpstreamAccount, len(groups))
	for i := range accounts {
		account := &accounts[i]
		for groupID := range accountGroups[account.ID] {
			group := groupByID[groupID]
			if group.Platform == model.PlatformComposite || group.Platform == account.Platform {
				accountsByGroup[groupID] = append(accountsByGroup[groupID], account)
			}
		}
	}

	discovered := make(map[string]upstreamModelItem)
	localPlatforms := make(map[string]struct{})
	fetched := make(map[int64][]upstreamModelItem, len(accounts))
	fetchErrors := make(map[int64]error, len(accounts))
	now := time.Now().UTC()
	staticGroupCount := 0
	staticGroupSuccesses := 0
	for _, group := range groups {
		groupAccounts := accountsByGroup[group.ID]
		if len(groupAccounts) == 0 {
			localPlatforms[group.Platform] = struct{}{}
			continue
		}
		staticGroupCount++
		groupSucceeded := false
		for _, account := range groupAccounts {
			if account.Status != model.StatusActive || (account.CooldownUntil != nil && account.CooldownUntil.After(now)) {
				continue
			}
			models, fetchedBefore := fetched[account.ID]
			fetchErr, failedBefore := fetchErrors[account.ID]
			if !fetchedBefore && !failedBefore {
				models, fetchErr = g.fetchUpstreamModels(c, account)
				if fetchErr != nil {
					fetchErrors[account.ID] = fetchErr
				} else {
					fetched[account.ID] = models
				}
			}
			if fetchErr != nil {
				continue
			}
			groupSucceeded = true
			for _, item := range models {
				discovered[item.ID] = item
			}
		}
		if groupSucceeded {
			staticGroupSuccesses++
		}
	}

	if len(localPlatforms) > 0 {
		platforms := make([]string, 0, len(localPlatforms))
		for platform := range localPlatforms {
			platforms = append(platforms, platform)
		}
		var configs []model.ModelConfig
		if err := g.db.Where("platform IN ? AND status = ?", platforms, model.StatusActive).Find(&configs).Error; err != nil {
			return nil, err
		}
		for _, config := range configs {
			id := strings.TrimSpace(config.Name)
			if id != "" {
				discovered[id] = upstreamModelItem{ID: id, Owner: config.Platform}
			}
		}
	}
	if staticGroupCount > 0 && staticGroupSuccesses == 0 && len(discovered) == 0 {
		return nil, fmt.Errorf("no upstream model catalogue is available")
	}

	modelItems := make([]upstreamModelItem, 0, len(discovered))
	for _, item := range discovered {
		modelItems = append(modelItems, item)
	}
	sort.Slice(modelItems, func(i, j int) bool {
		if modelItems[i].ID == modelItems[j].ID {
			return modelItems[i].Owner < modelItems[j].Owner
		}
		return modelItems[i].ID < modelItems[j].ID
	})
	result := make([]gin.H, 0, len(modelItems))
	for _, item := range modelItems {
		entry := gin.H{"id": item.ID, "object": "model", "owned_by": item.Owner}
		if item.Created > 0 {
			entry["created"] = item.Created
		}
		result = append(result, entry)
	}
	return result, nil
}

func (g *Gateway) fetchUpstreamModels(c *gin.Context, account *model.UpstreamAccount) ([]upstreamModelItem, error) {
	endpoint, err := upstreamModelsURL(account)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if account.Platform == model.PlatformAnthropic {
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	if err := g.applyCredential(c, request, account, account.Platform); err != nil {
		return nil, err
	}
	client, err := g.clientFor(account)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		return nil, fmt.Errorf("upstream models returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	items, err := parseUpstreamModels(body, account.Platform)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("upstream model list is empty")
	}
	return items, nil
}

func upstreamModelsURL(account *model.UpstreamAccount) (string, error) {
	if account == nil {
		return "", fmt.Errorf("upstream account is required")
	}
	base := strings.TrimSpace(account.BaseURL)
	if base == "" {
		switch account.Platform {
		case model.PlatformAnthropic:
			base = defaultAnthropic
		case model.PlatformOpenAI:
			base = defaultOpenAI
		case model.PlatformGemini:
			base = defaultGemini
		case model.PlatformGrok:
			base = defaultGrok
		case model.PlatformKimi:
			base = defaultKimiPayG
			if account.AccountMode == model.AccountModeCoding {
				base = defaultKimiCoding
			}
		case model.PlatformZhipu:
			base = defaultZhipuPayG
			if account.AccountMode == model.AccountModeCoding {
				base = defaultZhipuCoding
			}
		case model.PlatformDeepSeek:
			base = defaultDeepSeek
		default:
			return "", fmt.Errorf("unsupported upstream platform %q", account.Platform)
		}
	}
	path := "/v1/models"
	if account.Platform == model.PlatformGemini {
		path = "/v1beta/models"
	} else if account.Platform == model.PlatformZhipu {
		path = "/models"
	}
	return util.JoinUpstreamURL(base, path)
}

func parseUpstreamModels(body []byte, platform string) ([]upstreamModelItem, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	raw := root["data"]
	if len(raw) == 0 {
		raw = root["models"]
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("model list is missing")
	}
	if len(raw) > 0 && raw[0] == '{' {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		raw = envelope["data"]
		if len(raw) == 0 {
			raw = envelope["models"]
		}
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items := make([]upstreamModelItem, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		var id string
		var direct string
		if err := json.Unmarshal(value, &direct); err == nil {
			id = direct
		} else {
			var fields struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Model   string `json:"model"`
				Slug    string `json:"slug"`
				Created int64  `json:"created"`
			}
			if err := json.Unmarshal(value, &fields); err != nil {
				continue
			}
			for _, candidate := range []string{fields.ID, fields.Name, fields.Model, fields.Slug} {
				if strings.TrimSpace(candidate) != "" {
					id = candidate
					break
				}
			}
			id = strings.TrimSpace(id)
			if platform == model.PlatformGemini {
				id = strings.TrimPrefix(id, "models/")
			}
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, upstreamModelItem{ID: id, Owner: platform, Created: fields.Created})
			continue
		}
		id = strings.TrimSpace(id)
		if platform == model.PlatformGemini {
			id = strings.TrimPrefix(id, "models/")
		}
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		items = append(items, upstreamModelItem{ID: id, Owner: platform})
	}
	return items, nil
}

// handleUsage returns a compact, client-manager friendly view of the API key
// budget. It is intentionally not wrapped in the console API envelope because
// CCSwitch evaluates the JSON directly in its configured extractor script.
func (g *Gateway) handleUsage(c *gin.Context) {
	ak, ok := g.authenticateUsage(c)
	if !ok {
		return
	}

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var dailyUsedMicro int64
	if err := g.db.Model(&model.UsageLog{}).
		Where("api_key_id = ? AND created_at >= ?", ak.Key.ID, dayStart).
		Select("COALESCE(SUM(cost_micro), 0)").Scan(&dailyUsedMicro).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load API key usage failed")
		return
	}

	planName := "余额"
	if ak.AccessActive {
		planName = "有效期套餐"
	} else if ak.User.RemainingRequests > 0 {
		planName = "按次额度"
	}
	c.JSON(http.StatusOK, gin.H{
		"is_active":          true,
		"remaining":          microUSD(ak.User.BalanceMicro),
		"balance":            microUSD(ak.User.BalanceMicro),
		"unit":               "USD",
		"plan_name":          planName,
		"remaining_requests": ak.User.RemainingRequests,
		"quota": gin.H{
			"limit":     microUSD(ak.Key.QuotaMicro),
			"used":      microUSD(ak.Key.QuotaUsedMicro),
			"remaining": microUSD(remainingMicro(ak.Key.QuotaMicro, ak.Key.QuotaUsedMicro)),
		},
		"daily_quota": gin.H{
			"limit":     microUSD(ak.Key.DailyQuotaMicro),
			"used":      microUSD(dailyUsedMicro),
			"remaining": microUSD(remainingMicro(ak.Key.DailyQuotaMicro, dailyUsedMicro)),
		},
	})
}

func microUSD(value int64) float64 { return float64(value) / 1_000_000 }

func remainingMicro(limit, used int64) int64 {
	if limit <= 0 || used >= limit {
		return 0
	}
	return limit - used
}
