// Package model defines database entities for the gateway platform.
// Money values are stored as int64 micro-USD (1 USD = 1_000_000) to avoid
// floating point drift; model prices are stored as USD per 1M tokens, which
// makes cost calculation a plain multiplication (tokens * price = microUSD).
package model

import (
	"encoding/json"
	"time"

	"dengdeng/internal/crypto"
)

const (
	RoleUser  = "user"
	RoleAdmin = "admin"

	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusError    = "error"

	PlatformAnthropic = "anthropic"
	PlatformOpenAI    = "openai"
	PlatformGemini    = "gemini"
	// PlatformGrok forwards OpenAI-compatible Responses/Chat traffic to xAI.
	// Both xAI API keys and Grok subscription OAuth accounts live here.
	PlatformGrok = "grok"

	// Upstream credential styles.
	AuthAPIKey        = "api_key"        // static provider key sent as-is
	AuthOAuth         = "oauth"          // access_token (+ refresh_token) bearer, auto-renewed
	AuthAgentIdentity = "agent_identity" // OpenAI AgentAssertion signed per request

	RedeemKindAmount   = "amount"
	RedeemKindDays     = "days"
	RedeemKindRequests = "requests"

	PaymentProviderEasyPay   = "easypay"
	PaymentProviderAlipay    = "alipay"
	PaymentProviderWxPay     = "wxpay"
	PaymentProviderStripe    = "stripe"
	PaymentProviderAirwallex = "airwallex"

	PaymentStatusPending         = "PENDING"
	PaymentStatusPaid            = "PAID"
	PaymentStatusCompleted       = "COMPLETED"
	PaymentStatusExpired         = "EXPIRED"
	PaymentStatusCancelled       = "CANCELLED"
	PaymentStatusFailed          = "FAILED"
	PaymentStatusRefundRequested = "REFUND_REQUESTED"
	PaymentStatusRefunding       = "REFUNDING"
	PaymentStatusRefunded        = "REFUNDED"
)

var AllPlatforms = []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformGrok}

