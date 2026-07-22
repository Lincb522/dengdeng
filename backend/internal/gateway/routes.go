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
	// Older quick-setup snippets and some desktop clients accept a provider
	// URL ending in /v1, then append /v1/messages themselves. Keep these
	// aliases so that configuration mistake returns a real API response
	// instead of the SPA index document.
	r.POST("/v1/v1/messages", g.handleAnthropicMessages)
	r.POST("/v1/v1/messages/count_tokens", g.handleAnthropicCountTokens)

	// OpenAI
	r.POST("/v1/chat/completions", g.handleOpenAIChat)
	r.POST("/v1/responses", g.handleOpenAIResponses)
	r.POST("/v1/images/generations", g.handleOpenAIImageGeneration)
	r.POST("/v1/images/generations/async", g.handleOpenAIImageGenerationAsync)
	r.GET("/v1/images/tasks/:task_id", g.handleOpenAIImageTask)
	r.POST("/v1/images/edits", g.handleOpenAIImageEdit)

	// Gemini (native v1beta path style)
	r.POST("/v1beta/models/*action", g.handleGemini)

	// Model listing is answered from the local enabled catalogue. Unlike a
	// generation request, discovering models must not depend on an account being
	// online (otherwise a fresh group can never be configured through an SDK).
	r.GET("/v1/models", g.handleListModels)
	// CCSwitch and similar desktop clients use this authenticated endpoint to
	// display the key's remaining balance and configured caps.
	r.GET("/v1/usage", g.handleUsage)
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
	if !ak.selectGroup(model.PlatformOpenAI) {
		util.Fail(c, http.StatusBadRequest, "this key has no OpenAI image group")
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
	if !g.selectGroupForModel(ak, modelName, model.PlatformAnthropic, model.PlatformOpenAI, model.PlatformGrok) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with Anthropic Messages")
		return
	}
	// OpenAI/Codex and Grok groups both speak the OpenAI Responses contract,
	// so Claude Code's Messages request is bridged to Responses upstream.
	if ak.Group.Platform == model.PlatformOpenAI || ak.Group.Platform == model.PlatformGrok {
		g.relayAnthropicViaResponses(c, ak, body, ak.Group.Platform)
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformAnthropic,
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
	if !g.selectGroupForModel(ak, modelName, model.PlatformAnthropic, model.PlatformOpenAI, model.PlatformGrok) {
		util.Fail(c, http.StatusBadRequest, "this key has no group compatible with Anthropic Messages")
		return
	}
	// OpenAI has no equivalent of Anthropic's token-count endpoint. Claude
	// Code calls it before Messages requests, so return a conservative local
	// estimate for bridged OpenAI/Codex and Grok groups instead of rejecting setup.
	if ak.Group.Platform == model.PlatformOpenAI || ak.Group.Platform == model.PlatformGrok {
		c.JSON(http.StatusOK, gin.H{"input_tokens": estimateBridgeTokens(body)})
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformAnthropic,
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
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok, model.PlatformAnthropic, model.PlatformGemini) {
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
	if !g.selectGroupForModel(ak, requestedModel, model.PlatformOpenAI, model.PlatformGrok, model.PlatformAnthropic) {
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

// openAICompatiblePlatform returns the upstream platform for a request that
// arrived on the OpenAI wire. Only OpenAI-compatible groups (openai, grok)
// pass; Anthropic is bridged before this is called, and anything else (e.g. a
// Gemini group) gets the standard cross-platform rejection instead of having
// an OpenAI-shaped body forwarded to an incompatible upstream.
func openAICompatiblePlatform(c *gin.Context, ak *authedKey) (string, bool) {
	switch ak.Group.Platform {
	case model.PlatformOpenAI, model.PlatformGrok:
		return ak.Group.Platform, true
	default:
		util.Fail(c, http.StatusBadRequest,
			fmt.Sprintf("this key belongs to a %s group and cannot call %s endpoints", ak.Group.Platform, model.PlatformOpenAI))
		return "", false
	}
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
	if !ak.selectGroup(model.PlatformOpenAI) {
		util.Fail(c, http.StatusBadRequest, "this key has no OpenAI image group")
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
	modelName, imageGroupID, body, ok := g.rewriteJSONModel(c, model.PlatformOpenAI, fields, body, "gpt-image-2")
	if !ok {
		return
	}
	g.relay(c, ak, relayRequest{Platform: model.PlatformOpenAI, Path: "/v1/images/generations", Model: modelName, Body: body, Billable: true, Image: true, UpstreamGroupID: imageGroupID})
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
	platformSet := make(map[string]struct{}, len(ak.Groups))
	for _, group := range ak.Groups {
		platformSet[group.Platform] = struct{}{}
	}
	platforms := make([]string, 0, len(platformSet))
	requestedPlatform := strings.TrimSpace(c.Query("platform"))
	if requestedPlatform != "" {
		if _, allowed := platformSet[requestedPlatform]; !allowed {
			util.Fail(c, http.StatusForbidden, "platform is not available to this key")
			return
		}
		platforms = append(platforms, requestedPlatform)
	} else {
		for platform := range platformSet {
			platforms = append(platforms, platform)
		}
	}
	var configs []model.ModelConfig
	if err := g.db.Where("platform IN ? AND status = ?", platforms, model.StatusActive).Order("platform, name").Find(&configs).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load model catalogue failed")
		return
	}
	items := make([]gin.H, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, gin.H{"id": cfg.Name, "object": "model", "owned_by": cfg.Platform})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": items})
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
