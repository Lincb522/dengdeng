package service

import (
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
)

// SiteCustomizationSettings contains operator-editable presentation and
// navigation options. Executable markup is never accepted; HomeContent is
// rendered as plain Markdown by the frontend.
type SiteCustomizationSettings struct {
	LogoURL              string           `json:"logo_url"`
	ContactInfo          string           `json:"contact_info"`
	DocsURL              string           `json:"docs_url"`
	HomeContent          string           `json:"home_content"`
	BackendModeEnabled   bool             `json:"backend_mode_enabled"`
	HideCCSImportButton  bool             `json:"hide_ccs_import_button"`
	TableDefaultPageSize int              `json:"table_default_page_size"`
	TablePageSizeOptions []int            `json:"table_page_size_options"`
	CustomMenuItems      []CustomMenuItem `json:"custom_menu_items"`
	CustomEndpoints      []CustomEndpoint `json:"custom_endpoints"`
}

type CustomMenuItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	IconSVG    string `json:"icon_svg"`
	Visibility string `json:"visibility"` // user | admin | all
}

type CustomEndpoint struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type FeatureSwitchSettings struct {
	ChannelMonitorEnabled         bool     `json:"channel_monitor_enabled"`
	ChannelMonitorIntervalSeconds int      `json:"channel_monitor_interval_seconds"`
	ModelPlazaEnabled             bool     `json:"model_plaza_enabled"`
	RiskControlEnabled            bool     `json:"risk_control_enabled"`
	RiskControlAction             string   `json:"risk_control_action"` // block | log
	RiskControlBlockedPhrases     []string `json:"risk_control_blocked_phrases"`
	ReferralEnabled               bool     `json:"referral_enabled"`
	AllowUserViewErrorRequests    bool     `json:"allow_user_view_error_requests"`
}

type SecurityPolicySettings struct {
	EmailVerificationEnabled       bool     `json:"email_verification_enabled"`
	PasswordResetEnabled           bool     `json:"password_reset_enabled"`
	TOTPEnabled                    bool     `json:"totp_enabled"`
	SessionBindingEnabled          bool     `json:"session_binding_enabled"`
	StepUpEnabled                  bool     `json:"step_up_enabled"`
	AuditLogRetentionDays          int      `json:"audit_log_retention_days"`
	TurnstileEnabled               bool     `json:"turnstile_enabled"`
	TurnstileSiteKey               string   `json:"turnstile_site_key"`
	RegistrationProtectionEnabled  bool     `json:"registration_protection_enabled"`
	RegistrationCodeIPHourLimit    int      `json:"registration_code_ip_hour_limit"`
	RegistrationIPDayLimit         int      `json:"registration_ip_day_limit"`
	RegistrationSubnetDayLimit     int      `json:"registration_subnet_day_limit"`
	RegistrationDomainHourLimit    int      `json:"registration_domain_hour_limit"`
	RegistrationGrantOncePerIPDays int      `json:"registration_grant_once_per_ip_days"`
	RegistrationBlockedNetworks    []string `json:"registration_blocked_networks"`
	TrustForwardedIP               bool     `json:"trust_forwarded_ip"`
	ForwardedIPHeaders             []string `json:"forwarded_ip_headers"`
}

type DefaultSubscriptionSetting struct {
	GroupID      int64 `json:"group_id"`
	ValidityDays int   `json:"validity_days"`
}

type PlatformQuotaSetting struct {
	DailyMicro   int64 `json:"daily_micro"`
	WeeklyMicro  int64 `json:"weekly_micro"`
	MonthlyMicro int64 `json:"monthly_micro"`
}

type UserDefaultSettings struct {
	BalanceMicro         int64                           `json:"balance_micro"`
	Concurrency          int                             `json:"concurrency"`
	RPMLimit             int                             `json:"rpm_limit"`
	DefaultSubscriptions []DefaultSubscriptionSetting    `json:"default_subscriptions"`
	PlatformQuotas       map[string]PlatformQuotaSetting `json:"platform_quotas"`
	AuthSourceDefaults   map[string]AuthSourceDefault    `json:"auth_source_defaults"`
}

