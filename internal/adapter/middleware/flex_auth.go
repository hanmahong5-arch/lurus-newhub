package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// FlexAuth is a dual-mode authentication middleware that accepts either:
//   - OIDC JWT (for browser-based product frontends)
//   - API Token sk-xxx (for server-side product backends / CLI tools)
//
// On success it always sets "id" (lurus user ID) in gin context.
// The OIDC path additionally sets "tenant_context" and "identity_account_id".
// Token path additionally sets "token_id", "token_name", etc.
func FlexAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authorization header required",
			})
			c.Abort()
			return
		}

		bearer := auth
		if strings.HasPrefix(bearer, "Bearer ") || strings.HasPrefix(bearer, "bearer ") {
			bearer = strings.TrimSpace(bearer[7:])
		}

		// Heuristic: API tokens always start with "sk-".
		// Everything else is treated as a OIDC JWT.
		if strings.HasPrefix(bearer, "sk-") {
			flexAuthViaToken(c, bearer)
		} else {
			flexAuthViaJWT(c)
		}
	}
}

// flexAuthViaToken validates an API token and populates gin context.
func flexAuthViaToken(c *gin.Context, rawKey string) {
	key := strings.TrimPrefix(rawKey, "sk-")
	parts := strings.Split(key, "-")
	key = parts[0]

	token, err := repo.ValidateUserToken(key)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid API token",
		})
		c.Abort()
		return
	}

	userCache, err := repo.GetUserCache(token.UserId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to load user",
		})
		c.Abort()
		return
	}
	if userCache.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "User is disabled",
		})
		c.Abort()
		return
	}

	c.Set("id", token.UserId)
	c.Set("token_id", token.Id)
	c.Set("token_name", token.Name)
	c.Set("auth_method", "token")

	c.Request = c.Request.WithContext(common.WithUserID(c.Request.Context(), fmt.Sprintf("%d", token.UserId)))
	c.Next()
}

// flexAuthViaJWT delegates to the existing OIDCAuth flow.
// On success it sets "id" (user_id) so handlers can use c.GetInt("id").
func flexAuthViaJWT(c *gin.Context) {
	// Reuse the full OIDCAuth middleware (it calls c.Next() or c.Abort()).
	handler := OIDCAuth()
	handler(c)

	// If OIDCAuth succeeded (did NOT abort), copy user_id to "id" for handler compatibility.
	if !c.IsAborted() {
		if uid, exists := c.Get("user_id"); exists {
			c.Set("id", uid)
		}
		c.Set("auth_method", "jwt")
	}
}
