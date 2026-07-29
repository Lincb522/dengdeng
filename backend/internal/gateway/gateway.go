// Package gateway implements the relay core: client API-key auth, upstream
// account selection with failover, streaming passthrough and usage capture.
package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
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
	settings     *service.SystemSettingsService
	imageStorage *service.ImageStorageService
	quota        *service.AccountQuotaService
	concurrency  *service.ClientConcurrencyLimiter
	client       *http.Client
	proxyClients sync.Map // map[proxy-id:updated-at]*http.Client
	keyWindows   sync.Map // map[api-key-id]*keyRPMWindow
	userWindows  sync.Map // map[user-id]*keyRPMWindow
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

func (g *Gateway) SetSystemSettings(settings *service.SystemSettingsService) {
	g.settings = settings
}

func (g *Gateway) SetImageStorageService(storage *service.ImageStorageService) {
	g.imageStorage = storage
}

// SetAccountQuotaService lets the relay persist the rate-limit allowance
// headers returned by real upstream requests. Model-list probes do not always
// carry these headers, while inference responses usually contain the most
// current key-level request and token windows.
func (g *Gateway) SetAccountQuotaService(quota *service.AccountQuotaService) {
	g.quota = quota
}

func (g *Gateway) observeAccountQuota(account *model.UpstreamAccount, headers http.Header) {
	if g == nil || g.quota == nil || account == nil || account.ID <= 0 {
		return
	}
	accountCopy := *account
	headerCopy := headers.Clone()
	observedAt := time.Now().UTC()
	// Quota persistence must never delay the first response byte.
	go func() {
		if err := g.quota.ObserveRateLimitHeaders(&accountCopy, headerCopy, observedAt); err != nil {
			log.Printf("[gateway] account %d quota headers: %v", accountCopy.ID, err)
		}
	}()
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
	Groups          []model.Group
	AccessActive    bool
	RequestReserved bool
	Budget          usageBudgetReservation
}

func usageBillingMode(ak *authedKey) string {
	if ak == nil {
		return "none"
	}
	if ak.User.Role == model.RoleAdmin {
		return "admin"
	}
	if ak.AccessActive {
		return "day"
	}
	if ak.RequestReserved {
		return "request"
	}
	return "usage"
}

func (ak *authedKey) selectGroup(platforms ...string) bool {
	if ak == nil {
		return false
	}
	for _, platform := range platforms {
		if ak.Group.ID > 0 && ak.Group.Platform == platform {
			return true
		}
		for _, group := range ak.Groups {
			if group.Platform == platform {
				ak.Group = group
				return true
			}
		}
	}
	return false
}

func (ak *authedKey) groupsForPlatform(platform string) []model.Group {
	if ak == nil {
		return nil
	}
	result := make([]model.Group, 0, len(ak.Groups))
	seen := make(map[int64]struct{}, len(ak.Groups))
	if ak.Group.ID > 0 && ak.Group.Platform == platform {
		result = append(result, ak.Group)
		seen[ak.Group.ID] = struct{}{}
	}
	for _, group := range ak.Groups {
		if group.Platform != platform {
			continue
		}
		if _, exists := seen[group.ID]; exists {
			continue
		}
		result = append(result, group)
		seen[group.ID] = struct{}{}
	}
	return result
}

