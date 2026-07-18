// Package gateway implements the relay core: client API-key auth, upstream
// account selection with failover, streaming passthrough and usage capture.
package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// Keep the relay limit aligned with the public Nginx limit. Codex and
	// Responses clients can send a full tool context or image payload in one
	// JSON request, which routinely exceeds Nginx's default 1 MiB body limit.
	maxBodyBytes     = 64 << 20
	maxAttempts      = 3
	defaultAnthropic = "https://api.anthropic.com"
	defaultOpenAI    = "https://api.openai.com"
	defaultGemini    = "https://generativelanguage.googleapis.com"
	// xAI's REST surface is OpenAI-compatible. The public paths already carry
	// the "/v1" prefix, so the base host must not include it (grokBaseURL
	// trims a trailing /v1 to accept either form an operator enters).
	defaultGrok      = "https://api.x.ai"
	defaultGrokOAuth = "https://cli-chat-proxy.grok.com"
)

var errRequestBodyTooLarge = fmt.Errorf("request body exceeds the %d MiB limit", maxBodyBytes>>20)

type Gateway struct {
	db           *gorm.DB
	scheduler    *service.Scheduler
	billing      *service.BillingService
	rates        *service.UserGroupRateResolver
	oauth        *oauth.Manager
	runtime      *service.RuntimeMetrics
	policy       *service.RuntimePolicyService
	concurrency  *service.ClientConcurrencyLimiter
	client       *http.Client
	proxyClients sync.Map // map[proxy-id:updated-at]*http.Client
	keyWindows   sync.Map // map[api-key-id]*keyRPMWindow
}

type keyRPMWindow struct {
	mu    sync.Mutex
	start time.Time
	count int
}

// SetRuntimePolicy exposes only safe relay controls (attempt count and
// cooldowns). Provider identity and request semantics remain fixed by the
// configured account and are never operator-mutable here.
func (g *Gateway) SetRuntimePolicy(policy *service.RuntimePolicyService) {
	g.policy = policy
}

func (g *Gateway) relayAttempts() int {
	if g != nil && g.policy != nil {
		return g.policy.Current().MaxAttempts
	}
	return maxAttempts
}

func New(db *gorm.DB, scheduler *service.Scheduler, billing *service.BillingService, rates *service.UserGroupRateResolver, oauthManager *oauth.Manager, runtime *service.RuntimeMetrics, client *http.Client) *Gateway {
	if client == nil {
		client = &http.Client{}
	}
	return &Gateway{
		db:          db,
		scheduler:   scheduler,
		billing:     billing,
		rates:       rates,
		oauth:       oauthManager,
		runtime:     runtime,
		concurrency: service.NewClientConcurrencyLimiter(),
		// No global timeout: streaming responses can legitimately run for
		// many minutes. Dial/TLS limits come from DefaultTransport.
		client: client,
	}
}

type resolvedModel struct {
	UpstreamModel string
	ImageGroupID  int64
}

// resolveModel applies an administrator-defined alias. The public name is kept
// for billing while only the upstream request is rewritten. Image models may
// additionally choose a dedicated upstream account pool.
func (g *Gateway) resolveModel(platform, name string) (resolvedModel, error) {
	resolved := resolvedModel{UpstreamModel: name}
	if name == "" {
		return resolved, nil
	}
	var cfg model.ModelConfig
	if err := g.db.Where("name = ? AND platform = ?", name, platform).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resolved, nil
		}
		return resolvedModel{}, err
	}
	if cfg.Status != model.StatusActive {
		return resolvedModel{}, fmt.Errorf("model %s is disabled", name)
	}
	if cfg.UpstreamModel != "" {
		resolved.UpstreamModel = cfg.UpstreamModel
	}
	if cfg.Kind == "image" {
		resolved.ImageGroupID = cfg.ImageGroupID
	}
	return resolved, nil
}

type authedKey struct {
	Key             model.APIKey
	User            model.User
	Group           model.Group
	AccessActive    bool
	RequestReserved bool
}

type authOptions struct {
	consumeRPM         bool
	enforceUsageLimits bool
	touchLastUsed      bool
}

