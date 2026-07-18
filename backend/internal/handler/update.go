package handler

import (
	"errors"
	"net/http"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

type UpdateHandler struct {
	updates *service.UpdateService
	backups *service.BackupService
	audit   *service.AuditService
}

type updateActionResponse struct {
	Status *service.UpdateStatus `json:"status"`
	Backup *model.BackupRecord   `json:"backup,omitempty"`
}

func NewUpdateHandler(updates *service.UpdateService, backups *service.BackupService, audit *service.AuditService) *UpdateHandler {
	return &UpdateHandler{updates: updates, backups: backups, audit: audit}
}

func (h *UpdateHandler) Status(c *gin.Context) {
	status, err := h.updates.Status()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "读取更新状态失败")
		return
	}
	util.OK(c, status)
}

func (h *UpdateHandler) Check(c *gin.Context)    { h.request(c, "check", false) }
func (h *UpdateHandler) Apply(c *gin.Context)    { h.request(c, "apply", true) }
func (h *UpdateHandler) Rollback(c *gin.Context) { h.request(c, "rollback", true) }

func (h *UpdateHandler) request(c *gin.Context, action string, createBackup bool) {
	actor := middleware.CurrentUser(c)
	current, err := h.updates.Status()
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "读取更新状态失败")
		return
	}
	if !current.Enabled {
		util.Fail(c, http.StatusServiceUnavailable, "服务器仓库更新尚未启用")
		return
	}
	if current.Status == "queued" || current.Status == "running" {
		util.Fail(c, http.StatusConflict, "已有更新任务正在运行")
		return
	}
	var backup *model.BackupRecord
	if createBackup {
		record, err := h.backups.Create(actor.Email)
		if err != nil {
			util.Fail(c, http.StatusConflict, "更新前数据库快照创建失败，请先检查备份配置")
			return
		}
		backup = &record
	}
	status, err := h.updates.Request(c.Request.Context(), action, actor.Email)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUpdateDisabled):
			util.Fail(c, http.StatusServiceUnavailable, "服务器仓库更新尚未启用")
		case errors.Is(err, service.ErrUpdateBusy):
			util.Fail(c, http.StatusConflict, "已有更新任务正在运行")
		case errors.Is(err, service.ErrUpdateAction):
			util.Fail(c, http.StatusConflict, err.Error())
		default:
			util.Fail(c, http.StatusInternalServerError, "启动更新任务失败")
		}
		return
	}
	_ = h.audit.Record(actor, "system_update."+action, "system_update", status.TargetCommit, status.Message, c.ClientIP())
	util.OK(c, updateActionResponse{Status: &status, Backup: backup})
}
