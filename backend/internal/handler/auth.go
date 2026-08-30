package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	maxLoginFailures         = 5
	lockoutDuration          = 15 * time.Minute
	maxTrackedLoginAttempts  = 20_000
	codeTTL                  = 10 * time.Minute
	codeCooldown             = time.Minute
	registrationCodePurpose  = "register"
	passwordResetCodePurpose = "password_reset"
	registrationActionCode   = "code_sent"
	registrationActionCreate = "registered"
	registrationClientCookie = "dd_registration_client"
	registrationClientMaxAge = 180 * 24 * time.Hour
)

type loginAttempt struct {
	failures int
	until    time.Time
	lastSeen time.Time
}

type AuthHandler struct {
	db       *gorm.DB
	cfg      *config.Config
	mailer   service.RegistrationMailer
	settings *service.SystemSettingsService

	mu sync.Mutex
	// Scope failures to both account and source. An email-only counter lets
	// anyone who knows an address lock the real owner out remotely.
	attempts map[string]*loginAttempt
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config) *AuthHandler {
	return NewAuthHandlerWithMailer(db, cfg, service.NewSMTPMailer(cfg.SMTP, cfg.Site.Name, cfg.Site.PublicURL))
}

func NewAuthHandlerWithMailer(db *gorm.DB, cfg *config.Config, mailer service.RegistrationMailer) *AuthHandler {
	return &AuthHandler{
		db: db, cfg: cfg, mailer: mailer, settings: service.NewSystemSettingsService(db, cfg),
		attempts: make(map[string]*loginAttempt),
	}
}

func loginAttemptKey(email, clientIP string) string {
	return normalizedEmail(email) + "\x00" + strings.TrimSpace(clientIP)
}

// lockoutRemaining reports the exact remaining lockout duration for one
// account/source pair.
func (h *AuthHandler) lockoutRemaining(key string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.attempts[key]
	if a == nil || a.failures < maxLoginFailures {
		return 0
	}
	remaining := time.Until(a.until)
	if remaining <= 0 {
		delete(h.attempts, key)
		return 0
	}
	return remaining
}

func (h *AuthHandler) recordFailure(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if len(h.attempts) >= maxTrackedLoginAttempts {
		var oldestKey string
		var oldestTime time.Time
		for candidate, attempt := range h.attempts {
			if now.After(attempt.until) {
				delete(h.attempts, candidate)
				continue
			}
			if oldestKey == "" || attempt.lastSeen.Before(oldestTime) {
				oldestKey, oldestTime = candidate, attempt.lastSeen
			}
		}
		if len(h.attempts) >= maxTrackedLoginAttempts && oldestKey != "" {
			delete(h.attempts, oldestKey)
		}
	}
	a := h.attempts[key]
	if a == nil || now.After(a.until) {
		a = &loginAttempt{}
		h.attempts[key] = a
	}
	a.failures++
	a.until = now.Add(lockoutDuration)
	a.lastSeen = now
}

func (h *AuthHandler) clearFailures(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.attempts, key)
}

type credentials struct {
	Email          string `json:"email" binding:"required,email"`
	Password       string `json:"password" binding:"required,min=8,max=72"`
	TOTPCode       string `json:"totp_code"`
	TermsRevision  string `json:"terms_revision"`
	TurnstileToken string `json:"turnstile_token"`
}

type registrationCredentials struct {
	Email string `json:"email" binding:"required,email"`
	// Code is enforced in Register only while SMTP is configured; without a
	// mailer there is no way to receive one, so registration falls back to
	// plain email+password instead of locking everyone out.
	Code           string `json:"code"`
	Password       string `json:"password" binding:"required,min=8,max=72"`
	TermsRevision  string `json:"terms_revision"`
	ReferralCode   string `json:"referral_code"`
	TurnstileToken string `json:"turnstile_token"`
}

type emailAddress struct {
	Email          string `json:"email" binding:"required,email"`
	TurnstileToken string `json:"turnstile_token"`
}

