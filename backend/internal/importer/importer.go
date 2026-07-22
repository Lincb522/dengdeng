// Package importer parses upstream-account exports into a normalized shape.
// It accepts sub2api exports as well as the auth files written by Codex,
// CLIProxyAPI, and Claude Code. Both snake_case and camelCase credential keys
// are understood because the upstream CLIs use different conventions.
package importer

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/model"
)

// Account is the normalized result of parsing one entry.
type Account struct {
	Name     string
	Platform string
	// PlatformDetected is true when the payload contains a trustworthy
	// provider signal. Generic OAuth/API-key files intentionally keep the
	// legacy OpenAI fallback in Platform while allowing the import handler to
	// use the administrator-selected target group's platform.
	PlatformDetected bool
	AuthType         string
	APIKey           string
	AccessToken      string
	RefreshToken     string
	ExpiresAt        *time.Time
	Email            string
	AccountID        string
	BaseURL          string
	Priority         *int
	Concurrency      *int
	GroupIDs         []int64
	Extra            map[string]any
}

// Parse decodes raw bytes in the named format ("sub2api", "cpa", or
// "auto"). CPA means the broader CLI auth-file family, including native
// ~/.codex/auth.json and ~/.claude/.credentials.json payloads.
func Parse(format string, raw []byte) ([]Account, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, errors.New("empty payload")
	}
	switch format {
	case "sub2api":
		return parseSub2API(raw)
	case "cpa":
		return parseCPA(raw)
	case "", "auto":
		if looksLikeSub2API(raw) {
			return parseSub2API(raw)
		}
		return parseCPA(raw)
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

func looksLikeSub2API(raw []byte) bool {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	if _, ok := probe["accounts"]; ok {
		return true
	}
	if _, ok := probe["exported_at"]; ok {
		return true
	}
	if data, ok := probe["data"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(data, &nested) == nil {
			_, ok = nested["accounts"]
			return ok
		}
	}
	return false
}

// ---- sub2api ----

func parseSub2API(raw []byte) ([]Account, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse sub2api json: %w", err)
	}
	entriesRaw := env["accounts"]
	if len(entriesRaw) == 0 {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(env["data"], &data); err == nil {
			entriesRaw = data["accounts"]
		}
	}
	var entries []map[string]any
	if len(entriesRaw) == 0 || json.Unmarshal(entriesRaw, &entries) != nil || len(entries) == 0 {
		return nil, errors.New("no accounts in sub2api payload")
	}

	out := make([]Account, 0, len(entries))
	for i, entry := range entries {
		acc := parseEntry(entry, "")
		acc.Priority = intPointer(value(entry, "priority"))
		acc.Concurrency = intPointer(value(entry, "concurrency"))
		acc.GroupIDs = int64Slice(value(entry, "group_ids", "groupIds"))
		if acc.Name == "" {
			acc.Name = defaultName(acc, i)
		}
		out = append(out, acc)
	}
	return out, nil
}

// ---- CLI auth files ----

func parseCPA(raw []byte) ([]Account, error) {
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no accounts in auth payload")
	}

	out := make([]Account, 0, len(entries))
	for i, entry := range entries {
		// Claude Code stores its auth record beneath claudeAiOauth rather than
		// at the JSON root. Merge the outer metadata so an optional name/email
		// beside that record is preserved.
		if claude := object(value(entry, "claudeAiOauth", "claude_ai_oauth")); claude != nil {
			merged := cloneMap(entry)
			for key, val := range claude {
				merged[key] = val
			}
			merged["platform"] = model.PlatformAnthropic
			entry = merged
		}
		acc := parseEntry(entry, "")
		if acc.Name == "" {
			acc.Name = defaultName(acc, i)
		}
		out = append(out, acc)
	}
	return out, nil
}

func decodeEntries(raw []byte) ([]map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	entries := make([]map[string]any, 0, 1)
	var appendValue func(any) error
	appendValue = func(value any) error {
		switch typed := value.(type) {
		case map[string]any:
			entries = append(entries, typed)
			return nil
		case []any:
			for _, item := range typed {
				if err := appendValue(item); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("auth-file entry must be a JSON object, got %T", value)
		}
	}
	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse auth-file json: %w", err)
		}
		if err := appendValue(value); err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("no accounts in auth payload")
	}
	return entries, nil
}

