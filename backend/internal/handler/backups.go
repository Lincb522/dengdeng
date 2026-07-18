package handler

import (
	"errors"
	"strconv"

	"dengdeng/internal/middleware"
	"dengdeng/internal/service"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type BackupHandler struct {
	backups *service.BackupService
	audit   *service.AuditService
}

func NewBackupHandler(backups *service.BackupService, audit *service.AuditService) *BackupHandler {
	return &BackupHandler{backups: backups, audit: audit}
}

func (h *BackupHandler) List(c *gin.Context) {
	items, err := h.backups.List(100)
	if err != nil {
		util.Fail(c, 500, "load backups failed")
		return
	}
	util.OK(c, items)
}

func (h *BackupHandler) Create(c *gin.Context) {
	actor := middleware.CurrentUser(c)
	record, err := h.backups.Create(actor.Email)
	if errors.Is(err, service.ErrBackupUnsupported) {
		util.Fail(c, 409, err.Error())
		return
	}
	if err != nil {
		util.Fail(c, 500, "create database backup failed")
		return
	}
	_ = h.audit.Record(actor, "backup.created", "backup", strconv.FormatInt(record.ID, 10), record.Filename, c.ClientIP())
	util.OK(c, record)
}

func (h *BackupHandler) Policy(c *gin.Context) {
	policy, err := h.backups.GetPolicy()
	if err != nil {
		util.Fail(c, 500, "load backup policy failed")
		return
	}
	util.OK(c, policy)
}

func (h *BackupHandler) UpdatePolicy(c *gin.Context) {
	var input service.BackupPolicy
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, 400, "invalid backup policy")
		return
	}
	policy, err := h.backups.UpdatePolicy(input)
	if err != nil {
		util.Fail(c, 500, "save backup policy failed")
		return
	}
	actor := middleware.CurrentUser(c)
	detail := "enabled=" + strconv.FormatBool(policy.Enabled) + ", interval_hours=" + strconv.Itoa(policy.IntervalHours) + ", retention_days=" + strconv.Itoa(policy.RetentionDays) + ", retention_count=" + strconv.Itoa(policy.RetentionCount)
	_ = h.audit.Record(actor, "backup.policy.updated", "backup_policy", "default", detail, c.ClientIP())
	util.OK(c, policy)
}

func (h *BackupHandler) Cleanup(c *gin.Context) {
	policy, err := h.backups.GetPolicy()
	if err != nil {
		util.Fail(c, 500, "load backup policy failed")
		return
	}
	deleted, err := h.backups.PruneAuto(policy)
	if err != nil {
		util.Fail(c, 500, "cleanup automatic backups failed")
		return
	}
	actor := middleware.CurrentUser(c)
	_ = h.audit.Record(actor, "backup.cleaned", "backup", "automatic", "deleted="+strconv.Itoa(deleted), c.ClientIP())
	util.OK(c, gin.H{"deleted": deleted})
}

func (h *BackupHandler) Download(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid backup id")
		return
	}
	record, path, err := h.backups.SnapshotPath(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		util.Fail(c, 404, "backup not found")
		return
	}
	if err != nil {
		util.Fail(c, 409, "backup is unavailable")
		return
	}
	c.Header("Content-Type", "application/vnd.sqlite3")
	c.Header("Content-Disposition", "attachment; filename="+record.Filename)
	c.File(path)
}

func (h *BackupHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, 400, "invalid backup id")
		return
	}
	if err := h.backups.Delete(id); errors.Is(err, gorm.ErrRecordNotFound) {
		util.Fail(c, 404, "backup not found")
		return
	} else if err != nil {
		util.Fail(c, 409, "delete backup failed")
		return
	}
	_ = h.audit.Record(middleware.CurrentUser(c), "backup.deleted", "backup", strconv.FormatInt(id, 10), "", c.ClientIP())
	util.OK(c, gin.H{"deleted": true})
}