func (h *AuthHandler) verifyTurnstile(c *gin.Context, token string) bool {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load bot protection settings failed")
		return false
	}
	if !settings.Security.TurnstileEnabled {
		return true
	}
	secret, err := h.settings.Secret(service.SecretTurnstile)
	if err != nil || strings.TrimSpace(secret) == "" {
		util.Fail(c, http.StatusServiceUnavailable, "Turnstile is enabled but not completely configured")
		return false
	}
	if strings.TrimSpace(token) == "" {
		util.Fail(c, http.StatusBadRequest, "bot verification is required")
		return false
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
		"secret": {secret}, "response": {strings.TrimSpace(token)}, "remoteip": {c.ClientIP()},
	})
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "bot verification service is unavailable")
		return false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var result struct {
		Success bool `json:"success"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &result) != nil || !result.Success {
		util.Fail(c, http.StatusForbidden, "bot verification failed")
		return false
	}
	return true
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func registrationEmailDomain(email string) string {
	parts := strings.Split(normalizedEmail(email), "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func registrationSourceNetwork(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return strings.TrimSpace(raw)
	}
	if v4 := ip.To4(); v4 != nil {
		mask := net.CIDRMask(24, 32)
		return (&net.IPNet{IP: v4.Mask(mask), Mask: mask}).String()
	}
	mask := net.CIDRMask(64, 128)
	return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
}

func registrationClientFingerprint(c *gin.Context) string {
	clientID, err := c.Cookie(registrationClientCookie)
	decodedClientID, decodeErr := hex.DecodeString(clientID)
	if err != nil || decodeErr != nil || len(decodedClientID) != 32 {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err == nil {
			clientID = hex.EncodeToString(random)
			secure := c.Request.TLS != nil || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(registrationClientCookie, clientID, int(registrationClientMaxAge.Seconds()), "/", "", secure, true)
		} else {
			clientID = ""
		}
	}
	sum := sha256.Sum256([]byte(clientID + "\x00" + strings.TrimSpace(c.GetHeader("User-Agent")) + "\x00" + strings.TrimSpace(c.GetHeader("Accept-Language"))))
	return hex.EncodeToString(sum[:])
}

func registrationRiskEvent(c *gin.Context, email, action string) model.RegistrationRiskEvent {
	sourceIP := strings.TrimSpace(c.ClientIP())
	return model.RegistrationRiskEvent{
		SourceIP:          sourceIP,
		SourceNetwork:     registrationSourceNetwork(sourceIP),
		EmailDomain:       registrationEmailDomain(email),
		ClientFingerprint: registrationClientFingerprint(c),
		Action:            action,
	}
}

func (h *AuthHandler) registrationAutoBlockRemaining(event model.RegistrationRiskEvent) (time.Duration, error) {
	var longest time.Duration
	for _, source := range []struct{ kind, value string }{
		{kind: "ip", value: event.SourceIP},
		{kind: "fingerprint", value: event.ClientFingerprint},
	} {
		if source.value == "" {
			continue
		}
		var block model.RegistrationBlock
		err := h.db.Where("kind = ? AND value = ?", source.kind, source.value).First(&block).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if remaining := time.Until(block.ExpiresAt); remaining > longest {
			longest = remaining
		}
	}
	return longest, nil
}

func registrationBlockDuration(policy service.SecurityPolicySettings, strike int) time.Duration {
	minutes := policy.RegistrationAutoBlockMinutes
	maxMinutes := policy.RegistrationAutoBlockMaxMinutes
	if minutes <= 0 {
		minutes = 1440
	}
	if maxMinutes < minutes {
		maxMinutes = minutes
	}
	for i := 1; i < strike && minutes < maxMinutes; i++ {
		if minutes > maxMinutes/2 {
			minutes = maxMinutes
			break
		}
		minutes *= 2
	}
	if minutes > maxMinutes {
		minutes = maxMinutes
	}
	return time.Duration(minutes) * time.Minute
}

func (h *AuthHandler) autoBlockRegistrationSource(settings service.SystemSettings, event model.RegistrationRiskEvent, kind, value, reason string) (time.Duration, error) {
	if !settings.Security.RegistrationAutoBlockEnabled || value == "" {
		return 0, nil
	}
	now := time.Now()
	var duration time.Duration
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var block model.RegistrationBlock
		err := tx.Where("kind = ? AND value = ?", kind, value).First(&block).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			block = model.RegistrationBlock{Kind: kind, Value: value, StrikeCount: 1, CreatedAt: now}
		case err != nil:
			return err
		case block.ExpiresAt.After(now):
			duration = time.Until(block.ExpiresAt)
			return nil
		default:
			block.StrikeCount++
		}
		duration = registrationBlockDuration(settings.Security, block.StrikeCount)
		block.Reason = reason
		block.ExpiresAt = now.Add(duration)
		block.LastTriggeredAt = now
		if block.ID == 0 {
			if err := tx.Create(&block).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&block).Error; err != nil {
			return err
		}
		return tx.Create(&model.AuditLog{
			Action:     "auth.registration_auto_blocked",
			TargetType: "registration_" + kind,
			TargetID:   value,
			Detail:     fmt.Sprintf("%s; strike=%d; expires_at=%s", reason, block.StrikeCount, block.ExpiresAt.UTC().Format(time.RFC3339)),
			SourceIP:   event.SourceIP,
		}).Error
	})
	return duration, err
}

func (h *AuthHandler) registrationRiskRemaining(settings service.SystemSettings, event model.RegistrationRiskEvent) (time.Duration, error) {
	policy := settings.Security
	if !policy.RegistrationProtectionEnabled {
		return 0, nil
	}
	now := time.Now()
	type counter struct {
		field      string
		value      string
		since      time.Time
		limit      int
		window     time.Duration
		blockKind  string
		blockValue string
	}
	var checks []counter
	switch event.Action {
	case registrationActionCode:
		checks = []counter{
			{field: "source_ip", value: event.SourceIP, since: now.Add(-time.Hour), limit: policy.RegistrationCodeIPHourLimit, window: time.Hour, blockKind: "ip", blockValue: event.SourceIP},
			{field: "source_network", value: event.SourceNetwork, since: now.Add(-24 * time.Hour), limit: policy.RegistrationSubnetDayLimit, window: 24 * time.Hour, blockKind: "ip", blockValue: event.SourceIP},
			{field: "email_domain", value: event.EmailDomain, since: now.Add(-time.Hour), limit: policy.RegistrationDomainHourLimit, window: time.Hour, blockKind: "ip", blockValue: event.SourceIP},
		}
	case registrationActionCreate:
		checks = []counter{
			{field: "source_ip", value: event.SourceIP, since: now.Add(-24 * time.Hour), limit: policy.RegistrationIPDayLimit, window: 24 * time.Hour, blockKind: "ip", blockValue: event.SourceIP},
			{field: "source_network", value: event.SourceNetwork, since: now.Add(-24 * time.Hour), limit: policy.RegistrationSubnetDayLimit, window: 24 * time.Hour, blockKind: "ip", blockValue: event.SourceIP},
			{field: "email_domain", value: event.EmailDomain, since: now.Add(-time.Hour), limit: policy.RegistrationDomainHourLimit, window: time.Hour, blockKind: "ip", blockValue: event.SourceIP},
			{field: "client_fingerprint", value: event.ClientFingerprint, since: now.Add(-24 * time.Hour), limit: policy.RegistrationFingerprintDayLimit, window: 24 * time.Hour, blockKind: "fingerprint", blockValue: event.ClientFingerprint},
		}
	default:
		return 0, errors.New("unknown registration risk action")
	}
	for _, check := range checks {
		if check.limit <= 0 || check.value == "" {
			continue
		}
		var count int64
		if err := h.db.Model(&model.RegistrationRiskEvent{}).
			Where("action = ? AND "+check.field+" = ? AND created_at >= ?", event.Action, check.value, check.since).
			Count(&count).Error; err != nil {
			return 0, err
		}
		if count >= int64(check.limit) {
			if policy.RegistrationAutoBlockEnabled {
				reason := fmt.Sprintf("%s %s limit reached (%d/%d)", event.Action, check.field, count, check.limit)
				return h.autoBlockRegistrationSource(settings, event, check.blockKind, check.blockValue, reason)
			}
			return check.window, nil
		}
	}
	return 0, nil
}

func (h *AuthHandler) verificationHash(email, purpose, code string) string {
	mac := hmac.New(sha256.New, []byte(h.cfg.JWT.Secret))
	mac.Write([]byte(normalizedEmail(email)))
	mac.Write([]byte{0})
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

func newVerificationCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// SendRegistrationCode sends a short-lived, single-use code. Per-email
// cooldown limits mailbox abuse while the router's IP rate limit guards the
// endpoint globally.
func (h *AuthHandler) SendRegistrationCode(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load registration settings failed")
		return
	}
	if !settings.AllowRegister || settings.SiteCustomization.BackendModeEnabled {
		util.Fail(c, http.StatusForbidden, "registration is disabled")
		return
	}
	if !settings.Security.EmailVerificationEnabled {
		util.Fail(c, http.StatusForbidden, "registration email verification is disabled")
		return
	}
	if h.mailer == nil || !h.mailer.Configured() {
		util.Fail(c, http.StatusServiceUnavailable, "email verification is not configured")
		return
	}
	var req emailAddress
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "a valid email is required")
		return
	}
	if !settings.AllowsRegistrationIP(c.ClientIP()) {
		util.Fail(c, http.StatusForbidden, "registration is not available from this network")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	email := normalizedEmail(req.Email)
	if !settings.AllowsRegistrationEmail(email) {
		util.Fail(c, http.StatusForbidden, "this email domain is not allowed to register")
		return
	}
	riskEvent := registrationRiskEvent(c, email, registrationActionCode)
	if retryAfter, err := h.registrationAutoBlockRemaining(riskEvent); err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "registration protection is temporarily unavailable")
		return
	} else if retryAfter > 0 {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.registration_temporarily_blocked", "registration is temporarily blocked for this network", retryAfter)
		return
	}
	if retryAfter, err := h.registrationRiskRemaining(settings, riskEvent); err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "registration protection is temporarily unavailable")
		return
	} else if retryAfter > 0 {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.registration_risk_limited", "registration limit reached for this network or email domain", retryAfter)
		return
	}

	var count int64
	h.db.Model(&model.User{}).Where("email = ?", email).Count(&count)
	if count > 0 {
		util.Fail(c, http.StatusConflict, "email already registered")
		return
	}

	now := time.Now()
	var latest model.EmailVerification
	if err := h.db.Where("email = ? AND purpose = ?", email, registrationCodePurpose).
		Order("id DESC").First(&latest).Error; err == nil && now.Sub(latest.CreatedAt) < codeCooldown {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.code_rate_limited", "please wait before requesting another code", codeCooldown-now.Sub(latest.CreatedAt))
		return
	}
	code, err := newVerificationCode()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "generate verification code failed")
		return
	}
	record := model.EmailVerification{
		Email: email, Purpose: registrationCodePurpose,
		CodeHash:  h.verificationHash(email, registrationCodePurpose, code),
		ExpiresAt: now.Add(codeTTL),
	}
	if err := h.db.Create(&record).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "save verification code failed")
		return
	}
	if err := h.mailer.SendRegistrationCode(email, code); err != nil {
		h.db.Delete(&model.EmailVerification{}, record.ID)
		util.Fail(c, http.StatusBadGateway, "send verification email failed")
		return
	}
	if err := h.db.Create(&riskEvent).Error; err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "registration protection is temporarily unavailable")
		return
	}
	util.OK(c, gin.H{"expires_in": int(codeTTL.Seconds()), "resend_after": int(codeCooldown.Seconds())})
}

func (h *AuthHandler) SendPasswordResetCode(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load password reset settings failed")
		return
	}
	if !settings.Security.PasswordResetEnabled {
		util.Fail(c, http.StatusForbidden, "password reset is disabled")
		return
	}
	if h.mailer == nil || !h.mailer.Configured() {
		util.Fail(c, http.StatusServiceUnavailable, "email service is not configured")
		return
	}
	var req emailAddress
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "a valid email is required")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	email := normalizedEmail(req.Email)
	var user model.User
	if err := h.db.Where("email = ? AND status = ?", email, model.StatusActive).First(&user).Error; err != nil {
		util.OK(c, gin.H{"accepted": true, "resend_after": int(codeCooldown.Seconds())})
		return
	}
	now := time.Now()
	var latest model.EmailVerification
	if err := h.db.Where("email = ? AND purpose = ?", email, passwordResetCodePurpose).Order("id DESC").First(&latest).Error; err == nil && now.Sub(latest.CreatedAt) < codeCooldown {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.code_rate_limited", "please wait before requesting another code", codeCooldown-now.Sub(latest.CreatedAt))
		return
	}
	code, err := newVerificationCode()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "generate verification code failed")
		return
	}
	record := model.EmailVerification{Email: email, Purpose: passwordResetCodePurpose, CodeHash: h.verificationHash(email, passwordResetCodePurpose, code), ExpiresAt: now.Add(codeTTL)}
	if err := h.db.Create(&record).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "save verification code failed")
		return
	}
	var sendErr error
	if resetMailer, ok := h.mailer.(service.PasswordResetMailer); ok {
		sendErr = resetMailer.SendPasswordResetCode(email, code)
	} else {
		sendErr = h.mailer.SendRegistrationCode(email, code)
	}
	if sendErr != nil {
		h.db.Delete(&model.EmailVerification{}, record.ID)
		util.Fail(c, http.StatusBadGateway, "send password reset email failed")
		return
	}
	util.OK(c, gin.H{"accepted": true, "expires_in": int(codeTTL.Seconds()), "resend_after": int(codeCooldown.Seconds())})
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load password reset settings failed")
		return
	}
	if !settings.Security.PasswordResetEnabled {
		util.Fail(c, http.StatusForbidden, "password reset is disabled")
		return
	}
	var req struct {
		Email          string `json:"email" binding:"required,email"`
		Code           string `json:"code" binding:"required,len=6"`
		Password       string `json:"password" binding:"required,min=8,max=72"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "email, 6-digit code and new password are required")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	email := normalizedEmail(req.Email)
	hash, err := util.HashPassword(req.Password)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "hash password failed")
		return
	}
	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var verification model.EmailVerification
		if err := tx.Where("email = ? AND purpose = ? AND code_hash = ? AND used_at IS NULL AND expires_at > ?", email, passwordResetCodePurpose, h.verificationHash(email, passwordResetCodePurpose, strings.TrimSpace(req.Code)), now).Order("id DESC").First(&verification).Error; err != nil {
			return err
		}
		result := tx.Model(&model.User{}).Where("email = ? AND status = ?", email, model.StatusActive).Updates(map[string]any{"password_hash": hash, "token_version": gorm.Expr("token_version + 1")})
		if result.Error != nil || result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&model.EmailVerification{}).Where("id = ? AND used_at IS NULL", verification.ID).Update("used_at", now).Error
	})
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid or expired password reset code")
		return
	}
	util.OK(c, gin.H{"reset": true})
}

