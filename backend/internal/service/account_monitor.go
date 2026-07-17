package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// AccountMonitor performs non-billable health checks in the background. API
// key accounts use their provider's model-list endpoint; OAuth accounts only
// verify token freshness plus HTTPS transport because a Codex response would
// consume the account's subscription allowance.
type AccountMonitor struct {
	db      *gorm.DB
	cfg     *config.Config
	policy  *RuntimePolicyService
	alerts  *AlertService
	started sync.Once
	running atomic.Bool
}

func NewAccountMonitor(db *gorm.DB, cfg *config.Config) *AccountMonitor {
	return &AccountMonitor{db: db, cfg: cfg}
}

func (m *AccountMonitor) SetRuntimePolicy(policy *RuntimePolicyService) {
	m.policy = policy
}

func (m *AccountMonitor) SetAlertService(alerts *AlertService) { m.alerts = alerts }

func (m *AccountMonitor) runtimePolicy() GatewayRuntimePolicy {
	if m != nil && m.policy != nil {
		return m.policy.Current()
	}
	return DefaultGatewayRuntimePolicy()
}

func (m *AccountMonitor) Start() {
	if m == nil || m.db == nil {
		return
	}
	m.started.Do(func() {
		go func() {
			m.Trigger()
			for {
				timer := time.NewTimer(m.runtimePolicy().ProbeInterval())
				<-timer.C
				m.Trigger()
			}
		}()
	})
}

// Trigger starts one full pass if another one is not already in progress.
// It returns false when a running pass already owns the account set.
func (m *AccountMonitor) Trigger() bool {
	if m == nil || m.db == nil || !m.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer m.running.Store(false)
		m.runAll(context.Background())
	}()
	return true
}

func (m *AccountMonitor) runAll(parent context.Context) {
	var accounts []model.UpstreamAccount
	if err := m.db.Preload("Proxy").Where("status = ?", model.StatusActive).Find(&accounts).Error; err != nil {
		return
	}
	policy := m.runtimePolicy()
	sem := make(chan struct{}, policy.ProbeConcurrency)
	var group sync.WaitGroup
	for i := range accounts {
		account := accounts[i]
		group.Add(1)
		go func() {
			defer group.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, _ = m.Probe(parent, &account)
		}()
	}
	group.Wait()
	_ = m.db.Where("checked_at < ?", time.Now().UTC().Add(-policy.ProbeRetention())).Delete(&model.AccountProbe{}).Error
}

// ProbeAccount lets an administrator request a fresh check for one account.
func (m *AccountMonitor) ProbeAccount(parent context.Context, accountID int64) (model.AccountProbe, error) {
	if m == nil || m.db == nil || accountID <= 0 {
		return model.AccountProbe{}, fmt.Errorf("invalid account")
	}
	var account model.UpstreamAccount
	if err := m.db.Preload("Proxy").First(&account, accountID).Error; err != nil {
		return model.AccountProbe{}, err
	}
	return m.Probe(parent, &account)
}

func (m *AccountMonitor) Probe(parent context.Context, account *model.UpstreamAccount) (model.AccountProbe, error) {
	checkedAt := time.Now().UTC()
	if account == nil || account.ID == 0 {
		return model.AccountProbe{State: "down", CheckedAt: checkedAt}, fmt.Errorf("invalid account")
	}
	probe := model.AccountProbe{AccountID: account.ID, State: "down", CheckedAt: checkedAt}
	if account.AuthType == model.AuthOAuth {
		probe.Mode = "transport"
		if account.ExpiresAt != nil && !account.ExpiresAt.After(checkedAt) {
			probe.State = "expired"
			probe.ErrorMessage = "OAuth access token expired; it will refresh on the next provider request"
			return m.persistProbe(probe)
		}
	} else {
		probe.Mode = "api"
	}

	requestURL, err := accountProbeURL(account)
	if err != nil {
		probe.ErrorMessage = err.Error()
		return m.persistProbe(probe)
	}
	ctx, cancel := context.WithTimeout(parent, m.runtimePolicy().ProbeTimeout())
	defer cancel()
	method := http.MethodGet
	if account.AuthType == model.AuthOAuth {
		// A HEAD request is a pure transport check for the ChatGPT/Codex host.
		// Any completed HTTP response proves the account's configured proxy and
		// outbound TLS route are alive without generating model output.
		method = http.MethodHead
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		probe.ErrorMessage = err.Error()
		return m.persistProbe(probe)
	}
	applyProbeCredential(req, account)
	client, err := m.clientFor(account)
	if err != nil {
		probe.ErrorMessage = err.Error()
		return m.persistProbe(probe)
	}
	started := time.Now()
	response, err := client.Do(req)
	probe.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		probe.ErrorMessage = err.Error()
		return m.persistProbe(probe)
	}
	response.Body.Close()
	probe.StatusCode = response.StatusCode
	if account.AuthType == model.AuthOAuth {
		if response.StatusCode < http.StatusInternalServerError {
			probe.State = "healthy"
		} else {
			probe.State = "down"
			probe.ErrorMessage = "OAuth upstream returned " + response.Status
		}
		return m.persistProbe(probe)
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 400:
		probe.State = "healthy"
	case response.StatusCode >= 500:
		probe.State = "down"
		probe.ErrorMessage = "upstream returned " + response.Status
	default:
		probe.State = "degraded"
		probe.ErrorMessage = "credential or endpoint returned " + response.Status
	}
	return m.persistProbe(probe)
}

