// Package oauth renews upstream OAuth credentials (Claude / ChatGPT-Codex).
// Accounts store an access_token + refresh_token pair; this package returns a
// currently-valid access token, refreshing it against the provider shortly
// before expiry. Refreshes are serialized per account because providers issue
// one-time refresh tokens and concurrent use triggers invalid_grant.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// refreshSkew triggers a refresh this long before the token actually expires,
// so in-flight requests never carry an almost-dead token.
const refreshSkew = 2 * time.Minute

// Provider holds the public OAuth constants for a platform. These client IDs
// and endpoints are the ones the official Claude Code / Codex CLIs use.
type Provider struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	RedirectURL  string
	// BuiltinClient uses the public Codex client registration and therefore
	// needs its registered loopback ports and Codex-specific authorize flags.
	BuiltinClient bool
}

var providers = map[string]Provider{
	model.PlatformAnthropic: {
		AuthorizeURL:  "https://claude.ai/oauth/authorize",
		TokenURL:      "https://console.anthropic.com/v1/oauth/token",
		ClientID:      "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		BuiltinClient: true,
	},
	model.PlatformOpenAI: {
		AuthorizeURL: "https://auth.openai.com/oauth/authorize",
		TokenURL:     "https://auth.openai.com/oauth/token",
		ClientID:     "app_EMoamEEZ73f0CkXaXp7hrann",
		// The built-in Codex OAuth client is registered for the standard identity
		// scopes. API Platform scopes (api.model.*) are not valid additions for
		// this client and make the authorization server reject the login request.
		// OAuth traffic is routed through the Codex subscription channel instead.
		Scope:         "openid profile email offline_access",
		BuiltinClient: true,
	},
	// xAI / Grok. The public client identity is intentionally left blank: the
	// browser authorize flow only becomes available once an operator supplies
	// XAI_OAUTH_CLIENT_ID (+ redirect). Refreshing imported Grok subscription
	// tokens still works, because a refresh reuses the stored client_id.
	model.PlatformGrok: {
		AuthorizeURL: "https://auth.x.ai/oauth2/authorize",
		TokenURL:     "https://auth.x.ai/oauth2/token",
		Scope:        "openid profile email offline_access grok-cli:access api:access",
		RedirectURL:  "http://127.0.0.1:56121/callback",
	},
}

// SupportsOAuth reports whether a platform has a known refresh flow.
func SupportsOAuth(platform string) bool {
	_, ok := providers[platform]
	return ok
}

// Manager refreshes and persists OAuth tokens.
type Manager struct {
	db        *gorm.DB
	client    *http.Client
	providers map[string]Provider
	locks     sync.Map // accountID -> *sync.Mutex

	stateMu sync.Mutex
	states  map[string]loginState

	bridgeMu    sync.Mutex
	localBridge *http.Server
	localPort   int
}