func (h *AuthHandler) Register(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load registration settings failed")
		return
	}
	if !settings.AllowRegister || settings.SiteCustomization.BackendModeEnabled {
		util.Fail(c, http.StatusForbidden, "registration is disabled")
		return
	}
	var req registrationCredentials
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "email, verification code and password (>=8 chars) are required")
		return
	}
	if !settings.AllowsRegistrationIP(c.ClientIP()) {
		util.Fail(c, http.StatusForbidden, "registration is not available from this network")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	email := normalizedEmail(req.Email)
	if !settings.AllowsRegistrationEmail(email) {
		util.Fail(c, http.StatusForbidden, "this email domain is not allowed to register")
		return
	}
	riskEvent := registrationRiskEvent(c, email, registrationActionCreate)
	if retryAfter, err := h.registrationAutoBlockRemaining(riskEvent); err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "registration protection is temporarily unavailable")
		return
	} else if retryAfter > 0 {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.registration_temporarily_blocked", "registration is temporarily blocked for this network", retryAfter)
		return
	}
	if retryAfter, err := h.registrationRiskRemaining(settings, riskEvent); err != nil {
		util.Fail(c, http.StatusServiceUnavailable, "registration protection is temporarily unavailable")
		return
	} else if retryAfter > 0 {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.registration_risk_limited", "registration limit reached for this network or email domain", retryAfter)
		return
	}
	code := strings.TrimSpace(req.Code)
	if settings.LoginAgreement.Enabled && strings.TrimSpace(req.TermsRevision) != settings.LoginAgreement.Revision() {
		util.Fail(c, http.StatusForbidden, "latest terms must be accepted")
		return
	}
	if settings.Security.EmailVerificationEnabled && (h.mailer == nil || !h.mailer.Configured()) {
		util.Fail(c, http.StatusServiceUnavailable, "email verification is enabled but SMTP is not configured")
		return
	}
	verifyEmail := settings.Security.EmailVerificationEnabled && h.mailer != nil && h.mailer.Configured()
	if verifyEmail && len(code) != 6 {
		util.Fail(c, http.StatusBadRequest, "verification code must be 6 digits")
		return
	}

	hash, err := util.HashPassword(req.Password)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "hash password failed")
		return
	}
	user := model.User{}
	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var verificationID int64
		if verifyEmail {
			var verification model.EmailVerification
			if err := tx.Where("email = ? AND purpose = ? AND code_hash = ? AND used_at IS NULL AND expires_at > ?", email, registrationCodePurpose, h.verificationHash(email, registrationCodePurpose, code), now).
				Order("id DESC").First(&verification).Error; err != nil {
				return err
			}
			verificationID = verification.ID
		}
		var count int64
		if err := tx.Model(&model.User{}).Where("email = ?", email).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("email already registered")
		}
		var referral *model.ReferralCode
		if referralText := normalizeReferralCode(req.ReferralCode); settings.Features.ReferralEnabled && referralText != "" {
			var item model.ReferralCode
			if err := tx.Where("code = ? AND status = ?", referralText, model.StatusActive).First(&item).Error; err != nil {
				return fmt.Errorf("invalid referral code")
			}
			var owner model.User
			if err := tx.First(&owner, item.OwnerUserID).Error; err != nil || owner.Status != model.StatusActive {
				return fmt.Errorf("invalid referral code")
			}
			referral = &item
		}
		acceptedAt := now
		balance, concurrency, rpm := settings.UserDefaults.BalanceMicro, settings.UserDefaults.Concurrency, settings.UserDefaults.RPMLimit
		quotas, subscriptions := settings.UserDefaults.PlatformQuotas, settings.UserDefaults.DefaultSubscriptions
		if source, ok := settings.UserDefaults.AuthSourceDefaults["email"]; ok && source.Enabled && source.GrantOnSignup {
			balance, concurrency, rpm = source.BalanceMicro, source.Concurrency, source.RPMLimit
			if len(source.PlatformQuotas) > 0 {
				quotas = source.PlatformQuotas
			}
			if len(source.DefaultSubscriptions) > 0 {
				subscriptions = source.DefaultSubscriptions
			}
		}
		if balance > 0 && settings.Security.RegistrationProtectionEnabled && settings.Security.RegistrationGrantOncePerIPDays > 0 && riskEvent.SourceIP != "" {
			var previousGrants int64
			if err := tx.Model(&model.RegistrationRiskEvent{}).
				Where("action = ? AND source_ip = ? AND granted_balance_micro > 0 AND created_at >= ?", registrationActionCreate, riskEvent.SourceIP, now.AddDate(0, 0, -settings.Security.RegistrationGrantOncePerIPDays)).
				Count(&previousGrants).Error; err != nil {
				return err
			}
			if previousGrants > 0 {
				balance = 0
			}
		}
		if balance > 0 && settings.Security.RegistrationProtectionEnabled && settings.Security.RegistrationGrantOncePerFingerprintDays > 0 && riskEvent.ClientFingerprint != "" {
			var previousGrants int64
			if err := tx.Model(&model.RegistrationRiskEvent{}).
				Where("action = ? AND client_fingerprint = ? AND granted_balance_micro > 0 AND created_at >= ?", registrationActionCreate, riskEvent.ClientFingerprint, now.AddDate(0, 0, -settings.Security.RegistrationGrantOncePerFingerprintDays)).
				Count(&previousGrants).Error; err != nil {
				return err
			}
			if previousGrants > 0 {
				balance = 0
			}
		}
		user = model.User{
			Email: email, EmailVerified: verifyEmail, PasswordHash: hash,
			Role: model.RoleUser, Status: model.StatusActive,
			BalanceMicro: balance, RateMultiplier: 1,
			Concurrency: concurrency, RPMLimit: int64(rpm),
			TermsRevision: settings.LoginAgreement.Revision(), TermsAcceptedAt: &acceptedAt,
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		for platform, quota := range quotas {
			if quota.DailyMicro == 0 && quota.WeeklyMicro == 0 && quota.MonthlyMicro == 0 {
				continue
			}
			item := model.UserPlatformQuota{
				UserID: user.ID, Platform: strings.ToLower(strings.TrimSpace(platform)),
				DailyMicro: quota.DailyMicro, WeeklyMicro: quota.WeeklyMicro, MonthlyMicro: quota.MonthlyMicro,
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		for _, subscription := range subscriptions {
			var count int64
			if err := tx.Model(&model.Group{}).Where("id = ? AND status = ?", subscription.GroupID, model.StatusActive).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("default subscription group %d is unavailable", subscription.GroupID)
			}
			item := model.UserGroupSubscription{
				UserID: user.ID, GroupID: subscription.GroupID,
				ExpiresAt: now.AddDate(0, 0, subscription.ValidityDays),
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		if referral != nil {
			binding := model.ReferralBinding{
				ReferralCodeID: referral.ID, ReferrerUserID: referral.OwnerUserID, ReferredUserID: user.ID,
			}
			if err := tx.Create(&binding).Error; err != nil {
				return err
			}
		}
		if verifyEmail {
			res := tx.Model(&model.EmailVerification{}).Where("id = ? AND used_at IS NULL", verificationID).Update("used_at", now)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		riskEvent.GrantedBalanceMicro = balance
		if err := tx.Create(&riskEvent).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			util.Fail(c, http.StatusBadRequest, "invalid or expired verification code")
			return
		}
		if err.Error() == "email already registered" {
			util.Fail(c, http.StatusConflict, "email already registered")
			return
		}
		if err.Error() == "invalid referral code" {
			util.Fail(c, http.StatusBadRequest, "invalid referral code")
			return
		}
		util.Fail(c, http.StatusInternalServerError, "create user failed")
		return
	}
	_ = h.db.Create(&model.AuditLog{
		ActorUserID: user.ID,
		ActorEmail:  user.Email,
		Action:      "auth.registered",
		TargetType:  "user",
		TargetID:    fmt.Sprintf("%d", user.ID),
		Detail:      "email registration completed",
		SourceIP:    c.ClientIP(),
	}).Error
	h.issueToken(c, &user)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	if !h.verifyTurnstile(c, req.TurnstileToken) {
		return
	}
	email := normalizedEmail(req.Email)
	attemptKey := loginAttemptKey(email, c.ClientIP())

	if remaining := h.lockoutRemaining(attemptKey); remaining > 0 {
		util.FailRetry(c, http.StatusTooManyRequests, "auth.too_many_attempts", "too many failed attempts, try again later", remaining)
		return
	}

	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		h.recordFailure(attemptKey)
		util.FailCode(c, http.StatusUnauthorized, "auth.invalid_credentials", "incorrect email or password")
		return
	}
	if !util.CheckPassword(user.PasswordHash, req.Password) {
		h.recordFailure(attemptKey)
		util.FailCode(c, http.StatusUnauthorized, "auth.invalid_credentials", "incorrect email or password")
		return
	}
	if user.Status != model.StatusActive {
		util.FailCode(c, http.StatusForbidden, "auth.account_disabled", "account disabled")
		return
	}
	if user.TOTPEnabled && !util.ValidateTOTP(string(user.TOTPSecret), req.TOTPCode, time.Now()) {
		h.recordFailure(attemptKey)
		util.FailCode(c, http.StatusUnauthorized, "auth.totp_invalid", "authenticator code is required or invalid")
		return
	}
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load login settings failed")
		return
	}
	if settings.LoginAgreement.Enabled {
		revision := settings.LoginAgreement.Revision()
		if strings.TrimSpace(req.TermsRevision) != revision {
			util.Fail(c, http.StatusForbidden, "latest terms must be accepted")
			return
		}
		if user.TermsRevision != revision {
			now := time.Now()
			if err := h.db.Model(&user).Updates(map[string]any{"terms_revision": revision, "terms_accepted_at": now}).Error; err != nil {
				util.Fail(c, http.StatusInternalServerError, "record terms acceptance failed")
				return
			}
			user.TermsRevision, user.TermsAcceptedAt = revision, &now
		}
	}
	h.clearFailures(attemptKey)
	h.issueToken(c, &user)
}

