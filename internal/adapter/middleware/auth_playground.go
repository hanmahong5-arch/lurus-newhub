package middleware

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// PlaygroundAuth authenticates via session and auto-resolves the user's first
// active token. This lets the web UI playground work without requiring users
// to manually create and select API tokens.
// If a Bearer token IS provided, it falls through to standard TokenAuth behavior.
func PlaygroundAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		// If Bearer token provided, use standard TokenAuth flow.
		if authHeader := c.Request.Header.Get("Authorization"); authHeader != "" {
			TokenAuth()(c)
			return
		}

		// Session-based: authenticate user first.
		session := sessions.Default(c)
		id := session.Get("id")
		if id == nil {
			abortWithOpenAiMessage(c, http.StatusUnauthorized, "Not logged in, please log in first", string(types.ErrorCodeSessionRequired))
			return
		}
		userId, ok := id.(int)
		if !ok {
			abortWithOpenAiMessage(c, http.StatusUnauthorized, "Invalid session", string(types.ErrorCodeSessionRequired))
			return
		}

		// Find user's first active token.
		tokens, err := repo.GetAllUserTokens(userId, 0, 1)
		if err != nil || len(tokens) == 0 {
			// Auto-create a default token for the user.
			token, createErr := repo.AutoCreateDefaultToken(userId)
			if createErr != nil {
				common.SysError(fmt.Sprintf("PlaygroundAuth: failed to auto-create token for user %d: %v", userId, createErr))
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "Unable to create default token", string(types.ErrorCodeGatewayInternal))
				return
			}
			tokens = []*repo.Token{token}
			common.SysLog(fmt.Sprintf("PlaygroundAuth: auto-created default token for user %d", userId))
		}

		token := tokens[0]
		if token.Status != common.TokenStatusEnabled {
			abortWithOpenAiMessage(c, http.StatusForbidden, "Token is disabled, please enable it or create a new one in token management", string(types.ErrorCodeTokenDisabled))
			return
		}

		// TI: resolve the OWNING USER's tenant, not the token row's —
		// repo.AutoCreateDefaultToken now stamps the owner's own tenant, but
		// every row it created before that change carries TenantId:"default"
		// and there is no backfill, so trusting the token row here would
		// silently drop those non-default-tenant playground callers back onto
		// the platform-shared pool. Mirrors authHelper's session-path
		// resolution (tenantId defaults to "default"; a lookup error fails
		// OPEN, matching that code, rather than 500ing every playground call
		// on a transient DB hiccup). One GetUserCache call also resolves the
		// owning user's status and group, so the disabled check and the group
		// seed below share it instead of hitting the DB/cache twice.
		tenantId := "default"
		userGroup := token.Group
		userCache, cacheErr := repo.GetUserCache(userId)
		if cacheErr == nil && userCache != nil {
			if userCache.Status == common.UserStatusDisabled {
				abortWithOpenAiMessage(c, http.StatusForbidden, "User is banned", string(types.ErrorCodeUserBanned))
				return
			}
			if userCache.TenantId != "" {
				tenantId = userCache.TenantId
			}
			if userGroup == "" {
				userGroup = userCache.Group
			}
		}
		if userGroup == "" {
			userGroup = "default"
		}

		// TI-5 (mirrors authHelper): a disabled/suspended tenant is locked out
		// of the playground too, not just the console/bearer relay path. Same
		// shared decision (repo.TenantGate): "default" exempt, transient lookup
		// error fails open, absent row follows TENANT_MISSING_MODE.
		if ok, _ := repo.TenantGate(tenantId); !ok {
			c.JSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "所属租户已被禁用或暂停",
				"error_code": "TENANT_DISABLED",
			})
			c.Abort()
			return
		}

		repo.InjectTenantContext(c, tenantId, userId)
		emailVal, usernameVal := "", ""
		if userCache != nil {
			emailVal = userCache.Email
			usernameVal = userCache.Username
		}
		c.Set("tenant_context", &TenantContext{
			TenantID: tenantId,
			UserID:   userId,
			Email:    emailVal,
			Username: usernameVal,
			Roles:    []string{},
		})
		// Seed the using-group so channel eligibility/ratio resolve against
		// the caller's actual group instead of falling back to the global
		// list (Distribute reads ContextKeyUsingGroup before this fix it was
		// never set on this path at all).
		common.SetContextKey(c, constant.ContextKeyUsingGroup, userGroup)

		// Set up token context for relay.
		c.Set("id", token.UserId)
		if err := SetupContextForToken(c, token); err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "Token context initialization failed", string(types.ErrorCodeGatewayInternal))
			return
		}

		c.Next()
	}
}