func (g *Gateway) selectGroupForModel(ak *authedKey, modelName string, fallbacks ...string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName != "" {
		var cfg model.ModelConfig
		if err := g.db.Where("name = ? AND status = ?", modelName, model.StatusActive).First(&cfg).Error; err == nil {
			allowed := len(fallbacks) == 0
			for _, platform := range fallbacks {
				if cfg.Platform == platform {
					allowed = true
					break
				}
			}
			if allowed && ak.selectGroup(cfg.Platform) {
				return true
			}
		}
	}
	return ak.selectGroup(fallbacks...)
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
	err := g.db.Preload("Groups", func(db *gorm.DB) *gorm.DB { return db.Order("groups.id ASC") }).
		Where("key_hash = ?", util.HashAPIKey(strings.TrimSpace(raw))).First(&key).Error
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
		failInsufficientQuota(c, "API key quota exhausted")
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
			failInsufficientQuota(c, "API key daily quota reached")
			return nil, false
		}
	}

	var user model.User
	if err := g.db.First(&user, key.UserID).Error; err != nil || user.Status != model.StatusActive {
		util.Fail(c, http.StatusForbidden, "user disabled")
		return nil, false
	}
	if options.consumeRPM && !g.consumeUserRPM(user) {
		c.Header("Retry-After", "60")
		util.Fail(c, http.StatusTooManyRequests, "user rate limit reached")
		return nil, false
	}
	accessActive := user.AccessExpiresAt != nil && user.AccessExpiresAt.After(time.Now())
	if options.enforceUsageLimits && user.Role != model.RoleAdmin && !accessActive && user.RemainingRequests <= 0 && user.BalanceMicro <= 0 {
		failInsufficientQuota(c, "insufficient balance")
		return nil, false
	}

	activeGroups := make([]model.Group, 0, len(key.Groups)+1)
	var primaryGroup model.Group
	for _, group := range key.Groups {
		if group.Status != model.StatusActive {
			continue
		}
		if group.ID == key.GroupID {
			primaryGroup = group
			continue
		}
		activeGroups = append(activeGroups, group)
	}
	if primaryGroup.ID == 0 {
		if err := g.db.First(&primaryGroup, key.GroupID).Error; err != nil || primaryGroup.Status != model.StatusActive {
			primaryGroup = model.Group{}
		}
	}
	if primaryGroup.ID > 0 {
		activeGroups = append([]model.Group{primaryGroup}, activeGroups...)
	}
	if len(activeGroups) == 0 {
		util.Fail(c, http.StatusForbidden, "group disabled")
		return nil, false
	}
	key.Group = &activeGroups[0]
	key.Groups = activeGroups
	key.GroupIDs = make([]int64, 0, len(activeGroups))
	for _, group := range activeGroups {
		key.GroupIDs = append(key.GroupIDs, group.ID)
	}

	if options.touchLastUsed {
		go g.db.Model(&model.APIKey{}).Where("id = ?", key.ID).Update("last_used_at", time.Now())
	}
	return &authedKey{Key: key, User: user, Group: activeGroups[0], Groups: activeGroups, AccessActive: accessActive}, true
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

func (g *Gateway) consumeUserRPM(user model.User) bool {
	if user.RPMLimit <= 0 {
		return true
	}
	value, _ := g.userWindows.LoadOrStore(user.ID, &keyRPMWindow{})
	window := value.(*keyRPMWindow)
	currentMinute := time.Now().Truncate(time.Minute)
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.start.Before(currentMinute) {
		window.start, window.count = currentMinute, 0
	}
	if int64(window.count) >= user.RPMLimit {
		return false
	}
	window.count++
	return true
}