type AuthSourceDefault struct {
	Enabled              bool                            `json:"enabled"`
	RequireEmail         bool                            `json:"require_email"`
	GrantOnSignup        bool                            `json:"grant_on_signup"`
	GrantOnFirstBind     bool                            `json:"grant_on_first_bind"`
	BalanceMicro         int64                           `json:"balance_micro"`
	Concurrency          int                             `json:"concurrency"`
	RPMLimit             int                             `json:"rpm_limit"`
	DefaultSubscriptions []DefaultSubscriptionSetting    `json:"default_subscriptions"`
	PlatformQuotas       map[string]PlatformQuotaSetting `json:"platform_quotas"`
}

type NotificationSettings struct {
	BalanceLowEnabled         bool     `json:"balance_low_enabled"`
	BalanceLowThresholdMicro  int64    `json:"balance_low_threshold_micro"`
	BalanceLowRechargeURL     string   `json:"balance_low_recharge_url"`
	SubscriptionExpiryEnabled bool     `json:"subscription_expiry_enabled"`
	AccountQuotaEnabled       bool     `json:"account_quota_enabled"`
	AccountQuotaEmails        []string `json:"account_quota_emails"`
}

type EmailRuntimeSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	FromName string `json:"from_name"`
	From     string `json:"from"`
	UseTLS   bool   `json:"use_tls"`
}

type OAuthProviderSettings struct {
	Enabled              bool   `json:"enabled"`
	ProviderName         string `json:"provider_name"`
	ClientID             string `json:"client_id"`
	IssuerURL            string `json:"issuer_url"`
	DiscoveryURL         string `json:"discovery_url"`
	AuthorizeURL         string `json:"authorize_url"`
	TokenURL             string `json:"token_url"`
	UserInfoURL          string `json:"userinfo_url"`
	JWKSURL              string `json:"jwks_url"`
	Scopes               string `json:"scopes"`
	RedirectURL          string `json:"redirect_url"`
	FrontendRedirectURL  string `json:"frontend_redirect_url"`
	TokenAuthMethod      string `json:"token_auth_method"`
	UsePKCE              bool   `json:"use_pkce"`
	ValidateIDToken      bool   `json:"validate_id_token"`
	RequireVerifiedEmail bool   `json:"require_verified_email"`
	AllowedSigningAlgs   string `json:"allowed_signing_algs"`
	ClockSkewSeconds     int    `json:"clock_skew_seconds"`
	EmailPath            string `json:"email_path"`
	IDPath               string `json:"id_path"`
	UsernamePath         string `json:"username_path"`
}

type AuthProviderSettings struct {
	LinuxDO  OAuthProviderSettings `json:"linuxdo"`
	DingTalk OAuthProviderSettings `json:"dingtalk"`
	WeChat   OAuthProviderSettings `json:"wechat"`
	OIDC     OAuthProviderSettings `json:"oidc"`
	GitHub   OAuthProviderSettings `json:"github"`
	Google   OAuthProviderSettings `json:"google"`
}