// authenticate resolves the client credential from any of the header styles
// the three official SDK families use.
func (g *Gateway) authenticate(c *gin.Context) (*authedKey, bool) {
	return g.authenticateWithOptions(c, authOptions{consumeRPM: true, enforceUsageLimits: true, touchLastUsed: true})
}

// authenticateUsage verifies ownership and key safety rules without applying
// spend limits or request RPM. A zero-balance or exhausted key must still be
// able to read its own status in a client-side usage panel.
func (g *Gateway) authenticateUsage(c *gin.Context) (*authedKey, bool) {
	return g.authenticateWithOptions(c, authOptions{})
}

func (g *Gateway) authenticateWithOptions(c *gin.Context, options authOptions) (*authedKey, bool) {
	raw := ""
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		raw = strings.TrimPrefix(h, "Bearer ")
	}
	if raw == "" {
		raw = c.GetHeader("x-api-key")
	}
	if raw == "" {
		raw = c.GetHeader("x-goog-api-key")
	}
	if raw == "" {
		raw = c.Query("key")
	}
	if raw == "" {
		util.Fail(c, http.StatusUnauthorized, "missing API key")
		return nil, false
	}

	var key model.APIKey
	err := g.db.Where("key_hash = ?", util.HashAPIKey(strings.TrimSpace(raw))).First(&key).Error
	if err != nil {
		util.Fail(c, http.StatusUnauthorized, "invalid API key")
		return nil, false
	}
	if key.Status != model.StatusActive {
		util.Fail(c, http.StatusForbidden, "API key disabled")
		return nil, false
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
		util.Fail(c, http.StatusForbidden, "API key expired")
		return nil, false
	}
	if !g.allowKeySourceIP(key, c.ClientIP()) {
		util.Fail(c, http.StatusForbidden, "API key source IP is not allowed")
		return nil, false
	}
	if options.consumeRPM && !g.consumeKeyRPM(key) {
		c.Header("Retry-After", "60")
		util.Fail(c, http.StatusTooManyRequests, "API key rate limit reached")
		return nil, false
	}
	if options.enforceUsageLimits && key.QuotaMicro > 0 && key.QuotaUsedMicro >= key.QuotaMicro {
		util.Fail(c, http.StatusPaymentRequired, "API key quota exhausted")
		return nil, false
	}
	if options.enforceUsageLimits && key.DailyQuotaMicro > 0 {
		now := time.Now()
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		var dailyUsed int64
		if err := g.db.Model(&model.UsageLog{}).
			Where("api_key_id = ? AND created_at >= ?", key.ID, dayStart).
			Select("COALESCE(SUM(cost_micro), 0)").Scan(&dailyUsed).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "check API key quota failed")
			return nil, false
		}
		if dailyUsed >= key.DailyQuotaMicro {
			util.Fail(c, http.StatusPaymentRequired, "API key daily quota reached")
			return nil, false
		}
	}

	var user model.User
	if err := g.db.First(&user, key.UserID).Error; err != nil || user.Status != model.StatusActive {
		util.Fail(c, http.StatusForbidden, "user disabled")
		return nil, false
	}
	accessActive := user.AccessExpiresAt != nil && user.AccessExpiresAt.After(time.Now())
	if options.enforceUsageLimits && user.Role != model.RoleAdmin && !accessActive && user.RemainingRequests <= 0 && user.BalanceMicro <= 0 {
		util.Fail(c, http.StatusPaymentRequired, "insufficient balance")
		return nil, false
	}

	var group model.Group
	if err := g.db.First(&group, key.GroupID).Error; err != nil || group.Status != model.StatusActive {
		util.Fail(c, http.StatusForbidden, "group disabled")
		return nil, false
	}

	if options.touchLastUsed {
		go g.db.Model(&model.APIKey{}).Where("id = ?", key.ID).Update("last_used_at", time.Now())
	}
	return &authedKey{Key: key, User: user, Group: group, AccessActive: accessActive}, true
}