func parseEntry(entry map[string]any, platformHint string) Account {
	credentials := object(value(entry, "credentials", "credential"))
	tokens := object(value(entry, "tokens", "token"))
	agentIdentity := object(value(entry, "agent_identity", "agentIdentity"))
	values := []map[string]any{entry, credentials, tokens, agentIdentity}
	get := func(keys ...string) string { return stringFrom(values, keys...) }
	getAny := func(keys ...string) any { return valueFrom(values, keys...) }

	declared := firstNonEmpty(get("type", "auth_type", "authType", "auth_mode", "authMode"))
	authMode := get("auth_mode", "authMode")
	isAgentIdentity := agentIdentity != nil || strings.EqualFold(authMode, "agentIdentity") || strings.EqualFold(declared, "agentIdentity")
	accessToken := get("access_token", "accessToken")
	refreshToken := get("refresh_token", "refreshToken")
	idToken := get("id_token", "idToken")
	apiKey := get("api_key", "apiKey", "OPENAI_API_KEY", "openai_api_key")
	platform, platformDetected := detectPlatform(values, platformHint, declared, authMode, idToken, isAgentIdentity)
	email := get("email")
	accountID := get("chatgpt_account_id", "chatgptAccountId", "account_id", "accountId", "organization_id", "organizationId")
	if email == "" || accountID == "" {
		jwtEmail, jwtAccountID := identityFromIDToken(idToken)
		if email == "" {
			email = jwtEmail
		}
		if accountID == "" {
			accountID = jwtAccountID
		}
	}
	if platform == model.PlatformGrok && accountID == "" {
		accountID = get("sub", "team_id", "teamId")
		if accountID == "" {
			if claims := jwtClaims(idToken); claims != nil {
				accountID, _ = claims["sub"].(string)
				accountID = strings.TrimSpace(accountID)
			}
		}
	}

	extra := map[string]any{}
	if isAgentIdentity {
		extra["auth_mode"] = "agentIdentity"
		putIf(extra, "agent_runtime_id", get("agent_runtime_id", "agentRuntimeId"))
		putIf(extra, "agent_private_key", get("agent_private_key", "agentPrivateKey"))
		putIf(extra, "task_id", get("task_id", "taskId"))
		putIf(extra, "chatgpt_user_id", get("chatgpt_user_id", "chatgptUserId", "user_id", "userId"))
		if fedramp, ok := boolFrom(values, "chatgpt_account_is_fedramp", "chatgptAccountIsFedramp"); ok {
			extra["chatgpt_account_is_fedramp"] = fedramp
		}
		// The Agent Identity record is the complete durable credential. Codex may
		// keep its original OAuth tokens beside the record, but Sub2API's import
		// path intentionally does not copy those bootstrap credentials.
		apiKey = ""
		accessToken = ""
		refreshToken = ""
		idToken = ""
	}
	if !isAgentIdentity {
		putIf(extra, "id_token", idToken)
		putIf(extra, "session_token", get("session_token", "sessionToken"))
		putIf(extra, "client_id", get("client_id", "clientId"))
		putIf(extra, "client_secret", get("client_secret", "clientSecret"))
		putIf(extra, "scope", get("scope", "scopes"))
		putIf(extra, "project_id", get("project_id", "projectId"))
		putIf(extra, "oauth_type", get("oauth_type", "oauthType"))
	}
	putIf(extra, "plan_type", get("plan_type", "planType", "subscription_type", "subscriptionType"))
	putIf(extra, "subscription_tier", get("subscription_tier", "subscriptionTier"))
	putIf(extra, "entitlement_status", get("entitlement_status", "entitlementStatus"))
	putIf(extra, "team_id", get("team_id", "teamId"))
	putIf(extra, "sub", get("sub"))
	if subscriptionExpiry := firstTime(getAny("subscription_expires_at", "subscriptionExpiresAt", "subscription_active_until", "subscriptionActiveUntil")); subscriptionExpiry != nil {
		extra["subscription_expires_at"] = subscriptionExpiry.UTC().Format(time.RFC3339)
	} else if subscriptionExpiry := subscriptionExpiryFromIDToken(idToken); subscriptionExpiry != nil {
		extra["subscription_expires_at"] = subscriptionExpiry.UTC().Format(time.RFC3339)
	}
	putIf(extra, "organization_id", get("organization_id", "organizationId"))
	putIf(extra, "token_type", get("token_type", "tokenType"))
	expiresAt := firstTime(getAny("expires_at", "expiresAt", "expired", "expiration", "expiry"))
	// Agent Identity is a durable signing credential. Some exports retain the
	// expiry of the bootstrap OAuth token, but that timestamp must not expire or
	// auto-pause the imported identity itself.
	if isAgentIdentity {
		expiresAt = nil
	}

	return Account{
		Name:             get("name", "label", "display_name", "displayName"),
		Platform:         platform,
		PlatformDetected: platformDetected,
		AuthType: func() string {
			if isAgentIdentity {
				return model.AuthAgentIdentity
			}
			return resolveAuthType(declared, apiKey, accessToken)
		}(),
		APIKey:       apiKey,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		Email:        email,
		AccountID:    accountID,
		BaseURL:      get("base_url", "baseUrl", "api_base", "apiBase"),
		Concurrency:  intPointer(getAny("concurrency")),
		Extra:        extra,
	}
}

