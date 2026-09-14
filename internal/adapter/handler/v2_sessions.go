package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// currentSessionID returns the gin-contrib/sessions session id for this
// request, or "" if the session middleware is not mounted at all (some
// hermetic test routers omit it — sessions.Default would panic via
// c.MustGet) or the underlying store never assigned one (cookie-only
// stores never do; see entity.UserSession's doc comment). Never panics.
func currentSessionID(c *gin.Context) string {
	v, exists := c.Get(sessions.DefaultKey)
	if !exists {
		return ""
	}
	s, ok := v.(sessions.Session)
	if !ok {
		return ""
	}
	return s.ID()
}

// ListSessionsV2 returns session information for the authenticated user within
// a tenant. Route (registered in api-v2-router.go):
//
//	GET /api/v2/:tenant_slug/sessions
//	Auth: UserAuth middleware
//
// With SESSION_REGISTRY_ENABLED off (default), or on but this login has no
// registered row (cookie-only session store; a bearer/token-authenticated
// GET), this returns exactly what it always has: a single synthetic entry
// representing the current request — auth_method, active token count, and
// 30-day request count.
//
// With the flag on and at least one row registered (repo.UpsertUserSessionSeen,
// called from authHelper on cookie-session logins), it returns one item per
// registered device instead, additively carrying is_current/created_at/
// last_seen_at/ip (masked)/user_agent_family alongside the original fields —
// see renderRegistrySessions.
//
// Field whitelist is strict: access_token, cookie, and raw IP/User-Agent are
// never serialised — ip is always /24 (v4) or /48 (v6) masked, and the User
// -Agent is always reduced to a coarse family name. This is safe to log and
// forward.
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

	if repo.SessionRegistryEnabled() {
		if rows, rErr := repo.ListActiveUserSessions(userID); rErr == nil && len(rows) > 0 {
			renderRegistrySessions(c, rows, activeTokens, requestCount, authMethod, currentSessionID(c))
			return
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

// renderRegistrySessions writes the registry-backed items response for
// ListSessionsV2 when SESSION_REGISTRY_ENABLED is on and the caller has at
// least one registered device. activeTokens/requestCount are account-level
// aggregates (same query as the legacy path) — repeated on every row on
// purpose, since a token/request isn't attributable to one specific
// browser session. is_current is true for at most one row: the one whose
// session_key matches this very request's session id (empty when the
// caller authenticated via bearer/token, which carries no session_key).
func renderRegistrySessions(c *gin.Context, rows []entity.UserSession, activeTokens, requestCount int64, fallbackAuthMethod, currentKey string) {
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		isCurrent := currentKey != "" && r.SessionKey == currentKey
		am := r.AuthMethod
		if am == "" {
			am = fallbackAuthMethod
		}
		items = append(items, gin.H{
			"id":                r.Id,
			"current":           isCurrent,
			"is_current":        isCurrent,
			"auth_method":       am,
			"active_tokens":     activeTokens,
			"request_count":     requestCount,
			"last_seen":         r.LastSeenAt,
			"created_at":        r.CreatedAt,
			"last_seen_at":      r.LastSeenAt,
			"ip":                repo.MaskIP(r.IP),
			"user_agent_family": repo.UserAgentFamily(r.UserAgent),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": items,
			"total": len(items),
		},
	})
}
