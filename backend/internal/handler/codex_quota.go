package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// This endpoint is the same ChatGPT/Codex usage source used by sub2api. It
// reports subscription rate-limit windows, which are distinct from API
// Platform credit and from DengDeng's local billing balance.
var codexQuotaUsageURL = "https://chatgpt.com/backend-api/wham/usage"

const codexQuotaTimeout = 20 * time.Second

type codexQuotaWindowPayload struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type codexQuotaRateLimitPayload struct {
	Allowed         bool                     `json:"allowed"`
	LimitReached    bool                     `json:"limit_reached"`
	PrimaryWindow   *codexQuotaWindowPayload `json:"primary_window"`
	SecondaryWindow *codexQuotaWindowPayload `json:"secondary_window"`
}

type codexQuotaUsagePayload struct {
	PlanType  string                      `json:"plan_type"`
	RateLimit *codexQuotaRateLimitPayload `json:"rate_limit"`
}

// RefreshCodexQuota obtains and persists a safe snapshot of an OpenAI OAuth
// account's actual Codex subscription allowance. It never returns OAuth
// tokens, account credentials, or the raw upstream body to the browser.
func (h *AdminHandler) RefreshCodexQuota(c *gin.Context) {
	if h.oauth == nil {
		util.Fail(c, http.StatusServiceUnavailable, "OAuth 服务暂不可用")
		return
	}

	var account model.UpstreamAccount
	if err := h.db.Preload("Proxy").First(&account, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "账号不存在")
		return
	}
	if account.Platform != model.PlatformOpenAI || account.AuthType != model.AuthOAuth {
		util.Fail(c, http.StatusBadRequest, "仅 OpenAI OAuth 账号支持 Codex 额度查询")
		return
	}
	chatGPTAccountID := codexChatGPTAccountID(&account)
	if chatGPTAccountID == "" {
		util.Fail(c, http.StatusBadRequest, "缺少 ChatGPT Account ID，请重新授权或重新导入账号")
		return
	}

	accessToken, err := h.oauth.AccessToken(c.Request.Context(), &account)
	if err != nil || strings.TrimSpace(accessToken) == "" {
		util.Fail(c, http.StatusBadGateway, "无法获取有效的 OAuth 凭据，请重新授权后再试")
		return
	}
	client, err := h.codexQuotaClient(&account)
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "无法建立额度查询连接："+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), codexQuotaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexQuotaUsageURL, nil)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "无法创建额度查询请求")
		return
	}
	for key, value := range codexQuotaHeaders(accessToken, chatGPTAccountID) {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "Codex 额度查询请求失败，请检查服务器出口或账号授权")
		return
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if readErr != nil {
		util.Fail(c, http.StatusBadGateway, "读取 Codex 额度结果失败")
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		util.Fail(c, http.StatusBadGateway, quotaUpstreamStatusMessage(resp.StatusCode))
		return
	}

	var payload codexQuotaUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		util.Fail(c, http.StatusBadGateway, "Codex 返回的额度数据无法识别")
		return
	}
	snapshot := projectCodexQuotaSnapshot(account.ID, account.DecodeExtra(), payload, time.Now().UTC())
	if err := upsertCodexQuotaSnapshot(h.db, &snapshot); err != nil {
		util.Fail(c, http.StatusInternalServerError, "保存 Codex 额度快照失败")
		return
	}
	util.OK(c, snapshot)
}