func (g *Gateway) platformQuotaAllowed(userID int64, platform string) (bool, string, error) {
	if !g.db.Migrator().HasTable(&model.UserPlatformQuota{}) {
		return true, "", nil
	}
	var quota model.UserPlatformQuota
	err := g.db.Where("user_id = ? AND platform = ?", userID, strings.ToLower(strings.TrimSpace(platform))).First(&quota).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	now := time.Now().UTC()
	origin := quota.CreatedAt.UTC()
	if origin.IsZero() {
		origin = now
	}
	windowStart := func(duration time.Duration) time.Time {
		elapsed := now.Sub(origin)
		if elapsed <= 0 {
			return origin
		}
		return origin.Add(time.Duration(int64(elapsed/duration)) * duration)
	}
	dayStart, weekStart, monthStart := windowStart(24*time.Hour), windowStart(7*24*time.Hour), windowStart(30*24*time.Hour)
	windows := []struct {
		name  string
		start time.Time
		limit int64
	}{{"daily", dayStart, quota.DailyMicro}, {"weekly", weekStart, quota.WeeklyMicro}, {"monthly", monthStart, quota.MonthlyMicro}}
	for _, window := range windows {
		if window.limit <= 0 {
			continue
		}
		var used int64
		if err := g.db.Model(&model.UsageLog{}).
			Joins("JOIN groups quota_groups ON quota_groups.id = usage_logs.group_id").
			Where("usage_logs.user_id = ? AND quota_groups.platform = ? AND usage_logs.created_at >= ?", userID, platform, window.start).
			Select("COALESCE(SUM(usage_logs.cost_micro), 0)").Scan(&used).Error; err != nil {
			return false, "", err
		}
		if used >= window.limit {
			return false, window.name, nil
		}
	}
	return true, "", nil
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

type usageBudgetReservation struct {
	BalanceMicro int64
	KeyMicro     int64
	DailyMicro   int64
}

var errInsufficientUsageBudget = errors.New("insufficient available usage budget")

func (g *Gateway) reserveUsageBudget(ak *authedKey, amount int64) (usageBudgetReservation, error) {
	var reserved usageBudgetReservation
	if ak == nil || amount <= 0 {
		return reserved, nil
	}
	err := g.db.Transaction(func(tx *gorm.DB) error {
		cashBilling := ak.User.Role != model.RoleAdmin && !ak.AccessActive && !ak.RequestReserved
		if cashBilling {
			result := tx.Model(&model.User{}).
				Where("id = ? AND balance_micro - balance_held_micro >= ?", ak.User.ID, amount).
				Update("balance_held_micro", gorm.Expr("balance_held_micro + ?", amount))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errInsufficientUsageBudget
			}
			reserved.BalanceMicro = amount
		}

		// Serialize reservations and settlements for one key before reading the
		// usage ledger. Without this lock, a concurrent settlement can move an
		// amount from daily_quota_held_micro into usage_logs between the SUM and
		// UPDATE, letting both values briefly disappear from the limit check.
		keyLock := tx.Model(&model.APIKey{}).Where("id = ?", ak.Key.ID).
			UpdateColumn("quota_held_micro", gorm.Expr("quota_held_micro"))
		if keyLock.Error != nil {
			return keyLock.Error
		}
		if keyLock.RowsAffected != 1 {
			return errInsufficientUsageBudget
		}

		var limits struct {
			QuotaMicro      int64
			DailyQuotaMicro int64
		}
		if err := tx.Model(&model.APIKey{}).Select("quota_micro", "daily_quota_micro").Where("id = ?", ak.Key.ID).Scan(&limits).Error; err != nil {
			return err
		}
		if limits.QuotaMicro <= 0 && limits.DailyQuotaMicro <= 0 {
			return nil
		}
		now := time.Now()
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		var dailyUsed int64
		if limits.DailyQuotaMicro > 0 {
			if err := tx.Model(&model.UsageLog{}).
				Where("api_key_id = ? AND created_at >= ?", ak.Key.ID, dayStart).
				Select("COALESCE(SUM(cost_micro), 0)").Scan(&dailyUsed).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&model.APIKey{}).
			Where(
				"id = ? AND (quota_micro = 0 OR quota_used_micro + quota_held_micro + ? <= quota_micro) AND (daily_quota_micro = 0 OR ? + daily_quota_held_micro + ? <= daily_quota_micro)",
				ak.Key.ID, amount, dailyUsed, amount,
			).
			Updates(map[string]any{
				"quota_held_micro": gorm.Expr(
					"CASE WHEN quota_micro > 0 THEN quota_held_micro + ? ELSE quota_held_micro END",
					amount,
				),
				"daily_quota_held_micro": gorm.Expr(
					"CASE WHEN daily_quota_micro > 0 THEN daily_quota_held_micro + ? ELSE daily_quota_held_micro END",
					amount,
				),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errInsufficientUsageBudget
		}
		if limits.QuotaMicro > 0 {
			reserved.KeyMicro = amount
		}
		if limits.DailyQuotaMicro > 0 {
			reserved.DailyMicro = amount
		}
		return nil
	})
	return reserved, err
}

func (g *Gateway) releaseUsageBudget(ak *authedKey) {
	if ak == nil {
		return
	}
	reserved := ak.Budget
	if reserved.BalanceMicro <= 0 && reserved.KeyMicro <= 0 && reserved.DailyMicro <= 0 {
		return
	}
	if err := g.db.Transaction(func(tx *gorm.DB) error {
		if reserved.BalanceMicro > 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", ak.User.ID).Update(
				"balance_held_micro",
				gorm.Expr("CASE WHEN balance_held_micro >= ? THEN balance_held_micro - ? ELSE 0 END", reserved.BalanceMicro, reserved.BalanceMicro),
			).Error; err != nil {
				return err
			}
		}
		if reserved.KeyMicro > 0 || reserved.DailyMicro > 0 {
			return tx.Model(&model.APIKey{}).Where("id = ?", ak.Key.ID).Updates(map[string]any{
				"quota_held_micro": gorm.Expr(
					"CASE WHEN quota_held_micro >= ? THEN quota_held_micro - ? ELSE 0 END",
					reserved.KeyMicro, reserved.KeyMicro,
				),
				"daily_quota_held_micro": gorm.Expr(
					"CASE WHEN daily_quota_held_micro >= ? THEN daily_quota_held_micro - ? ELSE 0 END",
					reserved.DailyMicro, reserved.DailyMicro,
				),
			}).Error
		}
		return nil
	}); err != nil {
		log.Printf("[gateway] failed to release usage reservation for user %d: %v", ak.User.ID, err)
	}
	ak.Budget = usageBudgetReservation{}
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
	// ServiceTier is the client-requested upstream service tier, when the wire
	// protocol exposes one. It is recorded only for billing audit display.
	ServiceTier string
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
	QueueMs       int64
	ScheduleMs    int64
	UpstreamMs    int64
	AttemptCount  int
	LastAccountID int64
}

