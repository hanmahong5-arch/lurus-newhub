package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// ListSessionsV2 returns session information for the authenticated user within
// a tenant. Route (registered by Opus):
//
//	GET /api/v2/:tenant_slug/sessions
//	Auth: UserAuth middleware
//
// There is no multi-device session store. This endpoint returns a single
// synthetic entry representing the current request — auth_method, active token
// count, and 30-day request count. Per-session revoke and device list are
// deferred to v3 (requires backend session store).
//
// Field whitelist is strict: access_token, cookie, IP, and User-Agent are
// never serialised. This is safe to log and forward.
func ListSessionsV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	// Count enabled tokens for this user in this tenant.
	var activeTokens int64
	repo.DB.Model(&repo.Token{}).
		Where("user_id = ? AND tenant_id = ? AND status = 1", userID, tenant.Id).
		Count(&activeTokens)

	// Count log entries in the last 30 days for this user in this tenant.
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	var requestCount int64
	repo.LOG_DB.Model(&repo.Log{}).
		Where("user_id = ? AND tenant_id = ? AND created_at > ?", userID, tenant.Id, cutoff).
		Count(&requestCount)

	// auth_method: prefer OIDC JWT indicator set by OIDCAuth middleware;
	// fall back to "session" for cookie-based auth.
	authMethod := "session"
	if am, exists := c.Get("auth_method"); exists {
		if s, ok := am.(string); ok && s != "" {
			authMethod = s
		}
	}

	session := gin.H{
		"id":            "current",
		"current":       true,
		"auth_method":   authMethod,
		"active_tokens": activeTokens,
		"request_count": requestCount,
		"last_seen":     time.Now().Unix(),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": []gin.H{session},
			"total": 1,
		},
	})
}