func (h *AuthHandler) issueToken(c *gin.Context, user *model.User) {
	fingerprint := ""
	if settings, err := h.settings.Get(); err == nil && settings.Security.SessionBindingEnabled {
		fingerprint = util.SessionFingerprint(h.cfg.JWT.Secret, c.ClientIP()+"\x00"+c.Request.UserAgent())
	}
	token, err := util.SignJWTBound(
		h.cfg.JWT.Secret, user.ID, user.Role, user.TokenVersion,
		time.Duration(h.cfg.JWT.ExpireHour)*time.Hour,
		fingerprint, user.TOTPEnabled,
	)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "sign token failed")
		return
	}
	util.OK(c, gin.H{"token": token, "user": user})
}

// PublicSettings exposes branding info to the login page before auth.
func (h *AuthHandler) PublicSettings(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load public settings failed")
		return
	}
	oauthProviders := make([]gin.H, 0, 6)
	for _, item := range []struct {
		ID     string
		Config service.OAuthProviderSettings
	}{
		{"linuxdo", settings.AuthProviders.LinuxDO}, {"dingtalk", settings.AuthProviders.DingTalk}, {"wechat", settings.AuthProviders.WeChat},
		{"oidc", settings.AuthProviders.OIDC}, {"github", settings.AuthProviders.GitHub}, {"google", settings.AuthProviders.Google},
	} {
		if item.Config.Enabled {
			oauthProviders = append(oauthProviders, gin.H{"id": item.ID, "name": item.Config.ProviderName})
		}
	}
	util.OK(c, gin.H{
		"site_name":               settings.SiteName,
		"site_subtitle":           settings.SiteSubtitle,
		"allow_register":          settings.AllowRegister && !settings.SiteCustomization.BackendModeEnabled,
		"key_multi_group_enabled": settings.KeyMultiGroupEnabled,
		// Verification is only demanded when the deployment can send codes.
		"registration_verification": settings.Security.EmailVerificationEnabled && h.mailer != nil && h.mailer.Configured(),
		"oauth_providers":           oauthProviders,
		"site_customization": gin.H{
			"logo_url":                settings.SiteCustomization.LogoURL,
			"contact_info":            settings.SiteCustomization.ContactInfo,
			"docs_url":                settings.SiteCustomization.DocsURL,
			"home_content":            settings.SiteCustomization.HomeContent,
			"backend_mode_enabled":    settings.SiteCustomization.BackendModeEnabled,
			"hide_ccs_import_button":  settings.SiteCustomization.HideCCSImportButton,
			"table_default_page_size": settings.SiteCustomization.TableDefaultPageSize,
			"table_page_size_options": settings.SiteCustomization.TablePageSizeOptions,
			"custom_menu_items":       settings.SiteCustomization.CustomMenuItems,
			"custom_endpoints":        settings.SiteCustomization.CustomEndpoints,
		},
		"features": gin.H{
			"model_plaza_enabled":            settings.Features.ModelPlazaEnabled,
			"referral_enabled":               settings.Features.ReferralEnabled,
			"allow_user_view_error_requests": settings.Features.AllowUserViewErrorRequests,
		},
		"security": gin.H{
			"password_reset_enabled": settings.Security.PasswordResetEnabled,
			"totp_enabled":           settings.Security.TOTPEnabled,
			"turnstile_enabled":      settings.Security.TurnstileEnabled,
			"turnstile_site_key":     settings.Security.TurnstileSiteKey,
		},
		"login_agreement": gin.H{
			"enabled":    settings.LoginAgreement.Enabled,
			"mode":       settings.LoginAgreement.Mode,
			"updated_at": settings.LoginAgreement.UpdatedAt,
			"revision":   settings.LoginAgreement.Revision(),
			"documents":  settings.LoginAgreement.Documents,
		},
	})
}