// firstTokenWriter records the first response body write without treating an
// early WriteHeader as a token. It sits at the final client boundary, so the
// measurement works for passthrough SSE, protocol adapters and JSON replies.
type firstTokenWriter struct {
	gin.ResponseWriter
	started      time.Time
	once         sync.Once
	firstTokenMs int64
}

func (w *firstTokenWriter) mark(length int) {
	if length <= 0 {
		return
	}
	w.once.Do(func() {
		w.firstTokenMs = time.Since(w.started).Milliseconds()
		if w.firstTokenMs < 1 {
			w.firstTokenMs = 1
		}
	})
}

func (w *firstTokenWriter) Write(data []byte) (int, error) {
	w.mark(len(data))
	return w.ResponseWriter.Write(data)
}

func (w *firstTokenWriter) WriteString(data string) (int, error) {
	w.mark(len(data))
	return w.ResponseWriter.WriteString(data)
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

func maxRatePlan(current, candidate service.RatePlan) service.RatePlan {
	current.Base = math.Max(current.Base, candidate.Base)
	current.CacheRead = math.Max(current.CacheRead, candidate.CacheRead)
	current.CacheWrite5m = math.Max(current.CacheWrite5m, candidate.CacheWrite5m)
	current.CacheWrite1h = math.Max(current.CacheWrite1h, candidate.CacheWrite1h)
	current.Image = math.Max(current.Image, candidate.Image)
	return current
}

func (g *Gateway) estimateRequestBudget(ak *authedKey, req relayRequest, groups []model.Group) int64 {
	if g.billing == nil || ak == nil || !req.Billable {
		return 0
	}
	var rates service.RatePlan
	for _, group := range groups {
		candidate := g.effortRates(
			billingRates(ak.User, group, g.rates.Resolve(ak.User.ID, group.ID, group.RateMultiplier)),
			req.Effort,
		)
		rates = maxRatePlan(rates, candidate)
	}
	var maxOutput, imageCount int64
	if req.Image {
		imageCount = 1
	}
	if fields := peekJSON(req.Body); fields != nil {
		maxOutput = firstPositive(fields["max_output_tokens"], fields["max_completion_tokens"], fields["max_tokens"])
		if req.Image {
			if count := firstPositive(fields["n"]); count > 0 {
				imageCount = count
			}
		}
	}
	return g.billing.EstimateMaximum(req.Model, len(req.Body), maxOutput, imageCount, rates)
}

// relay runs the account failover loop and, on success, streams the response
// while capturing usage for billing.
func (g *Gateway) relay(c *gin.Context, ak *authedKey, req relayRequest) {
	runtimePolicy := service.DefaultGatewayRuntimePolicy()
	if g.policy != nil {
		runtimePolicy = g.policy.Current()
	}
	if err := enforceClientPolicy(c.Request.Header, req.Platform, runtimePolicy); err != nil {
		util.Fail(c, http.StatusForbidden, err.Error())
		return
	}
	if g.settings != nil && len(req.Body) > 0 {
		if settings, err := g.settings.Get(); err == nil && settings.Features.RiskControlEnabled {
			lowerBody := strings.ToLower(string(req.Body))
			for _, phrase := range settings.Features.RiskControlBlockedPhrases {
				if phrase == "" || !strings.Contains(lowerBody, phrase) {
					continue
				}
				log.Printf("[risk-control] request_id=%s user_id=%d key_id=%d action=%s phrase_hash=%x", middleware.RequestIDFromContext(c), ak.User.ID, ak.Key.ID, settings.Features.RiskControlAction, sha256.Sum256([]byte(phrase)))
				if settings.Features.RiskControlAction == "block" {
					util.Fail(c, http.StatusForbidden, "request blocked by content policy")
					return
				}
				break
			}
		}
	}
	if req.ServiceTier == "" {
		req.ServiceTier = requestServiceTier(req.Body)
	}
	if req.Billable && ak.User.Role != model.RoleAdmin && g.db.Migrator().HasTable(&model.UserGroupSubscription{}) {
		allowed, window, err := g.platformQuotaAllowed(ak.User.ID, req.Platform)
		if err != nil {
			util.Fail(c, http.StatusInternalServerError, "check platform quota failed")
			return
		}
		if !allowed {
			failInsufficientQuota(c, fmt.Sprintf("%s platform quota reached", window))
			return
		}
	}
	routeGroups := ak.groupsForPlatform(req.Platform)
	if len(routeGroups) == 0 {
		util.Fail(c, http.StatusBadRequest, fmt.Sprintf("this key has no %s group", req.Platform))
		return
	}
	routeGroup := routeGroups[0]
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
		routeGroups = []model.Group{routeGroup}
	}
	if req.Billable && ak.User.Role != model.RoleAdmin {
		groupIDs := make([]int64, 0, len(routeGroups))
		for _, group := range routeGroups {
			groupIDs = append(groupIDs, group.ID)
		}
		var subscribedIDs []int64
		if err := g.db.Model(&model.UserGroupSubscription{}).
			Where("user_id = ? AND group_id IN ? AND expires_at > ?", ak.User.ID, groupIDs, time.Now().UTC()).
			Pluck("group_id", &subscribedIDs).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "check group subscription failed")
			return
		}
		if len(subscribedIDs) > 0 {
			allowed := make(map[int64]struct{}, len(subscribedIDs))
			for _, id := range subscribedIDs {
				allowed[id] = struct{}{}
			}
			subscribed := make([]model.Group, 0, len(subscribedIDs))
			for _, group := range routeGroups {
				if _, ok := allowed[group.ID]; ok {
					subscribed = append(subscribed, group)
				}
			}
			routeGroups = subscribed
			routeGroup = routeGroups[0]
			ak.AccessActive = true
		}
	}
	activeRequest := g.runtime.Begin(req.Platform, routeGroup.ID, ak.User.ID)
	defer activeRequest.Finish()
	start := time.Now()
	trace := relayTrace{}
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
				failInsufficientQuota(c, "insufficient balance")
				return
			}
		}
	}

	budgetSettled := false
	if req.Billable {
		estimate := g.estimateRequestBudget(ak, req, routeGroups)
		reserved, err := g.reserveUsageBudget(ak, estimate)
		if err != nil {
			if errors.Is(err, errInsufficientUsageBudget) {
				failInsufficientQuota(c, "insufficient available balance or API key quota")
			} else {
				util.Fail(c, http.StatusInternalServerError, "reserve usage budget failed")
			}
			return
		}
		ak.Budget = reserved
		defer func() {
			if !budgetSettled {
				g.releaseUsageBudget(ak)
			}
		}()
	}

	var tried []int64
	var lastStatus int
	var lastBody []byte
	lastAttemptGroup := routeGroup
	sessionID := relaySessionID(c, ak.Key.ID, req.Body)
	req.SessionID = sessionID
	groupIndex := 0
	unavailableGroups := make(map[int64]struct{}, len(routeGroups))
	advanceGroup := func(markCurrentUnavailable bool) bool {
		if markCurrentUnavailable {
			unavailableGroups[routeGroup.ID] = struct{}{}
		}
		for offset := 1; offset <= len(routeGroups); offset++ {
			candidateIndex := (groupIndex + offset) % len(routeGroups)
			candidate := routeGroups[candidateIndex]
			if _, unavailable := unavailableGroups[candidate.ID]; unavailable {
				continue
			}
			groupIndex = candidateIndex
			routeGroup = candidate
			activeRequest.SetGroup(routeGroup.ID)
			return true
		}
		return false
	}

	for attempt := 0; attempt < g.relayAttempts(); {
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
				if advanceGroup(true) {
					continue
				}
				c.Header("Retry-After", "1")
				g.setRelayTimingHeaders(c, trace)
				g.recordRelayFailure(c, ak, routeGroup, req, start, trace, http.StatusTooManyRequests, "upstream account concurrency limit reached")
				util.Fail(c, http.StatusTooManyRequests, "upstream accounts are busy; retry later")
				return
			}
			if errors.Is(err, service.ErrNoAccount) {
				if advanceGroup(true) {
					continue
				}
				if lastStatus != 0 {
					break // fall through to lastStatus passthrough
				}
				g.setRelayTimingHeaders(c, trace)
				diagnosticMessage := g.schedulerFailureMessage(routeGroup.ID, "no available upstream account in the selected groups")
				log.Printf("[scheduler] group=%d model=%s %s", routeGroup.ID, req.Model, diagnosticMessage)
				g.recordRelayFailure(c, ak, routeGroup, req, start, trace, http.StatusServiceUnavailable, diagnosticMessage)
				util.Fail(c, http.StatusServiceUnavailable, "no available upstream account in the selected groups")
				return
			}
			util.Fail(c, http.StatusInternalServerError, "scheduler error")
			return
		}
		tried = append(tried, acc.ID)
		trace.LastAccountID = acc.ID
		attempt++
		trace.AttemptCount++
		lastAttemptGroup = routeGroup
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
			if len(routeGroups) > 1 {
				advanceGroup(false)
			}
			continue
		}
		g.observeAccountQuota(acc, resp.Header)

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			g.scheduler.Release(acc.ID)
			lastStatus, lastBody = resp.StatusCode, body
			if retryableUpstream(resp.StatusCode, body) {
				g.scheduler.ReportFailureForModel(acc.ID, req.Model, resp.StatusCode, string(body))
				if len(routeGroups) > 1 {
					advanceGroup(false)
				}
				continue
			}
			break
		}

		// Success: stream through and bill afterwards.
		g.scheduler.ReportSuccessForModelWithLatency(acc.ID, req.Model, attemptUpstreamMs)
		g.setRelayTimingHeaders(c, trace)
		originalWriter := c.Writer
		timingWriter := &firstTokenWriter{ResponseWriter: originalWriter, started: start}
		c.Writer = timingWriter
		usage, streamed := g.pipeAdapted(c, resp, req.Platform, req.Image, req.ResponseAdapter, req.Model)
		c.Writer = originalWriter
		resp.Body.Close()
		g.scheduler.Release(acc.ID)

		if req.Billable {
			if err := g.billing.Record(service.BillContext{
				RequestID:             middleware.RequestIDFromContext(c),
				ClientRequestID:       clientRequestID(c),
				UserID:                ak.User.ID,
				APIKeyID:              ak.Key.ID,
				AccountID:             acc.ID,
				GroupID:               routeGroup.ID,
				Model:                 req.Model,
				Platform:              req.Platform,
				RequestPath:           publicRequestPath(c),
				ClientIP:              c.ClientIP(),
				UserAgent:             truncate(c.Request.UserAgent(), 512),
				Stream:                streamed,
				Effort:                req.Effort,
				ServiceTier:           req.ServiceTier,
				BillingMode:           usageBillingMode(ak),
				Usage:                 usage,
				Rates:                 g.effortRates(billingRates(ak.User, routeGroup, g.rates.Resolve(ak.User.ID, routeGroup.ID, routeGroup.RateMultiplier)), req.Effort),
				FirstTokenMs:          timingWriter.firstTokenMs,
				DurationMs:            time.Since(start).Milliseconds(),
				QueueMs:               trace.QueueMs,
				ScheduleMs:            trace.ScheduleMs,
				UpstreamMs:            trace.UpstreamMs,
				AttemptCount:          trace.AttemptCount,
				StatusCode:            resp.StatusCode,
				SkipBalance:           ak.AccessActive || ak.RequestReserved,
				ReservedBalanceMicro:  ak.Budget.BalanceMicro,
				ReservedKeyQuotaMicro: ak.Budget.KeyMicro,
				ReservedDailyMicro:    ak.Budget.DailyMicro,
			}); err != nil {
				return
			}
			budgetSettled = true
			ak.Budget = usageBudgetReservation{}
		}
		completed = true
		return
	}

	// All attempts failed: pass the last upstream error to the client.
	if lastStatus == 0 {
		lastStatus, lastBody = http.StatusServiceUnavailable, []byte(`{"error":{"message":"no available upstream account"}}`)
	}
	g.setRelayTimingHeaders(c, trace)
	g.recordRelayFailure(c, ak, lastAttemptGroup, req, start, trace, lastStatus, truncate(string(lastBody), 500))
	c.Data(lastStatus, "application/json", lastBody)
}

