package handler

import (
	"net/http"

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
	audit    *service.AuditService
	engine   *gin.Engine
}

func (h *SystemSettingsHandler) SetAuditService(audit *service.AuditService) { h.audit = audit }
func (h *SystemSettingsHandler) SetEngine(engine *gin.Engine)                { h.engine = engine }

func NewSystemSettingsHandler(db *gorm.DB, cfg *config.Config) *SystemSettingsHandler {
	return &SystemSettingsHandler{settings: service.NewSystemSettingsService(db, cfg)}
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
	var req service.SystemSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid system settings")
		return
	}
	next, err := h.settings.Update(req)
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
	util.OK(c, next)
}
