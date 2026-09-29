package middleware

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// legacyInternalKeyHeaderWarning is set on every response to a request that
// authenticated through X-API-Key. Same RFC 7234 warn-code 299 shape platform
// uses for its legacy INTERNAL_API_KEY (router.go internalKeyAuth).
const legacyInternalKeyHeaderWarning = `299 - "X-API-Key is deprecated; use Authorization: Bearer"`

// internalKeySource names which request header supplied the internal API key.
type internalKeySource int

const (
	internalKeyNone internalKeySource = iota
	internalKeyBearer
	internalKeyLegacyHeader
)

// extractInternalApiKey picks the one credential this request is judged on.
//
//   - `Authorization: Bearer <key>` (scheme case-insensitive, RFC 7235) wins
//     whenever present — even when X-API-Key is also sent and differs, and
//     even when the bearer key is empty or wrong. One request is judged on
//     one credential: falling through to X-API-Key after a failed bearer
//     would let a caller whose bearer key has drifted keep working silently
//     on the legacy header, hiding exactly the drift the migration must
//     surface, and would give a guesser two tries per request.
//   - An Authorization header that is not a Bearer credential (another scheme,
//     a bare key, "Bearer" with no space) is not a claim on this scheme and is
//     ignored; X-API-Key is then consulted. That keeps existing X-API-Key
//     callers working if a proxy or HTTP client adds an unrelated
//     Authorization header.
//   - Otherwise X-API-Key, the deprecated header.
func extractInternalApiKey(c *gin.Context) (string, internalKeySource) {
	if auth := c.GetHeader("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:]), internalKeyBearer
	}
	if key := c.GetHeader("X-API-Key"); key != "" {
		return key, internalKeyLegacyHeader
	}
	return "", internalKeyNone
}

// InternalApiAuth authenticates /internal requests with an internal API key
// (lurus_ik_…), sent as `Authorization: Bearer <key>` or, deprecated,
// `X-API-Key: <key>`. See extractInternalApiKey for precedence. Key
// validation (hash lookup, enabled, expiry) is the same for both headers.
func InternalApiAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, source := extractInternalApiKey(c)
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "API key required",
			})
			c.Abort()
			return
		}

		// Validate key
		key, err := repo.ValidateInternalApiKey(apiKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid or expired API key",
			})
			c.Abort()
			return
		}

		// Store key info in context
		c.Set("internal_api_key", key)
		c.Set("internal_api_scopes", key.GetScopes())
		c.Set("internal_api_key_id", key.Id)
		c.Set("internal_api_key_name", key.Name)

		if source == internalKeyLegacyHeader {
			c.Header("Warning", legacyInternalKeyHeaderWarning)
			metrics.RecordInternalAuthLegacyHeader(c.FullPath(), key.Id)
		}

		c.Next()
	}
}

// RequireScope middleware checks if the API key has required scope
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get key from context
		keyInterface, exists := c.Get("internal_api_key")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "API key not found in context",
			})
			c.Abort()
			return
		}

		key, ok := keyInterface.(*repo.InternalApiKey)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Invalid API key type",
			})
			c.Abort()
			return
		}

		// Check scope
		if !key.HasScope(scope) {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Insufficient permissions. Required scope: " + scope,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAnyScope middleware checks if the API key has any of the required scopes
func RequireAnyScope(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyInterface, exists := c.Get("internal_api_key")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "API key not found in context",
			})
			c.Abort()
			return
		}

		key, ok := keyInterface.(*repo.InternalApiKey)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Invalid API key type",
			})
			c.Abort()
			return
		}

		// Check any scope
		for _, scope := range scopes {
			if key.HasScope(scope) {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Insufficient permissions",
		})
		c.Abort()
	}
}