func (g *Gateway) allowKeySourceIP(key model.APIKey, sourceIP string) bool {
	blocked, err := util.MatchIPRules(sourceIP, key.BlockedIPs)
	if err != nil || blocked {
		return false
	}
	if strings.TrimSpace(key.AllowedIPs) == "" {
		return true
	}
	allowed, err := util.MatchIPRules(sourceIP, key.AllowedIPs)
	return err == nil && allowed
}

func (g *Gateway) consumeKeyRPM(key model.APIKey) bool {
	if key.RPM <= 0 {
		return true
	}
	value, _ := g.keyWindows.LoadOrStore(key.ID, &keyRPMWindow{})
	window := value.(*keyRPMWindow)
	now := time.Now()
	currentMinute := now.Truncate(time.Minute)
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.start.Before(currentMinute) {
		window.start, window.count = currentMinute, 0
	}
	if window.count >= key.RPM {
		return false
	}
	window.count++
	return true
}

// reserveRequestQuota atomically reserves one request entitlement. Reserving
// before dialing the upstream keeps concurrent calls from spending the same
// final request. relay refunds it when no upstream response succeeds.
func (g *Gateway) reserveRequestQuota(userID int64) bool {
	res := g.db.Model(&model.User{}).
		Where("id = ? AND remaining_requests > 0", userID).
		Update("remaining_requests", gorm.Expr("remaining_requests - 1"))
	return res.Error == nil && res.RowsAffected == 1
}

func (g *Gateway) refundRequestQuota(userID int64) {
	if err := g.db.Model(&model.User{}).Where("id = ?", userID).
		Update("remaining_requests", gorm.Expr("remaining_requests + 1")).Error; err != nil {
		log.Printf("[gateway] failed to refund request quota for user %d: %v", userID, err)
	}
}

type relayRequest struct {
	Platform string // platform this endpoint belongs to
	Path     string // upstream path (incl. query for gemini)
	Model    string // resolved model name for billing
	Stream   bool
	// Effort is the effective OpenAI-wire reasoning effort of this request
	// (client field first, key default second, "" for model default). It
	// selects the per-effort billing multiplier and lands in the usage log.
	Effort string
	// ResponseAdapter presents a different public wire protocol while Platform
	// remains the real upstream protocol used for routing and accounting.
	ResponseAdapter responseAdapter
	Body            []byte
	ContentType     string // optional replacement after multipart model aliasing
	Billable        bool
	Image           bool
	// SessionID is the relay's stable per-conversation identifier (if any). It
	// pins scheduler account selection and seeds the upstream session headers
	// that make OAuth traffic look like a continuous client session.
	SessionID string
	// UpstreamGroupID is only accepted for image requests. It lets a public
	// image model use an account pool that is separate from the API key group.
	UpstreamGroupID int64
}

type relayTrace struct {
	QueueMs      int64
	ScheduleMs   int64
	UpstreamMs   int64
	AttemptCount int
}

// effortRates applies the operator-configured per-effort billing multiplier
// on top of the request's rate plan. Image pricing is left untouched: image
// generation has no reasoning phase.
func (g *Gateway) effortRates(rates service.RatePlan, effort string) service.RatePlan {
	if effort == "" || g.policy == nil {
		return rates
	}
	multiplier := g.policy.Current().EffortMultiplier(effort)
	if multiplier == 1 {
		return rates
	}
	rates.Base *= multiplier
	rates.CacheRead *= multiplier
	rates.CacheWrite5m *= multiplier
	rates.CacheWrite1h *= multiplier
	return rates
}

// relaySessionID extracts only explicit, client-provided conversation
// identifiers. It deliberately avoids hashing the prompt body: two unrelated
// callers saying the same thing must never be treated as one session.
func relaySessionID(c *gin.Context, apiKeyID int64, body []byte) string {
	if c == nil || apiKeyID <= 0 {
		return ""
	}
	for _, name := range []string{"X-Session-ID", "Session-ID", "X-Conversation-ID", "X-Client-Request-ID"} {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			return strconv.FormatInt(apiKeyID, 10) + ":" + value
		}
	}
	if len(body) == 0 || !json.Valid(body) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, path := range [][]string{
		{"conversation_id"},
		{"session_id"},
		{"prompt_cache_key"},
		{"conversation", "id"},
		{"metadata", "session_id"},
		{"metadata", "user_id"},
	} {
		if value := jsonStringPath(payload, path...); value != "" {
			return strconv.FormatInt(apiKeyID, 10) + ":" + value
		}
	}
	return ""
}

