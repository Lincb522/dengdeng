package handler

import (
	"net/http"
	"net/mail"
	"strings"

	"dengdeng/internal/config"
	"dengdeng/internal/middleware"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SystemSettingsHandler exposes only non-secret runtime settings. SMTP and
// deployment credentials intentionally stay out of this API and are changed
// through the server environment instead.
type SystemSettingsHandler struct {
	settings *service.SystemSettingsService
	cfg      *config.Config
	audit    *service.AuditService
	engine   *gin.Engine
}

func (h *SystemSettingsHandler) SetAuditService(audit *service.AuditService) { h.audit = audit }
func (h *SystemSettingsHandler) SetEngine(engine *gin.Engine)                { h.engine = engine }

func NewSystemSettingsHandler(db *gorm.DB, cfg *config.Config) *SystemSettingsHandler {
	return &SystemSettingsHandler{settings: service.NewSystemSettingsService(db, cfg), cfg: cfg}
}

func (h *SystemSettingsHandler) Get(c *gin.Context) {
	view, err := h.settings.AdminView()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load system settings failed")
		return
	}
	util.OK(c, view)
}

func (h *SystemSettingsHandler) Update(c *gin.Context) {
	// Start from the persisted value so older cached frontends that do not yet
	// send a newly introduced field cannot accidentally reset it to a zero value.
	current, err := h.settings.Get()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load system settings failed")
		return
	}
	req := struct {
		service.SystemSettings
		Secrets service.SystemSecretUpdate `json:"secrets"`
	}{SystemSettings: current}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid system settings")
		return
	}
	if !current.Security.StepUpEnabled && req.Security.StepUpEnabled {
		actor := middleware.CurrentUser(c)
		claims := middleware.CurrentClaims(c)
		if actor == nil || !actor.TOTPEnabled || claims == nil || !claims.MFA {
			util.Fail(c, http.StatusBadRequest, "enable two-factor authentication before enabling step-up verification")
			return
		}
	}
	next, err := h.settings.UpdateAll(req.SystemSettings, req.Secrets)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if h.engine != nil {
		if err := h.engine.SetTrustedProxies(next.TrustedProxies); err != nil {
			util.Fail(c, http.StatusBadRequest, "apply trusted proxies failed")
			return
		}
		h.engine.RemoteIPHeaders = append([]string(nil), next.ForwardedClientIPHeaders...)
	}
	if h.audit != nil {
		_ = h.audit.Record(middleware.CurrentUser(c), "system_settings.updated", "system_settings", "site", "updated site, registration and agreement settings", c.ClientIP())
	}
	view, err := h.settings.AdminView()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "load updated system settings failed")
		return
	}
	util.OK(c, view)
}

func (h *SystemSettingsHandler) TestEmail(c *gin.Context) {
	var req struct {
		To string `json:"to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "test recipient is required")
		return
	}
	recipient := strings.TrimSpace(req.To)
	if parsed, err := mail.ParseAddress(recipient); err != nil || !strings.EqualFold(parsed.Address, recipient) {
		util.Fail(c, http.StatusBadRequest, "test recipient is invalid")
		return
	}
	mailer := service.NewRuntimeSMTPMailer(h.settings, h.cfg.SMTP, h.cfg.Site.PublicURL)
	if !mailer.Configured() {
		util.Fail(c, http.StatusServiceUnavailable, "SMTP is not completely configured")
		return
	}
	if err := mailer.SendOperationalAlert(recipient, "邮件连接测试", "SMTP 配置已验证，可以正常发送系统邮件。"); err != nil {
		util.Fail(c, http.StatusBadGateway, "send test email failed: "+err.Error())
		return
	}
	if h.audit != nil {
		_ = h.audit.Record(middleware.CurrentUser(c), "system_settings.email_test", "system_settings", "email", "sent SMTP test email", c.ClientIP())
	}
	util.OK(c, gin.H{"sent": true})
}