func codexChatGPTAccountID(account *model.UpstreamAccount) string {
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

func (h *AdminHandler) codexQuotaClient(account *model.UpstreamAccount) (*http.Client, error) {
	if account == nil || account.ProxyID == 0 {
		if h != nil && h.codexQuotaHTTPClient != nil {
			return h.codexQuotaHTTPClient, nil
		}
		return config.NewProxyHTTPClient("", "", codexQuotaTimeout)
	}
	if account.Proxy == nil || account.Proxy.ID != account.ProxyID {
		return nil, fmt.Errorf("assigned proxy is unavailable")
	}
	if account.Proxy.Status != model.StatusActive {
		return nil, fmt.Errorf("assigned proxy is disabled")
	}
	proxyURL, err := account.Proxy.URL()
	if err != nil {
		return nil, fmt.Errorf("assigned proxy is invalid")
	}
	return config.NewProxyHTTPClient(proxyURL, "", codexQuotaTimeout)
}

func codexQuotaHeaders(accessToken, chatGPTAccountID string) map[string]string {
	return map[string]string{
		"Authorization":      "Bearer " + accessToken,
		"chatgpt-account-id": chatGPTAccountID,
		"OpenAI-Beta":        "codex-1",
		"oai-language":       "zh-CN",
		"Originator":         "Codex Desktop",
		"Accept":             "application/json",
		"Sec-Fetch-Site":     "none",
		"Sec-Fetch-Mode":     "no-cors",
		"Sec-Fetch-Dest":     "empty",
		"Priority":           "u=4, i",
		"User-Agent":         "codex_cli_rs/0.144.1 (Ubuntu 22.04; x86_64) xterm-256color",
	}
}

func projectCodexQuotaSnapshot(accountID int64, extra map[string]any, payload codexQuotaUsagePayload, now time.Time) model.CodexQuotaSnapshot {
	planType := strings.TrimSpace(payload.PlanType)
	if planType == "" {
		planType, _ = extra["plan_type"].(string)
	}
	snapshot := model.CodexQuotaSnapshot{
		UpstreamAccountID: accountID,
		PlanType:          strings.TrimSpace(planType),
		Allowed:           true,
		FetchedAt:         now.UTC(),
	}
	if payload.RateLimit == nil {
		return snapshot
	}
	snapshot.Allowed = payload.RateLimit.Allowed
	snapshot.LimitReached = payload.RateLimit.LimitReached
	if w := payload.RateLimit.PrimaryWindow; w != nil {
		snapshot.HasPrimaryWindow = true
		snapshot.PrimaryUsedPercent = normalizeCodexUsedPercent(w.UsedPercent)
		snapshot.PrimaryWindowSeconds = maxInt64(0, w.LimitWindowSeconds)
		snapshot.PrimaryResetAfterSeconds = maxInt64(0, w.ResetAfterSeconds)
		snapshot.PrimaryResetAt = codexWindowResetAt(now, w.ResetAt, w.ResetAfterSeconds)
	}
	if w := payload.RateLimit.SecondaryWindow; w != nil {
		snapshot.HasSecondaryWindow = true
		snapshot.SecondaryUsedPercent = normalizeCodexUsedPercent(w.UsedPercent)
		snapshot.SecondaryWindowSeconds = maxInt64(0, w.LimitWindowSeconds)
		snapshot.SecondaryResetAfterSeconds = maxInt64(0, w.ResetAfterSeconds)
		snapshot.SecondaryResetAt = codexWindowResetAt(now, w.ResetAt, w.ResetAfterSeconds)
	}
	return snapshot
}

func normalizeCodexUsedPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func codexWindowResetAt(now time.Time, resetAt, resetAfterSeconds int64) *time.Time {
	if resetAt > 0 {
		value := time.Unix(resetAt, 0).UTC()
		return &value
	}
	if resetAfterSeconds > 0 {
		value := now.UTC().Add(time.Duration(resetAfterSeconds) * time.Second)
		return &value
	}
	return nil
}

func maxInt64(min, value int64) int64 {
	if value < min {
		return min
	}
	return value
}

func upsertCodexQuotaSnapshot(db *gorm.DB, snapshot *model.CodexQuotaSnapshot) error {
	var existing model.CodexQuotaSnapshot
	err := db.Where("upstream_account_id = ?", snapshot.UpstreamAccountID).First(&existing).Error
	if err == nil {
		snapshot.ID = existing.ID
		return db.Model(&existing).Select("plan_type", "allowed", "limit_reached", "has_primary_window", "primary_used_percent", "primary_window_seconds", "primary_reset_after_seconds", "primary_reset_at", "has_secondary_window", "secondary_used_percent", "secondary_window_seconds", "secondary_reset_after_seconds", "secondary_reset_at", "fetched_at").Updates(snapshot).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Create(snapshot).Error
}

func quotaUpstreamStatusMessage(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "Codex 拒绝了该账号的额度查询，请重新授权"
	case http.StatusTooManyRequests:
		return "Codex 额度查询过于频繁，请稍后再试"
	default:
		return fmt.Sprintf("Codex 额度查询失败（上游状态 %d）", status)
	}
}