func NewManager(db *gorm.DB, cfg config.OAuthConfig, client *http.Client) *Manager {
	configured := make(map[string]Provider, len(providers))
	for platform, p := range providers {
		var override config.OAuthProviderConfig
		switch platform {
		case model.PlatformOpenAI:
			override = cfg.OpenAI
		case model.PlatformAnthropic:
			override = cfg.Anthropic
		case model.PlatformGrok:
			override = cfg.Grok
		}
		if override.ClientID != "" {
			p.ClientID = override.ClientID
			p.BuiltinClient = false
		}
		if override.ClientSecret != "" {
			p.ClientSecret = override.ClientSecret
		}
		if override.AuthorizeURL != "" {
			p.AuthorizeURL = override.AuthorizeURL
		}
		if override.TokenURL != "" {
			p.TokenURL = override.TokenURL
		}
		if override.Scope != "" {
			p.Scope = override.Scope
		}
		if override.RedirectURL != "" {
			p.RedirectURL = override.RedirectURL
		}
		configured[platform] = p
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{
		db: db, client: client, providers: configured,
		states: make(map[string]loginState),
	}
}

func (m *Manager) provider(platform string) (Provider, bool) {
	p, ok := m.providers[platform]
	return p, ok
}

func (m *Manager) lockFor(id int64) *sync.Mutex {
	v, _ := m.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// AccessToken returns a valid bearer token for an OAuth account, refreshing it
// if it is missing an expiry buffer. Safe for concurrent callers.
func (m *Manager) AccessToken(ctx context.Context, acc *model.UpstreamAccount) (string, error) {
	if acc.AuthType != model.AuthOAuth {
		return "", errors.New("account is not oauth")
	}
	if !needsRefresh(acc) {
		return string(acc.AccessToken), nil
	}

	lk := m.lockFor(acc.ID)
	lk.Lock()
	defer lk.Unlock()

	// Re-read under the lock: another goroutine may have just refreshed, and we
	// need the freshest (one-time) refresh token.
	var fresh model.UpstreamAccount
	if err := m.db.First(&fresh, acc.ID).Error; err == nil {
		*acc = fresh
	}
	if !needsRefresh(acc) {
		return string(acc.AccessToken), nil
	}
	return m.refresh(ctx, acc)
}

// Refresh renews an OAuth account even when its recorded expiry has not been
// reached. Gateways use this once after an upstream invalid-token response:
// providers can revoke a session before the JWT's nominal expiry time.
// Refresh grants may rotate their token, so the work is serialized per account.
func (m *Manager) Refresh(ctx context.Context, acc *model.UpstreamAccount) (string, error) {
	if acc.AuthType != model.AuthOAuth {
		return "", errors.New("account is not oauth")
	}

	lk := m.lockFor(acc.ID)
	lk.Lock()
	defer lk.Unlock()

	// Always reload before a forced refresh so concurrent invalid-token retries
	// use the newest one-time refresh token rather than an already-rotated one.
	if m.db != nil {
		var fresh model.UpstreamAccount
		if err := m.db.First(&fresh, acc.ID).Error; err == nil {
			*acc = fresh
		}
	}
	return m.refresh(ctx, acc)
}

func needsRefresh(acc *model.UpstreamAccount) bool {
	if acc.AccessToken == "" {
		return true
	}
	if acc.ExpiresAt == nil {
		return false // no known expiry: trust the stored token
	}
	return time.Now().Add(refreshSkew).After(*acc.ExpiresAt)
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func (m *Manager) refresh(ctx context.Context, acc *model.UpstreamAccount) (string, error) {
	prov, ok := m.provider(acc.Platform)
	if !ok {
		return "", fmt.Errorf("no oauth provider for platform %s", acc.Platform)
	}
	if acc.RefreshToken == "" {
		return "", errors.New("missing refresh_token; re-import this account")
	}

	// A stored client_id (from import) overrides the built-in default.
	clientID := prov.ClientID
	extra := acc.DecodeExtra()
	if v, _ := extra["client_id"].(string); v != "" {
		clientID = v
	}

	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {string(acc.RefreshToken)},
		"client_id":     {clientID},
	}
	if prov.ClientSecret != "" {
		values.Set("client_secret", prov.ClientSecret)
	}
	// Do not send scope on refresh: OAuth refresh grants cannot be expanded.
	// New scopes (such as api.model.read) require a fresh browser authorization.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token refresh request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("token refresh failed: status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("token refresh: unexpected response: %s", truncate(string(body), 300))
	}

	acc.AccessToken = crypto.EncryptedString(tr.AccessToken)
	if tr.RefreshToken != "" {
		acc.RefreshToken = crypto.EncryptedString(tr.RefreshToken)
	}
	updates := map[string]any{
		"access_token":  acc.AccessToken,
		"refresh_token": acc.RefreshToken,
	}
	if tr.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
		acc.ExpiresAt = &exp
		updates["expires_at"] = exp
	}
	if tr.IDToken != "" {
		extra["id_token"] = tr.IDToken
		if enc, err := model.EncodeExtra(extra); err == nil {
			acc.Extra = enc
			updates["extra"] = enc
		}
	}
	m.db.Model(&model.UpstreamAccount{}).Where("id = ?", acc.ID).Updates(updates)
	return tr.AccessToken, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// LoginIntent describes the account to create after a browser-based OAuth
// authorization succeeds. It is kept server-side alongside the PKCE verifier.
type LoginIntent struct {
	GroupID  int64
	Name     string
	BaseURL  string
	Priority int
}

// LoginResult is the provider response plus the original, one-time intent.
type LoginResult struct {
	Intent       LoginIntent
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    *time.Time
	Origin       string
}

// Identity is the non-secret account metadata available in an ID token. The
// token itself comes directly from the provider; this helper only reads claims
// to make the newly-created account easy to identify in the console.
type Identity struct {
	Email     string
	AccountID string
}

func IdentityFromIDToken(token string) Identity {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return Identity{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return Identity{}
	}
	identity := Identity{
		Email:     claimString(claims, "email"),
		AccountID: claimString(claims, "chatgpt_account_id", "account_id"),
	}
	if identity.AccountID == "" {
		if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			identity.AccountID = claimString(auth, "chatgpt_account_id", "account_id")
		}
	}
	return identity
}