func jsonStringPath(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = object[key]
		if !ok {
			return ""
		}
	}
	value, _ := current.(string)
	return strings.TrimSpace(value)
}

// relay runs the account failover loop and, on success, streams the response
// while capturing usage for billing.
func (g *Gateway) relay(c *gin.Context, ak *authedKey, req relayRequest) {
	if ak.Group.Platform != req.Platform {
		util.Fail(c, http.StatusBadRequest,
			fmt.Sprintf("this key belongs to a %s group and cannot call %s endpoints", ak.Group.Platform, req.Platform))
		return
	}
	routeGroup := ak.Group
	if req.UpstreamGroupID > 0 {
		if !req.Image {
			util.Fail(c, http.StatusBadRequest, "dedicated upstream groups are only supported for image requests")
			return
		}
		if err := g.db.First(&routeGroup, req.UpstreamGroupID).Error; err != nil || routeGroup.Status != model.StatusActive {
			util.Fail(c, http.StatusServiceUnavailable, "configured image upstream group is unavailable")
			return
		}
		if routeGroup.Platform != req.Platform {
			util.Fail(c, http.StatusBadRequest, "configured image upstream group has a different platform")
			return
		}
	}
	activeRequest := g.runtime.Begin(req.Platform, routeGroup.ID, ak.User.ID)
	defer activeRequest.Finish()
	start := time.Now()
	trace := relayTrace{}
	runtimePolicy := service.DefaultGatewayRuntimePolicy()
	if g.policy != nil {
		runtimePolicy = g.policy.Current()
	}
	concurrencyWait := time.Duration(runtimePolicy.ConcurrencyWaitMilliseconds) * time.Millisecond
	activeRequest.SetWaiting(true)
	lease, waited, err := g.concurrency.Acquire(
		c.Request.Context(),
		ak.User.ID,
		ak.User.Concurrency,
		ak.Key.ID,
		ak.Key.Concurrency,
		concurrencyWait,
		runtimePolicy.ConcurrencyQueueDepth,
	)
	activeRequest.SetWaiting(false)
	trace.QueueMs += waited.Milliseconds()
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		c.Header("Retry-After", "1")
		g.setRelayTimingHeaders(c, trace)
		g.recordRelayFailure(c, ak, routeGroup, req, start, trace, http.StatusTooManyRequests, "concurrency limit reached")
		util.Fail(c, http.StatusTooManyRequests, "concurrency limit reached; retry later")
		return
	}
	defer lease.Release()

	// Day passes take precedence. Otherwise, use a request entitlement before
	// falling back to the cash balance. This is deliberately done only after
	// endpoint/body validation and group checks have succeeded.
	completed := false
	if req.Billable && ak.User.Role != model.RoleAdmin && !ak.AccessActive {
		if g.reserveRequestQuota(ak.User.ID) {
			ak.RequestReserved = true
			defer func() {
				if !completed {
					g.refundRequestQuota(ak.User.ID)
				}
			}()
		} else {
			var current model.User
			if err := g.db.Select("balance_micro", "access_expires_at").First(&current, ak.User.ID).Error; err != nil {
				util.Fail(c, http.StatusUnauthorized, "user unavailable")
				return
			}
			if current.AccessExpiresAt != nil && current.AccessExpiresAt.After(time.Now()) {
				ak.AccessActive = true
			} else if current.BalanceMicro <= 0 {
				util.Fail(c, http.StatusPaymentRequired, "insufficient balance")
				return
			}
		}
	}

	var tried []int64
	var lastStatus int
	var lastBody []byte
	sessionID := relaySessionID(c, ak.Key.ID, req.Body)
	req.SessionID = sessionID

	for attempt := 0; attempt < g.relayAttempts(); attempt++ {
		if c.Request.Context().Err() != nil {
			return
		}
		scheduleStarted := time.Now()
		activeRequest.SetWaiting(true)
		acc, queuedForAccount, err := g.scheduler.PickForSessionWait(
			c.Request.Context(), routeGroup.ID, req.Model, sessionID, tried,
			concurrencyWait, runtimePolicy.ConcurrencyQueueDepth,
		)
		activeRequest.SetWaiting(false)
		scheduleElapsed := time.Since(scheduleStarted)
		trace.QueueMs += queuedForAccount.Milliseconds()
		if scheduling := scheduleElapsed - queuedForAccount; scheduling > 0 {
			trace.ScheduleMs += scheduling.Milliseconds()
		}
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if errors.Is(err, service.ErrAccountQueueFull) || errors.Is(err, service.ErrAccountWaitTimeout) || errors.Is(err, service.ErrAccountConcurrencyBusy) {
				c.Header("Retry-After", "1")
				g.setRelayTimingHeaders(c, trace)
				g.recordRelayFailure(c, ak, routeGroup, req, start, trace, http.StatusTooManyRequests, "upstream account concurrency limit reached")
				util.Fail(c, http.StatusTooManyRequests, "upstream accounts are busy; retry later")
				return
			}
			if errors.Is(err, service.ErrNoAccount) && lastStatus != 0 {
				break // fall through to lastStatus passthrough
			}
			if errors.Is(err, service.ErrNoAccount) {
				g.setRelayTimingHeaders(c, trace)
				g.recordRelayFailure(c, ak, routeGroup, req, start, trace, http.StatusServiceUnavailable, "no available upstream account in this group")
				util.Fail(c, http.StatusServiceUnavailable, "no available upstream account in this group")
				return
			}
			util.Fail(c, http.StatusInternalServerError, "scheduler error")
			return
		}
		tried = append(tried, acc.ID)
		trace.AttemptCount++
		activeRequest.SetAccount(acc.ID)

		upstreamStarted := time.Now()
		resp, err := g.forward(c, acc, req)
		attemptUpstreamMs := time.Since(upstreamStarted).Milliseconds()
		trace.UpstreamMs += attemptUpstreamMs
		if err != nil {
			g.scheduler.Release(acc.ID)
			if c.Request.Context().Err() != nil {
				return
			}
			log.Printf("[gateway] account %d network error: %v", acc.ID, err)
			g.scheduler.ReportFailure(acc.ID, 0, err.Error())
			lastStatus, lastBody = http.StatusBadGateway, []byte(`{"error":{"message":"upstream connection failed"}}`)
			continue
		}

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			g.scheduler.Release(acc.ID)
			lastStatus, lastBody = resp.StatusCode, body
			if retryableUpstream(resp.StatusCode, body) {
				g.scheduler.ReportFailureForModel(acc.ID, req.Model, resp.StatusCode, string(body))
				continue
			}
			break
		}

		// Success: stream through and bill afterwards.
		g.scheduler.ReportSuccessForModelWithLatency(acc.ID, req.Model, attemptUpstreamMs)
		g.setRelayTimingHeaders(c, trace)
		usage, streamed := g.pipeAdapted(c, resp, req.Platform, req.Image, req.ResponseAdapter, req.Model)
		resp.Body.Close()
		g.scheduler.Release(acc.ID)

		if req.Billable {
			g.billing.Record(service.BillContext{
				RequestID:    middleware.RequestIDFromContext(c),
				UserID:       ak.User.ID,
				APIKeyID:     ak.Key.ID,
				AccountID:    acc.ID,
				GroupID:      routeGroup.ID,
				Model:        req.Model,
				Stream:       streamed,
				Effort:       req.Effort,
				Usage:        usage,
				Rates:        g.effortRates(billingRates(ak.User, routeGroup, g.rates.Resolve(ak.User.ID, routeGroup.ID, routeGroup.RateMultiplier)), req.Effort),
				DurationMs:   time.Since(start).Milliseconds(),
				QueueMs:      trace.QueueMs,
				ScheduleMs:   trace.ScheduleMs,
				UpstreamMs:   trace.UpstreamMs,
				AttemptCount: trace.AttemptCount,
				StatusCode:   resp.StatusCode,
				SkipBalance:  ak.AccessActive || ak.RequestReserved,
			})
		}
		completed = true
		return
	}

	// All attempts failed: pass the last upstream error to the client.
	if lastStatus == 0 {
		lastStatus, lastBody = http.StatusServiceUnavailable, []byte(`{"error":{"message":"no available upstream account"}}`)
	}
	g.setRelayTimingHeaders(c, trace)
	g.recordRelayFailure(c, ak, routeGroup, req, start, trace, lastStatus, truncate(string(lastBody), 500))
	c.Data(lastStatus, "application/json", lastBody)
}

