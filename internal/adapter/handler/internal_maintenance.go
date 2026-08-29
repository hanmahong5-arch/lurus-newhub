package handler

import (
	"fmt"
	"net/http"

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
