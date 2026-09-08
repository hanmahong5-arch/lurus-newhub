package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// InternalRotateDueTokens triggers an immediate auto-rotation pass over every
// token whose rotation interval has elapsed — the same logic the HA leader
// runs on its schedule (see lifecycle.StartSecretRotationWithContext). It
// exists for two operational needs: rehearsing the rotation path without
// waiting a day, and an emergency sweep (e.g. a suspected key leak). The pass
// is idempotent — only tokens actually due are rotated, and the per-token CAS
// (repo.RotateKeyWithTimestampCAS) makes a concurrent scheduled pass safe —
// so it can be called repeatedly.
//
// POST /internal/admin/rotate-due-tokens — admin-scoped (repo.ScopeAdmin).
func InternalRotateDueTokens(c *gin.Context) {
	rotated, err := app.RotateDueTokens(c.Request.Context(), common.GetTimestamp(), common.SendEmail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "rotation pass failed: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("manual rotate-due-tokens via key %q rotated %d token(s)", keyName, rotated))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "rotation pass complete",
		"data": gin.H{
			"rotated": rotated,
		},
	})
}

// InternalResetDuePools triggers an immediate scheduled credit-pool reset
// pass — the same repo.ResetDuePools query and CAS the leader's reconcile
// ticker runs after every stranded-topup sweep (app.ResetDuePools /
// resetDuePoolsSeam in credit_pool_reconcile.go). It exists to rehearse the
// pass without waiting for a tick: ?mode=observe lists every due pool with
// the delta a reset WOULD apply and writes nothing; ?mode=enforce applies it.
// The query param overrides CREDIT_POOL_RESET_MODE for this call only — an
// omitted mode falls back to the environment's default (observe unless an
// operator has turned enforcement on); a mode that IS supplied but is
// neither "observe" nor "enforce" is rejected with 400 rather than silently
// treated as observe, so a typo in the query string cannot masquerade as a
// successful rehearsal.
//
// POST /internal/admin/reset-due-pools?mode=observe|enforce — admin-scoped
// (repo.ScopeAdmin).
func InternalResetDuePools(c *gin.Context) {
	mode := c.Query("mode")
	if mode == "" {
		mode = app.CreditPoolResetModeFromEnv()
	} else if mode != app.CreditPoolResetModeObserve && mode != app.CreditPoolResetModeEnforce {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("unrecognised mode %q — accepted values: %q, %q", mode, app.CreditPoolResetModeObserve, app.CreditPoolResetModeEnforce),
		})
		return
	}
	results, err := app.ResetDuePoolsWithMode(c.Request.Context(), mode)
	respondResetDuePools(c, mode, results, err)
}

// respondResetDuePools renders the shared success/error JSON shape for
// InternalResetDuePools and logs the caller identity + pass size, mirroring
// InternalRotateDueTokens's SysLog line.
func respondResetDuePools(c *gin.Context, mode string, results []repo.PoolResetResult, err error) {
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "reset pass failed: " + err.Error(),
		})
		return
	}

	pools := make([]gin.H, 0, len(results))
	for _, r := range results {
		nextResetAt := ""
		if r.NextResetAt != nil {
			nextResetAt = r.NextResetAt.UTC().Format(time.RFC3339)
		}
		pools = append(pools, gin.H{
			"tenant_id":     r.TenantID,
			"pool_id":       r.PoolID,
			"delta":         r.Delta,
			"action":        r.Action,
			"next_reset_at": nextResetAt,
		})
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("manual reset-due-pools via key %q mode=%s pools=%d", keyName, mode, len(pools)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "reset pass complete",
		"data": gin.H{
			"mode":  mode,
			"pools": pools,
		},
	})
}
