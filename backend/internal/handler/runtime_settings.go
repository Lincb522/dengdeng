package handler

import (
	"net/http"
	"strconv"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RuntimeSettingsHandler owns policies that affect the live relay and health
// monitor. It purposefully does not expose environment secrets or upstream
// identity-masking knobs in the browser.
type RuntimeSettingsHandler struct {
	db     *gorm.DB
	policy *service.RuntimePolicyService
	audit  *service.AuditService
}

func NewRuntimeSettingsHandler(db *gorm.DB, policy *service.RuntimePolicyService, audit *service.AuditService) *RuntimeSettingsHandler {
	return &RuntimeSettingsHandler{db: db, policy: policy, audit: audit}
}

func (h *RuntimeSettingsHandler) Get(c *gin.Context) { util.OK(c, h.policy.Current()) }

func (h *RuntimeSettingsHandler) Update(c *gin.Context) {
	var req service.GatewayRuntimePolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid runtime policy")
		return
	}
	updated, err := h.policy.Update(req)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "runtime_policy.updated", "runtime_policy", "gateway", "updated gateway retries, cooldowns and health probes", c.ClientIP())
	util.OK(c, updated)
}

func (h *RuntimeSettingsHandler) ListAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	var items []model.AuditLog
	if err := h.db.Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load audit log failed")
		return
	}
	util.OK(c, gin.H{"items": items, "limit": limit})
}