func defaultExtendedSystemSettings() (SiteCustomizationSettings, FeatureSwitchSettings, SecurityPolicySettings, UserDefaultSettings, NotificationSettings, EmailRuntimeSettings, AuthProviderSettings) {
	site := SiteCustomizationSettings{
		TableDefaultPageSize: 20,
		TablePageSizeOptions: []int{10, 20, 50, 100},
	}
	features := FeatureSwitchSettings{
		ChannelMonitorEnabled:         true,
		ChannelMonitorIntervalSeconds: 300,
		ModelPlazaEnabled:             true,
		RiskControlAction:             "block",
		ReferralEnabled:               true,
		AllowUserViewErrorRequests:    true,
	}
	security := SecurityPolicySettings{
		EmailVerificationEnabled:       true,
		PasswordResetEnabled:           true,
		TOTPEnabled:                    true,
		AuditLogRetentionDays:          180,
		RegistrationProtectionEnabled:  true,
		RegistrationCodeIPHourLimit:    3,
		RegistrationIPDayLimit:         3,
		RegistrationSubnetDayLimit:     12,
		RegistrationDomainHourLimit:    20,
		RegistrationGrantOncePerIPDays: 30,
		RegistrationBlockedNetworks:    []string{},
		TrustForwardedIP:               true,
		ForwardedIPHeaders:             []string{"X-Forwarded-For", "X-Real-IP"},
	}
	users := UserDefaultSettings{
		Concurrency:        0,
		RPMLimit:           0,
		PlatformQuotas:     map[string]PlatformQuotaSetting{},
		AuthSourceDefaults: map[string]AuthSourceDefault{},
	}
	for _, source := range []string{"email", "linuxdo", "dingtalk", "wechat", "oidc", "github", "google"} {
		users.AuthSourceDefaults[source] = AuthSourceDefault{Enabled: true, RequireEmail: true, PlatformQuotas: map[string]PlatformQuotaSetting{}}
	}
	notifications := NotificationSettings{}
	email := EmailRuntimeSettings{Port: 465, UseTLS: true}
	auth := AuthProviderSettings{
		LinuxDO:  OAuthProviderSettings{ProviderName: "LinuxDO", AuthorizeURL: "https://connect.linux.do/oauth2/authorize", TokenURL: "https://connect.linux.do/oauth2/token", UserInfoURL: "https://connect.linux.do/api/user", Scopes: "user:read", EmailPath: "email", IDPath: "id", UsernamePath: "username", FrontendRedirectURL: "/auth/linuxdo/callback"},
		DingTalk: OAuthProviderSettings{ProviderName: "钉钉", AuthorizeURL: "https://login.dingtalk.com/oauth2/auth", TokenURL: "https://api.dingtalk.com/v1.0/oauth2/userAccessToken", UserInfoURL: "https://api.dingtalk.com/v1.0/contact/users/me", Scopes: "openid", EmailPath: "email", IDPath: "unionId", UsernamePath: "nick", FrontendRedirectURL: "/auth/dingtalk/callback"},
		WeChat:   OAuthProviderSettings{ProviderName: "微信", AuthorizeURL: "https://open.weixin.qq.com/connect/qrconnect", TokenURL: "https://api.weixin.qq.com/sns/oauth2/access_token", UserInfoURL: "https://api.weixin.qq.com/sns/userinfo", Scopes: "snsapi_login", EmailPath: "email", IDPath: "unionid", UsernamePath: "nickname", FrontendRedirectURL: "/auth/wechat/callback"},
		OIDC:     OAuthProviderSettings{ProviderName: "OIDC", Scopes: "openid email profile", TokenAuthMethod: "client_secret_post", UsePKCE: true, ValidateIDToken: true, RequireVerifiedEmail: true, AllowedSigningAlgs: "RS256,ES256,PS256", ClockSkewSeconds: 60, EmailPath: "email", IDPath: "sub", UsernamePath: "name", FrontendRedirectURL: "/auth/oidc/callback"},
		GitHub:   OAuthProviderSettings{ProviderName: "GitHub", AuthorizeURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", UserInfoURL: "https://api.github.com/user", Scopes: "read:user user:email", EmailPath: "email", IDPath: "id", UsernamePath: "login", FrontendRedirectURL: "/auth/github/callback"},
		Google:   OAuthProviderSettings{ProviderName: "Google", IssuerURL: "https://accounts.google.com", DiscoveryURL: "https://accounts.google.com/.well-known/openid-configuration", AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", Scopes: "openid email profile", UsePKCE: true, ValidateIDToken: true, RequireVerifiedEmail: true, AllowedSigningAlgs: "RS256", ClockSkewSeconds: 60, EmailPath: "email", IDPath: "sub", UsernamePath: "name", FrontendRedirectURL: "/auth/google/callback"},
	}
	return site, features, security, users, notifications, email, auth
}

func normalizeExtendedSystemSettings(next *SystemSettings) error {
	site := &next.SiteCustomization
	site.LogoURL = strings.TrimSpace(site.LogoURL)
	site.ContactInfo = strings.TrimSpace(site.ContactInfo)
	site.DocsURL = strings.TrimSpace(site.DocsURL)
	site.HomeContent = strings.TrimSpace(site.HomeContent)
	if len([]rune(site.HomeContent)) > 32_000 || len([]rune(site.ContactInfo)) > 1_000 {
		return errors.New("site content is too large")
	}
	for _, raw := range []string{site.LogoURL, site.DocsURL} {
		if raw == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && !strings.HasPrefix(raw, "/")) {
			return errors.New("site URLs must be HTTPS, HTTP, or root-relative paths")
		}
	}
	if site.TableDefaultPageSize <= 0 {
		site.TableDefaultPageSize = 20
	}
	pageSizes := make(map[int]struct{}, len(site.TablePageSizeOptions)+1)
	pageSizes[site.TableDefaultPageSize] = struct{}{}
	for _, value := range site.TablePageSizeOptions {
		if value < 5 || value > 500 {
			return errors.New("table page sizes must be between 5 and 500")
		}
		pageSizes[value] = struct{}{}
	}
	site.TablePageSizeOptions = site.TablePageSizeOptions[:0]
	for value := range pageSizes {
		site.TablePageSizeOptions = append(site.TablePageSizeOptions, value)
	}
	sort.Ints(site.TablePageSizeOptions)
	if len(site.CustomMenuItems) > 24 || len(site.CustomEndpoints) > 24 {
		return errors.New("at most 24 custom menu items and endpoints are allowed")
	}
	if site.CustomMenuItems == nil {
		site.CustomMenuItems = []CustomMenuItem{}
	}
	if site.CustomEndpoints == nil {
		site.CustomEndpoints = []CustomEndpoint{}
	}
	for i := range site.CustomMenuItems {
		item := &site.CustomMenuItems[i]
		item.ID = normalizeDocumentID(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		item.URL = strings.TrimSpace(item.URL)
		item.IconSVG = strings.TrimSpace(item.IconSVG)
		if item.ID == "" || item.Name == "" || item.URL == "" || len(item.IconSVG) > 8_000 {
			return errors.New("custom menu items need an ID, name, and URL")
		}
		if item.Visibility != "user" && item.Visibility != "admin" {
			item.Visibility = "all"
		}
	}
	for i := range site.CustomEndpoints {
		endpoint := &site.CustomEndpoints[i]
		endpoint.ID = normalizeDocumentID(endpoint.ID)
		endpoint.Name = strings.TrimSpace(endpoint.Name)
		endpoint.URL = strings.TrimSpace(endpoint.URL)
		endpoint.Description = strings.TrimSpace(endpoint.Description)
		if endpoint.ID == "" || endpoint.Name == "" || endpoint.URL == "" {
			return errors.New("custom endpoints need an ID, name, and URL")
		}
	}

	features := &next.Features
	if features.ChannelMonitorIntervalSeconds < 15 || features.ChannelMonitorIntervalSeconds > 86400 {
		return errors.New("channel monitor interval must be between 15 and 86400 seconds")
	}
	if features.RiskControlAction != "log" {
		features.RiskControlAction = "block"
	}
	if len(features.RiskControlBlockedPhrases) > 200 {
		return errors.New("at most 200 risk-control phrases are allowed")
	}
	seenPhrases := map[string]struct{}{}
	phrases := make([]string, 0, len(features.RiskControlBlockedPhrases))
	for _, raw := range features.RiskControlBlockedPhrases {
		phrase := strings.ToLower(strings.TrimSpace(raw))
		if phrase == "" {
			continue
		}
		if len([]rune(phrase)) > 200 {
			return errors.New("risk-control phrase is too long")
		}
		if _, exists := seenPhrases[phrase]; !exists {
			seenPhrases[phrase] = struct{}{}
			phrases = append(phrases, phrase)
		}
	}
	features.RiskControlBlockedPhrases = phrases
	if features.RiskControlBlockedPhrases == nil {
		features.RiskControlBlockedPhrases = []string{}
	}

	security := &next.Security
	if security.AuditLogRetentionDays < 0 || security.AuditLogRetentionDays > 3650 {
		return errors.New("audit retention must be between 0 and 3650 days")
	}
	security.TurnstileSiteKey = strings.TrimSpace(security.TurnstileSiteKey)
	if security.TurnstileEnabled && security.TurnstileSiteKey == "" {
		return errors.New("Turnstile site key is required when Turnstile is enabled")
	}
	if security.RegistrationProtectionEnabled {
		if security.RegistrationCodeIPHourLimit < 1 || security.RegistrationCodeIPHourLimit > 1000 ||
			security.RegistrationIPDayLimit < 1 || security.RegistrationIPDayLimit > 1000 ||
			security.RegistrationSubnetDayLimit < 1 || security.RegistrationSubnetDayLimit > 10_000 ||
			security.RegistrationDomainHourLimit < 1 || security.RegistrationDomainHourLimit > 10_000 ||
			security.RegistrationGrantOncePerIPDays < 0 || security.RegistrationGrantOncePerIPDays > 3650 {
			return errors.New("registration protection limits are out of range")
		}
	}
	blockedNetworks := make([]string, 0, len(security.RegistrationBlockedNetworks))
	seenNetworks := make(map[string]struct{}, len(security.RegistrationBlockedNetworks))
	for _, raw := range security.RegistrationBlockedNetworks {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			value = ip.String()
		} else if _, network, err := net.ParseCIDR(value); err == nil {
			value = network.String()
		} else {
			return errors.New("registration blocked networks must be IP addresses or CIDR ranges")
		}
		if _, exists := seenNetworks[value]; !exists {
			seenNetworks[value] = struct{}{}
			blockedNetworks = append(blockedNetworks, value)
		}
	}
	if len(blockedNetworks) > 256 {
		return errors.New("at most 256 registration blocked networks are allowed")
	}
	security.RegistrationBlockedNetworks = blockedNetworks

	users := &next.UserDefaults
	if users.AuthSourceDefaults == nil {
		users.AuthSourceDefaults = map[string]AuthSourceDefault{}
	}
	for _, source := range []string{"email", "linuxdo", "dingtalk", "wechat", "oidc", "github", "google"} {
		if _, ok := users.AuthSourceDefaults[source]; !ok {
			users.AuthSourceDefaults[source] = AuthSourceDefault{Enabled: true, RequireEmail: true, PlatformQuotas: map[string]PlatformQuotaSetting{}}
		}
	}
	if users.BalanceMicro < 0 || users.BalanceMicro > 1_000_000_000_000 || users.Concurrency < 0 || users.Concurrency > 10_000 || users.RPMLimit < 0 || users.RPMLimit > 1_000_000 {
		return errors.New("user defaults are out of range")
	}
	if len(users.DefaultSubscriptions) > 64 || len(users.PlatformQuotas) > 16 || len(users.AuthSourceDefaults) > 16 {
		return errors.New("too many user default entries")
	}
	quotaMap := make(map[string]PlatformQuotaSetting, len(users.PlatformQuotas))
	for platform, quota := range users.PlatformQuotas {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if !validPlatformQuota(platform, quota) {
			return errors.New("platform quotas are invalid")
		}
		quotaMap[platform] = quota
	}
	users.PlatformQuotas = quotaMap
	if users.DefaultSubscriptions == nil {
		users.DefaultSubscriptions = []DefaultSubscriptionSetting{}
	}
	seenSubscriptions := map[int64]struct{}{}
	for i := range users.DefaultSubscriptions {
		item := &users.DefaultSubscriptions[i]
		if item.GroupID <= 0 || item.ValidityDays <= 0 || item.ValidityDays > 36500 {
			return errors.New("default subscriptions are invalid")
		}
		if _, duplicate := seenSubscriptions[item.GroupID]; duplicate {
			return errors.New("default subscription groups must be unique")
		}
		seenSubscriptions[item.GroupID] = struct{}{}
	}
	for source, value := range users.AuthSourceDefaults {
		if value.DefaultSubscriptions == nil {
			value.DefaultSubscriptions = []DefaultSubscriptionSetting{}
		}
		if value.PlatformQuotas == nil {
			value.PlatformQuotas = map[string]PlatformQuotaSetting{}
		}
		if strings.TrimSpace(source) == "" || value.BalanceMicro < 0 || value.Concurrency < 0 || value.RPMLimit < 0 {
			return errors.New("auth source defaults are invalid")
		}
		for platform, quota := range value.PlatformQuotas {
			if !validPlatformQuota(platform, quota) {
				return errors.New("auth source platform quotas are invalid")
			}
		}
		seen := map[int64]struct{}{}
		for _, item := range value.DefaultSubscriptions {
			if item.GroupID <= 0 || item.ValidityDays <= 0 || item.ValidityDays > 36500 {
				return errors.New("auth source subscriptions are invalid")
			}
			if _, ok := seen[item.GroupID]; ok {
				return errors.New("auth source subscription groups must be unique")
			}
			seen[item.GroupID] = struct{}{}
		}
		users.AuthSourceDefaults[source] = value
	}

	notify := &next.Notifications
	if notify.BalanceLowThresholdMicro < 0 || notify.BalanceLowThresholdMicro > 1_000_000_000_000 || len(notify.AccountQuotaEmails) > 64 {
		return errors.New("notification settings are invalid")
	}
	seenEmails := map[string]struct{}{}
	emails := make([]string, 0, len(notify.AccountQuotaEmails))
	for _, raw := range notify.AccountQuotaEmails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		if !strings.Contains(email, "@") || len(email) > 254 {
			return errors.New("notification email is invalid")
		}
		if _, ok := seenEmails[email]; !ok {
			seenEmails[email] = struct{}{}
			emails = append(emails, email)
		}
	}
	notify.AccountQuotaEmails = emails
	if notify.AccountQuotaEmails == nil {
		notify.AccountQuotaEmails = []string{}
	}

	email := &next.Email
	email.Host = strings.TrimSpace(email.Host)
	email.Username = strings.TrimSpace(email.Username)
	email.FromName = strings.TrimSpace(email.FromName)
	email.From = strings.TrimSpace(email.From)
	if email.Port < 1 || email.Port > 65535 {
		return errors.New("SMTP port must be between 1 and 65535")
	}

	providers := []*OAuthProviderSettings{&next.AuthProviders.LinuxDO, &next.AuthProviders.DingTalk, &next.AuthProviders.WeChat, &next.AuthProviders.OIDC, &next.AuthProviders.GitHub, &next.AuthProviders.Google}
	for _, provider := range providers {
		if err := normalizeOAuthProvider(provider); err != nil {
			return err
		}
	}
	return nil
}

