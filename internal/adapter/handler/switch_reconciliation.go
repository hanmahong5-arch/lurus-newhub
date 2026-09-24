package handler

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Switch Usage Reconciliation
//
// Switch's desktop client records every gateway request locally. The Hub
// independently logs the same traffic under the gateway's user_token. This
// endpoint returns the Hub-side consume-log totals for a window so Switch can
// detect aggregate drift between the two ledgers.
//
// Auth goes through authenticateSwitchRawTokenWithCode (switch_user_info.go),
// the SAME helper /switch/user/info and /switch/user/topup use: a raw
// user_token (Token.Key) in the Authorization header, then Token.Status, then
// the owning User and its status, then the owning tenant's gate. We do NOT use
// middleware.UserAuth — that resolves access_tokens (User.AccessToken), not
// per-token API keys, and would reject every call.
//
// This comment used to claim the auth "mirrors UserHeartbeat" while the
// handler did neither of the two checks that make that claim true: it looked
// the token up (the lookup applies no status filter) and went straight to
// aggregating, so an operator-disabled token and a banned user's token both
// kept answering with that account's billing-relevant totals. Sharing the
// helper is what makes the sentence above checkable rather than aspirational.
//
// The resolved user also scopes the aggregation: we only ever sum that user's
// own logs, so a token cannot read another user's usage. A token row carrying
// no owning user id (UserId == 0) is REFUSED rather than aggregated — the
// query is scoped by user_id, so such a token would otherwise sum every
// user_id-0 consume row in the install, i.e. rows belonging to other
// credentials. GetUserById rejects id 0 outright, so the helper answers 401.
//
// Read-only on the Hub side; no DB migration.
// ============================================================================

// switchReconciliationTenantMessage is the 403 sentence this endpoint has
// answered since it shipped, kept byte-for-byte while the auth moved onto the
// shared helper (cycle-14 L9). The helper's own refusal text is a CJK sentence
// (switchTenantSuspendedMessage), so adopting it wholesale would have rewritten
// a live response body as a side effect of a security fix. Two reasons not to:
// the repo's wire contract is "API error.message is English-only"
// (internal/adapter/middleware/wire_message_language_gate_test.go), and
// UserHeartbeat — the surface this handler's header says it mirrors — still
// answers exactly this sentence for the identical condition
// (user_heartbeat.go). The machine-readable half, error_code TENANT_DISABLED,
// is identical on all four Switch surfaces, so a client branching on the code
// rather than the text sees no difference either way.
const switchReconciliationTenantMessage = "owning tenant is disabled or suspended"

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

	// 3. Resolve token → user through the shared raw-token helper: token
	//    status, owning user existence and status, and the owning tenant's
	//    gate, in that order. It distinguishes "not found"/revoked/banned
	//    (401) from a transient DB error (500) and from a suspended tenant
	//    (403 + error_code).
	token, user, httpStatus, message, errorCode := authenticateSwitchRawTokenWithCode(c)
	if httpStatus != 0 {
		// The tenant refusal keeps THIS endpoint's own sentence — see
		// switchReconciliationTenantMessage. Every other refusal the helper
		// produces is already the ASCII text this endpoint used to write
		// inline ("token not found", "token disabled", "user disabled",
		// "token lookup failed"), so only the gated one is remapped.
		if errorCode == switchTenantDisabledCode {
			message = switchReconciliationTenantMessage
		}
		body := gin.H{"success": false, "message": message}
		if errorCode != "" {
			body["error_code"] = errorCode
		}
		c.JSON(httpStatus, body)
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

	// 4b. The owning-tenant gate that used to sit here (cycle-13 L9) now runs
	//     inside authenticateSwitchRawTokenWithCode, which applies it to BOTH
	//     routes — the figures this returns are the tenant's own
	//     billing-relevant totals, and a tenant an operator suspended should
	//     stop answering them on the same request that stops answering the
	//     heartbeat. The refusal is byte-identical to the one this handler
	//     wrote inline: 403, error_code TENANT_DISABLED, and the sentence in
	//     switchReconciliationTenantMessage.
	//
	//     COST OF THE CONSOLIDATION: this cell no longer has a gate site of
	//     its own. Deleting the single TenantGate call in switch_user_info.go
	//     now reddens three surfaces at once (user/info, user/topup,
	//     reconciliation) instead of one each — see the hand-off note about
	//     tenant_reaches_data_c13_test.go's per-surface independence claim,
	//     which that consolidation falsified.

	// 5. Aggregate this user's consume logs by model within the window. A single
	//    grouped SUM keeps it bounded by the number of distinct models, not the
	//    row count. user.Id rather than token.UserId: the helper has already
	//    proved that row exists and is not banned.
	tx := repo.LOG_DB.Model(&repo.Log{}).
		Select("model_name, "+
			"COALESCE(SUM(quota),0) as total_quota, "+
			"COALESCE(SUM(prompt_tokens),0) as prompt_tokens, "+
			"COALESCE(SUM(completion_tokens),0) as completion_tokens, "+
			"COUNT(*) as request_count").
		Where("user_id = ? AND type = ?", user.Id, repo.LogTypeConsume).
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
