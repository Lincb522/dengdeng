package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/oauth"

	"gorm.io/gorm"
)

const (
	accountQuotaRefreshInterval = 15 * time.Minute
	accountQuotaTimeout         = 20 * time.Second
)

var (
	openAICodexUsageURL    = "https://chatgpt.com/backend-api/wham/usage"
	openAISubscriptionsURL = "https://chatgpt.com/backend-api/subscriptions"
	openAIAccountsCheckURL = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"
	claudeOAuthUsageURL    = "https://api.anthropic.com/api/oauth/usage"
	// Grok billing lives below the CLI proxy's versioned API root. Keeping /v1
	// here matches the official CLI and prevents /billing from returning an
	// HTML 404 that can be mistaken for an invalid OAuth credential.
	grokCLIBillingBaseURL = "https://cli-chat-proxy.grok.com/v1"
)

// AccountQuotaService normalizes provider subscription windows, passive
// rate-limit headers, and DengDeng-observed account usage into one snapshot.
// Automatic refreshes are intentionally bounded and never generate model
// output, so keeping the account screen current does not consume messages.
type AccountQuotaService struct {
	db            *gorm.DB
	cfg           *config.Config
	oauth         *oauth.Manager
	defaultClient *http.Client
	locks         sync.Map // account id -> *sync.Mutex
}

func NewAccountQuotaService(db *gorm.DB, cfg *config.Config, oauthManager *oauth.Manager, defaultClient *http.Client) *AccountQuotaService {
	return &AccountQuotaService{db: db, cfg: cfg, oauth: oauthManager, defaultClient: defaultClient}
}

