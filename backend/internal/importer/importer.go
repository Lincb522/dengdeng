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
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/model"
)

// Account is the normalized result of parsing one entry.
type Account struct {
	Name         string
	Platform     string
	AuthType     string
	APIKey       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	Email        string
	AccountID    string
	BaseURL      string
	Priority     *int
	GroupIDs     []int64
	Extra        map[string]any
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
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("parse auth-file array: %w", err)
		}
		return entries, nil
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("parse auth-file json: %w", err)
	}
	if entry == nil {
		return nil, errors.New("no accounts in auth payload")
	}
	return []map[string]any{entry}, nil
}

func parseEntry(entry map[string]any, platformHint string) Account {
	credentials := object(value(entry, "credentials", "credential"))
	tokens := object(value(entry, "tokens", "token"))
	values := []map[string]any{entry, credentials, tokens}
	get := func(keys ...string) string { return stringFrom(values, keys...) }
	getAny := func(keys ...string) any { return valueFrom(values, keys...) }

	platform := normalizePlatform(firstNonEmpty(platformHint, get("platform", "provider", "provider_type")))
	declared := firstNonEmpty(get("type", "auth_type", "authType", "auth_mode", "authMode"))
	if platform == "" {
		switch strings.ToLower(declared) {
		case "claude", "anthropic":
			platform = model.PlatformAnthropic
		case "gemini", "google":
			platform = model.PlatformGemini
		default:
			platform = model.PlatformOpenAI // native Codex auth.json has no platform field
		}
	}
	accessToken := get("access_token", "accessToken")
	refreshToken := get("refresh_token", "refreshToken")
	idToken := get("id_token", "idToken")
	apiKey := get("api_key", "apiKey", "OPENAI_API_KEY", "openai_api_key")
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

	extra := map[string]any{}
	putIf(extra, "id_token", idToken)
	putIf(extra, "session_token", get("session_token", "sessionToken"))
	putIf(extra, "client_id", get("client_id", "clientId"))
	putIf(extra, "plan_type", get("plan_type", "planType", "subscription_type", "subscriptionType"))
	putIf(extra, "organization_id", get("organization_id", "organizationId"))
	putIf(extra, "token_type", get("token_type", "tokenType"))

	return Account{
		Name:         get("name", "label", "display_name", "displayName"),
		Platform:     platform,
		AuthType:     resolveAuthType(declared, apiKey, accessToken),
		APIKey:       apiKey,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    firstTime(getAny("expires_at", "expiresAt", "expired", "expiration", "expiry")),
		Email:        email,
		AccountID:    accountID,
		BaseURL:      get("base_url", "baseUrl", "api_base", "apiBase"),
		Extra:        extra,
	}
}

// ---- shared helpers ----

func resolveAuthType(declared, apiKey, accessToken string) string {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case model.AuthAPIKey, "apikey", "api-key":
		return model.AuthAPIKey
	case model.AuthOAuth, "setup_token", "chatgpt", "claudeai", "claude_ai_oauth":
		return model.AuthOAuth
	}
	if accessToken != "" {
		return model.AuthOAuth
	}
	return model.AuthAPIKey
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
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
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