func (m *AccountMonitor) persistProbe(probe model.AccountProbe) (model.AccountProbe, error) {
	if err := m.db.Create(&probe).Error; err != nil {
		return probe, err
	}
	if m.alerts != nil {
		m.alerts.EvaluateProbe(probe)
	}
	return probe, nil
}

func (m *AccountMonitor) clientFor(account *model.UpstreamAccount) (*http.Client, error) {
	if account.ProxyID > 0 {
		proxy := account.Proxy
		if proxy == nil || proxy.ID != account.ProxyID {
			proxy = &model.Proxy{}
			if err := m.db.First(proxy, account.ProxyID).Error; err != nil {
				return nil, fmt.Errorf("assigned proxy is unavailable")
			}
		}
		if proxy.Status != model.StatusActive {
			return nil, fmt.Errorf("assigned proxy is disabled")
		}
		proxyURL, err := proxy.URL()
		if err != nil {
			return nil, fmt.Errorf("assigned proxy is invalid: %w", err)
		}
		return config.NewProxyHTTPClient(proxyURL, "", m.runtimePolicy().ProbeTimeout())
	}
	if m.cfg == nil {
		return config.NewProxyHTTPClient("", "", m.runtimePolicy().ProbeTimeout())
	}
	return m.cfg.Proxy.HTTPClient(m.runtimePolicy().ProbeTimeout())
}

func accountProbeURL(account *model.UpstreamAccount) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(account.BaseURL), "/")
	if account.AuthType == model.AuthOAuth {
		if base == "" {
			if account.Platform == model.PlatformGrok {
				return "https://cli-chat-proxy.grok.com/", nil
			}
			return "https://chatgpt.com/", nil
		}
		parsed, err := url.Parse(base)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("invalid OAuth upstream URL")
		}
		return parsed.Scheme + "://" + parsed.Host + "/", nil
	}
	if account.Platform == model.PlatformGrok {
		// The relay path carries /v1, so drop a trailing /v1 an operator may
		// have entered as part of the xAI base URL.
		base = strings.TrimSuffix(base, "/v1")
	}
	if base == "" {
		switch account.Platform {
		case model.PlatformAnthropic:
			base = "https://api.anthropic.com"
		case model.PlatformOpenAI:
			base = "https://api.openai.com"
		case model.PlatformGemini:
			base = "https://generativelanguage.googleapis.com"
		case model.PlatformGrok:
			base = "https://api.x.ai"
		default:
			return "", fmt.Errorf("unsupported platform")
		}
	}
	if account.Platform == model.PlatformGemini {
		return base + "/v1beta/models", nil
	}
	return base + "/v1/models", nil
}

func applyProbeCredential(req *http.Request, account *model.UpstreamAccount) {
	if account.AuthType == model.AuthOAuth {
		req.Header.Set("Authorization", "Bearer "+string(account.AccessToken))
		if account.AccountID != "" {
			req.Header.Set("chatgpt-account-id", account.AccountID)
		}
		return
	}
	switch account.Platform {
	case model.PlatformAnthropic:
		req.Header.Set("x-api-key", string(account.APIKey))
		req.Header.Set("anthropic-version", "2023-06-01")
	case model.PlatformOpenAI, model.PlatformGrok:
		req.Header.Set("Authorization", "Bearer "+string(account.APIKey))
	case model.PlatformGemini:
		req.Header.Set("x-goog-api-key", string(account.APIKey))
	}
}