func (s *AccountQuotaService) lockFor(accountID int64) *sync.Mutex {
	value, _ := s.locks.LoadOrStore(accountID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *AccountQuotaService) RefreshIfStale(ctx context.Context, accountID int64) (model.AccountQuotaSnapshot, error) {
	if s == nil || s.db == nil || accountID <= 0 {
		return model.AccountQuotaSnapshot{}, fmt.Errorf("invalid account")
	}
	var existing model.AccountQuotaSnapshot
	if err := s.db.Where("upstream_account_id = ?", accountID).First(&existing).Error; err == nil &&
		time.Since(existing.LastAttemptAt) < accountQuotaRefreshInterval {
		return existing, nil
	}
	return s.RefreshAccount(ctx, accountID)
}

func (s *AccountQuotaService) RefreshAccount(ctx context.Context, accountID int64) (model.AccountQuotaSnapshot, error) {
	if s == nil || s.db == nil || accountID <= 0 {
		return model.AccountQuotaSnapshot{}, fmt.Errorf("invalid account")
	}
	lock := s.lockFor(accountID)
	lock.Lock()
	defer lock.Unlock()
	var account model.UpstreamAccount
	if err := s.db.Preload("Proxy").First(&account, accountID).Error; err != nil {
		return model.AccountQuotaSnapshot{}, err
	}
	return s.refresh(ctx, &account)
}

func (s *AccountQuotaService) refresh(parent context.Context, account *model.UpstreamAccount) (model.AccountQuotaSnapshot, error) {
	now := time.Now().UTC()
	var snapshot model.AccountQuotaSnapshot
	_ = s.db.Where("upstream_account_id = ?", account.ID).First(&snapshot).Error
	snapshot.UpstreamAccountID = account.ID
	snapshot.Platform = account.Platform
	snapshot.PlanType = accountPlanType(account)
	snapshot.SubscriptionExpiresAt = accountSubscriptionExpiresAt(account)
	snapshot.LastAttemptAt = now
	snapshot.ObservedUsage = s.observedUsage(account.ID, now)
	snapshot.Message = ""

	if account.AuthType == model.AuthAPIKey {
		ctx, cancel := context.WithTimeout(parent, accountQuotaTimeout)
		defer cancel()
		if err := s.refreshAPIKey(ctx, account, &snapshot); err != nil {
			return s.saveFailure(&snapshot, err)
		}
		return snapshot, s.save(&snapshot)
	}

	if account.AuthType != model.AuthOAuth && !IsOpenAIAgentIdentity(account) {
		snapshot.Source = "local_observed"
		snapshot.State = "local_only"
		snapshot.Windows = nil
		snapshot.Message = "当前凭证类型没有可用的额度查询方式；当前显示本站记录"
		fetched := now
		snapshot.FetchedAt = &fetched
		return snapshot, s.save(&snapshot)
	}

	if IsOpenAIAgentIdentity(account) {
		ctx, cancel := context.WithTimeout(parent, accountQuotaTimeout)
		defer cancel()
		if err := s.refreshOpenAIAgentIdentity(ctx, account, &snapshot); err != nil {
			return s.saveFailure(&snapshot, err)
		}
		if snapshot.State == "" {
			snapshot.State = "ready"
		}
		return snapshot, s.save(&snapshot)
	}

	if s.oauth == nil {
		return s.saveFailure(&snapshot, fmt.Errorf("OAuth service unavailable"))
	}
	beforeToken := string(account.AccessToken)
	var beforeExpiry time.Time
	if account.ExpiresAt != nil {
		beforeExpiry = *account.ExpiresAt
	}
	ctx, cancel := context.WithTimeout(parent, accountQuotaTimeout)
	defer cancel()
	accessToken, err := s.oauth.AccessToken(ctx, account)
	if err != nil {
		return s.saveFailure(&snapshot, fmt.Errorf("credential refresh failed: %w", err))
	}
	if accessToken != beforeToken || (account.ExpiresAt != nil && !account.ExpiresAt.Equal(beforeExpiry)) {
		refreshed := now
		snapshot.LastCredentialRefresh = &refreshed
	}
	snapshot.SubscriptionExpiresAt = accountSubscriptionExpiresAt(account)

	switch account.Platform {
	case model.PlatformOpenAI:
		err = s.refreshOpenAI(ctx, account, "Bearer "+accessToken, accessToken, &snapshot)
	case model.PlatformAnthropic:
		err = s.refreshClaude(ctx, account, accessToken, &snapshot)
	case model.PlatformGrok:
		err = s.refreshGrok(ctx, account, accessToken, &snapshot)
	default:
		snapshot.Source = "local_observed"
		snapshot.State = "local_only"
		snapshot.Windows = nil
		snapshot.Message = "当前凭证没有可安全读取的上游额度接口；当前显示本站记录"
		fetched := now
		snapshot.FetchedAt = &fetched
	}
	if err != nil {
		return s.saveFailure(&snapshot, err)
	}
	if snapshot.State == "" {
		snapshot.State = "ready"
	}
	if snapshot.FetchedAt == nil {
		fetched := now
		snapshot.FetchedAt = &fetched
	}
	return snapshot, s.save(&snapshot)
}

func (s *AccountQuotaService) saveFailure(snapshot *model.AccountQuotaSnapshot, err error) (model.AccountQuotaSnapshot, error) {
	snapshot.State = "error"
	snapshot.Message = friendlyQuotaError(err)
	saveErr := s.save(snapshot)
	if saveErr != nil {
		return *snapshot, saveErr
	}
	return *snapshot, err
}

func (s *AccountQuotaService) save(snapshot *model.AccountQuotaSnapshot) error {
	if snapshot.ID > 0 {
		return s.db.Save(snapshot).Error
	}
	return s.db.Create(snapshot).Error
}

func (s *AccountQuotaService) observedUsage(accountID int64, now time.Time) []model.AccountObservedUsage {
	type aggregate struct {
		Requests     int64
		InputTokens  int64
		OutputTokens int64
		CostMicro    int64
	}
	windows := []struct {
		key, label string
		since      time.Time
	}{
		{"24h", "24 小时", now.Add(-24 * time.Hour)},
		{"7d", "7 天", now.Add(-7 * 24 * time.Hour)},
		{"30d", "30 天", now.Add(-30 * 24 * time.Hour)},
	}
	result := make([]model.AccountObservedUsage, 0, len(windows))
	for _, window := range windows {
		var row aggregate
		_ = s.db.Model(&model.UsageLog{}).
			Select("COUNT(*) AS requests, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(cost_micro), 0) AS cost_micro").
			Where("account_id = ? AND created_at >= ?", accountID, window.since).Scan(&row).Error
		result = append(result, model.AccountObservedUsage{
			Key: window.key, Label: window.label, Requests: row.Requests,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CostMicro: row.CostMicro,
		})
	}
	return result
}

// refreshAPIKey actively queries a static-key upstream instead of treating all
// API keys as local-only. OpenAI-compatible relays commonly expose /v1/usage;
// official providers generally expose allowance through response headers, so a
// non-billable model-list request is used as the portable fallback.
func (s *AccountQuotaService) refreshAPIKey(ctx context.Context, account *model.UpstreamAccount, snapshot *model.AccountQuotaSnapshot) error {
	client, err := s.clientFor(account)
	if err != nil {
		return err
	}
	existingRateWindows := onlyRateLimitWindows(snapshot.Windows)
	var lastErr error
	endpoints, err := apiKeyUsageEndpoints(account)
	if err != nil {
		return err
	}
	customEndpointConfigured := strings.TrimSpace(account.QuotaURL) != ""
	for index, endpoint := range endpoints {
		payload, headers, status, fetchErr := fetchAPIKeyJSON(ctx, client, endpoint, account)
		rateWindows := quotaWindowsFromHeaders(account.Platform, headers)
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		if customEndpointConfigured && index == 0 && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
			return fmt.Errorf("custom API key quota endpoint returned status %d", status)
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			continue
		}
		windows, plan, active, recognized := parseAPIKeyUsage(payload)
		if !recognized {
			continue
		}
		if !active {
			return fmt.Errorf("upstream API key is inactive")
		}
		snapshot.Source = "api_key_usage"
		snapshot.State = "ready"
		snapshot.Windows = mergeQuotaWindows(windows, existingRateWindows, rateWindows)
		if plan != "" {
			snapshot.PlanType = plan
		}
		if expiry := apiKeyUsageExpiry(payload); expiry != nil {
			snapshot.SubscriptionExpiresAt = expiry
		}
		if len(snapshot.Windows) == 0 {
			snapshot.State = "partial"
			snapshot.Message = "上游额度接口已响应，但没有返回可识别的余额或限额字段"
		}
		fetched := time.Now().UTC()
		snapshot.FetchedAt = &fetched
		return nil
	}
	if base, official, baseErr := apiKeyQuotaBase(account); baseErr == nil && !official {
		windows, expiry, headers, complete, recognized, dashboardErr := fetchLegacyDashboardQuota(ctx, client, base, account)
		if dashboardErr != nil {
			lastErr = dashboardErr
		} else if recognized {
			snapshot.Source = "api_key_usage"
			snapshot.State = "ready"
			snapshot.PlanType = "中转额度"
			snapshot.Windows = mergeQuotaWindows(windows, existingRateWindows, quotaWindowsFromHeaders(account.Platform, headers))
			snapshot.SubscriptionExpiresAt = expiry
			if !complete {
				snapshot.State = "partial"
				snapshot.Message = "第三方中转返回了总额度，但没有返回已用额度"
			}
			fetched := time.Now().UTC()
			snapshot.FetchedAt = &fetched
			return nil
		}
	}

	probeURL, probeErr := accountProbeURL(account)
	if probeErr != nil {
		return probeErr
	}
	headers, status, probeErr := fetchAPIKeyProbe(ctx, client, probeURL, account)
	if probeErr != nil {
		if lastErr != nil {
			return fmt.Errorf("API key usage query failed: %v; credential probe failed: %w", lastErr, probeErr)
		}
		return fmt.Errorf("API key credential probe failed: %w", probeErr)
	}
	rateWindows := quotaWindowsFromHeaders(account.Platform, headers)
	snapshot.Windows = mergeQuotaWindows(existingRateWindows, rateWindows)
	fetched := time.Now().UTC()
	snapshot.FetchedAt = &fetched
	switch {
	case status >= http.StatusOK && status < http.StatusBadRequest:
		if len(snapshot.Windows) > 0 {
			snapshot.Source = "rate_limit_headers"
			snapshot.State = "ready"
			snapshot.Message = "上游未返回账户余额；已同步 API Key 的请求与 Token 限额"
		} else {
			snapshot.Source = "api_key_probe"
			snapshot.State = "partial"
			snapshot.Message = "上游 API Key 已验证，但没有返回余额或限额字段；继续显示本站实测用量"
		}
		return nil
	case status == http.StatusTooManyRequests:
		snapshot.Source = "rate_limit_headers"
		snapshot.State = "partial"
		snapshot.Message = "上游 API Key 当前触发限流；已保留可读取的限额窗口"
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("API key credential probe returned status %d", status)
	default:
		return fmt.Errorf("API key credential probe returned status %d", status)
	}
}

func apiKeyUsageEndpoints(account *model.UpstreamAccount) ([]string, error) {
	base, official, err := apiKeyQuotaBase(account)
	if err != nil {
		return nil, err
	}
	endpoints := make([]string, 0, 5)
	if strings.TrimSpace(account.QuotaURL) != "" {
		custom, resolveErr := resolveAPIKeyQuotaURL(account.QuotaURL, base)
		if resolveErr != nil {
			return nil, resolveErr
		}
		endpoints = append(endpoints, custom)
	}
	if !official {
		endpoints = append(endpoints,
			base+"/v1/usage",        // Sub2API and DengDeng
			base+"/api/usage/token", // New API
			base+"/v1/dashboard/billing/credit_grants",
			base+"/dashboard/billing/credit_grants",
		)
	}
	result := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		if _, exists := seen[endpoint]; exists {
			continue
		}
		seen[endpoint] = struct{}{}
		result = append(result, endpoint)
	}
	return result, nil
}

