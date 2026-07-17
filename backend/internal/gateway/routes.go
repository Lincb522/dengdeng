package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
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

	// OpenAI
	r.POST("/v1/chat/completions", g.handleOpenAIChat)
	r.POST("/v1/responses", g.handleOpenAIResponses)
	r.POST("/v1/images/generations", g.handleOpenAIImageGeneration)
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
	// OpenAI/Codex and Grok groups both speak the OpenAI Responses contract,
	// so Claude Code's Messages request is bridged to Responses upstream.
	if ak.Group.Platform == model.PlatformOpenAI || ak.Group.Platform == model.PlatformGrok {
		g.relayAnthropicViaResponses(c, ak, body, ak.Group.Platform)
		return
	}
	g.relay(c, ak, relayRequest{
		Platform: model.PlatformAnthropic,
		Path:     "/v1/messages",
		Model:    jsonString(fields["model"]),
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
	if ak.Group.Platform == model.PlatformAnthropic {
		converted, modelName, stream, err := openAIChatToAnthropic(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		g.relayOpenAIViaAnthropic(c, ak, converted, modelName, stream, adapterAnthropicToOpenAIChat)
		return
	}
	platform := ak.Group.Platform // openai or grok; both are OpenAI-compatible
	modelName, _, body, ok := g.rewriteJSONModel(c, platform, fields, body, "")
	if !ok {
		return
	}
	body, effort := applyOpenAIReasoningDefault(fields, body, ak.Key.ReasoningEffort, openAIReasoningChatCompletions)
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
	if ak.Group.Platform == model.PlatformAnthropic {
		converted, modelName, stream, err := openAIResponsesToAnthropic(body)
		if err != nil {
			util.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		g.relayOpenAIViaAnthropic(c, ak, converted, modelName, stream, adapterAnthropicToOpenAIResponses)
		return
	}
	platform := ak.Group.Platform // openai or grok
	modelName, _, body, ok := g.rewriteJSONModel(c, platform, fields, body, "")
	if !ok {
		return
	}
	body, effort := applyOpenAIReasoningDefault(fields, body, ak.Key.ReasoningEffort, openAIReasoningResponses)
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
	effort := applyOpenAIResponsesReasoningDefault(converted, ak.Key.ReasoningEffort)
	encoded, err := json.Marshal(converted)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "convert Anthropic request failed")
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
	var configs []model.ModelConfig
	if err := g.db.Where("platform = ? AND status = ?", ak.Group.Platform, model.StatusActive).Order("name").Find(&configs).Error; err != nil {
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