func (g *Gateway) schedulerFailureMessage(groupID int64, fallback string) string {
	if g == nil || g.scheduler == nil {
		return fallback
	}
	if diagnostic, ok := g.scheduler.Diagnostic(groupID); ok {
		return fallback + " (" + diagnostic.Summary() + ")"
	}
	return fallback
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
		RequestID:       middleware.RequestIDFromContext(c),
		ClientRequestID: clientRequestID(c),
		UserID:          ak.User.ID,
		APIKeyID:        ak.Key.ID,
		AccountID:       trace.LastAccountID,
		GroupID:         group.ID,
		Model:           req.Model,
		Platform:        req.Platform,
		RequestPath:     publicRequestPath(c),
		ClientIP:        c.ClientIP(),
		UserAgent:       truncate(c.Request.UserAgent(), 512),
		Stream:          false,
		Effort:          req.Effort,
		ServiceTier:     req.ServiceTier,
		BillingMode:     "none",
		Rates:           billingRates(ak.User, group, g.rates.Resolve(ak.User.ID, group.ID, group.RateMultiplier)),
		DurationMs:      time.Since(started).Milliseconds(),
		QueueMs:         trace.QueueMs,
		ScheduleMs:      trace.ScheduleMs,
		UpstreamMs:      trace.UpstreamMs,
		AttemptCount:    trace.AttemptCount,
		StatusCode:      status,
		ErrorMessage:    message,
		SkipBalance:     true,
	})
}

func clientRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	for _, header := range []string{"X-Client-Request-ID", "X-Stainless-Request-ID", "X-Request-ID"} {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			return truncate(value, 64)
		}
	}
	return ""
}

func publicRequestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return truncate(c.Request.URL.Path, 256)
}

func requestServiceTier(body []byte) string {
	if len(body) == 0 || !json.Valid(body) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	tier := strings.ToLower(jsonStringPath(payload, "service_tier"))
	if len(tier) > 32 {
		return tier[:32]
	}
	return tier
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

func failInsufficientQuota(c *gin.Context, message string) {
	if strings.Contains(c.Request.URL.Path, "/messages") {
		c.JSON(http.StatusPaymentRequired, gin.H{"type": "error", "error": gin.H{"type": "billing_error", "message": message}})
		return
	}
	c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
		"message": message, "type": "insufficient_quota", "param": nil, "code": "insufficient_quota",
	}})
}

func retryableUpstream(status int, body []byte) bool {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
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
	if req.Platform == model.PlatformOpenAI && req.Path == "/backend-api/codex/models" {
		return g.forwardOpenAIModelsManifest(c, acc, req)
	}
	// An OpenAI OAuth credential is a ChatGPT/Codex subscription credential,
	// not an API Platform key. It has a separate Responses-shaped upstream and
	// needs protocol adaptation before it can serve the public OpenAI APIs.
	if req.Platform == model.PlatformOpenAI && (acc.AuthType == model.AuthOAuth || service.IsOpenAIAgentIdentity(acc)) {
		return g.forwardOpenAIOAuth(c, acc, req)
	}
	// Grok subscription credentials use the CLI Responses transport. Its HTTP
	// Chat Completions endpoint requires a protocol upgrade, so a normal JSON
	// POST receives 426. Bridge public Chat Completions requests through
	// /v1/responses and translate the result back for the caller.
	if req.Platform == model.PlatformGrok && acc.AuthType == model.AuthOAuth && req.Path == "/v1/chat/completions" {
		return g.forwardGrokOAuthChat(c, acc, req)
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
	policy := service.DefaultGatewayRuntimePolicy()
	if g.policy != nil {
		policy = g.policy.Current()
	}
	if req.Platform == model.PlatformAnthropic && acc.AuthType == model.AuthOAuth && req.Path == "/v1/messages" {
		if policy.ClaudeOAuthSystemPromptInjection {
			outboundBody = injectClaudeCodeSystemPromptWithText(outboundBody, policy.ClaudeOAuthSystemPrompt)
		}
		if policy.RewriteMessageCacheControl {
			outboundBody = rewriteAnthropicMessageCacheControl(outboundBody)
		}
		if policy.AnthropicCacheTTL1hInjection {
			outboundBody = injectAnthropicCacheTTL1h(outboundBody)
		}
	}

	target, err := util.JoinUpstreamURL(base, req.Path)
	if err != nil {
		return nil, err
	}
	upReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, bytes.NewReader(outboundBody))
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
			unifyFingerprint := true
			if g.policy != nil {
				unifyFingerprint = g.policy.Current().FingerprintUnification
			}
			if unifyFingerprint {
				applyAnthropicOAuthIdentityHeaders(upReq.Header)
			} else {
				upReq.Header.Set("User-Agent", c.Request.UserAgent())
				for name, values := range c.Request.Header {
					if strings.HasPrefix(strings.ToLower(name), "x-stainless-") {
						upReq.Header[name] = append([]string(nil), values...)
					}
				}
			}
		case model.PlatformOpenAI:
			if acc.AccountID != "" {
				upReq.Header.Set("chatgpt-account-id", acc.AccountID)
			}
		case model.PlatformGrok:
			applyGrokOAuthIdentityHeaders(upReq.Header)
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