func resolveAPIKeyQuotaURL(raw, base string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return "", fmt.Errorf("invalid API key upstream URL")
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "/") {
		resolved := &url.URL{Scheme: baseURL.Scheme, Host: baseURL.Host, Path: raw}
		if parsed, parseErr := url.Parse(raw); parseErr == nil {
			resolved.Path = parsed.Path
			resolved.RawQuery = parsed.RawQuery
		}
		return resolved.String(), nil
	}
	custom, err := url.Parse(raw)
	if err != nil || custom.Scheme == "" || custom.Host == "" || custom.User != nil {
		return "", fmt.Errorf("invalid API key quota URL")
	}
	if !strings.EqualFold(custom.Scheme, baseURL.Scheme) || !strings.EqualFold(custom.Host, baseURL.Host) {
		return "", fmt.Errorf("API key quota URL must use the same origin as Base URL")
	}
	custom.Fragment = ""
	return custom.String(), nil
}

func apiKeyQuotaBase(account *model.UpstreamAccount) (string, bool, error) {
	if account == nil {
		return "", false, fmt.Errorf("invalid account")
	}
	base := strings.TrimRight(strings.TrimSpace(account.BaseURL), "/")
	if base == "" {
		switch account.Platform {
		case model.PlatformOpenAI:
			base = "https://api.openai.com"
		case model.PlatformAnthropic:
			base = "https://api.anthropic.com"
		case model.PlatformGemini:
			base = "https://generativelanguage.googleapis.com"
		case model.PlatformGrok:
			base = "https://api.x.ai"
		default:
			return "", false, fmt.Errorf("unsupported platform")
		}
	}
	base = strings.TrimSuffix(base, "/v1beta")
	base = strings.TrimSuffix(base, "/v1")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false, fmt.Errorf("invalid API key upstream URL")
	}
	host := strings.ToLower(parsed.Hostname())
	official := host == "api.openai.com" || host == "api.anthropic.com" || host == "generativelanguage.googleapis.com" || host == "api.x.ai"
	return strings.TrimRight(base, "/"), official, nil
}

func fetchAPIKeyJSON(ctx context.Context, client *http.Client, endpoint string, account *model.UpstreamAccount) (map[string]any, http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	applyProbeCredential(req, account)
	// Compatible relay usage endpoints conventionally authenticate with a
	// bearer key even when their inference surface emulates Anthropic/Gemini.
	// Keep the native provider header as well so either convention works.
	if account.AuthType == model.AuthAPIKey && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+string(account.APIKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.Header.Clone(), resp.StatusCode, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, resp.Header.Clone(), resp.StatusCode, nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, resp.Header.Clone(), resp.StatusCode, fmt.Errorf("invalid upstream usage JSON")
	}
	return payload, resp.Header.Clone(), resp.StatusCode, nil
}

func fetchAPIKeyProbe(ctx context.Context, client *http.Client, endpoint string, account *model.UpstreamAccount) (http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	applyProbeCredential(req, account)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Header.Clone(), resp.StatusCode, nil
}