// recordRelayFailure persists an authenticated, billable request that never
// produced an upstream success. This is intentionally cost-neutral, but makes
// scheduler exhaustion and final upstream failures visible in the same ledger
// that powers monitoring and user-side troubleshooting.
func (g *Gateway) recordRelayFailure(c *gin.Context, ak *authedKey, group model.Group, req relayRequest, started time.Time, trace relayTrace, status int, message string) {
	if !req.Billable || g.billing == nil {
		return
	}
	g.billing.Record(service.BillContext{
		RequestID:    middleware.RequestIDFromContext(c),
		UserID:       ak.User.ID,
		APIKeyID:     ak.Key.ID,
		GroupID:      group.ID,
		Model:        req.Model,
		Stream:       false,
		Effort:       req.Effort,
		Rates:        billingRates(ak.User, group, g.rates.Resolve(ak.User.ID, group.ID, group.RateMultiplier)),
		DurationMs:   time.Since(started).Milliseconds(),
		QueueMs:      trace.QueueMs,
		ScheduleMs:   trace.ScheduleMs,
		UpstreamMs:   trace.UpstreamMs,
		AttemptCount: trace.AttemptCount,
		StatusCode:   status,
		ErrorMessage: message,
		SkipBalance:  true,
	})
}

func (g *Gateway) setRelayTimingHeaders(c *gin.Context, trace relayTrace) {
	if c == nil {
		return
	}
	c.Header("Server-Timing", fmt.Sprintf("queue;dur=%d, route;dur=%d, upstream;dur=%d", trace.QueueMs, trace.ScheduleMs, trace.UpstreamMs))
	c.Header("X-DengDeng-Upstream-Attempts", strconv.Itoa(trace.AttemptCount))
}

