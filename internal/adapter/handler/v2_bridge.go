package handler

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// BridgeEnabled reports whether the e2e bridge endpoint should be wired.
// Route registration MUST consult this — if the token is empty in prod the
// route is not registered at all (defense in depth: even a misrouted
// request can never reach the handler).
func BridgeEnabled() bool {
	return os.Getenv("E2E_BRIDGE_TOKEN") != ""
}

// BridgeExchange swaps a static, env-gated token for a real newhub session
// cookie. Exists only for Playwright e2e tests — the Zita SDK cookie flow
// can't be driven from a headless browser without a real platform login,
// and we don't want to teach the e2e harness about the OIDC provider either.
//
// Route: POST /api/v2/bridge/exchange?token=<E2E_BRIDGE_TOKEN>&user_id=<int>
// Registered ONLY when env E2E_BRIDGE_TOKEN is non-empty (prod is safe by
// construction — the route simply does not exist there).
//
// Returns 200 + Set-Cookie + JSON {id, username, role, status, group, email,
// tenant_slug} mirroring the ZitaBootstrap response so the frontend can
// treat it identically. Failures:
//   - 403 if token query param missing or mismatched
//   - 400 if user_id missing or non-numeric
//   - 404 if the user does not exist
//   - 403 if the user is disabled
//
// Constant-time compare prevents timing attacks against the token (the
// token is short enough that ms-scale leaks would otherwise be feasible
// across CI re-runs).
func BridgeExchange(c *gin.Context) {
	expected := os.Getenv("E2E_BRIDGE_TOKEN")
	if expected == "" {
		// Defense in depth: even if the route somehow gets registered, refuse.
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "bridge disabled",
		})
		return
	}

	supplied := c.Query("token")
	if supplied == "" || subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "invalid bridge token",
		})
		return
	}

	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "user_id query param required",
		})
		return
	}
	userID, err := strconv.Atoi(userIDStr)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "user_id must be a positive integer",
		})
		return
	}

	user, err := repo.GetUserById(userID, false)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "user not found",
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "user account is disabled",
		})
		return
	}

	session := sessions.Default(c)
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("group", user.Group)
	if err := session.Save(); err != nil {
		common.SysError(fmt.Sprintf("bridge-exchange: session save failed for user_id=%d: %v", user.Id, err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to persist session",
		})
		return
	}

	common.SysLog(fmt.Sprintf("bridge-exchange: e2e session issued for user_id=%d username=%s", user.Id, user.Username))

	tenantSlug := resolveTenantSlug(user.TenantId)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
			"email":        user.Email,
			"tenant_slug":  tenantSlug,
		},
	})
}