func claimString(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := claims[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type loginState struct {
	platform      string
	verifier      string
	redirectURL   string // URI registered with / shown to the OAuth provider
	completionURL string // console callback which persists the returned tokens
	intent        LoginIntent
	expiresAt     time.Time
}

const loginStateTTL = 10 * time.Minute

// CallbackURLs returns the provider-facing URI and the console completion URI.
// The built-in OpenAI client accepts localhost loopback callbacks at
// /auth/callback (with an arbitrary local port), but rejects 127.0.0.1 and
// application-specific callback paths. The local bridge route then redirects
// back to the console completion URL without exposing any credential.
func (m *Manager) CallbackURLs(platform, requestHost string, isTLS bool) (providerURL, completionURL string, err error) {
	prov, ok := m.provider(platform)
	if !ok || prov.AuthorizeURL == "" || prov.TokenURL == "" || prov.ClientID == "" {
		return "", "", fmt.Errorf("oauth login is not configured for %s", platform)
	}
	if prov.RedirectURL != "" {
		if defaults, exists := providers[platform]; exists && prov.ClientID == defaults.ClientID {
			return "", "", fmt.Errorf("configure oauth.%s.client_id together with redirect_url", platform)
		}
		u, err := url.Parse(prov.RedirectURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", "", fmt.Errorf("invalid configured oauth redirect_url for %s", platform)
		}
		return prov.RedirectURL, prov.RedirectURL, nil
	}

	host, port, err := splitHostPort(requestHost)
	if err != nil {
		return "", "", err
	}
	if !isLoopbackHost(host) {
		return "", "", fmt.Errorf("configure oauth.%s.redirect_url for a non-local deployment", platform)
	}
	scheme := "http"
	if isTLS {
		scheme = "https"
	}
	if platform == model.PlatformOpenAI && prov.BuiltinClient {
		bridgePort, err := m.ensureOpenAILocalBridge()
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("http://localhost:%d/auth/callback", bridgePort),
			fmt.Sprintf("%s://%s/api/admin/oauth/%s/callback", scheme, requestHost, platform), nil
	}
	if port == "" && host == "::1" {
		requestHost = "[::1]"
	} else if port == "" {
		requestHost = host
	}
	completionURL = fmt.Sprintf("%s://%s/api/admin/oauth/%s/callback", scheme, requestHost, platform)
	return completionURL, completionURL, nil
}

// CallbackURL remains a convenience for callers that only need the URI shown
// to the provider.
func (m *Manager) CallbackURL(platform, requestHost string, isTLS bool) (string, error) {
	providerURL, _, err := m.CallbackURLs(platform, requestHost, isTLS)
	return providerURL, err
}

func splitHostPort(raw string) (host, port string, err error) {
	u, err := url.Parse("//" + raw)
	if err != nil || u.Hostname() == "" {
		return "", "", errors.New("invalid request host for oauth callback")
	}
	return u.Hostname(), u.Port(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// BeginLogin creates a short-lived PKCE state and returns the provider URL the
// browser should open. The state is deliberately opaque and one-time use.
func (m *Manager) BeginLogin(platform, redirectURL string, intent LoginIntent) (string, error) {
	return m.BeginLoginWithCompletion(platform, redirectURL, redirectURL, intent)
}

// BeginLoginWithCompletion lets localhost OAuth use a provider-compatible
// bridge URL while retaining a same-origin console callback for the popup.
func (m *Manager) BeginLoginWithCompletion(platform, redirectURL, completionURL string, intent LoginIntent) (string, error) {
	prov, ok := m.provider(platform)
	if !ok || prov.AuthorizeURL == "" || prov.TokenURL == "" || prov.ClientID == "" {
		return "", fmt.Errorf("oauth login is not configured for %s", platform)
	}
	state, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	m.stateMu.Lock()
	now := time.Now()
	for key, value := range m.states {
		if now.After(value.expiresAt) {
			delete(m.states, key)
		}
	}
	m.states[state] = loginState{
		platform: platform, verifier: verifier, redirectURL: redirectURL, completionURL: completionURL,
		intent: intent, expiresAt: now.Add(loginStateTTL),
	}
	m.stateMu.Unlock()

	u, err := url.Parse(prov.AuthorizeURL)
	if err != nil {
		m.dropState(state)
		return "", fmt.Errorf("invalid oauth authorize URL: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", prov.ClientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if prov.Scope != "" {
		q.Set("scope", prov.Scope)
	}
	if platform == model.PlatformOpenAI && prov.BuiltinClient {
		q.Set("id_token_add_organizations", "true")
		q.Set("codex_cli_simplified_flow", "true")
		q.Set("originator", "codex_cli_rs")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CompleteLogin consumes a callback state, exchanges the authorization code,
// and returns the credentials for the handler to persist.
func (m *Manager) CompleteLogin(ctx context.Context, platform, state, code string) (*LoginResult, error) {
	flow, err := m.takeState(platform, state)
	if err != nil {
		return nil, err
	}
	prov, _ := m.provider(platform)
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {flow.redirectURL},
		"client_id":     {prov.ClientID},
		"code_verifier": {flow.verifier},
	}
	if prov.ClientSecret != "" {
		values.Set("client_secret", prov.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("token exchange failed: status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var tokens tokenResp
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" {
		return nil, fmt.Errorf("token exchange: unexpected response: %s", truncate(string(body), 300))
	}
	var expiresAt *time.Time
	if tokens.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		expiresAt = &exp
	}
	origin := ""
	if callback, err := url.Parse(flow.completionURL); err == nil {
		origin = callback.Scheme + "://" + callback.Host
	}
	return &LoginResult{
		Intent: flow.intent, AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		IDToken: tokens.IDToken, ExpiresAt: expiresAt, Origin: origin,
	}, nil
}

// CancelLogin consumes a pending state after a provider-side denial. It
// returns its callback origin so the frontend popup can still be notified.
func (m *Manager) CancelLogin(platform, state string) string {
	flow, err := m.takeState(platform, state)
	if err != nil {
		return ""
	}
	u, err := url.Parse(flow.completionURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// LocalCallbackTarget translates the provider-compatible local bridge URL to
// the callback held in the one-time server state. It only forwards expected
// OAuth fields and never trusts a redirect location supplied by the browser.
func (m *Manager) LocalCallbackTarget(state string, providerQuery url.Values) (string, error) {
	m.stateMu.Lock()
	flow, ok := m.states[state]
	m.stateMu.Unlock()
	if !ok || time.Now().After(flow.expiresAt) || flow.completionURL == "" {
		return "", errors.New("oauth login has expired or is invalid; start again")
	}
	u, err := url.Parse(flow.completionURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid oauth completion URL")
	}
	q := u.Query()
	q.Set("state", state)
	for _, key := range []string{"code", "error", "error_description", "error_uri"} {
		if value := providerQuery.Get(key); value != "" {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OpenAI's public Codex client is registered for these two loopback callback
// ports (the same pair used by the official CLI). The short-lived local bridge
// receives that callback and redirects it into the state-bound console route.
const (
	openAILocalPort         = 1455
	openAILocalFallbackPort = 1457
)

func (m *Manager) ensureOpenAILocalBridge() (int, error) {
	m.bridgeMu.Lock()
	defer m.bridgeMu.Unlock()
	if m.localBridge != nil {
		return m.localPort, nil
	}

	var lastErr error
	for _, port := range []int{openAILocalPort, openAILocalFallbackPort} {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			lastErr = err
			continue
		}
		server := &http.Server{
			Handler:           http.HandlerFunc(m.serveOpenAILocalCallback),
			ReadHeaderTimeout: 10 * time.Second,
		}
		m.localBridge = server
		m.localPort = port
		go func() {
			_ = server.Serve(listener)
		}()
		return port, nil
	}
	return 0, fmt.Errorf("unable to start the local OpenAI OAuth callback on ports %d or %d: %w", openAILocalPort, openAILocalFallbackPort, lastErr)
}

func (m *Manager) serveOpenAILocalCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" {
		http.NotFound(w, r)
		return
	}
	target, err := m.LocalCallbackTarget(r.URL.Query().Get("state"), r.URL.Query())
	if err != nil {
		http.Error(w, "OAuth callback is invalid or expired. Please start again.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// Close releases the local callback bridge. The application keeps it for its
// lifetime; this method is mainly useful to deterministic test cleanup.
func (m *Manager) Close() error {
	m.bridgeMu.Lock()
	server := m.localBridge
	m.localBridge = nil
	m.localPort = 0
	m.bridgeMu.Unlock()
	if server == nil {
		return nil
	}
	return server.Close()
}

func (m *Manager) takeState(platform, state string) (loginState, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	flow, ok := m.states[state]
	if ok {
		delete(m.states, state)
	}
	if !ok || flow.platform != platform || time.Now().After(flow.expiresAt) {
		return loginState{}, errors.New("oauth login has expired or is invalid; start again")
	}
	return flow, nil
}

func (m *Manager) dropState(state string) {
	m.stateMu.Lock()
	delete(m.states, state)
	m.stateMu.Unlock()
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