func normalizedMultiplier(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}

// billingRates converts the group configuration into a request-local pricing
// snapshot. User-level pricing remains a top-level multiplier, while a group
// can tune cache hit, 5m creation and 1h creation independently. This avoids
// a later admin edit changing an already completed usage entry's semantics.
func billingRates(user model.User, group model.Group, groupRate float64) service.RatePlan {
	userRate := normalizedMultiplier(user.RateMultiplier)
	base := userRate * normalizedMultiplier(groupRate)
	image := base
	if group.ImageRateIndependent {
		image = userRate * normalizedMultiplier(group.ImageRateMultiplier)
	}
	return service.RatePlan{
		Base:         base,
		CacheRead:    base * normalizedMultiplier(group.CacheReadMultiplier),
		CacheWrite5m: base * normalizedMultiplier(group.CacheWrite5mMultiplier),
		CacheWrite1h: base * normalizedMultiplier(group.CacheWrite1hMultiplier),
		Image:        image,
	}
}

func retryableUpstream(status int, body []byte) bool {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusNotFound,
		status == http.StatusMethodNotAllowed,
		status == http.StatusRequestTimeout,
		status == http.StatusConflict,
		status == http.StatusRequestEntityTooLarge,
		status == http.StatusTooEarly,
		status == http.StatusTooManyRequests,
		status >= http.StatusInternalServerError:
		return true
	case status != http.StatusBadRequest && status != http.StatusUnprocessableEntity:
		return false
	}
	// A generic malformed client request should be returned immediately. Only
	// retry 400/422 responses that identify an account/model capability mismatch.
	message := strings.ToLower(string(body))
	for _, marker := range []string{
		"model_not_found", "unsupported model", "model is not supported",
		"model not supported", "model is not available", "model unavailable",
		"not supported when using codex", "does not support image",
		"does not support this model", "unsupported endpoint",
		"capability is not available", "capability not supported",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// forward builds and executes the upstream request for one account.
func (g *Gateway) forward(c *gin.Context, acc *model.UpstreamAccount, req relayRequest) (*http.Response, error) {
	// An OpenAI OAuth credential is a ChatGPT/Codex subscription credential,
	// not an API Platform key. It has a separate Responses-shaped upstream and
	// needs protocol adaptation before it can serve the public OpenAI APIs.
	if req.Platform == model.PlatformOpenAI && acc.AuthType == model.AuthOAuth {
		return g.forwardOpenAIOAuth(c, acc, req)
	}

	base := strings.TrimSuffix(acc.BaseURL, "/")
	if req.Platform == model.PlatformGrok {
		base = grokBaseURL(base, acc.AuthType)
	} else if base == "" {
		switch req.Platform {
		case model.PlatformAnthropic:
			base = defaultAnthropic
		case model.PlatformOpenAI:
			base = defaultOpenAI
		case model.PlatformGemini:
			base = defaultGemini
		}
	}

	// A Claude subscription OAuth credential is only authorized for Claude
	// Code. Ensure the request carries the Claude Code identity so the upstream
	// accepts it and the traffic matches the official CLI. API-key accounts are
	// left untouched.
	outboundBody := req.Body
	if req.Platform == model.PlatformAnthropic && acc.AuthType == model.AuthOAuth && req.Path == "/v1/messages" {
		outboundBody = injectClaudeCodeSystemPrompt(outboundBody)
	}

	upReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, base+req.Path, bytes.NewReader(outboundBody))
	if err != nil {
		return nil, err
	}

	// Copy protocol headers, never the client's credentials. Anthropic-only
	// headers must not leak onto an OpenAI request produced by the bridge.
	headers := []string{"Content-Type", "Accept", "x-stainless-helper"}
	if req.Platform == model.PlatformAnthropic {
		headers = append(headers, "anthropic-version", "anthropic-beta")
	}
	for _, h := range headers {
		if v := c.GetHeader(h); v != "" {
			upReq.Header.Set(h, v)
		}
	}
	if upReq.Header.Get("Content-Type") == "" && len(req.Body) > 0 {
		upReq.Header.Set("Content-Type", "application/json")
	}
	if req.ContentType != "" {
		upReq.Header.Set("Content-Type", req.ContentType)
	}
	if req.Platform == model.PlatformAnthropic && upReq.Header.Get("anthropic-version") == "" {
		upReq.Header.Set("anthropic-version", "2023-06-01")
	}

	if err := g.applyCredential(c, upReq, acc, req.Platform); err != nil {
		return nil, err
	}
	client, err := g.clientFor(acc)
	if err != nil {
		return nil, err
	}
	return client.Do(upReq)
}