// detectPlatform separates a trustworthy provider signal from the historical
// OpenAI default. That distinction matters for generic OAuth exports: the same
// access/refresh-token shape is used by OpenAI, Grok and other CLI clients, so
// the selected group is the only reliable provider hint when metadata is
// absent.
func detectPlatform(values []map[string]any, platformHint, declared, authMode, idToken string, isAgentIdentity bool) (string, bool) {
	if platform := normalizePlatform(firstNonEmpty(platformHint, stringFrom(values, "platform", "provider", "provider_type"))); platform != "" {
		return platform, true
	}

	switch strings.ToLower(strings.TrimSpace(declared)) {
	case "claude", "anthropic":
		return model.PlatformAnthropic, true
	case "gemini", "google":
		return model.PlatformGemini, true
	case "grok", "xai", "x.ai":
		return model.PlatformGrok, true
	case "chatgpt", "codex":
		return model.PlatformOpenAI, true
	}
	if isAgentIdentity || strings.EqualFold(authMode, "chatgpt") || strings.EqualFold(authMode, "agentIdentity") {
		return model.PlatformOpenAI, true
	}
	if platform := platformFromCredentialMetadata(values, idToken); platform != "" {
		return platform, true
	}

	// Keep Parse's backwards-compatible result for callers that do not have a
	// target group. ImportAccounts checks PlatformDetected and replaces only
	// this uncertain fallback with the selected group's platform.
	return model.PlatformOpenAI, false
}

func platformFromCredentialMetadata(values []map[string]any, idToken string) string {
	baseURL := strings.ToLower(stringFrom(values, "base_url", "baseUrl", "api_base", "apiBase"))
	scope := strings.ToLower(stringFrom(values, "scope", "scopes"))
	oauthType := strings.ToLower(stringFrom(values, "oauth_type", "oauthType"))
	if strings.Contains(baseURL, "cli-chat-proxy.grok.com") || strings.Contains(baseURL, "api.x.ai") ||
		strings.Contains(scope, "grok-cli:") || strings.Contains(oauthType, "grok") || strings.Contains(oauthType, "xai") {
		return model.PlatformGrok
	}
	if stringFrom(values, "subscription_tier", "subscriptionTier", "entitlement_status", "entitlementStatus") != "" {
		return model.PlatformGrok
	}
	if claims := jwtClaims(idToken); claims != nil {
		identity := strings.ToLower(strings.Join(jwtProviderClaims(claims), " "))
		switch {
		case strings.Contains(identity, "auth.x.ai"), strings.Contains(identity, "api.x.ai"), strings.Contains(identity, "grok-cli"):
			return model.PlatformGrok
		case strings.Contains(identity, "auth.openai.com"), strings.Contains(identity, "api.openai.com"):
			return model.PlatformOpenAI
		case strings.Contains(identity, "accounts.google.com"), strings.Contains(identity, "googleapis.com"):
			return model.PlatformGemini
		case strings.Contains(identity, "anthropic.com"), strings.Contains(identity, "claude.ai"):
			return model.PlatformAnthropic
		}
	}
	return ""
}

func jwtProviderClaims(claims map[string]any) []string {
	out := make([]string, 0, 5)
	for _, key := range []string{"iss", "aud", "azp", "scope", "scp"} {
		switch value := claims[key].(type) {
		case string:
			out = append(out, value)
		case []any:
			for _, item := range value {
				if text, ok := item.(string); ok {
					out = append(out, text)
				}
			}
		}
	}
	return out
}

