package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================================
// V2 Switch Usage Reconciliation
//
// Switch's desktop client records every gateway request locally. The Hub
// independently logs the same traffic under the gateway's user_token. This
// endpoint returns the Hub-side consume-log totals for a window so Switch can
// detect aggregate drift between the two ledgers.
//
// Auth mirrors UserHeartbeat: a raw user_token (Token.Key) in the Authorization
// header, validated inline. We do NOT use middleware.UserAuth — that resolves
// access_tokens (User.AccessToken), not per-token API keys, and would reject
// every call. The token also scopes the aggregation: we only ever sum the
// owning user's own logs, so a token can't read another user's usage.
//
// Read-only on the Hub side; no DB migration.
// ============================================================================

// reconcileWindow is the inclusive Unix-second window Switch asks to aggregate.
type reconcileWindow struct {
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
}

// reconcileModelAgg is the GORM scan target for the per-model rollup.
type reconcileModelAgg struct {
	ModelName        string `gorm:"column:model_name"`
	TotalQuota       int64  `gorm:"column:total_quota"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
	RequestCount     int64  `gorm:"column:request_count"`
}

// SwitchReconciliation handles POST /api/v2/switch/reconciliation.
func SwitchReconciliation(c *gin.Context) {
	// 1. Extract + normalize the raw user_token (same rules as heartbeat so a
	//    token that authenticates the heartbeat authenticates this too).
	key := normalizeSwitchToken(c.Request.Header.Get("Authorization"))
	if key == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "missing or empty Authorization token",
		})
		return
	}

	// 2. Parse the window. A malformed body is rejected; an absent window
	//    aggregates over all of the user's consume history.
	var win reconcileWindow
	if err := c.ShouldBindJSON(&win); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "malformed request body: " + err.Error(),
		})
		return
	}

	// 3. Resolve token → user. Distinguish "not found" (revoked, 401) from a
	//    transient DB error (500).
	token, err := repo.GetTokenByKey(key, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "token not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "token lookup failed"})
		return
	}
	if token == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "token not found"})
		return
	}

	// 4. Tenant cross-check only when a tenant-scoped route is used. The public
	//    /switch/reconciliation route has no :tenant_slug; this keeps the
	//    handler reusable if a tenant-scoped sibling is ever registered.
	if tenantSlug := c.Param("tenant_slug"); tenantSlug != "" {
		tenant, terr := repo.GetTenantBySlug(tenantSlug)
		if terr != nil || tenant == nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "tenant not found"})
			return
		}
		if token.TenantId != tenant.Id {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "token does not belong to this tenant"})
			return
		}
	}

	// 4b. Owning-tenant gate (cycle-13 L9). Outside the slug branch above, so
	//     the public /switch/reconciliation route is covered too: the figures
	//     this returns are the tenant's own billing-relevant totals, and a
	//     tenant an operator suspended should stop answering them on the same
	//     request that stops answering the heartbeat. repo.TenantGate holds
	//     the shared rules ("default"/"" exempt, fail-OPEN on a transient
	//     lookup fault, TENANT_MISSING_MODE for a soft-deleted row).
	if ok, reason := repo.TenantGate(token.TenantId); !ok {
		common.SysLog("switch reconciliation: refused, tenant gate closed tenant=" +
			token.TenantId + " reason=" + reason)
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "owning tenant is disabled or suspended",
			"error_code": switchTenantDisabledCode,
		})
		return
	}

	// 5. Aggregate this user's consume logs by model within the window. A single
	//    grouped SUM keeps it bounded by the number of distinct models, not the
	//    row count.
	tx := repo.LOG_DB.Model(&repo.Log{}).
		Select("model_name, " +
			"COALESCE(SUM(quota),0) as total_quota, " +
			"COALESCE(SUM(prompt_tokens),0) as prompt_tokens, " +
			"COALESCE(SUM(completion_tokens),0) as completion_tokens, " +
			"COUNT(*) as request_count").
		Where("user_id = ? AND type = ?", token.UserId, repo.LogTypeConsume).
		Group("model_name")
	if token.TenantId != "" {
		tx = tx.Where("tenant_id = ?", token.TenantId)
	}
	if win.StartTime > 0 {
		tx = tx.Where("created_at >= ?", win.StartTime)
	}
	if win.EndTime > 0 {
		tx = tx.Where("created_at <= ?", win.EndTime)
	}

	var rows []reconcileModelAgg
	if err := tx.Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to aggregate usage"})
		return
	}

	// 6. Roll up totals + emit the per-model breakdown.
	var totalQuota, totalPrompt, totalCompletion, totalCount int64
	models := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		totalQuota += r.TotalQuota
		totalPrompt += r.PromptTokens
		totalCompletion += r.CompletionTokens
		totalCount += r.RequestCount
		models = append(models, gin.H{
			"model_name":        r.ModelName,
			"quota":             r.TotalQuota,
			"prompt_tokens":     r.PromptTokens,
			"completion_tokens": r.CompletionTokens,
			"request_count":     r.RequestCount,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"total_quota":             totalQuota,
			"total_prompt_tokens":     totalPrompt,
			"total_completion_tokens": totalCompletion,
			"request_count":           totalCount,
			"models":                  models,
		},
	})
}

// normalizeSwitchToken extracts the bare Token.Key from an Authorization header
// the same way UserHeartbeat does: strip an optional "Bearer " prefix and the
// relay "sk-<key>-<channel>" decoration. Returns "" when nothing usable remains.
func normalizeSwitchToken(rawAuth string) string {
	key := strings.TrimSpace(rawAuth)
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	key = strings.TrimPrefix(key, "sk-")
	if idx := strings.IndexByte(key, '-'); idx > 0 {
		key = key[:idx]
	}
	return key
}