func (g *Gateway) clientFor(acc *model.UpstreamAccount) (*http.Client, error) {
	if acc.ProxyID == 0 {
		return g.client, nil
	}
	item := acc.Proxy
	if item == nil || item.ID != acc.ProxyID {
		item = &model.Proxy{}
		if err := g.db.First(item, acc.ProxyID).Error; err != nil {
			return nil, fmt.Errorf("assigned proxy is unavailable")
		}
	}
	if item.Status != model.StatusActive {
		return nil, fmt.Errorf("assigned proxy is disabled")
	}
	proxyURL, err := item.URL()
	if err != nil {
		return nil, fmt.Errorf("assigned proxy is invalid: %w", err)
	}
	cacheKey := fmt.Sprintf("%d:%d", item.ID, item.UpdatedAt.UnixNano())
	if cached, ok := g.proxyClients.Load(cacheKey); ok {
		return cached.(*http.Client), nil
	}
	client, err := config.NewProxyHTTPClient(proxyURL, "", 0)
	if err != nil {
		return nil, err
	}
	actual, _ := g.proxyClients.LoadOrStore(cacheKey, client)
	return actual.(*http.Client), nil
}

// applyCredential attaches the upstream auth headers for an account, handling
// both static API keys and auto-renewed OAuth bearer tokens.
func (g *Gateway) applyCredential(c *gin.Context, upReq *http.Request, acc *model.UpstreamAccount, platform string) error {
	if acc.AuthType == model.AuthOAuth {
		token, err := g.oauth.AccessToken(c.Request.Context(), acc)
		if err != nil {
			return fmt.Errorf("oauth token: %w", err)
		}
		upReq.Header.Set("Authorization", "Bearer "+token)
		switch platform {
		case model.PlatformAnthropic:
			// OAuth calls use the beta bearer flow, not the x-api-key flow.
			// The token is a Claude Code credential: attach the CLI identity
			// headers and mandatory beta flags so the upstream accepts it and
			// the request is indistinguishable from the official client.
			upReq.Header.Del("x-api-key")
			for _, flag := range strings.Split(anthropicOAuthBeta, ",") {
				upReq.Header.Set("anthropic-beta", mergeBeta(upReq.Header.Get("anthropic-beta"), flag))
			}
			applyAnthropicOAuthIdentityHeaders(upReq.Header)
		case model.PlatformOpenAI:
			if acc.AccountID != "" {
				upReq.Header.Set("chatgpt-account-id", acc.AccountID)
			}
		}
		return nil
	}

	apiKey := string(acc.APIKey)
	switch platform {
	case model.PlatformAnthropic:
		upReq.Header.Set("x-api-key", apiKey)
	case model.PlatformOpenAI, model.PlatformGrok:
		upReq.Header.Set("Authorization", "Bearer "+apiKey)
	case model.PlatformGemini:
		upReq.Header.Set("x-goog-api-key", apiKey)
	}
	return nil
}

