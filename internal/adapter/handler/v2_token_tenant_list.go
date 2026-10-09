package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// tenantTokenView is tokenView plus the owner and retention fields the
// tenant-wide listing needs. The raw key stays masked.
type tenantTokenView struct {
	tokenView
	UserId           int    `json:"user_id"`
	OwnerName        string `json:"owner_name"`
	ContentRetention string `json:"content_retention"`
}

// listTenantTokensV2 serves GET /tokens?scope=tenant (tenant admin only):
// every token of the caller's own tenant, filterable by user_id, project_id,
// status and keyword (name / employee_ref).
func listTenantTokensV2(c *gin.Context, tc *middleware.TenantContext, page, pageSize, offset int) {
	if !requireTenantAdmin(c, tc) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}
	f := repo.TenantTokenFilter{Keyword: c.Query("keyword")}
	f.UserId, _ = strconv.Atoi(c.Query("user_id"))
	f.ProjectId, _ = strconv.Atoi(c.Query("project_id"))
	f.Status, _ = strconv.Atoi(c.Query("status"))
	tokens, total, err := repo.ListTokensByTenant(tc.TenantID, f, offset, pageSize)
	if err != nil {
		common.SysError("Failed to list tenant tokens: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to retrieve tokens"})
		return
	}
	ids := make([]int, 0, len(tokens))
	for _, t := range tokens {
		ids = append(ids, t.UserId)
	}
	names, err := repo.UserNamesByID(tc.TenantID, ids)
	if err != nil {
		common.SysError("Failed to resolve token owners: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to retrieve tokens"})
		return
	}
	items := make([]tenantTokenView, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, tenantTokenView{
			tokenView: toTokenView(t), UserId: t.UserId, OwnerName: names[t.UserId],
			ContentRetention: t.LogRetention,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items": items, "total": total, "page": page, "page_size": pageSize,
	}})
}
