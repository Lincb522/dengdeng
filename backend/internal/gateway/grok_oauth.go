package gateway

// Grok OAuth accounts are subscription credentials issued to the Grok CLI.
// The public gateway still exposes OpenAI Chat Completions, but the CLI HTTP
// transport speaks the Responses protocol and requires its client identity
// headers. Keep that provider-specific behavior out of the ordinary API-key
// relay path.

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
)

const (
	grokOAuthClientVersion = "0.2.93"
	grokOAuthUserAgent     = "xai-grok-workspace/0.2.93"
)

func (g *Gateway) forwardGrokOAuthChat(c *gin.Context, acc *model.UpstreamAccount, req relayRequest) (*http.Response, error) {
	body, clientStream, requestedModel, err := chatCompletionsToOAuthResponses(req.Body)
	if err != nil {
		return nil, err
	}
	upstream, err := g.doGrokOAuthResponses(c, acc, body, req.SessionID)
	if upstream, err = requireOAuthSSE(upstream, err); err != nil || upstream.StatusCode >= http.StatusBadRequest {
		return upstream, err
	}
	if clientStream {
		return streamOAuthResponsesAsChat(upstream, requestedModel), nil
	}
	return bufferOAuthResponsesAsChat(upstream, requestedModel)
}

func (g *Gateway) doGrokOAuthResponses(c *gin.Context, acc *model.UpstreamAccount, body []byte, sessionSeed string) (*http.Response, error) {
	token, err := g.oauth.AccessToken(c.Request.Context(), acc)
	if err != nil {
		return nil, fmt.Errorf("grok oauth token: %w", err)
	}
	sessionID := oauthSessionHeader(sessionSeed)
	upstream, err := g.doGrokOAuthResponsesRequest(c, acc, token, body, sessionID)
	if err != nil || upstream == nil || upstream.StatusCode != http.StatusUnauthorized {
		return upstream, err
	}

	// An imported token can be revoked before its recorded expiry. Refresh it
	// once and replay the request, matching the OpenAI OAuth behavior.
	upstream.Body.Close()
	token, err = g.oauth.Refresh(c.Request.Context(), acc)
	if err != nil {
		return oauthJSONResponse(http.StatusUnauthorized, `{"error":{"message":"Grok OAuth session was rejected and could not be refreshed. Import a fresh session."}}`), nil
	}
	return g.doGrokOAuthResponsesRequest(c, acc, token, body, sessionID)
}

func (g *Gateway) doGrokOAuthResponsesRequest(c *gin.Context, acc *model.UpstreamAccount, token string, body []byte, sessionID string) (*http.Response, error) {
	base := grokBaseURL(strings.TrimSpace(acc.BaseURL), model.AuthOAuth)
	upReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, strings.TrimSuffix(base, "/")+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Accept", "application/json, text/event-stream")
	applyGrokOAuthIdentityHeaders(upReq.Header)
	if sessionID != "" {
		upReq.Header.Set("X-Grok-Conv-Id", sessionID)
	}
	client, err := g.clientFor(acc)
	if err != nil {
		return nil, err
	}
	return client.Do(upReq)
}

func applyGrokOAuthIdentityHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("X-XAI-Token-Auth", "xai-grok-cli")
	headers.Set("X-Grok-Client-Version", grokOAuthClientVersion)
	headers.Set("User-Agent", grokOAuthUserAgent)
}