// grokBaseURL resolves the upstream host for a Grok account. xAI API keys hit
// api.x.ai; subscription OAuth tokens use the Grok CLI proxy. A trailing /v1
// on an operator-entered base is dropped because the relay path already
// includes it (otherwise the URL would contain /v1/v1).
func grokBaseURL(base, authType string) string {
	if base == "" {
		if authType == model.AuthOAuth {
			return defaultGrokOAuth
		}
		return defaultGrok
	}
	return strings.TrimSuffix(base, "/v1")
}

// mergeBeta appends a beta flag without dropping any the client already sent.
func mergeBeta(existing, want string) string {
	if existing == "" {
		return want
	}
	for _, f := range strings.Split(existing, ",") {
		if strings.TrimSpace(f) == want {
			return existing
		}
	}
	return existing + "," + want
}

// pipe copies the upstream response to the client while feeding a usage
// extractor. Returns captured usage and whether the response was SSE.
func (g *Gateway) pipe(c *gin.Context, resp *http.Response, platform string, image bool) (service.Usage, bool) {
	contentType := resp.Header.Get("Content-Type")
	isStream := strings.Contains(contentType, "text/event-stream")

	for k, vals := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "connection" || lk == "transfer-encoding" {
			continue
		}
		for _, v := range vals {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)

	extractor := newUsageExtractor(platform, isStream, image)

	if !isStream {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if err == nil {
			extractor.feedJSON(body)
		}
		_, _ = c.Writer.Write(body)
		return extractor.usage(), false
	}

	flusher, _ := c.Writer.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
			extractor.feedChunk(buf[:n])
		}
		if err != nil {
			break
		}
	}
	extractor.finish()
	return extractor.usage(), true
}

func readBody(c *gin.Context) ([]byte, error) {
	defer c.Request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errRequestBodyTooLarge
	}
	return body, nil
}

func writeReadBodyError(c *gin.Context, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		util.Fail(c, http.StatusRequestEntityTooLarge, errRequestBodyTooLarge.Error())
		return
	}
	util.Fail(c, http.StatusBadRequest, "read body failed")
}

func peekJSON(body []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return m
}

func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func jsonBool(raw json.RawMessage) bool {
	var b bool
	_ = json.Unmarshal(raw, &b)
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
