package handler

import (
	"net/http"
	"strconv"

	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
)

// RefreshAccountQuota refreshes the normalized quota snapshot for any
// upstream platform. Provider failures are returned as snapshot state instead
// of discarding the previous useful windows, so the console can explain the
// problem while the background monitor keeps retrying.
func (h *AdminHandler) RefreshAccountQuota(c *gin.Context) {
	if h.quota == nil {
		util.Fail(c, http.StatusServiceUnavailable, "额度刷新服务暂不可用")
		return
	}
	accountID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
	if parseErr != nil || accountID <= 0 {
		util.Fail(c, http.StatusBadRequest, "账号无效")
		return
	}
	snapshot, err := h.quota.RefreshAccount(c.Request.Context(), accountID)
	if snapshot.UpstreamAccountID == 0 {
		if err != nil {
			util.Fail(c, http.StatusNotFound, "账号不存在")
			return
		}
		util.Fail(c, http.StatusBadRequest, "账号无效")
		return
	}
	util.OK(c, snapshot)
}