type User struct {
	ID            int64  `gorm:"primaryKey" json:"id"`
	Email         string `gorm:"uniqueIndex;size:255;not null" json:"email"`
	EmailVerified bool   `gorm:"not null;default:false" json:"email_verified"`
	PasswordHash  string `gorm:"size:255;not null" json:"-"`
	Role          string `gorm:"size:16;not null;default:user" json:"role"`
	Status        string `gorm:"size:16;not null;default:active" json:"status"`
	BalanceMicro  int64  `gorm:"not null;default:0" json:"balance_micro"`
	// AccessExpiresAt grants time-based access. While it is in the future,
	// requests are recorded but do not consume the cash balance.
	AccessExpiresAt   *time.Time `gorm:"index" json:"access_expires_at"`
	RemainingRequests int64      `gorm:"not null;default:0" json:"remaining_requests"`
	RateMultiplier    float64    `gorm:"not null;default:1" json:"rate_multiplier"`
	// Concurrency is the maximum number of simultaneous relay requests owned
	// by this user. Zero preserves the legacy unlimited behaviour.
	Concurrency int    `gorm:"not null;default:0" json:"concurrency"`
	Note        string `gorm:"size:512" json:"note"`
	// TokenVersion is bumped to invalidate all previously issued JWTs
	// (password change, ban, role change).
	TokenVersion int                    `gorm:"not null;default:0" json:"-"`
	TOTPEnabled  bool                   `gorm:"not null;default:false" json:"totp_enabled"`
	TOTPSecret   crypto.EncryptedString `gorm:"size:512" json:"-"`
	// TermsRevision and TermsAcceptedAt record the policy revision accepted
	// during login or registration. A revised agreement deliberately asks for
	// fresh acceptance without invalidating API keys or historical usage.
	TermsRevision   string     `gorm:"size:64" json:"terms_revision,omitempty"`
	TermsAcceptedAt *time.Time `json:"terms_accepted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Group is a routing pool: API keys bind to a group, and the group owns a set
// of upstream accounts of the same platform.
type Group struct {
	ID          int64  `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Platform    string `gorm:"size:16;not null" json:"platform"`
	Description string `gorm:"size:512" json:"description"`
	// RateMultiplier is the base customer-billing multiplier for text calls.
	RateMultiplier float64 `gorm:"not null;default:1" json:"rate_multiplier"`
	// Cache multipliers are applied after the group's base multiplier. Keeping
	// cache-hit, short-TTL creation, and long-TTL creation independent mirrors
	// providers that price a 1h cache write differently from a 5m write.
	CacheReadMultiplier    float64 `gorm:"not null;default:1" json:"cache_read_multiplier"`
	CacheWrite5mMultiplier float64 `gorm:"not null;default:1" json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier float64 `gorm:"not null;default:1" json:"cache_write_1h_multiplier"`
	// ImageRateIndependent lets image-token billing use its own multiplier
	// instead of inheriting RateMultiplier, matching Sub2API's group model.
	ImageRateIndependent bool    `gorm:"not null;default:false" json:"image_rate_independent"`
	ImageRateMultiplier  float64 `gorm:"not null;default:1" json:"image_rate_multiplier"`
	// MaxReasoningEffort is an optional group-level ceiling for OpenAI-wire
	// requests. ReasoningEffortMappings lets an operator remap individual
	// client choices before that ceiling is applied (for example max -> high).
	MaxReasoningEffort      string            `gorm:"size:16;not null;default:auto" json:"max_reasoning_effort"`
	ReasoningEffortMappings map[string]string `gorm:"serializer:json;type:text" json:"reasoning_effort_mappings"`
	IsPublic                bool              `gorm:"not null;default:true" json:"is_public"`
	Status                  string            `gorm:"size:16;not null;default:active" json:"status"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type APIKey struct {
	ID     int64 `gorm:"primaryKey" json:"id"`
	UserID int64 `gorm:"index;not null" json:"user_id"`
	// GroupID remains the primary/default group for backwards compatibility.
	// Groups is the authoritative set of pools this credential may use.
	GroupID    int64   `gorm:"index;not null" json:"group_id"`
	GroupIDs   []int64 `gorm:"-" json:"group_ids"`
	KeyHash    string  `gorm:"uniqueIndex;size:64;not null" json:"-"`
	KeyPreview string  `gorm:"size:32;not null" json:"key_preview"`
	Name       string  `gorm:"size:64;not null" json:"name"`
	Status     string  `gorm:"size:16;not null;default:active" json:"status"`
	// ReasoningEffort is the OpenAI-compatible default for this key. "auto"
	// leaves the client's request untouched; a client-supplied value always wins.
	ReasoningEffort string `gorm:"size:16;not null;default:auto" json:"reasoning_effort"`
	// QuotaMicro is an optional lifetime budget for one key. A value of zero
	// means the key follows the owner's shared balance without a key-level cap.
	QuotaMicro     int64 `gorm:"not null;default:0" json:"quota_micro"`
	QuotaUsedMicro int64 `gorm:"not null;default:0" json:"quota_used_micro"`
	// DailyQuotaMicro is an optional rolling calendar-day budget for one key.
	// Its actual consumption is derived from the immutable usage ledger.
	DailyQuotaMicro int64 `gorm:"not null;default:0" json:"daily_quota_micro"`
	// Concurrency optionally narrows the owning user's concurrency for this one
	// credential. Zero means no additional key-level cap.
	Concurrency int `gorm:"not null;default:0" json:"concurrency"`
	// RPM and IP rules are optional per-key protection. They are enforced in
	// gateway authentication before any upstream request is made.
	RPM        int        `gorm:"not null;default:0" json:"rpm"`
	AllowedIPs string     `gorm:"size:2048" json:"allowed_ips"`
	BlockedIPs string     `gorm:"size:2048" json:"blocked_ips"`
	ExpiresAt  *time.Time `gorm:"index" json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`

	User   *User   `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Group  *Group  `gorm:"foreignKey:GroupID" json:"group,omitempty"`
	Groups []Group `gorm:"many2many:api_key_groups;constraint:OnDelete:CASCADE;" json:"groups,omitempty"`
}

// APIKeyGroup is the durable many-to-many binding between one client key and
// the upstream groups it may route through. The legacy APIKey.GroupID column
// mirrors the first selection so older clients and rollback builds keep
// working during rolling upgrades.
type APIKeyGroup struct {
	APIKeyID  int64     `gorm:"primaryKey;autoIncrement:false" json:"api_key_id"`
	GroupID   int64     `gorm:"primaryKey;autoIncrement:false;index" json:"group_id"`
	CreatedAt time.Time `json:"created_at"`
}

// UserGroupRate overrides a group's base billing multiplier for one user.
// It deliberately does not duplicate the group's cache/image settings: those
// still apply on top, so a discounted user cannot accidentally bypass cache
// or image pricing rules.
type UserGroupRate struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	UserID         int64     `gorm:"not null;uniqueIndex:idx_user_group_rate" json:"user_id"`
	GroupID        int64     `gorm:"not null;uniqueIndex:idx_user_group_rate" json:"group_id"`
	RateMultiplier float64   `gorm:"not null;default:1" json:"rate_multiplier"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ReferralCode belongs to one promoter. CommissionBps is constrained by the
// handlers to 500–1000 (5%–10%). One promoter has one stable code so existing
// links never silently change ownership.
type ReferralCode struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	Code          string    `gorm:"uniqueIndex;size:32;not null" json:"code"`
	OwnerUserID   int64     `gorm:"uniqueIndex;not null" json:"owner_user_id"`
	CommissionBps int       `gorm:"not null;default:500" json:"commission_bps"`
	Status        string    `gorm:"size:16;not null;default:active" json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	Owner *User `gorm:"foreignKey:OwnerUserID" json:"owner,omitempty"`
}

// ReferralBinding is immutable from the user's point of view. A unique
// ReferredUserID prevents code switching after the account has started using
// paid services.
type ReferralBinding struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	ReferralCodeID int64     `gorm:"index;not null" json:"referral_code_id"`
	ReferrerUserID int64     `gorm:"index;not null" json:"referrer_user_id"`
	ReferredUserID int64     `gorm:"uniqueIndex;not null" json:"referred_user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// ReferralCommission is a usage-ledger sidecar. UsageLogID is unique, making
// settlement idempotent even if a billing record is retried.
type ReferralCommission struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	UsageLogID     int64     `gorm:"uniqueIndex;not null" json:"usage_log_id"`
	ReferralCodeID int64     `gorm:"index;not null" json:"referral_code_id"`
	ReferrerUserID int64     `gorm:"index;not null" json:"referrer_user_id"`
	ReferredUserID int64     `gorm:"index;not null" json:"referred_user_id"`
	BaseCostMicro  int64     `gorm:"not null" json:"base_cost_micro"`
	CommissionBps  int       `gorm:"not null" json:"commission_bps"`
	AmountMicro    int64     `gorm:"not null" json:"amount_micro"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

// UpstreamAccount is a credential for an upstream provider. It supports a
// static API key, a refreshable OAuth token pair, or an OpenAI Agent Identity
// that signs every request. All secret fields are encrypted at rest.
type UpstreamAccount struct {
	ID      int64 `gorm:"primaryKey" json:"id"`
	GroupID int64 `gorm:"index;not null" json:"group_id"`
	// ProxyID is optional. A zero value continues to use the deployment-wide
	// outbound route, while a non-zero value selects a separately managed
	// proxy for this one upstream account.
	ProxyID  int64  `gorm:"index;not null;default:0" json:"proxy_id"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Platform string `gorm:"size:16;not null" json:"platform"`
	BaseURL  string `gorm:"size:512" json:"base_url"`
	// AuthType is api_key (default), oauth, or agent_identity.
	AuthType string `gorm:"size:16;not null;default:api_key" json:"auth_type"`
	// APIKey holds the provider key for AuthType == api_key (encrypted).
	APIKey crypto.EncryptedString `gorm:"size:2048" json:"-"`
	// OAuth credentials for AuthType == oauth (encrypted).
	AccessToken  crypto.EncryptedString `gorm:"size:6144" json:"-"`
	RefreshToken crypto.EncryptedString `gorm:"size:2048" json:"-"`
	// ExpiresAt is when AccessToken stops being valid; drives proactive refresh.
	ExpiresAt *time.Time `json:"expires_at"`
	// Non-secret identity metadata carried by imported OAuth accounts.
	Email     string `gorm:"size:255" json:"email"`
	AccountID string `gorm:"size:128" json:"account_id"`
	// Extra keeps provider-specific credential bits (id_token, client_id,
	// plan_type, organization_id...) as an encrypted JSON blob.
	Extra crypto.EncryptedString `gorm:"size:8192" json:"-"`

	Priority int `gorm:"not null;default:10" json:"priority"`
	// Concurrency is a hard upstream slot count. Zero means unlimited. The slot
	// remains held for the complete lifetime of a streaming response.
	Concurrency int `gorm:"not null;default:0" json:"concurrency"`
	// DisplayOrder is a console-only order chosen by administrators. It is not
	// consulted by the gateway scheduler, which continues to use Priority.
	DisplayOrder  int        `gorm:"not null;default:0;index" json:"display_order"`
	Status        string     `gorm:"size:16;not null;default:active" json:"status"`
	ErrorCount    int        `gorm:"not null;default:0" json:"error_count"`
	CooldownUntil *time.Time `json:"cooldown_until"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastError     string     `gorm:"size:1024" json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	Group *Group `gorm:"foreignKey:GroupID" json:"group,omitempty"`
	Proxy *Proxy `gorm:"foreignKey:ProxyID" json:"proxy,omitempty"`
	// Quota is the normalized provider allowance plus DengDeng-observed usage.
	// Every platform receives a snapshot. Providers with a subscription usage
	// endpoint add real upstream windows; API-key providers that do not expose
	// balance data still retain local 24h/7d/30d usage and rate-limit headers.
	Quota *AccountQuotaSnapshot `gorm:"foreignKey:UpstreamAccountID;references:ID" json:"quota,omitempty"`
	// CodexQuota is a cached snapshot returned by ChatGPT's Codex usage
	// endpoint for an OpenAI OAuth account. It is intentionally separate from
	// this service's billing ledger: provider subscription limits are not USD.
	CodexQuota *CodexQuotaSnapshot `gorm:"foreignKey:UpstreamAccountID;references:ID" json:"codex_quota,omitempty"`
}

// AccountQuotaWindow is one provider-side allowance or rate-limit window.
// Pointer values preserve the difference between a real zero and an omitted
// field. Unit is normally "%", "requests", "tokens", or "USD".
type AccountQuotaWindow struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	UsedPercent *float64   `json:"used_percent,omitempty"`
	Limit       *float64   `json:"limit,omitempty"`
	Remaining   *float64   `json:"remaining,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
}

// AccountObservedUsage is usage recorded by DengDeng for an upstream account.
// It is not presented as a provider balance; it remains available for every
// provider, including static API keys whose vendor exposes no quota endpoint.
type AccountObservedUsage struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostMicro    int64  `json:"cost_micro"`
}

// AccountQuotaSnapshot is the latest unified quota view for any upstream
// account. State is ready, partial, local_only, or error. FetchedAt tracks the
// last successful provider result; LastAttemptAt advances on every automatic
// or manual refresh so stale/error states are visible without discarding the
// previous useful windows.
type AccountQuotaSnapshot struct {
	ID                    int64                  `gorm:"primaryKey" json:"id"`
	UpstreamAccountID     int64                  `gorm:"not null;uniqueIndex" json:"upstream_account_id"`
	Platform              string                 `gorm:"size:16;not null" json:"platform"`
	Source                string                 `gorm:"size:32;not null" json:"source"`
	State                 string                 `gorm:"size:16;not null;default:local_only" json:"state"`
	PlanType              string                 `gorm:"size:64" json:"plan_type"`
	SubscriptionExpiresAt *time.Time             `json:"subscription_expires_at,omitempty"`
	Message               string                 `gorm:"size:512" json:"message"`
	Windows               []AccountQuotaWindow   `gorm:"serializer:json;type:text" json:"windows"`
	ObservedUsage         []AccountObservedUsage `gorm:"serializer:json;type:text" json:"observed_usage"`
	FetchedAt             *time.Time             `gorm:"index" json:"fetched_at"`
	LastAttemptAt         time.Time              `gorm:"not null;index" json:"last_attempt_at"`
	LastCredentialRefresh *time.Time             `json:"last_credential_refresh,omitempty"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

// CodexQuotaSnapshot is the most recent, non-secret result from the Codex
// subscription usage endpoint. The two windows normally represent a short
// (for example 5-hour) and long (for example 7-day) allowance. A zero used
// percentage is meaningful, so HasPrimaryWindow / HasSecondaryWindow retain
// the distinction between zero and an omitted upstream window.
type CodexQuotaSnapshot struct {
	ID                         int64      `gorm:"primaryKey" json:"id"`
	UpstreamAccountID          int64      `gorm:"not null;uniqueIndex" json:"upstream_account_id"`
	PlanType                   string     `gorm:"size:64" json:"plan_type"`
	Allowed                    bool       `gorm:"not null;default:true" json:"allowed"`
	LimitReached               bool       `gorm:"not null;default:false" json:"limit_reached"`
	HasPrimaryWindow           bool       `gorm:"not null;default:false" json:"has_primary_window"`
	PrimaryUsedPercent         float64    `gorm:"not null;default:0" json:"primary_used_percent"`
	PrimaryWindowSeconds       int64      `gorm:"not null;default:0" json:"primary_window_seconds"`
	PrimaryResetAfterSeconds   int64      `gorm:"not null;default:0" json:"primary_reset_after_seconds"`
	PrimaryResetAt             *time.Time `json:"primary_reset_at"`
	HasSecondaryWindow         bool       `gorm:"not null;default:false" json:"has_secondary_window"`
	SecondaryUsedPercent       float64    `gorm:"not null;default:0" json:"secondary_used_percent"`
	SecondaryWindowSeconds     int64      `gorm:"not null;default:0" json:"secondary_window_seconds"`
	SecondaryResetAfterSeconds int64      `gorm:"not null;default:0" json:"secondary_reset_after_seconds"`
	SecondaryResetAt           *time.Time `json:"secondary_reset_at"`
	FetchedAt                  time.Time  `gorm:"not null;index" json:"fetched_at"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// AccountProbe is an independently persisted health check for an upstream
// account. It is intentionally separate from request routing failures: a
// scheduled check must never put an account into the traffic cooldown pool.
// OAuth probes avoid creating generation requests, so they do not spend a
// subscription message merely to populate an operations screen.
type AccountProbe struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	AccountID    int64     `gorm:"not null;index:idx_account_probe_account_checked" json:"account_id"`
	Mode         string    `gorm:"size:16;not null" json:"mode"`        // api | transport | local
	State        string    `gorm:"size:16;not null;index" json:"state"` // healthy | degraded | down | expired
	StatusCode   int       `gorm:"not null;default:0" json:"status_code"`
	LatencyMs    int64     `gorm:"not null;default:0" json:"latency_ms"`
	ErrorMessage string    `gorm:"size:1024" json:"error_message"`
	CheckedAt    time.Time `gorm:"not null;index:idx_account_probe_account_checked" json:"checked_at"`
}

// AlertRule describes a health condition attached to an upstream account
// pool. Filters are optional; a zero ID / empty platform means "all".
type AlertRule struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:120;not null" json:"name"`
	Enabled     bool      `gorm:"not null;default:true" json:"enabled"`
	Condition   string    `gorm:"size:32;not null" json:"condition"` // down | degraded_or_down | not_healthy
	Platform    string    `gorm:"size:16;index" json:"platform"`
	GroupID     int64     `gorm:"index;not null;default:0" json:"group_id"`
	AccountID   int64     `gorm:"index;not null;default:0" json:"account_id"`
	NotifyEmail string    `gorm:"size:255" json:"notify_email"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AlertEvent is one continuous incident for a rule/account pair. Repeated
// failing probes update LastSeenAt rather than generating notification noise;
// a healthy probe resolves the incident automatically.
type AlertEvent struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	RuleID         int64      `gorm:"index:idx_alert_event_rule_account_state" json:"rule_id"`
	AccountID      int64      `gorm:"index:idx_alert_event_rule_account_state" json:"account_id"`
	GroupID        int64      `gorm:"index" json:"group_id"`
	Platform       string     `gorm:"size:16;index" json:"platform"`
	State          string     `gorm:"size:16;index;not null" json:"state"` // open | resolved
	Severity       string     `gorm:"size:16;not null" json:"severity"`
	Title          string     `gorm:"size:255;not null" json:"title"`
	Message        string     `gorm:"size:1024" json:"message"`
	FirstSeenAt    time.Time  `gorm:"index" json:"first_seen_at"`
	LastSeenAt     time.Time  `gorm:"index" json:"last_seen_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	AcknowledgedBy string     `gorm:"size:255" json:"acknowledged_by"`
	DeliveryStatus string     `gorm:"size:16" json:"delivery_status"` // console | sent | failed
	DeliveryError  string     `gorm:"size:512" json:"delivery_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Proxy is a separately managed outbound proxy. Authentication fields are
// encrypted at rest and never serialized back to the console. Accounts may
// opt into one proxy independently, so an unreliable route does not affect
// every provider account.
type Proxy struct {
	ID        int64                  `gorm:"primaryKey" json:"id"`
	Name      string                 `gorm:"uniqueIndex;size:100;not null" json:"name"`
	Protocol  string                 `gorm:"size:16;not null" json:"protocol"` // http | https | socks5
	Host      string                 `gorm:"size:255;not null" json:"host"`
	Port      int                    `gorm:"not null" json:"port"`
	Username  crypto.EncryptedString `gorm:"size:512" json:"-"`
	Password  crypto.EncryptedString `gorm:"size:512" json:"-"`
	Status    string                 `gorm:"size:16;not null;default:active" json:"status"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// DecodeExtra unpacks the encrypted Extra JSON blob (id_token, client_id,
// plan_type, organization_id, session_token...). Always returns a usable map.
func (a *UpstreamAccount) DecodeExtra() map[string]any {
	m := map[string]any{}
	if a.Extra == "" {
		return m
	}
	_ = json.Unmarshal([]byte(a.Extra), &m)
	return m
}

// EncodeExtra serializes a metadata map into the encrypted Extra field value.
func EncodeExtra(m map[string]any) (crypto.EncryptedString, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return crypto.EncryptedString(b), nil
}

// ModelPrice defines USD per 1M tokens. ImagePricePerImage is the optional
// fixed price in USD for one generated image. When it is set, it takes
// precedence over the image-token fields. Match is an exact model name or a
// prefix pattern ending with '*'.
type ModelPrice struct {
	ID             int64   `gorm:"primaryKey" json:"id"`
	Match          string  `gorm:"uniqueIndex;size:128;not null" json:"match"`
	Platform       string  `gorm:"size:16" json:"platform"`
	InputPrice     float64 `gorm:"not null;default:0" json:"input_price"`
	OutputPrice    float64 `gorm:"not null;default:0" json:"output_price"`
	CacheReadPrice float64 `gorm:"not null;default:0" json:"cache_read_price"`
	// CacheWritePrice is retained as the legacy/default cache-creation price.
	// The two TTL-specific fields override it only when configured, so an
	// upgrade never changes an existing price rule unexpectedly.
	CacheWritePrice   float64 `gorm:"not null;default:0" json:"cache_write_price"`
	CacheWrite5mPrice float64 `gorm:"not null;default:0" json:"cache_write_5m_price"`
	CacheWrite1hPrice float64 `gorm:"not null;default:0" json:"cache_write_1h_price"`
	// Image token prices are also USD per 1M tokens. Image APIs return these
	// token counts separately from text tokens, so keeping them apart avoids
	// charging GPT Image requests with a text-only rate.
	ImageInputPrice     float64   `gorm:"not null;default:0" json:"image_input_price"`
	ImageOutputPrice    float64   `gorm:"not null;default:0" json:"image_output_price"`
	ImageCacheReadPrice float64   `gorm:"not null;default:0" json:"image_cache_read_price"`
	ImagePricePerImage  float64   `gorm:"not null;default:0" json:"image_price_per_image"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ModelConfig is an optional public-model alias. When a configured public
// name is called, the relay sends UpstreamModel to the provider but bills the
// configured public name. Leaving an upstream model empty keeps the same name.
// Unknown models continue to pass through so existing deployments are not
// broken while administrators gradually configure their model catalogue.
type ModelConfig struct {
	ID            int64  `gorm:"primaryKey" json:"id"`
	Name          string `gorm:"uniqueIndex;size:128;not null" json:"name"`
	Platform      string `gorm:"size:16;not null" json:"platform"`
	Kind          string `gorm:"size:16;not null;default:chat" json:"kind"` // chat | image
	UpstreamModel string `gorm:"size:128" json:"upstream_model"`
	// These fields are catalogue metadata only. The upstream provider remains
	// the final authority for accepted context and output sizes.
	ContextWindow     int64 `gorm:"not null;default:0" json:"context_window"`
	MaxOutputTokens   int64 `gorm:"not null;default:0" json:"max_output_tokens"`
	SupportsVision    bool  `gorm:"not null;default:false" json:"supports_vision"`
	SupportsTools     bool  `gorm:"not null;default:false" json:"supports_tools"`
	SupportsReasoning bool  `gorm:"not null;default:false" json:"supports_reasoning"`
	// ImageGroupID optionally routes image requests to a dedicated account
	// pool. It is only used for Kind == "image" and must be on the same
	// provider platform as the model configuration.
	ImageGroupID int64     `gorm:"index;not null;default:0" json:"image_group_id"`
	Description  string    `gorm:"size:512" json:"description"`
	Status       string    `gorm:"size:16;not null;default:active" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UsageLog struct {
	ID int64 `gorm:"primaryKey" json:"id"`
	// RequestID is the correlation identifier returned as X-Request-ID. It
	// lets a user or operator locate one completed/failed relay call without
	// exposing payloads, API keys or upstream credentials.
	RequestID string `gorm:"size:32;index" json:"request_id"`
	UserID    int64  `gorm:"index" json:"user_id"`
	APIKeyID  int64  `gorm:"index" json:"api_key_id"`
	AccountID int64  `gorm:"index" json:"account_id"`
	GroupID   int64  `gorm:"index" json:"group_id"`
	Model     string `gorm:"size:128" json:"model"`
	Stream    bool   `json:"stream"`
	// ReasoningEffort is the effective OpenAI-wire reasoning effort of this
	// call (client value first, key default otherwise). It is stored so the
	// per-effort billing multiplier applied to CostMicro stays auditable.
	ReasoningEffort  string `gorm:"size:16" json:"reasoning_effort,omitempty"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	// CacheWrite5mTokens and CacheWrite1hTokens preserve Anthropic's detailed
	// cache-creation response. CacheWriteTokens remains the aggregate for
	// backwards-compatible API responses and reporting.
	CacheWrite5mTokens int64 `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64 `json:"cache_write_1h_tokens"`
	ImageCount         int64 `json:"image_count"`
	CostMicro          int64 `json:"cost_micro"`
	// FirstTokenMs measures time from relay admission to the first response
	// body bytes written to the client. DurationMs covers the whole request,
	// including queueing and the complete streamed response.
	FirstTokenMs int64 `gorm:"not null;default:0" json:"first_token_ms"`
	DurationMs   int64 `json:"duration_ms"`
	// ScheduleMs and UpstreamMs split internal routing time from time spent
	// waiting on selected providers. AttemptCount makes failover visible without
	// disclosing which upstream credentials were tried.
	ScheduleMs   int64     `gorm:"not null;default:0" json:"schedule_ms"`
	QueueMs      int64     `gorm:"not null;default:0" json:"queue_ms"`
	UpstreamMs   int64     `gorm:"not null;default:0" json:"upstream_ms"`
	AttemptCount int       `gorm:"not null;default:0" json:"attempt_count"`
	StatusCode   int       `json:"status_code"`
	ErrorMessage string    `gorm:"size:512" json:"error_message"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`

	UserEmail   string `gorm:"-" json:"user_email,omitempty"`
	KeyName     string `gorm:"-" json:"key_name,omitempty"`
	GroupName   string `gorm:"-" json:"group_name,omitempty"`
	AccountName string `gorm:"-" json:"account_name,omitempty"`
}

type RedeemCode struct {
	ID   int64  `gorm:"primaryKey" json:"id"`
	Code string `gorm:"uniqueIndex;size:64;not null" json:"code"`
	// Kind is amount, days, or requests. An empty value belongs to legacy
	// amount codes created before the three entitlement modes were introduced.
	Kind        string     `gorm:"size:16;not null;default:amount" json:"kind"`
	AmountMicro int64      `gorm:"not null" json:"amount_micro"`
	Value       int64      `gorm:"not null;default:0" json:"value"`
	Batch       string     `gorm:"size:64;index" json:"batch"`
	UsedBy      *int64     `json:"used_by"`
	UsedAt      *time.Time `json:"used_at"`
	CreatedAt   time.Time  `json:"created_at"`

	UsedByEmail string `gorm:"-" json:"used_by_email,omitempty"`
}

// PaymentConfig controls self-service top-ups. Monetary charge values are in
// the minor unit of Currency (for example cents for CNY/USD), while the
// account balance remains micro-USD. CreditMicroPerUnit explicitly bridges
// those two units and prevents a CNY charge from accidentally becoming USD.
// A zero exchange rate keeps the payment subsystem safely disabled.
type PaymentConfig struct {
	ID                  int64     `gorm:"primaryKey" json:"id"`
	Enabled             bool      `gorm:"not null;default:false" json:"enabled"`
	Currency            string    `gorm:"size:8;not null;default:CNY" json:"currency"`
	CreditMicroPerUnit  int64     `gorm:"not null;default:0" json:"credit_micro_per_unit"`
	MinAmountMinor      int64     `gorm:"not null;default:100" json:"min_amount_minor"`
	MaxAmountMinor      int64     `gorm:"not null;default:1000000" json:"max_amount_minor"`
	DailyLimitMinor     int64     `gorm:"not null;default:0" json:"daily_limit_minor"`
	OrderExpiryMinutes  int       `gorm:"not null;default:30" json:"order_expiry_minutes"`
	MaxPendingOrders    int       `gorm:"not null;default:3" json:"max_pending_orders"`
	LoadBalanceStrategy string    `gorm:"size:32;not null;default:round_robin" json:"load_balance_strategy"`
	ProductName         string    `gorm:"size:128;not null;default:DengDeng AI 账户充值" json:"product_name"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PaymentProviderInstance is a separately configured merchant account. Its
// JSON config carries credentials and is always AES-GCM encrypted at rest.
// SupportedMethods is a comma-separated list such as "alipay,wxpay" or
// "card,link". Public APIs only expose non-sensitive instance metadata.
type PaymentProviderInstance struct {
	ID               int64                  `gorm:"primaryKey" json:"id"`
	Name             string                 `gorm:"size:96;not null" json:"name"`
	ProviderKey      string                 `gorm:"size:32;index;not null" json:"provider_key"`
	Currency         string                 `gorm:"size:8;not null;default:CNY" json:"currency"`
	SupportedMethods string                 `gorm:"size:256" json:"supported_methods"`
	PaymentMode      string                 `gorm:"size:32;not null;default:qrcode" json:"payment_mode"`
	Config           crypto.EncryptedString `gorm:"size:16384;not null" json:"-"`
	Status           string                 `gorm:"size:16;not null;default:active" json:"status"`
	MinAmountMinor   int64                  `gorm:"not null;default:0" json:"min_amount_minor"`
	MaxAmountMinor   int64                  `gorm:"not null;default:0" json:"max_amount_minor"`
	DailyLimitMinor  int64                  `gorm:"not null;default:0" json:"daily_limit_minor"`
	Priority         int                    `gorm:"not null;default:10" json:"priority"`
	LastSelectedAt   *time.Time             `json:"last_selected_at"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// PaymentOrder stores a provider-independent immutable charge amount and the
// resulting micro-USD credit. Its state transitions are CAS guarded by the
// service to make repeated provider webhooks harmless.
type PaymentOrder struct {
	ID               int64                  `gorm:"primaryKey" json:"id"`
	OutTradeNo       string                 `gorm:"uniqueIndex;size:64;not null" json:"out_trade_no"`
	UserID           int64                  `gorm:"index;not null" json:"user_id"`
	ProviderID       int64                  `gorm:"index;not null" json:"provider_id"`
	ProviderKey      string                 `gorm:"size:32;not null" json:"provider_key"`
	PaymentMethod    string                 `gorm:"size:32;not null" json:"payment_method"`
	Status           string                 `gorm:"size:32;index;not null;default:PENDING" json:"status"`
	Currency         string                 `gorm:"size:8;not null" json:"currency"`
	AmountMinor      int64                  `gorm:"not null" json:"amount_minor"`
	CreditMicro      int64                  `gorm:"not null" json:"credit_micro"`
	ProviderTradeNo  string                 `gorm:"size:256;index" json:"provider_trade_no"`
	ProviderSnapshot string                 `gorm:"size:4096" json:"provider_snapshot"`
	CheckoutData     crypto.EncryptedString `gorm:"size:8192" json:"-"`
	FailureReason    string                 `gorm:"size:1024" json:"failure_reason,omitempty"`
	RefundTradeNo    string                 `gorm:"size:256" json:"refund_trade_no,omitempty"`
	RefundedMicro    int64                  `gorm:"not null;default:0" json:"refunded_micro"`
	ExpiresAt        time.Time              `gorm:"index;not null" json:"expires_at"`
	PaidAt           *time.Time             `json:"paid_at"`
	CompletedAt      *time.Time             `json:"completed_at"`
	CancelledAt      *time.Time             `json:"cancelled_at"`
	RefundedAt       *time.Time             `json:"refunded_at"`
	FulfillmentLease *time.Time             `json:"-"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`

	User     *User                    `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Provider *PaymentProviderInstance `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
}

// PaymentAuditLog is deliberately append-only and contains no raw webhook
// payload or credentials. It makes payment state changes explainable without
// leaking sensitive merchant information to administration views.
type PaymentAuditLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	OrderID   int64     `gorm:"index;not null" json:"order_id"`
	Action    string    `gorm:"size:64;not null" json:"action"`
	Actor     string    `gorm:"size:64;not null" json:"actor"`
	Detail    string    `gorm:"size:2048" json:"detail"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// EmailVerification stores only a keyed digest of a short-lived code. The
// plaintext never reaches the database, and each row can be consumed once.
type EmailVerification struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	Email     string     `gorm:"size:255;index;not null" json:"email"`
	Purpose   string     `gorm:"size:32;index;not null" json:"purpose"`
	CodeHash  string     `gorm:"size:64;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"index;not null" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `gorm:"index" json:"created_at"`
}

// Setting is a simple key/value store for runtime-configurable options.
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AuditLog is the console's append-only administrative activity trail. It
// deliberately stores summaries rather than request bodies or credentials so
// reviewing a change never becomes another secret-disclosure surface.
type AuditLog struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	ActorUserID int64     `gorm:"index" json:"actor_user_id"`
	ActorEmail  string    `gorm:"size:255;index" json:"actor_email"`
	Action      string    `gorm:"size:96;index;not null" json:"action"`
	TargetType  string    `gorm:"size:64;index" json:"target_type"`
	TargetID    string    `gorm:"size:128;index" json:"target_id"`
	Detail      string    `gorm:"size:2048" json:"detail"`
	SourceIP    string    `gorm:"size:64" json:"source_ip"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

// BackupRecord is metadata for a server-side database snapshot. Path is a
// basename only (never a client-controlled absolute path), and the snapshot
// itself remains inaccessible unless an authenticated administrator requests
// its download endpoint.
type BackupRecord struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	Filename    string     `gorm:"uniqueIndex;size:255;not null" json:"filename"`
	Status      string     `gorm:"size:16;index;not null" json:"status"` // creating | ready | failed
	SizeBytes   int64      `json:"size_bytes"`
	Error       string     `gorm:"size:512" json:"error"`
	CreatedBy   string     `gorm:"size:255" json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// ImageStorageConfig holds the operator's S3-compatible object storage used
// by asynchronous image tasks. The secret key is encrypted at rest and never
// returned by the administration API.
type ImageStorageConfig struct {
	ID                 int64                  `gorm:"primaryKey" json:"id"`
	Enabled            bool                   `gorm:"not null;default:false" json:"enabled"`
	Endpoint           string                 `gorm:"size:1024" json:"endpoint"`
	Region             string                 `gorm:"size:64;not null;default:auto" json:"region"`
	Bucket             string                 `gorm:"size:255" json:"bucket"`
	AccessKeyID        crypto.EncryptedString `gorm:"size:2048" json:"-"`
	SecretAccessKey    crypto.EncryptedString `gorm:"size:4096" json:"-"`
	Prefix             string                 `gorm:"size:512;not null;default:images/" json:"prefix"`
	ForcePathStyle     bool                   `gorm:"not null;default:false" json:"force_path_style"`
	PublicBaseURL      string                 `gorm:"size:1024" json:"public_base_url"`
	PresignExpiryHours int                    `gorm:"not null;default:24" json:"presign_expiry_hours"`
	MaxDownloadBytes   int64                  `gorm:"not null;default:33554432" json:"max_download_bytes"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

type ImageTask struct {
	ID          string     `gorm:"primaryKey;size:64" json:"id"`
	UserID      int64      `gorm:"index;not null" json:"-"`
	APIKeyID    int64      `gorm:"index;not null" json:"-"`
	Status      string     `gorm:"index;size:24;not null" json:"status"`
	HTTPStatus  int        `gorm:"not null;default:0" json:"http_status,omitempty"`
	Result      string     `gorm:"type:text" json:"result,omitempty"`
	Error       string     `gorm:"type:text" json:"error,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time  `gorm:"index;not null" json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
