package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type proxyReq struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	ClearAuth bool   `json:"clear_auth"`
	Status    string `json:"status"`
}

type proxyView struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Protocol       string    `json:"protocol"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	Status         string    `json:"status"`
	AuthConfigured bool      `json:"auth_configured"`
	AccountCount   int64     `json:"account_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func proxyToView(db *gorm.DB, item model.Proxy) proxyView {
	var accountCount int64
	db.Model(&model.UpstreamAccount{}).Where("proxy_id = ?", item.ID).Count(&accountCount)
	return proxyView{
		ID: item.ID, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port, Status: item.Status,
		AuthConfigured: item.Username != "", AccountCount: accountCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func (h *AdminHandler) ListProxies(c *gin.Context) {
	var items []model.Proxy
	if err := h.db.Order("id DESC").Find(&items).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "list proxies failed")
		return
	}
	views := make([]proxyView, 0, len(items))
	for _, item := range items {
		views = append(views, proxyToView(h.db, item))
	}
	util.OK(c, views)
}

func normalizeProxyReq(req *proxyReq) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.Host = strings.TrimSpace(req.Host)
	if req.Name == "" || req.Protocol == "" || req.Host == "" || req.Port == 0 {
		return errInvalidProxy
	}
	if req.Status == "" {
		req.Status = model.StatusActive
	}
	if req.Status != model.StatusActive && req.Status != model.StatusDisabled {
		return errInvalidProxy
	}
	_, err := (model.Proxy{Protocol: req.Protocol, Host: req.Host, Port: req.Port}).URL()
	return err
}

var errInvalidProxy = &proxyValidationError{}

type proxyValidationError struct{}

func (*proxyValidationError) Error() string { return "name, protocol, host and port are required" }

func (h *AdminHandler) validateProxyAssignment(id int64) error {
	if id == 0 {
		return nil
	}
	var item model.Proxy
	if err := h.db.First(&item, id).Error; err != nil {
		return errors.New("selected proxy not found")
	}
	if item.Status != model.StatusActive {
		return errors.New("selected proxy is disabled")
	}
	_, err := item.URL()
	return err
}

func (h *AdminHandler) CreateProxy(c *gin.Context) {
	var req proxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid proxy")
		return
	}
	if err := normalizeProxyReq(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	item := model.Proxy{
		Name: req.Name, Protocol: req.Protocol, Host: req.Host, Port: req.Port, Status: req.Status,
		Username: crypto.EncryptedString(req.Username), Password: crypto.EncryptedString(req.Password),
	}
	if err := h.db.Create(&item).Error; err != nil {
		util.Fail(c, http.StatusBadRequest, "create proxy failed: name may already exist")
		return
	}
	util.OK(c, proxyToView(h.db, item))
}

func (h *AdminHandler) UpdateProxy(c *gin.Context) {
	var item model.Proxy
	if err := h.db.First(&item, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "proxy not found")
		return
	}
	var req proxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid proxy")
		return
	}
	if err := normalizeProxyReq(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	updates := map[string]any{
		"name": req.Name, "protocol": req.Protocol, "host": req.Host, "port": req.Port, "status": req.Status,
	}
	if req.ClearAuth {
		updates["username"] = crypto.EncryptedString("")
		updates["password"] = crypto.EncryptedString("")
	} else {
		if req.Username != "" {
			updates["username"] = crypto.EncryptedString(req.Username)
		}
		if req.Password != "" {
			updates["password"] = crypto.EncryptedString(req.Password)
		}
	}
	if err := h.db.Model(&item).Updates(updates).Error; err != nil {
		util.Fail(c, http.StatusBadRequest, "update proxy failed")
		return
	}
	if err := h.db.First(&item, item.ID).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "reload proxy failed")
		return
	}
	util.OK(c, proxyToView(h.db, item))
}

func (h *AdminHandler) DeleteProxy(c *gin.Context) {
	var item model.Proxy
	if err := h.db.First(&item, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "proxy not found")
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpstreamAccount{}).Where("proxy_id = ?", item.ID).Update("proxy_id", 0).Error; err != nil {
			return err
		}
		return tx.Delete(&item).Error
	}); err != nil {
		util.Fail(c, http.StatusInternalServerError, "delete proxy failed")
		return
	}
	util.OK(c, gin.H{"deleted": true})
}

// TestProxy validates the actual outbound route through a small public 204
// endpoint. It never reveals proxy credentials and has a hard timeout so a
// broken proxy cannot tie up an admin request.
func (h *AdminHandler) TestProxy(c *gin.Context) {
	var item model.Proxy
	if err := h.db.First(&item, c.Param("id")).Error; err != nil {
		util.Fail(c, http.StatusNotFound, "proxy not found")
		return
	}
	proxyURL, err := item.URL()
	if err != nil {
		util.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	client, err := config.NewProxyHTTPClient(proxyURL, "", 12*time.Second)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, "create proxy client failed")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.gstatic.com/generate_204", nil)
	if err != nil {
		util.Fail(c, http.StatusInternalServerError, "create test request failed")
		return
	}
	started := time.Now()
	resp, err := client.Do(request)
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "proxy connection failed: "+err.Error())
		return
	}
	resp.Body.Close()
	util.OK(c, gin.H{"ok": resp.StatusCode < 500, "status": resp.StatusCode, "latency_ms": time.Since(started).Milliseconds()})
}