func validPlatformQuota(platform string, quota PlatformQuotaSetting) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "anthropic", "openai", "gemini", "grok":
	default:
		return false
	}
	const max = int64(1_000_000_000_000)
	return quota.DailyMicro >= 0 && quota.DailyMicro <= max && quota.WeeklyMicro >= 0 && quota.WeeklyMicro <= max && quota.MonthlyMicro >= 0 && quota.MonthlyMicro <= max
}

func normalizeOAuthProvider(provider *OAuthProviderSettings) error {
	provider.ProviderName = strings.TrimSpace(provider.ProviderName)
	provider.ClientID = strings.TrimSpace(provider.ClientID)
	provider.IssuerURL = strings.TrimSpace(provider.IssuerURL)
	provider.DiscoveryURL = strings.TrimSpace(provider.DiscoveryURL)
	provider.AuthorizeURL = strings.TrimSpace(provider.AuthorizeURL)
	provider.TokenURL = strings.TrimSpace(provider.TokenURL)
	provider.UserInfoURL = strings.TrimSpace(provider.UserInfoURL)
	provider.JWKSURL = strings.TrimSpace(provider.JWKSURL)
	provider.Scopes = strings.TrimSpace(provider.Scopes)
	provider.RedirectURL = strings.TrimSpace(provider.RedirectURL)
	provider.FrontendRedirectURL = strings.TrimSpace(provider.FrontendRedirectURL)
	provider.AllowedSigningAlgs = strings.TrimSpace(provider.AllowedSigningAlgs)
	provider.EmailPath = strings.TrimSpace(provider.EmailPath)
	provider.IDPath = strings.TrimSpace(provider.IDPath)
	provider.UsernamePath = strings.TrimSpace(provider.UsernamePath)
	if provider.ClockSkewSeconds < 0 || provider.ClockSkewSeconds > 600 {
		return errors.New("OAuth clock skew must be between 0 and 600 seconds")
	}
	if provider.Enabled && provider.ClientID == "" {
		return errors.New("enabled OAuth providers need a client ID")
	}
	return nil
}
