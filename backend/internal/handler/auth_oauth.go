package handler

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func randomOAuthValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (h *AuthHandler) oauthProvider(name string, settings service.SystemSettings) (service.OAuthProviderSettings, string, bool) {
	switch strings.ToLower(name) {
	case "linuxdo":
		return settings.AuthProviders.LinuxDO, service.SecretLinuxDOOAuth, true
	case "dingtalk":
		return settings.AuthProviders.DingTalk, service.SecretDingTalkOAuth, true
	case "wechat":
		return settings.AuthProviders.WeChat, service.SecretWeChatOAuth, true
	case "oidc":
		return settings.AuthProviders.OIDC, service.SecretOIDCOAuth, true
	case "github":
		return settings.AuthProviders.GitHub, service.SecretGitHubOAuth, true
	case "google":
		return settings.AuthProviders.Google, service.SecretGoogleOAuth, true
	default:
		return service.OAuthProviderSettings{}, "", false
	}
}

func (h *AuthHandler) resolveOAuthProvider(provider service.OAuthProviderSettings) (service.OAuthProviderSettings, error) {
	if provider.AuthorizeURL != "" && provider.TokenURL != "" && provider.UserInfoURL != "" {
		return provider, nil
	}
	discovery := strings.TrimSpace(provider.DiscoveryURL)
	if discovery == "" && provider.IssuerURL != "" {
		discovery = strings.TrimRight(provider.IssuerURL, "/") + "/.well-known/openid-configuration"
	}
	if discovery == "" {
		return provider, fmt.Errorf("OAuth endpoints are incomplete")
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(discovery)
	if err != nil {
		return provider, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return provider, fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}
	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserInfoEndpoint      string `json:"userinfo_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&doc) != nil {
		return provider, fmt.Errorf("invalid OIDC discovery document")
	}
	if provider.AuthorizeURL == "" {
		provider.AuthorizeURL = doc.AuthorizationEndpoint
	}
	if provider.TokenURL == "" {
		provider.TokenURL = doc.TokenEndpoint
	}
	if provider.UserInfoURL == "" {
		provider.UserInfoURL = doc.UserInfoEndpoint
	}
	if provider.JWKSURL == "" {
		provider.JWKSURL = doc.JWKSURI
	}
	if provider.AuthorizeURL == "" || provider.TokenURL == "" || provider.UserInfoURL == "" {
		return provider, fmt.Errorf("OIDC discovery is missing required endpoints")
	}
	return provider, nil
}

func (h *AuthHandler) StartUserOAuth(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, 500, "load OAuth settings failed")
		return
	}
	providerName := strings.ToLower(c.Param("provider"))
	provider, secretName, ok := h.oauthProvider(providerName, settings)
	if !ok || !provider.Enabled {
		util.Fail(c, 404, "OAuth provider is disabled")
		return
	}
	provider, err = h.resolveOAuthProvider(provider)
	if err != nil {
		util.Fail(c, 503, err.Error())
		return
	}
	if provider.ClientID == "" {
		util.Fail(c, 503, "OAuth client ID is not configured")
		return
	}
	if secret, _ := h.settings.Secret(secretName); secret == "" {
		util.Fail(c, 503, "OAuth client secret is not configured")
		return
	}
	var req struct {
		TermsRevision  string `json:"terms_revision"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, 400, "invalid OAuth request")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	if settings.LoginAgreement.Enabled && strings.TrimSpace(req.TermsRevision) != settings.LoginAgreement.Revision() {
		util.Fail(c, 403, "latest terms must be accepted")
		return
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		util.Fail(c, 500, "generate OAuth state failed")
		return
	}
	verifier, err := randomOAuthValue(48)
	if err != nil {
		util.Fail(c, 500, "generate PKCE verifier failed")
		return
	}
	nonce, err := randomOAuthValue(32)
	if err != nil {
		util.Fail(c, 500, "generate OAuth nonce failed")
		return
	}
	if err := h.storeOAuthFlow(state, model.UserOAuthFlow{
		Kind: oauthFlowState, Provider: providerName, Verifier: verifier, Nonce: nonce,
		TermsRevision: req.TermsRevision, ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		util.Fail(c, 500, "save OAuth state failed")
		return
	}
	redirectURI := provider.RedirectURL
	if redirectURI == "" {
		redirectURI = strings.TrimRight(h.cfg.Site.PublicURL, "/") + "/api/auth/oauth/" + providerName + "/callback"
	}
	query := url.Values{"client_id": {provider.ClientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "state": {state}, "scope": {provider.Scopes}}
	if strings.Contains(" "+provider.Scopes+" ", " openid ") || provider.ValidateIDToken {
		query.Set("nonce", nonce)
	}
	if providerName == "wechat" {
		query.Del("client_id")
		query.Set("appid", provider.ClientID)
	}
	if provider.UsePKCE {
		sum := sha256.Sum256([]byte(verifier))
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		query.Set("code_challenge_method", "S256")
	}
	separator := "?"
	if strings.Contains(provider.AuthorizeURL, "?") {
		separator = "&"
	}
	authorizationURL := provider.AuthorizeURL + separator + query.Encode()
	if providerName == "wechat" {
		authorizationURL += "#wechat_redirect"
	}
	util.OK(c, gin.H{"authorization_url": authorizationURL})
}

func (h *AuthHandler) CompleteUserOAuth(c *gin.Context) {
	stateValue, code := c.Query("state"), c.Query("code")
	state, err := h.consumeOAuthFlow(oauthFlowState, stateValue)
	if err != nil || code == "" {
		h.redirectOAuthError(c, state.Provider, "OAuth 回调已失效")
		return
	}
	settings, err := h.settings.Get()
	if err != nil {
		h.redirectOAuthError(c, state.Provider, "读取登录配置失败")
		return
	}
	provider, secretName, ok := h.oauthProvider(state.Provider, settings)
	if !ok || !provider.Enabled {
		h.redirectOAuthError(c, state.Provider, "登录方式已关闭")
		return
	}
	provider, err = h.resolveOAuthProvider(provider)
	if err != nil {
		h.redirectOAuthError(c, state.Provider, err.Error())
		return
	}
	secret, err := h.settings.Secret(secretName)
	if err != nil || secret == "" {
		h.redirectOAuthError(c, state.Provider, "OAuth 密钥未配置")
		return
	}
	redirectURI := provider.RedirectURL
	if redirectURI == "" {
		redirectURI = strings.TrimRight(h.cfg.Site.PublicURL, "/") + "/api/auth/oauth/" + state.Provider + "/callback"
	}
	accessToken, tokenPayload, err := h.exchangeOAuthToken(state.Provider, provider, secret, code, redirectURI, state.Verifier)
	if err != nil {
		h.redirectOAuthError(c, state.Provider, "交换登录凭据失败")
		return
	}
	var idClaims map[string]any
	if provider.ValidateIDToken {
		idClaims, err = validateOIDCIDToken(provider, jsonPathString(tokenPayload, "id_token"), state.Nonce)
		if err != nil {
			h.redirectOAuthError(c, state.Provider, "ID Token 校验失败")
			return
		}
	}
	profile, err := h.fetchOAuthProfile(state.Provider, provider, accessToken, tokenPayload)
	if err != nil {
		h.redirectOAuthError(c, state.Provider, "读取用户资料失败")
		return
	}
	for key, value := range idClaims {
		if _, exists := profile[key]; !exists {
			profile[key] = value
		}
	}
	subject := jsonPathString(profile, provider.IDPath)
	if subject == "" {
		subject = jsonPathString(profile, "sub")
		if subject == "" {
			subject = jsonPathString(profile, "id")
		}
	}
	email := normalizedEmail(jsonPathString(profile, provider.EmailPath))
	if email == "" {
		email = normalizedEmail(jsonPathString(profile, "email"))
	}
	if provider.RequireVerifiedEmail {
		if verified, ok := profile["email_verified"].(bool); !ok || !verified {
			h.redirectOAuthError(c, state.Provider, "第三方邮箱尚未验证")
			return
		}
	}
	sourcePolicy := settings.UserDefaults.AuthSourceDefaults[state.Provider]
	if subject == "" || ((provider.RequireVerifiedEmail || sourcePolicy.RequireEmail) && email == "") {
		h.redirectOAuthError(c, state.Provider, "OAuth 用户资料不完整")
		return
	}
	user, err := h.findOrCreateOAuthUser(settings, state.Provider, subject, email, state.TermsRevision)
	if err != nil {
		h.redirectOAuthError(c, state.Provider, err.Error())
		return
	}
	resultCode, _ := randomOAuthValue(32)
	if err := h.storeOAuthFlow(resultCode, model.UserOAuthFlow{Kind: oauthFlowResult, Provider: state.Provider, UserID: user.ID, ExpiresAt: time.Now().UTC().Add(2 * time.Minute)}); err != nil {
		h.redirectOAuthError(c, state.Provider, "创建登录凭据失败")
		return
	}
	frontend := provider.FrontendRedirectURL
	if frontend == "" {
		frontend = "/login"
	}
	base := strings.TrimRight(h.cfg.Site.PublicURL, "/")
	target, _ := url.Parse(base + frontend)
	query := target.Query()
	query.Set("oauth_code", resultCode)
	target.RawQuery = query.Encode()
	c.Redirect(http.StatusFound, target.String())
}

func (h *AuthHandler) redirectOAuthError(c *gin.Context, provider, message string) {
	target, _ := url.Parse(strings.TrimRight(h.cfg.Site.PublicURL, "/") + "/login")
	q := target.Query()
	q.Set("oauth_error", message)
	if provider != "" {
		q.Set("provider", provider)
	}
	target.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, target.String())
}

func (h *AuthHandler) ExchangeUserOAuth(c *gin.Context) {
	var req struct {
		Code     string `json:"code"`
		TOTPCode string `json:"totp_code"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Code == "" {
		util.Fail(c, 400, "OAuth login code is required")
		return
	}
	result, err := h.loadOAuthFlow(oauthFlowResult, req.Code)
	if err != nil {
		util.Fail(c, 400, "OAuth login code expired")
		return
	}
	var user model.User
	if h.db.First(&user, result.UserID).Error != nil || user.Status != model.StatusActive {
		util.Fail(c, 403, "account unavailable")
		return
	}
	if user.TOTPEnabled && !util.ValidateTOTP(string(user.TOTPSecret), req.TOTPCode, time.Now()) {
		util.Fail(c, 401, "authenticator code is required or invalid")
		return
	}
	if !h.deleteOAuthFlow(result.ID) {
		util.Fail(c, 400, "OAuth login code expired")
		return
	}
	h.issueToken(c, &user)
}

func (h *AuthHandler) exchangeOAuthToken(name string, provider service.OAuthProviderSettings, secret, code, redirectURI, verifier string) (string, map[string]any, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var req *http.Request
	var err error
	if name == "dingtalk" {
		body, _ := json.Marshal(gin.H{"clientId": provider.ClientID, "clientSecret": secret, "code": code, "grantType": "authorization_code"})
		req, err = http.NewRequest(http.MethodPost, provider.TokenURL, bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else if name == "wechat" {
		q := url.Values{"appid": {provider.ClientID}, "secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
		req, err = http.NewRequest(http.MethodGet, provider.TokenURL+"?"+q.Encode(), nil)
	} else {
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "client_id": {provider.ClientID}}
		if verifier != "" && provider.UsePKCE {
			form.Set("code_verifier", verifier)
		}
		if provider.TokenAuthMethod != "client_secret_basic" {
			form.Set("client_secret", secret)
		}
		req, err = http.NewRequest(http.MethodPost, provider.TokenURL, strings.NewReader(form.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")
			if provider.TokenAuthMethod == "client_secret_basic" {
				req.SetBasicAuth(provider.ClientID, secret)
			}
		}
	}
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	token := jsonPathString(payload, "access_token")
	if token == "" {
		return "", nil, fmt.Errorf("token response has no access token")
	}
	return token, payload, nil
}

func (h *AuthHandler) fetchOAuthProfile(name string, provider service.OAuthProviderSettings, accessToken string, tokenPayload map[string]any) (map[string]any, error) {
	target := provider.UserInfoURL
	if name == "wechat" {
		q := url.Values{"access_token": {accessToken}, "openid": {jsonPathString(tokenPayload, "openid")}, "lang": {"zh_CN"}}
		target += "?" + q.Encode()
	}
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if name == "dingtalk" {
		req.Header.Set("x-acs-dingtalk-access-token", accessToken)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var profile map[string]any
	if json.Unmarshal(body, &profile) != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo returned %d", resp.StatusCode)
	}
	if name == "github" && jsonPathString(profile, "email") == "" {
		emailReq, _ := http.NewRequest(http.MethodGet, "https://api.github.com/user/emails", nil)
		emailReq.Header.Set("Authorization", "Bearer "+accessToken)
		emailReq.Header.Set("Accept", "application/vnd.github+json")
		if emailResp, emailErr := (&http.Client{Timeout: 15 * time.Second}).Do(emailReq); emailErr == nil {
			defer emailResp.Body.Close()
			var emails []struct {
				Email    string `json:"email"`
				Primary  bool   `json:"primary"`
				Verified bool   `json:"verified"`
			}
			if emailResp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(emailResp.Body, 128<<10)).Decode(&emails) == nil {
				for _, item := range emails {
					if item.Primary && item.Verified {
						profile["email"] = item.Email
						break
					}
				}
			}
		}
	}
	return profile, nil
}

func jsonPathString(root map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var current any = root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[part]
	}
	switch value := current.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		return ""
	}
}

func (h *AuthHandler) findOrCreateOAuthUser(settings service.SystemSettings, provider, subject, email, termsRevision string) (model.User, error) {
	var user model.User
	err := h.db.Transaction(func(tx *gorm.DB) error {
		createdUser := false
		var identity model.UserOAuthIdentity
		if err := tx.Where("provider = ? AND subject = ?", provider, subject).First(&identity).Error; err == nil {
			return tx.First(&user, identity.UserID).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if email != "" {
			_ = tx.Where("email = ?", email).First(&user).Error
		}
		if user.ID == 0 {
			if !settings.AllowRegister || settings.SiteCustomization.BackendModeEnabled {
				return fmt.Errorf("registration is disabled")
			}
			if email == "" {
				return fmt.Errorf("OAuth provider did not return an email")
			}
			randomPassword, _ := randomOAuthValue(32)
			hash, err := util.HashPassword(randomPassword)
			if err != nil {
				return err
			}
			now := time.Now()
			balance, concurrency, rpm := settings.UserDefaults.BalanceMicro, settings.UserDefaults.Concurrency, settings.UserDefaults.RPMLimit
			quotas, subscriptions := settings.UserDefaults.PlatformQuotas, settings.UserDefaults.DefaultSubscriptions
			if source, ok := settings.UserDefaults.AuthSourceDefaults[provider]; ok && source.Enabled && source.GrantOnSignup {
				balance, concurrency, rpm = source.BalanceMicro, source.Concurrency, source.RPMLimit
				if len(source.PlatformQuotas) > 0 {
					quotas = source.PlatformQuotas
				}
				if len(source.DefaultSubscriptions) > 0 {
					subscriptions = source.DefaultSubscriptions
				}
			}
			user = model.User{Email: email, EmailVerified: true, PasswordHash: hash, Role: model.RoleUser, Status: model.StatusActive, BalanceMicro: balance, Concurrency: concurrency, RPMLimit: int64(rpm), RateMultiplier: 1, TermsRevision: termsRevision, TermsAcceptedAt: &now}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			createdUser = true
			for platform, quota := range quotas {
				if quota.DailyMicro+quota.WeeklyMicro+quota.MonthlyMicro > 0 {
					if err := tx.Create(&model.UserPlatformQuota{UserID: user.ID, Platform: platform, DailyMicro: quota.DailyMicro, WeeklyMicro: quota.WeeklyMicro, MonthlyMicro: quota.MonthlyMicro}).Error; err != nil {
						return err
					}
				}
			}
			for _, sub := range subscriptions {
				if err := tx.Create(&model.UserGroupSubscription{UserID: user.ID, GroupID: sub.GroupID, ExpiresAt: now.AddDate(0, 0, sub.ValidityDays)}).Error; err != nil {
					return err
				}
			}
		}
		if !createdUser {
			if err := applyOAuthFirstBindDefaults(tx, settings, &user, provider, time.Now().UTC()); err != nil {
				return err
			}
		}
		return tx.Create(&model.UserOAuthIdentity{UserID: user.ID, Provider: provider, Subject: subject, Email: email}).Error
	})
	if err == nil && user.ID != 0 {
		err = h.db.First(&user, user.ID).Error
	}
	return user, err
}

func applyOAuthFirstBindDefaults(tx *gorm.DB, settings service.SystemSettings, user *model.User, provider string, now time.Time) error {
	policy, ok := settings.UserDefaults.AuthSourceDefaults[provider]
	if !ok || !policy.Enabled || !policy.GrantOnFirstBind || user == nil || user.ID == 0 {
		return nil
	}
	record := model.UserProviderDefaultGrant{UserID: user.ID, Provider: provider, Reason: "first_bind"}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if policy.BalanceMicro != 0 || policy.Concurrency != 0 || policy.RPMLimit != 0 {
		updates := map[string]any{}
		if policy.BalanceMicro != 0 {
			updates["balance_micro"] = gorm.Expr("balance_micro + ?", policy.BalanceMicro)
		}
		if policy.Concurrency != 0 {
			updates["concurrency"] = gorm.Expr("concurrency + ?", policy.Concurrency)
		}
		if policy.RPMLimit != 0 {
			updates["rpm_limit"] = gorm.Expr("rpm_limit + ?", policy.RPMLimit)
		}
		if err := tx.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	for platform, quota := range policy.PlatformQuotas {
		if quota.DailyMicro+quota.WeeklyMicro+quota.MonthlyMicro == 0 {
			continue
		}
		var existing model.UserPlatformQuota
		err := tx.Where("user_id = ? AND platform = ?", user.ID, platform).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&model.UserPlatformQuota{UserID: user.ID, Platform: platform, DailyMicro: quota.DailyMicro, WeeklyMicro: quota.WeeklyMicro, MonthlyMicro: quota.MonthlyMicro}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := tx.Model(&existing).Updates(map[string]any{
			"daily_micro": gorm.Expr("daily_micro + ?", quota.DailyMicro), "weekly_micro": gorm.Expr("weekly_micro + ?", quota.WeeklyMicro), "monthly_micro": gorm.Expr("monthly_micro + ?", quota.MonthlyMicro),
		}).Error; err != nil {
			return err
		}
	}
	for _, sub := range policy.DefaultSubscriptions {
		var existing model.UserGroupSubscription
		err := tx.Where("user_id = ? AND group_id = ?", user.ID, sub.GroupID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&model.UserGroupSubscription{UserID: user.ID, GroupID: sub.GroupID, ExpiresAt: now.AddDate(0, 0, sub.ValidityDays)}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			base := existing.ExpiresAt
			if base.Before(now) {
				base = now
			}
			if err := tx.Model(&existing).Update("expires_at", base.AddDate(0, 0, sub.ValidityDays)).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
