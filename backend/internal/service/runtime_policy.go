package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

const runtimePolicyKey = "runtime.gateway_policy.v1"

// GatewayRuntimePolicy contains the operational switches that genuinely
// affect relay selection and low-cost account health checks. Values are kept
// intentionally bounded: this is a resilience control plane, not a way to
// change how the service identifies itself to an upstream provider.
type GatewayRuntimePolicy struct {
	MaxAttempts                    int `json:"max_attempts"`
	UnauthorizedCooldownSeconds    int `json:"unauthorized_cooldown_seconds"`
	RateLimitCooldownSeconds       int `json:"rate_limit_cooldown_seconds"`
	UpstreamFailureCooldownSeconds int `json:"upstream_failure_cooldown_seconds"`
	NetworkFailureCooldownSeconds  int `json:"network_failure_cooldown_seconds"`
	ProbeIntervalSeconds           int `json:"probe_interval_seconds"`
	ProbeTimeoutSeconds            int `json:"probe_timeout_seconds"`
	ProbeRetentionDays             int `json:"probe_retention_days"`
	ProbeConcurrency               int `json:"probe_concurrency"`
}

func DefaultGatewayRuntimePolicy() GatewayRuntimePolicy {
	return GatewayRuntimePolicy{
		MaxAttempts:                    3,
		UnauthorizedCooldownSeconds:    600,
		RateLimitCooldownSeconds:       60,
		UpstreamFailureCooldownSeconds: 30,
		NetworkFailureCooldownSeconds:  15,
		ProbeIntervalSeconds:           300,
		ProbeTimeoutSeconds:            12,
		ProbeRetentionDays:             30,
		ProbeConcurrency:               4,
	}
}

func (p GatewayRuntimePolicy) CooldownFor(statusCode int) time.Duration {
	seconds := p.NetworkFailureCooldownSeconds
	switch {
	case statusCode == 401 || statusCode == 403:
		seconds = p.UnauthorizedCooldownSeconds
	case statusCode == 429:
		seconds = p.RateLimitCooldownSeconds
	case statusCode >= 500:
		seconds = p.UpstreamFailureCooldownSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (p GatewayRuntimePolicy) ProbeInterval() time.Duration {
	return time.Duration(p.ProbeIntervalSeconds) * time.Second
}
func (p GatewayRuntimePolicy) ProbeTimeout() time.Duration {
	return time.Duration(p.ProbeTimeoutSeconds) * time.Second
}
func (p GatewayRuntimePolicy) ProbeRetention() time.Duration {
	return time.Duration(p.ProbeRetentionDays) * 24 * time.Hour
}

func normalizeGatewayRuntimePolicy(p GatewayRuntimePolicy) (GatewayRuntimePolicy, error) {
	defaults := DefaultGatewayRuntimePolicy()
	if p.MaxAttempts == 0 {
		p.MaxAttempts = defaults.MaxAttempts
	}
	if p.UnauthorizedCooldownSeconds == 0 {
		p.UnauthorizedCooldownSeconds = defaults.UnauthorizedCooldownSeconds
	}
	if p.RateLimitCooldownSeconds == 0 {
		p.RateLimitCooldownSeconds = defaults.RateLimitCooldownSeconds
	}
	if p.UpstreamFailureCooldownSeconds == 0 {
		p.UpstreamFailureCooldownSeconds = defaults.UpstreamFailureCooldownSeconds
	}
	if p.NetworkFailureCooldownSeconds == 0 {
		p.NetworkFailureCooldownSeconds = defaults.NetworkFailureCooldownSeconds
	}
	if p.ProbeIntervalSeconds == 0 {
		p.ProbeIntervalSeconds = defaults.ProbeIntervalSeconds
	}
	if p.ProbeTimeoutSeconds == 0 {
		p.ProbeTimeoutSeconds = defaults.ProbeTimeoutSeconds
	}
	if p.ProbeRetentionDays == 0 {
		p.ProbeRetentionDays = defaults.ProbeRetentionDays
	}
	if p.ProbeConcurrency == 0 {
		p.ProbeConcurrency = defaults.ProbeConcurrency
	}

	checks := []struct {
		label           string
		value, min, max int
	}{
		{"max_attempts", p.MaxAttempts, 1, 8},
		{"unauthorized_cooldown_seconds", p.UnauthorizedCooldownSeconds, 30, 86400},
		{"rate_limit_cooldown_seconds", p.RateLimitCooldownSeconds, 5, 3600},
		{"upstream_failure_cooldown_seconds", p.UpstreamFailureCooldownSeconds, 5, 3600},
		{"network_failure_cooldown_seconds", p.NetworkFailureCooldownSeconds, 1, 3600},
		{"probe_interval_seconds", p.ProbeIntervalSeconds, 30, 86400},
		{"probe_timeout_seconds", p.ProbeTimeoutSeconds, 2, 120},
		{"probe_retention_days", p.ProbeRetentionDays, 1, 365},
		{"probe_concurrency", p.ProbeConcurrency, 1, 32},
	}
	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return p, fmt.Errorf("%s must be between %d and %d", check.label, check.min, check.max)
		}
	}
	return p, nil
}

type RuntimePolicyService struct {
	db     *gorm.DB
	mu     sync.RWMutex
	policy GatewayRuntimePolicy
}

func NewRuntimePolicyService(db *gorm.DB) *RuntimePolicyService {
	s := &RuntimePolicyService{db: db, policy: DefaultGatewayRuntimePolicy()}
	if db == nil {
		return s
	}
	var row model.Setting
	if err := db.Where("key = ?", runtimePolicyKey).First(&row).Error; err == nil {
		var stored GatewayRuntimePolicy
		if json.Unmarshal([]byte(row.Value), &stored) == nil {
			if normalized, err := normalizeGatewayRuntimePolicy(stored); err == nil {
				s.policy = normalized
			}
		}
	}
	return s
}

func (s *RuntimePolicyService) Current() GatewayRuntimePolicy {
	if s == nil {
		return DefaultGatewayRuntimePolicy()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy
}

func (s *RuntimePolicyService) Update(next GatewayRuntimePolicy) (GatewayRuntimePolicy, error) {
	if s == nil || s.db == nil {
		return GatewayRuntimePolicy{}, fmt.Errorf("runtime policy store is unavailable")
	}
	normalized, err := normalizeGatewayRuntimePolicy(next)
	if err != nil {
		return GatewayRuntimePolicy{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return GatewayRuntimePolicy{}, err
	}
	row := model.Setting{Key: runtimePolicyKey, Value: string(raw)}
	if err := s.db.Save(&row).Error; err != nil {
		return GatewayRuntimePolicy{}, err
	}
	s.mu.Lock()
	s.policy = normalized
	s.mu.Unlock()
	return normalized, nil
}