// fetchLegacyDashboardQuota supports One API and compatible New API releases.
// Their OpenAI-compatible billing surface splits the total allowance and used
// amount across two endpoints, so neither response is useful on its own.
func fetchLegacyDashboardQuota(ctx context.Context, client *http.Client, base string, account *model.UpstreamAccount) ([]model.AccountQuotaWindow, *time.Time, http.Header, bool, bool, error) {
	subscription, subscriptionHeaders, status, err := fetchAPIKeyJSON(ctx, client, base+"/v1/dashboard/billing/subscription", account)
	if err != nil {
		return nil, nil, nil, false, false, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices || subscription == nil || quotaMap(subscription["error"]) != nil {
		return nil, nil, subscriptionHeaders, false, false, nil
	}
	limit, hasLimit := quotaNumberField(subscription, "hard_limit_usd", "system_hard_limit_usd", "soft_limit_usd")
	if !hasLimit || limit <= 0 {
		return nil, nil, subscriptionHeaders, false, false, nil
	}

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := now.Format("2006-01-02")
	usageURL := base + "/v1/dashboard/billing/usage?start_date=" + url.QueryEscape(start) + "&end_date=" + url.QueryEscape(end)
	usage, usageHeaders, usageStatus, usageErr := fetchAPIKeyJSON(ctx, client, usageURL, account)
	headers := make(http.Header)
	for key, values := range subscriptionHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	for key, values := range usageHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	expiresAt := quotaTimeValue(subscription["access_until"])
	if usageErr != nil {
		window := model.AccountQuotaWindow{Key: "total", Label: "中转总额度", Limit: &limit, Unit: "upstream"}
		return []model.AccountQuotaWindow{window}, expiresAt, headers, false, true, nil
	}
	if usageStatus < http.StatusOK || usageStatus >= http.StatusMultipleChoices || usage == nil || quotaMap(usage["error"]) != nil {
		window := model.AccountQuotaWindow{Key: "total", Label: "中转总额度", Limit: &limit, Unit: "upstream"}
		return []model.AccountQuotaWindow{window}, expiresAt, headers, false, true, nil
	}
	usedCents, hasUsed := quotaNumberField(usage, "total_usage")
	if !hasUsed {
		window := model.AccountQuotaWindow{Key: "total", Label: "中转总额度", Limit: &limit, Unit: "upstream"}
		return []model.AccountQuotaWindow{window}, expiresAt, headers, false, true, nil
	}
	used := usedCents / 100
	remaining := math.Max(0, limit-used)
	window := quotaAmountWindow("total", "中转额度", limit, used, remaining, true, "upstream")
	return []model.AccountQuotaWindow{window}, expiresAt, headers, true, true, nil
}

func parseAPIKeyUsage(payload map[string]any) ([]model.AccountQuotaWindow, string, bool, bool) {
	if payload == nil {
		return nil, "", true, false
	}
	root := payload
	for _, key := range []string{"data", "result"} {
		if nested := quotaMap(root[key]); nested != nil {
			root = nested
			break
		}
	}
	active := true
	if value, ok := quotaBoolField(root, "is_active", "isValid", "is_valid", "active", "enabled"); ok {
		active = value
	}
	plan := quotaStringField(root, "plan_name", "planName", "plan", "plan_type")
	unit := quotaStringField(root, "unit", "currency")
	if unit == "" {
		unit = "USD"
		if quotaStringField(root, "object") == "token_usage" {
			unit = "quota"
		}
	}
	windows := make([]model.AccountQuotaWindow, 0, 8)
	recognized := false
	if unlimited, ok := quotaBoolField(root, "unlimited_quota", "unlimited"); ok && unlimited {
		recognized = true
		if plan == "" {
			plan = "无限额度"
		}
		windows = append(windows, model.AccountQuotaWindow{Key: "unlimited", Label: "无限额度"})
	}
	if balance, ok := quotaNumberField(root, "remaining", "balance", "available_balance", "available_credits", "total_available"); ok {
		recognized = true
		windows = append(windows, model.AccountQuotaWindow{Key: "balance", Label: "可用余额", Remaining: &balance, Unit: unit})
	}
	if granted, ok := quotaNumberField(root, "total_granted"); ok {
		recognized = true
		used, _ := quotaNumberField(root, "total_used")
		remaining, hasRemaining := quotaNumberField(root, "total_available")
		windows = append(windows, quotaAmountWindow("total", "累计额度", granted, used, remaining, hasRemaining, unit))
	}
	if quota := quotaMap(root["quota"]); quota != nil {
		if window, ok := quotaObjectWindow("total", "密钥总额度", quota, unit); ok {
			recognized = true
			windows = append(windows, window)
		}
	}
	if quota := quotaMap(root["daily_quota"]); quota != nil {
		if window, ok := quotaObjectWindow("daily", "今日额度", quota, unit); ok {
			recognized = true
			windows = append(windows, window)
		}
	}
	if remaining, ok := quotaNumberField(root, "remaining_requests"); ok {
		recognized = true
		windows = append(windows, model.AccountQuotaWindow{Key: "remaining_requests", Label: "剩余调用次数", Remaining: &remaining, Unit: "requests"})
	}
	if rateLimits := quotaSlice(root["rate_limits"]); len(rateLimits) > 0 {
		for _, rateLimit := range rateLimits {
			windowName := quotaStringField(rateLimit, "window", "name")
			key := "rate_" + strings.TrimSpace(windowName)
			if key == "rate_" {
				continue
			}
			label := strings.ToUpper(windowName) + " 额度"
			if window, ok := quotaObjectWindow(key, label, rateLimit, unit); ok {
				window.ResetAt = quotaTimeValue(rateLimit["reset_at"])
				recognized = true
				windows = append(windows, window)
			}
		}
	}
	if subscription := quotaMap(root["subscription"]); subscription != nil {
		for _, item := range []struct {
			key, label, limitKey, usedKey, resetKey string
		}{
			{"subscription_daily", "订阅日额度", "daily_limit_usd", "daily_usage_usd", ""},
			{"subscription_weekly", "订阅周额度", "weekly_limit_usd", "weekly_usage_usd", "weekly_window_start"},
			{"subscription_monthly", "订阅月额度", "monthly_limit_usd", "monthly_usage_usd", ""},
		} {
			limit, hasLimit := quotaNumberField(subscription, item.limitKey)
			used, hasUsed := quotaNumberField(subscription, item.usedKey)
			if !hasLimit || limit <= 0 {
				continue
			}
			remaining := math.Max(0, limit-used)
			window := quotaAmountWindow(item.key, item.label, limit, used, remaining, hasUsed, "USD")
			if item.resetKey != "" {
				if start := quotaTimeValue(subscription[item.resetKey]); start != nil {
					reset := start.Add(7 * 24 * time.Hour)
					window.ResetAt = &reset
				}
			}
			recognized = true
			windows = append(windows, window)
		}
	}
	return mergeQuotaWindows(windows), plan, active, recognized
}

func apiKeyUsageExpiry(payload map[string]any) *time.Time {
	root := payload
	for _, key := range []string{"data", "result"} {
		if nested := quotaMap(root[key]); nested != nil {
			root = nested
			break
		}
	}
	if expiry := quotaTimeValue(root["expires_at"]); expiry != nil {
		return expiry
	}
	if subscription := quotaMap(root["subscription"]); subscription != nil {
		return quotaTimeValue(subscription["expires_at"])
	}
	return nil
}

func quotaObjectWindow(key, label string, payload map[string]any, fallbackUnit string) (model.AccountQuotaWindow, bool) {
	limit, hasLimit := quotaNumberField(payload, "limit", "total", "quota")
	used, hasUsed := quotaNumberField(payload, "used", "usage", "consumed")
	remaining, hasRemaining := quotaNumberField(payload, "remaining", "available")
	if !hasLimit && !hasUsed && !hasRemaining {
		return model.AccountQuotaWindow{}, false
	}
	// DengDeng-compatible usage responses use limit=0 for an unlimited key.
	// Do not turn that sentinel into a misleading "0 remaining" allowance.
	if hasLimit && limit <= 0 {
		return model.AccountQuotaWindow{}, false
	}
	unit := quotaStringField(payload, "unit", "currency")
	if unit == "" {
		unit = fallbackUnit
	}
	window := model.AccountQuotaWindow{Key: key, Label: label, Unit: unit}
	if hasLimit && limit > 0 {
		window.Limit = &limit
	}
	if hasRemaining {
		window.Remaining = &remaining
	} else if hasLimit && hasUsed && limit > 0 {
		value := math.Max(0, limit-used)
		window.Remaining = &value
	}
	if hasLimit && limit > 0 {
		value := used
		if !hasUsed && hasRemaining {
			value = limit - remaining
		}
		percent := clampPercent(value / limit * 100)
		window.UsedPercent = &percent
	}
	return window, true
}

func quotaAmountWindow(key, label string, limit, used, remaining float64, hasRemaining bool, unit string) model.AccountQuotaWindow {
	window := model.AccountQuotaWindow{Key: key, Label: label, Limit: &limit, Unit: unit}
	if hasRemaining {
		window.Remaining = &remaining
	} else {
		value := math.Max(0, limit-used)
		window.Remaining = &value
	}
	if limit > 0 {
		percent := clampPercent(used / limit * 100)
		window.UsedPercent = &percent
	}
	return window
}

func quotaNumberField(payload map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, exists := payload[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed, true
		case float32:
			return float64(typed), true
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return parsed, true
			}
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func quotaStringField(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func quotaBoolField(payload map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := payload[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func mergeQuotaWindows(groups ...[]model.AccountQuotaWindow) []model.AccountQuotaWindow {
	result := make([]model.AccountQuotaWindow, 0)
	positions := make(map[string]int)
	for _, windows := range groups {
		for _, window := range windows {
			if window.Key == "" {
				continue
			}
			if index, exists := positions[window.Key]; exists {
				result[index] = window
				continue
			}
			positions[window.Key] = len(result)
			result = append(result, window)
		}
	}
	return result
}

func onlyRateLimitWindows(windows []model.AccountQuotaWindow) []model.AccountQuotaWindow {
	result := make([]model.AccountQuotaWindow, 0, len(windows))
	for _, window := range windows {
		if strings.HasPrefix(window.Key, "rate_") || window.Key == "requests" || window.Key == "tokens" || window.Key == "input_tokens" || window.Key == "output_tokens" {
			result = append(result, window)
		}
	}
	return result
}

type quotaWindowPayload struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type codexUsagePayload struct {
	PlanType  string `json:"plan_type"`
	RateLimit *struct {
		Allowed         bool                `json:"allowed"`
		LimitReached    bool                `json:"limit_reached"`
		PrimaryWindow   *quotaWindowPayload `json:"primary_window"`
		SecondaryWindow *quotaWindowPayload `json:"secondary_window"`
	} `json:"rate_limit"`
}

func (s *AccountQuotaService) refreshOpenAI(ctx context.Context, account *model.UpstreamAccount, authorization, metadataToken string, snapshot *model.AccountQuotaSnapshot) error {
	accountID := chatGPTAccountID(account)
	if accountID == "" {
		return fmt.Errorf("missing ChatGPT account id")
	}
	client, err := s.clientFor(account)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAICodexUsageURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
	var payload codexUsagePayload
	if err := doQuotaJSON(client, req, &payload); err != nil {
		return fmt.Errorf("Codex quota: %w", err)
	}
	snapshot.Source = "codex_subscription"
	if strings.TrimSpace(payload.PlanType) != "" {
		snapshot.PlanType = strings.TrimSpace(payload.PlanType)
	}
	snapshot.Windows = nil
	if payload.RateLimit != nil {
		if payload.RateLimit.PrimaryWindow != nil {
			snapshot.Windows = append(snapshot.Windows, normalizedProviderWindow("primary", providerWindowLabel(payload.RateLimit.PrimaryWindow.LimitWindowSeconds, "主窗口"), payload.RateLimit.PrimaryWindow))
		}
		if payload.RateLimit.SecondaryWindow != nil {
			snapshot.Windows = append(snapshot.Windows, normalizedProviderWindow("secondary", providerWindowLabel(payload.RateLimit.SecondaryWindow.LimitWindowSeconds, "次窗口"), payload.RateLimit.SecondaryWindow))
		}
		if payload.RateLimit.LimitReached || !payload.RateLimit.Allowed {
			snapshot.Message = "上游订阅额度已用尽，等待窗口重置"
		}
	}
	// Token expiry and subscription expiry are unrelated. The latter is read
	// from ChatGPT's lightweight subscription endpoint, with accounts/check as
	// a fallback for organization and education plans. Failure is best-effort:
	// the valid usage windows above must remain available.
	if strings.TrimSpace(metadataToken) != "" {
		enrichOpenAISubscription(ctx, client, account, metadataToken, snapshot)
	}
	snapshot.State = "ready"
	fetched := time.Now().UTC()
	snapshot.FetchedAt = &fetched
	return nil
}

// refreshOpenAIAgentIdentity uses the same signed assertion as normal Codex
// traffic. A stale task is renewed once and persisted atomically under the
// per-account refresh lock; no OAuth token is required or stored.
func (s *AccountQuotaService) refreshOpenAIAgentIdentity(ctx context.Context, account *model.UpstreamAccount, snapshot *model.AccountQuotaSnapshot) error {
	if account == nil || account.Platform != model.PlatformOpenAI {
		return fmt.Errorf("Agent Identity is only available for OpenAI accounts")
	}
	client, err := s.clientFor(account)
	if err != nil {
		return err
	}
	record, err := AgentIdentityRecordFromAccount(account)
	if err != nil {
		return err
	}
	for recovered := false; ; {
		if record.TaskID == "" {
			record.TaskID, err = RegisterOpenAIAgentTask(ctx, client, record)
			if err != nil {
				return fmt.Errorf("Agent Identity task registration failed: %w", err)
			}
			if err := s.persistAgentIdentityRecord(account, record); err != nil {
				return err
			}
		}
		authorization, authErr := OpenAIAgentIdentityAuthorization(record, time.Now())
		if authErr != nil {
			return authErr
		}
		err = s.refreshOpenAI(ctx, account, authorization, "", snapshot)
		if err == nil {
			return nil
		}
		var upstreamErr *quotaHTTPError
		if recovered || !errors.As(err, &upstreamErr) || !IsOpenAIAgentTaskInvalid(upstreamErr.StatusCode, upstreamErr.Body) {
			return err
		}
		recovered = true
		record.TaskID = ""
	}
}

func (s *AccountQuotaService) persistAgentIdentityRecord(account *model.UpstreamAccount, record AgentIdentityRecord) error {
	extra, err := model.EncodeExtra(AgentIdentityExtra(record))
	if err != nil {
		return err
	}
	if err := s.db.Model(&model.UpstreamAccount{}).Where("id = ?", account.ID).Update("extra", extra).Error; err != nil {
		return err
	}
	account.Extra = extra
	return nil
}

type openAISubscriptionPayload struct {
	PlanType    string `json:"plan_type"`
	ActiveUntil string `json:"active_until"`
}

func enrichOpenAISubscription(ctx context.Context, client *http.Client, account *model.UpstreamAccount, token string, snapshot *model.AccountQuotaSnapshot) {
	if client == nil || account == nil || snapshot == nil || strings.TrimSpace(token) == "" {
		return
	}
	if accountID := chatGPTAccountID(account); accountID != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAISubscriptionsURL, nil)
		if err == nil {
			query := req.URL.Query()
			query.Set("account_id", accountID)
			req.URL.RawQuery = query.Encode()
			applyChatGPTMetadataHeaders(req, token)
			var payload openAISubscriptionPayload
			if doQuotaJSON(client, req, &payload) == nil {
				if plan := strings.TrimSpace(payload.PlanType); plan != "" {
					snapshot.PlanType = plan
				}
				if expiry := parseQuotaTime(payload.ActiveUntil); expiry != nil {
					snapshot.SubscriptionExpiresAt = expiry
					return
				}
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIAccountsCheckURL, nil)
	if err != nil {
		return
	}
	applyChatGPTMetadataHeaders(req, token)
	var payload map[string]any
	if doQuotaJSON(client, req, &payload) != nil {
		return
	}
	plan, expiry := selectOpenAIAccountEntitlement(payload, account, snapshot.PlanType)
	if plan != "" {
		snapshot.PlanType = plan
	}
	if expiry != nil {
		snapshot.SubscriptionExpiresAt = expiry
	}
}

func applyChatGPTMetadataHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("Accept", "application/json")
}

func selectOpenAIAccountEntitlement(payload map[string]any, account *model.UpstreamAccount, currentPlan string) (string, *time.Time) {
	accounts := quotaMap(payload["accounts"])
	if len(accounts) == 0 {
		return "", nil
	}
	if organizationID := accountOrganizationID(account); organizationID != "" {
		if candidate := quotaMap(accounts[organizationID]); candidate != nil {
			if plan, expiry := openAIEntitlement(candidate); plan != "" || expiry != nil {
				return plan, expiry
			}
		}
	}
	type candidate struct {
		plan      string
		expiry    *time.Time
		isDefault bool
	}
	var preferred, paid, fallback candidate
	for _, raw := range accounts {
		item := quotaMap(raw)
		if item == nil {
			continue
		}
		plan, expiry := openAIEntitlement(item)
		if plan == "" && expiry == nil {
			continue
		}
		entry := candidate{plan: plan, expiry: expiry}
		if metadata := quotaMap(item["account"]); metadata != nil {
			entry.isDefault, _ = metadata["is_default"].(bool)
		}
		if fallback.plan == "" && fallback.expiry == nil {
			fallback = entry
		}
		if strings.EqualFold(plan, strings.TrimSpace(currentPlan)) && currentPlan != "" {
			preferred = entry
		}
		if paid.plan == "" && !strings.EqualFold(plan, "free") {
			paid = entry
		}
		if entry.isDefault {
			return entry.plan, entry.expiry
		}
	}
	if preferred.plan != "" || preferred.expiry != nil {
		return preferred.plan, preferred.expiry
	}
	if paid.plan != "" || paid.expiry != nil {
		return paid.plan, paid.expiry
	}
	return fallback.plan, fallback.expiry
}

func openAIEntitlement(item map[string]any) (string, *time.Time) {
	metadata := quotaMap(item["account"])
	plan, _ := metadata["plan_type"].(string)
	entitlement := quotaMap(item["entitlement"])
	if strings.TrimSpace(plan) == "" {
		plan, _ = entitlement["subscription_plan"].(string)
	}
	return strings.TrimSpace(plan), quotaTimeValue(entitlement["expires_at"])
}

type claudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeUsagePayload struct {
	FiveHour                *claudeUsageWindow `json:"five_hour"`
	SevenDay                *claudeUsageWindow `json:"seven_day"`
	SevenDaySonnet          *claudeUsageWindow `json:"seven_day_sonnet"`
	SevenDayOverageIncluded *claudeUsageWindow `json:"seven_day_overage_included"`
}

func (s *AccountQuotaService) refreshClaude(ctx context.Context, account *model.UpstreamAccount, token string, snapshot *model.AccountQuotaSnapshot) error {
	client, err := s.clientFor(account)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOAuthUsageURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "claude-code/2.1.7")
	var payload claudeUsagePayload
	if err := doQuotaJSON(client, req, &payload); err != nil {
		return fmt.Errorf("Claude quota: %w", err)
	}
	snapshot.Source = "claude_subscription"
	snapshot.Windows = nil
	for _, item := range []struct {
		key, label string
		window     *claudeUsageWindow
	}{
		{"five_hour", "5 小时窗口", payload.FiveHour},
		{"seven_day", "7 天窗口", payload.SevenDay},
		{"seven_day_sonnet", "Sonnet 7 天", payload.SevenDaySonnet},
		{"seven_day_fable", "扩展 7 天", payload.SevenDayOverageIncluded},
	} {
		if item.window == nil {
			continue
		}
		used := clampPercent(item.window.Utilization)
		snapshot.Windows = append(snapshot.Windows, model.AccountQuotaWindow{
			Key: item.key, Label: item.label, UsedPercent: &used, Unit: "%", ResetAt: parseQuotaTime(item.window.ResetsAt),
		})
	}
	if len(snapshot.Windows) == 0 {
		snapshot.State = "partial"
		snapshot.Message = "Claude 已响应，但没有返回可识别的额度窗口"
	} else {
		snapshot.State = "ready"
	}
	fetched := time.Now().UTC()
	snapshot.FetchedAt = &fetched
	return nil
}

type grokBillingPeriod struct {
	Type  string `json:"type"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type grokProductUsage struct {
	Product      string   `json:"product"`
	UsagePercent *float64 `json:"usagePercent"`
}

type grokBillingPayload struct {
	Config *struct {
		CurrentPeriod      *grokBillingPeriod `json:"currentPeriod"`
		CreditUsagePercent *float64           `json:"creditUsagePercent"`
		ProductUsage       []grokProductUsage `json:"productUsage"`
		MonthlyLimit       json.RawMessage    `json:"monthlyLimit"`
		Used               json.RawMessage    `json:"used"`
		BillingPeriodStart string             `json:"billingPeriodStart"`
		BillingPeriodEnd   string             `json:"billingPeriodEnd"`
	} `json:"config"`
}

func (s *AccountQuotaService) refreshGrok(ctx context.Context, account *model.UpstreamAccount, token string, snapshot *model.AccountQuotaSnapshot) error {
	type result struct {
		weekly  bool
		payload grokBillingPayload
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, weekly := range []bool{true, false} {
		weekly := weekly
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload, err := s.fetchGrokBilling(ctx, account, token, weekly)
			results <- result{weekly: weekly, payload: payload, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var weeklyPayload, monthlyPayload *grokBillingPayload
	var errorsFound []string
	for item := range results {
		if item.err != nil {
			errorsFound = append(errorsFound, item.err.Error())
			continue
		}
		payload := item.payload
		if item.weekly {
			weeklyPayload = &payload
		} else {
			monthlyPayload = &payload
		}
	}
	if weeklyPayload == nil && monthlyPayload == nil {
		return fmt.Errorf("Grok billing: %s", strings.Join(errorsFound, "; "))
	}
	snapshot.Source = "grok_billing"
	snapshot.Windows = nil
	if weeklyPayload != nil && weeklyPayload.Config != nil {
		config := weeklyPayload.Config
		if config.CreditUsagePercent != nil {
			used := clampPercent(*config.CreditUsagePercent)
			window := model.AccountQuotaWindow{Key: "weekly", Label: "周额度", UsedPercent: &used, Unit: "%"}
			if config.CurrentPeriod != nil {
				window.ResetAt = parseQuotaTime(config.CurrentPeriod.End)
			}
			snapshot.Windows = append(snapshot.Windows, window)
		} else if config.CurrentPeriod != nil {
			// Free and unified-billing accounts can return the weekly period
			// without creditUsagePercent. Preserve the provider's reset time
			// instead of treating the otherwise valid response as unknown.
			snapshot.Windows = append(snapshot.Windows, model.AccountQuotaWindow{
				Key: "weekly", Label: "周额度", Unit: "%", ResetAt: parseQuotaTime(config.CurrentPeriod.End),
			})
		}
		for _, product := range config.ProductUsage {
			if product.UsagePercent == nil || strings.TrimSpace(product.Product) == "" {
				continue
			}
			used := clampPercent(*product.UsagePercent)
			snapshot.Windows = append(snapshot.Windows, model.AccountQuotaWindow{
				Key:   "product_" + strings.ToLower(strings.ReplaceAll(product.Product, " ", "_")),
				Label: product.Product, UsedPercent: &used, Unit: "%",
			})
		}
	}
	if monthlyPayload != nil && monthlyPayload.Config != nil {
		config := monthlyPayload.Config
		limitCents := rawFloat(config.MonthlyLimit)
		usedCents := rawFloat(config.Used)
		if limitCents != nil || usedCents != nil {
			window := model.AccountQuotaWindow{Key: "monthly", Label: "月额度", Unit: "USD", ResetAt: parseQuotaTime(config.BillingPeriodEnd)}
			if limitCents != nil {
				limit := *limitCents / 100
				window.Limit = &limit
			}
			if limitCents != nil && usedCents != nil {
				remaining := math.Max(0, (*limitCents-*usedCents)/100)
				window.Remaining = &remaining
				if *limitCents > 0 {
					used := clampPercent(*usedCents / *limitCents * 100)
					window.UsedPercent = &used
				}
			}
			snapshot.Windows = append(snapshot.Windows, window)
			if snapshot.PlanType == "" && limitCents != nil {
				switch math.Round(*limitCents) {
				case 15000:
					snapshot.PlanType = "SuperGrok"
				case 150000:
					snapshot.PlanType = "SuperGrok Heavy"
				}
			}
		}
	}
	if len(errorsFound) > 0 {
		snapshot.State = "partial"
		snapshot.Message = "部分 Grok 额度窗口暂时查询失败，已保留成功结果"
	} else if len(snapshot.Windows) == 0 {
		snapshot.State = "partial"
		snapshot.Message = "Grok 已响应，但没有返回可识别的额度窗口"
	} else {
		snapshot.State = "ready"
	}
	fetched := time.Now().UTC()
	snapshot.FetchedAt = &fetched
	return nil
}

func (s *AccountQuotaService) fetchGrokBilling(ctx context.Context, account *model.UpstreamAccount, token string, weekly bool) (grokBillingPayload, error) {
	client, err := s.clientFor(account)
	if err != nil {
		return grokBillingPayload{}, err
	}
	path := "/billing"
	if weekly {
		path += "?format=credits"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grokCLIBillingBaseURL+path, nil)
	if err != nil {
		return grokBillingPayload{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-xai-token-auth", "xai-grok-cli")
	req.Header.Set("x-grok-client-version", "0.2.93")
	req.Header.Set("User-Agent", "grok-pager/0.2.93 grok-shell/0.2.93 (macos; aarch64)")
	var payload grokBillingPayload
	if err := doQuotaJSON(client, req, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

// ObserveRateLimitHeaders stores non-billable limit information returned by
// model-list probes. This is how static API-key accounts can expose useful
// request/token headroom without pretending that it is a cash balance.
func (s *AccountQuotaService) ObserveRateLimitHeaders(account *model.UpstreamAccount, headers http.Header, observedAt time.Time) error {
	if s == nil || s.db == nil || account == nil || account.ID <= 0 {
		return nil
	}
	windows := quotaWindowsFromHeaders(account.Platform, headers)
	if len(windows) == 0 {
		return nil
	}
	lock := s.lockFor(account.ID)
	lock.Lock()
	defer lock.Unlock()
	var snapshot model.AccountQuotaSnapshot
	_ = s.db.Where("upstream_account_id = ?", account.ID).First(&snapshot).Error
	snapshot.UpstreamAccountID = account.ID
	snapshot.Platform = account.Platform
	if snapshot.Source != "api_key_usage" {
		snapshot.Source = "rate_limit_headers"
	}
	snapshot.State = "ready"
	snapshot.PlanType = accountPlanType(account)
	if snapshot.Source == "api_key_usage" {
		snapshot.Message = "上游 API Key 余额与实时限额已同步"
		snapshot.Windows = mergeQuotaWindows(snapshot.Windows, windows)
	} else {
		snapshot.Message = ""
		snapshot.Windows = windows
	}
	fetched := observedAt.UTC()
	snapshot.FetchedAt = &fetched
	if len(snapshot.ObservedUsage) == 0 {
		snapshot.ObservedUsage = s.observedUsage(account.ID, fetched)
	}
	return s.save(&snapshot)
}

func quotaWindowsFromHeaders(platform string, headers http.Header) []model.AccountQuotaWindow {
	dimensions := []struct {
		key, label, dimension string
	}{
		{"rate_requests", "请求限额", "requests"},
		{"rate_tokens", "Token 限额", "tokens"},
		{"rate_input_tokens", "输入 Token 限额", "input_tokens"},
		{"rate_output_tokens", "输出 Token 限额", "output_tokens"},
	}
	windows := make([]model.AccountQuotaWindow, 0, len(dimensions))
	for _, item := range dimensions {
		limit, remaining, reset := providerRateHeaders(platform, headers, item.dimension)
		if limit == nil && remaining == nil {
			continue
		}
		windows = append(windows, rateHeaderWindow(item.key, item.label, limit, remaining, reset))
	}
	return windows
}

func providerRateHeaders(platform string, headers http.Header, dimension string) (*float64, *float64, *time.Time) {
	headerDimension := strings.ReplaceAll(dimension, "_", "-")
	prefix := "x-ratelimit-"
	if platform == model.PlatformAnthropic {
		prefix = "anthropic-ratelimit-"
	}
	limitNames := []string{prefix + headerDimension + "-limit", "x-ratelimit-limit-" + headerDimension}
	remainingNames := []string{prefix + headerDimension + "-remaining", "x-ratelimit-remaining-" + headerDimension}
	resetNames := []string{prefix + headerDimension + "-reset", "x-ratelimit-reset-" + headerDimension}
	if dimension == "requests" {
		limitNames = append(limitNames, "ratelimit-limit")
		remainingNames = append(remainingNames, "ratelimit-remaining")
		resetNames = append(resetNames, "ratelimit-reset")
	}
	limit := headerFloat(headers, limitNames...)
	remaining := headerFloat(headers, remainingNames...)
	reset := headerReset(headers, resetNames...)
	return limit, remaining, reset
}

func rateHeaderWindow(key, label string, limit, remaining *float64, reset *time.Time) model.AccountQuotaWindow {
	unit := "tokens"
	if strings.Contains(key, "request") {
		unit = "requests"
	}
	window := model.AccountQuotaWindow{Key: key, Label: label, Limit: limit, Remaining: remaining, Unit: unit, ResetAt: reset}
	if limit != nil && remaining != nil && *limit > 0 {
		used := clampPercent((*limit - *remaining) / *limit * 100)
		window.UsedPercent = &used
	}
	return window
}

func (s *AccountQuotaService) clientFor(account *model.UpstreamAccount) (*http.Client, error) {
	if account.ProxyID > 0 {
		proxy := account.Proxy
		if proxy == nil || proxy.ID != account.ProxyID {
			proxy = &model.Proxy{}
			if err := s.db.First(proxy, account.ProxyID).Error; err != nil {
				return nil, fmt.Errorf("assigned proxy is unavailable")
			}
		}
		if proxy.Status != model.StatusActive {
			return nil, fmt.Errorf("assigned proxy is disabled")
		}
		proxyURL, err := proxy.URL()
		if err != nil {
			return nil, fmt.Errorf("assigned proxy is invalid")
		}
		return config.NewProxyHTTPClient(proxyURL, "", accountQuotaTimeout)
	}
	if s.defaultClient != nil {
		return s.defaultClient, nil
	}
	if s.cfg != nil {
		return s.cfg.Proxy.HTTPClient(accountQuotaTimeout)
	}
	return config.NewProxyHTTPClient("", "", accountQuotaTimeout)
}

type quotaHTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *quotaHTTPError) Error() string {
	return fmt.Sprintf("upstream status %d: %s", e.StatusCode, truncateQuotaText(string(e.Body), 220))
}

func doQuotaJSON(client *http.Client, req *http.Request, target any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &quotaHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid upstream JSON")
	}
	return nil
}

func accountPlanType(account *model.UpstreamAccount) string {
	if account == nil {
		return ""
	}
	extra := account.DecodeExtra()
	for _, key := range []string{"plan_type", "subscription_tier", "tier_id", "subscription_type"} {
		if value, _ := extra[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func accountSubscriptionExpiresAt(account *model.UpstreamAccount) *time.Time {
	if account == nil {
		return nil
	}
	extra := account.DecodeExtra()
	for _, key := range []string{"subscription_expires_at", "subscription_active_until", "chatgpt_subscription_active_until"} {
		if expiry := quotaTimeValue(extra[key]); expiry != nil {
			return expiry
		}
	}
	for _, tokenKey := range []string{"id_token", "access_token"} {
		token, _ := extra[tokenKey].(string)
		if tokenKey == "access_token" && token == "" {
			token = string(account.AccessToken)
		}
		claims := quotaJWTClaims(token)
		if claims == nil {
			continue
		}
		for _, source := range []map[string]any{claims, quotaMap(claims["https://api.openai.com/auth"])} {
			for _, key := range []string{"subscription_expires_at", "subscription_active_until", "chatgpt_subscription_active_until"} {
				if expiry := quotaTimeValue(source[key]); expiry != nil {
					return expiry
				}
			}
		}
	}
	return nil
}

func quotaJWTClaims(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func quotaMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func quotaSlice(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped := quotaMap(item); mapped != nil {
			result = append(result, mapped)
		}
	}
	return result
}

func quotaTimeValue(value any) *time.Time {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, text); err == nil {
				utc := parsed.UTC()
				return &utc
			}
		}
		if unix, err := strconv.ParseInt(text, 10, 64); err == nil {
			return quotaUnixTime(unix)
		}
	case float64:
		return quotaUnixTime(int64(typed))
	case json.Number:
		if unix, err := typed.Int64(); err == nil {
			return quotaUnixTime(unix)
		}
	}
	return nil
}

func quotaUnixTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	var parsed time.Time
	if value > 1e12 {
		parsed = time.UnixMilli(value).UTC()
	} else {
		parsed = time.Unix(value, 0).UTC()
	}
	return &parsed
}

func chatGPTAccountID(account *model.UpstreamAccount) string {
	if account == nil {
		return ""
	}
	if id := strings.TrimSpace(account.AccountID); id != "" {
		return id
	}
	extra := account.DecodeExtra()
	for _, key := range []string{"chatgpt_account_id", "organization_id"} {
		if id, _ := extra[key].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func accountOrganizationID(account *model.UpstreamAccount) string {
	if account == nil {
		return ""
	}
	extra := account.DecodeExtra()
	for _, key := range []string{"organization_id", "org_id", "poid"} {
		if id, _ := extra[key].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func normalizedProviderWindow(key, label string, source *quotaWindowPayload) model.AccountQuotaWindow {
	used := clampPercent(source.UsedPercent)
	return model.AccountQuotaWindow{
		Key: key, Label: label, UsedPercent: &used, Unit: "%",
		ResetAt: quotaResetAt(source.ResetAt, source.ResetAfterSeconds),
	}
}

func providerWindowLabel(seconds int64, fallback string) string {
	switch {
	case seconds >= 6*24*60*60:
		return "7 天窗口"
	case seconds >= 4*60*60 && seconds <= 6*60*60:
		return "5 小时窗口"
	case seconds > 0:
		return fmt.Sprintf("%d 小时窗口", int(math.Round(float64(seconds)/3600)))
	default:
		return fallback
	}
}

func quotaResetAt(unixSeconds, afterSeconds int64) *time.Time {
	if unixSeconds > 0 {
		return quotaUnixTime(unixSeconds)
	}
	if afterSeconds > 0 {
		value := time.Now().UTC().Add(time.Duration(afterSeconds) * time.Second)
		return &value
	}
	return nil
}

func parseQuotaTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if value, err := time.Parse(layout, raw); err == nil {
			utc := value.UTC()
			return &utc
		}
	}
	return nil
}

func rawFloat(raw json.RawMessage) *float64 {
	if len(raw) == 0 {
		return nil
	}
	// Grok billing represents currency values as {"val": 123}; older
	// responses and compatible relays may still return a bare number/string.
	var wrapped struct {
		Val json.RawMessage `json:"val"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Val) > 0 {
		return rawFloat(wrapped.Val)
	}
	text := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if text == "" || text == "null" {
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil
	}
	return &value
}

func headerFloat(headers http.Header, names ...string) *float64 {
	for _, name := range names {
		if raw := strings.TrimSpace(headers.Get(name)); raw != "" {
			if value, err := strconv.ParseFloat(raw, 64); err == nil {
				return &value
			}
		}
	}
	return nil
}

func headerReset(headers http.Header, names ...string) *time.Time {
	for _, name := range names {
		raw := strings.TrimSpace(headers.Get(name))
		if raw == "" {
			continue
		}
		if parsed := parseQuotaTime(raw); parsed != nil {
			return parsed
		}
		if unixValue, err := strconv.ParseInt(raw, 10, 64); err == nil && unixValue > 0 {
			parsed := time.Unix(unixValue, 0).UTC()
			return &parsed
		}
		if duration, err := time.ParseDuration(raw); err == nil {
			parsed := time.Now().UTC().Add(duration)
			return &parsed
		}
	}
	return nil
}

func clampPercent(value float64) float64 {
	return math.Min(100, math.Max(0, value))
}

func friendlyQuotaError(err error) string {
	if err == nil {
		return "额度刷新失败"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "missing chatgpt account"):
		return "缺少上游账号标识，请重新授权或重新导入凭证"
	case strings.Contains(message, "quota url must use the same origin"):
		return "额度查询地址必须与 Base URL 同域"
	case strings.Contains(message, "invalid api key quota url"):
		return "额度查询地址格式无效，请填写同站路径或同域完整地址"
	case strings.Contains(message, "custom api key quota endpoint returned status"):
		return "第三方中转拒绝了额度查询，请检查 API Key 与查询地址"
	case strings.Contains(message, "grok billing") && strings.Contains(message, "status 404"):
		return "Grok 额度接口地址无效，请检查上游 Base URL"
	case strings.Contains(message, "api key is inactive"):
		return "上游返回该 API Key 已停用"
	case strings.Contains(message, "api key credential probe returned status 401"), strings.Contains(message, "api key credential probe returned status 403"):
		return "上游拒绝了当前 API Key，请检查密钥和 Base URL"
	case strings.Contains(message, "status 401"), strings.Contains(message, "status 403"), strings.Contains(message, "invalid_grant"):
		return "上游拒绝了当前凭证，请重新授权"
	case strings.Contains(message, "status 429"):
		return "上游额度查询过于频繁，系统稍后会自动重试"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "上游额度查询超时，系统稍后会自动重试"
	case strings.Contains(message, "proxy"):
		return "额度查询无法通过当前代理连接上游"
	default:
		return "上游额度暂时查询失败，系统稍后会自动重试"
	}
}

func truncateQuotaText(value string, length int) string {
	value = strings.TrimSpace(value)
	if len(value) <= length {
		return value
	}
	return value[:length]
}