// ---- shared helpers ----

func resolveAuthType(declared, apiKey, accessToken string) string {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case model.AuthAPIKey, "apikey", "api-key":
		return model.AuthAPIKey
	case model.AuthOAuth, "setup_token", "chatgpt", "claudeai", "claude_ai_oauth":
		return model.AuthOAuth
	case model.AuthAgentIdentity, "agentidentity", "agent-identity":
		return model.AuthAgentIdentity
	}
	if accessToken != "" {
		return model.AuthOAuth
	}
	return model.AuthAPIKey
}

func boolFrom(maps []map[string]any, keys ...string) (bool, bool) {
	for _, m := range maps {
		if m == nil {
			continue
		}
		for _, key := range keys {
			switch value := m[key].(type) {
			case bool:
				return value, true
			case string:
				parsed, err := strconv.ParseBool(strings.TrimSpace(value))
				if err == nil {
					return parsed, true
				}
			}
		}
	}
	return false, false
}

func normalizePlatform(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "anthropic", "claude", "claudeai":
		return model.PlatformAnthropic
	case "openai", "chatgpt", "codex":
		return model.PlatformOpenAI
	case "gemini", "google":
		return model.PlatformGemini
	case "grok", "xai", "x.ai":
		return model.PlatformGrok
	}
	return ""
}

func defaultName(a Account, i int) string {
	if a.Email != "" {
		return a.Email
	}
	return fmt.Sprintf("%s-import-%d", firstNonEmpty(a.Platform, "account"), i+1)
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func cloneMap(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func value(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func valueFrom(maps []map[string]any, keys ...string) any {
	for _, m := range maps {
		if m == nil {
			continue
		}
		if v := value(m, keys...); v != nil {
			return v
		}
	}
	return nil
}

func stringFrom(maps []map[string]any, keys ...string) string {
	for _, m := range maps {
		if m == nil {
			continue
		}
		for _, key := range keys {
			if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func putIf(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func intPointer(v any) *int {
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case json.Number:
		if i, err := n.Int64(); err == nil {
			value := int(i)
			return &value
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return &i
		}
	}
	return nil
}

func int64Slice(v any) []int64 {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int64, 0, len(items))
	for _, item := range items {
		switch n := item.(type) {
		case float64:
			out = append(out, int64(n))
		case string:
			if value, err := strconv.ParseInt(n, 10, 64); err == nil {
				out = append(out, value)
			}
		}
	}
	return out
}

func firstTime(v any) *time.Time { return anyToTime(v) }

// anyToTime accepts RFC3339 strings, epoch seconds, or epoch milliseconds.
func anyToTime(v any) *time.Time {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return &ts
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epochToTime(n)
		}
	case float64:
		return epochToTime(int64(t))
	case int64:
		return epochToTime(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return epochToTime(n)
		}
	}
	return nil
}

func epochToTime(n int64) *time.Time {
	if n <= 0 {
		return nil
	}
	if n > 1e12 { // milliseconds
		t := time.UnixMilli(n)
		return &t
	}
	t := time.Unix(n, 0)
	return &t
}

func identityFromIDToken(token string) (email, accountID string) {
	claims := jwtClaims(token)
	if claims == nil {
		return "", ""
	}
	email, _ = claims["email"].(string)
	accountID, _ = claims["chatgpt_account_id"].(string)
	if accountID == "" {
		accountID, _ = claims["account_id"].(string)
	}
	if accountID == "" {
		if auth := object(claims["https://api.openai.com/auth"]); auth != nil {
			accountID, _ = auth["chatgpt_account_id"].(string)
		}
	}
	return strings.TrimSpace(email), strings.TrimSpace(accountID)
}

func subscriptionExpiryFromIDToken(token string) *time.Time {
	claims := jwtClaims(token)
	if claims == nil {
		return nil
	}
	for _, source := range []map[string]any{claims, object(claims["https://api.openai.com/auth"])} {
		if source == nil {
			continue
		}
		for _, key := range []string{"subscription_expires_at", "chatgpt_subscription_active_until", "subscription_active_until"} {
			if expiry := anyToTime(source[key]); expiry != nil {
				return expiry
			}
		}
	}
	return nil
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
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
