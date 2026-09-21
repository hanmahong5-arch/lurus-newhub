package middleware

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// auth_token_context.go — SetupContextForToken, split out of auth.go by the
// cycle-13 wiring pass. Pure move: the function is byte-identical to the one
// that stood in auth.go. internal/pkg/gates' source-size ratchet holds
// auth.go at its measured line count, so this cycle's additions to that file
// are paid for by a move rather than by raising the ceiling.
//
// It is also the one piece of auth.go that is not a refusal decision: by the
// time it runs the token has already been accepted, and all it does is put
// the resolved identity on the request context for everything downstream.
// Keeping auth.go to the accept/refuse logic is what
// root_jwt_denial_shape_extra_test.go parses it for.

func SetupContextForToken(c *gin.Context, token *repo.Token, parts ...string) error {
	if token == nil {
		return fmt.Errorf("token is nil")
	}
	c.Set("id", token.UserId)
	c.Set("token_id", token.Id)
	c.Set("token_key", token.Key)
	c.Set("token_name", token.Name)
	c.Set("token_unlimited_quota", token.UnlimitedQuota)
	if !token.UnlimitedQuota {
		c.Set("token_quota", token.RemainQuota)
	}
	if token.ModelLimitsEnabled {
		c.Set("token_model_limit_enabled", true)
		c.Set("token_model_limit", token.GetModelLimitsMap())
	} else {
		c.Set("token_model_limit_enabled", false)
	}
	common.SetContextKey(c, constant.ContextKeyTokenGroup, token.Group)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, token.CrossGroupRetry)
	// Cost attribution (migration 029): carry the token's project into the
	// request so every consume-log row this request produces can be stamped
	// with it. 0 = unassigned. This is the ONLY place project attribution
	// enters the relay path — a log row that misses it is permanently
	// unattributable, so it must not be conditional on anything.
	common.SetContextKey(c, constant.ContextKeyProjectId, token.ProjectId)
	// Carry identity account ID from token for platform billing (if not already set by session auth).
	if token.IdentityAccountID > 0 {
		if _, exists := c.Get("identity_account_id"); !exists {
			c.Set("identity_account_id", token.IdentityAccountID)
		}
	}
	if len(parts) > 1 {
		if repo.IsAdmin(token.UserId) {
			c.Set("specific_channel_id", parts[1])
			// TI (round-3 #1): a root (cross-tenant) operator may pin ANY
			// tenant's channel; a mere tenant-admin may not — Distribute rejects
			// an override that points at another tenant's channel so a tenant
			// admin can't relay through, and exfiltrate the upstream key of, a
			// channel it doesn't own. Record the root distinction here where the
			// token identity is known.
			if repo.IsRoot(token.UserId) {
				common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelRootOverride, true)
			}
		} else {
			abortWithOpenAiMessage(c, http.StatusForbidden, "Regular users cannot specify a channel", string(types.ErrorCodeChannelSpecifyForbidden))
			return fmt.Errorf("regular users cannot specify a channel")
		}
	}
	return nil
}
