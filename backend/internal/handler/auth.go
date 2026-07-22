package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
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
	maxLoginFailures        = 5
	lockoutDuration         = 15 * time.Minute
	codeTTL                 = 10 * time.Minute
	codeCooldown            = time.Minute
	registrationCodePurpose = "register"
)

type loginAttempt struct {
	failures int
	until    time.Time
}

type AuthHandler struct {
	db       *gorm.DB
	cfg      *config.Config
	mailer   service.RegistrationMailer
	settings *service.SystemSettingsService

	mu       sync.Mutex
	attempts map[string]*loginAttempt // keyed by email
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

// locked reports whether the email is currently locked out.
func (h *AuthHandler) locked(email string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.attempts[email]
	return a != nil && a.failures >= maxLoginFailures && time.Now().Before(a.until)
}

func (h *AuthHandler) recordFailure(email string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.attempts[email]
	if a == nil || time.Now().After(a.until) {
		a = &loginAttempt{}
		h.attempts[email] = a
	}
	a.failures++
	a.until = time.Now().Add(lockoutDuration)
}

func (h *AuthHandler) clearFailures(email string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.attempts, email)
}

type credentials struct {
	Email         string `json:"email" binding:"required,email"`
	Password      string `json:"password" binding:"required,min=8,max=72"`
	TOTPCode      string `json:"totp_code"`
	TermsRevision string `json:"terms_revision"`
}

type registrationCredentials struct {
	Email string `json:"email" binding:"required,email"`
	// Code is enforced in Register only while SMTP is configured; without a
	// mailer there is no way to receive one, so registration falls back to
	// plain email+password instead of locking everyone out.
	Code          string `json:"code"`
	Password      string `json:"password" binding:"required,min=8,max=72"`
	TermsRevision string `json:"terms_revision"`
	ReferralCode  string `json:"referral_code"`
}

type emailAddress struct {
	Email string `json:"email" binding:"required,email"`
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
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
	if !settings.AllowRegister {
		util.Fail(c, http.StatusForbidden, "registration is disabled")
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
	email := normalizedEmail(req.Email)
	if !settings.AllowsRegistrationEmail(email) {
		util.Fail(c, http.StatusForbidden, "this email domain is not allowed to register")
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
		util.Fail(c, http.StatusTooManyRequests, "please wait before requesting another code")
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
	util.OK(c, gin.H{"expires_in": int(codeTTL.Seconds()), "resend_after": int(codeCooldown.Seconds())})
}

func (h *AuthHandler) Register(c *gin.Context) {
	settings, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load registration settings failed")
		return
	}
	if !settings.AllowRegister {
		util.Fail(c, http.StatusForbidden, "registration is disabled")
		return
	}
	var req registrationCredentials
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "email, verification code and password (>=8 chars) are required")
		return
	}
	email := normalizedEmail(req.Email)
	if !settings.AllowsRegistrationEmail(email) {
		util.Fail(c, http.StatusForbidden, "this email domain is not allowed to register")
		return
	}
	code := strings.TrimSpace(req.Code)
	if settings.LoginAgreement.Enabled && strings.TrimSpace(req.TermsRevision) != settings.LoginAgreement.Revision() {
		util.Fail(c, http.StatusForbidden, "latest terms must be accepted")
		return
	}
	// Email verification is only enforceable when a mailer can actually send
	// codes. Without SMTP the flow degrades to email+password so a fresh
	// deployment is still usable; the account is marked unverified.
	verifyEmail := h.mailer != nil && h.mailer.Configured()
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
		if referralText := normalizeReferralCode(req.ReferralCode); referralText != "" {
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
		user = model.User{
			Email: email, EmailVerified: verifyEmail, PasswordHash: hash,
			Role: model.RoleUser, Status: model.StatusActive,
			BalanceMicro: settings.InitBalanceMicro, RateMultiplier: 1,
			TermsRevision: settings.LoginAgreement.Revision(), TermsAcceptedAt: &acceptedAt,
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
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
	h.issueToken(c, &user)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	email := normalizedEmail(req.Email)

	if h.locked(email) {
		util.Fail(c, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}

	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		h.recordFailure(email)
		util.Fail(c, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if !util.CheckPassword(user.PasswordHash, req.Password) {
		h.recordFailure(email)
		util.Fail(c, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if user.Status != model.StatusActive {
		util.Fail(c, http.StatusForbidden, "account disabled")
		return
	}
	if user.TOTPEnabled && !util.ValidateTOTP(string(user.TOTPSecret), req.TOTPCode, time.Now()) {
		h.recordFailure(email)
		util.Fail(c, http.StatusUnauthorized, "authenticator code is required or invalid")
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
	h.clearFailures(email)
	h.issueToken(c, &user)
}

func (h *AuthHandler) issueToken(c *gin.Context, user *model.User) {
	token, err := util.SignJWTBound(
		h.cfg.JWT.Secret, user.ID, user.Role, user.TokenVersion,
		time.Duration(h.cfg.JWT.ExpireHour)*time.Hour,
		util.SessionFingerprint(h.cfg.JWT.Secret, c.Request.UserAgent()), user.TOTPEnabled,
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
	util.OK(c, gin.H{
		"site_name":      settings.SiteName,
		"site_subtitle":  settings.SiteSubtitle,
		"allow_register": settings.AllowRegister,
		// Verification is only demanded when the deployment can send codes.
		"registration_verification": h.mailer != nil && h.mailer.Configured(),
		"login_agreement": gin.H{
			"enabled":    settings.LoginAgreement.Enabled,
			"mode":       settings.LoginAgreement.Mode,
			"updated_at": settings.LoginAgreement.UpdatedAt,
			"revision":   settings.LoginAgreement.Revision(),
			"documents":  settings.LoginAgreement.Documents,
		},
	})
}
