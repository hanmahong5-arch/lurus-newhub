package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
	"gorm.io/gorm"
)

// ZitaBootstrap converts a platform-issued SDK session into a newhub
// session. Replaces the legacy OIDC OAuth callback path for the v2
// frontend (ADR-0011 Layer C).
//
// Flow:
//  1. zita.AuthMiddleware validates the lurus_session cookie and stuffs
//     *Identity into context.
//  2. Look up users WHERE lurus_account_id = identity.AccountID.
//  3. If absent, auto-create with username "lurus_<account_id>" — the SDK
//     MVP only carries account_id, so we cannot seed email/displayName
//     from claims (those land with v0.2.x Whoami()).
//  4. Set V1 session vars (id/username/role/status/group) so existing
//     middleware.UserAuth() admits the request, and return user JSON for
//     the frontend's localStorage 'user' shim.
//
// Route: POST /api/v2/auth/zita-bootstrap (must be registered behind
// ZitaClient.AuthMiddleware()).
func ZitaBootstrap(c *gin.Context) {
	id, ok := zita.IdentityFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "zita identity missing — SDK middleware did not validate session",
		})
		return
	}
	if id.AccountID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "zita identity carries no account_id",
		})
		return
	}

	user, err := repo.GetUserByLurusAccountID(id.AccountID)
	autoCreated := false
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user, err = autoCreateBridgedUser(id.AccountID)
		if err != nil {
			common.SysError(fmt.Sprintf("zita-bootstrap: auto-create user failed (account_id=%d): %v", id.AccountID, err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to provision newhub account",
			})
			return
		}
		autoCreated = true
		common.SysLog(fmt.Sprintf("zita-bootstrap: auto-created user %s (id=%d, lurus_account_id=%d)", user.Username, user.Id, id.AccountID))
	} else if err != nil {
		common.SysError(fmt.Sprintf("zita-bootstrap: lookup failed (account_id=%d): %v", id.AccountID, err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to resolve account",
		})
		return
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "User account is disabled",
		})
		return
	}

	session := sessions.Default(c)
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("group", user.Group)
	session.Set("identity_account_id", id.AccountID)
	if err := session.Save(); err != nil {
		common.SysError(fmt.Sprintf("zita-bootstrap: session save failed: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to persist session",
		})
		return
	}

	// Audit trail — append-only, must be emitted on every successful bridge so
	// the compliance log can answer "who logged in via the SDK bridge, when,
	// from which IP, was a new newhub account auto-provisioned." Missing this
	// at launch is unrecoverable (events cannot be backfilled after the fact).
	details, _ := json.Marshal(map[string]any{
		"lurus_account_id": id.AccountID,
		"auto_created":     autoCreated,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorUser, user.Id,
		governance.ActionAuthBootstrapped, governance.ResourceUser, user.Id,
		string(details),
	))

	// tenant_slug lets the v2 frontend route every subsequent API call
	// through /api/v2/:tenant_slug/... without an extra round trip to look
	// up the slug. user.TenantId is a UUID; resolve it to the human-readable
	// slug here. Missing/empty TenantId falls back to "default" so the
	// frontend still has a routable value.
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

// resolveTenantSlug converts a tenant UUID into its human-readable slug.
// On lookup failure (tenant deleted, "default" placeholder, DB hiccup) it
// returns "default" rather than failing the bootstrap — the bridge is on
// the login critical path and we'd rather log the user in with a fallback
// slug than block them from the console entirely.
func resolveTenantSlug(tenantID string) string {
	if tenantID == "" || tenantID == "default" {
		return "default"
	}
	tenant, err := repo.GetTenantByID(tenantID)
	if err != nil || tenant == nil {
		common.SysError(fmt.Sprintf("zita-bootstrap: tenant slug lookup failed for tenant_id=%s: %v", tenantID, err))
		return "default"
	}
	if tenant.Slug == "" {
		return "default"
	}
	return tenant.Slug
}

// autoCreateBridgedUser provisions a minimum-viable newhub user for a
// platform-authenticated visitor with no existing binding. Username is
// derived from account_id (guaranteed unique by the platform); email and
// display_name stay empty until the SDK Whoami() ships and the bridge
// can backfill them.
func autoCreateBridgedUser(accountID int64) (*repo.User, error) {
	username := fmt.Sprintf("lurus_%d", accountID)
	user := &repo.User{
		Username:       username,
		DisplayName:    username,
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
		Group:          "default",
		TenantId:       "default",
		LurusAccountID: &accountID,
	}
	if err := user.Insert(); err != nil {
		// Concurrent first-login race: another in-flight bootstrap (e.g. user
		// opened two tabs simultaneously) already won the unique-index check
		// on lurus_account_id. Probe by the same key — if the row now exists,
		// we lost the race cleanly and can return the winner. Otherwise the
		// failure is genuine (DB down, schema drift) and propagates up.
		if existing, lookupErr := repo.GetUserByLurusAccountID(accountID); lookupErr == nil {
			return existing, nil
		}
		return nil, err
	}
	// Welcome-quota parity with the OIDC auto-create path: Insert() stamps
	// common.QuotaForNewUser (an option that defaults to 0), while
	// CreateUserFromIDPClaims grants the per-tenant policy
	// quota.new_user_quota (default 10000). Live-probed 2026-08-31: a fresh
	// bridged user landed with quota 0 and 402'd on its very first relay
	// call. Same tenant, same "new user" event — same grant. Best-effort:
	// a failed grant must not fail the login itself.
	//
	// MUST go through IncreaseUserQuota, not a raw DB update: Insert()'s
	// sidebar-config step calls user.Update(), which caches the user hash
	// (Quota=0) in Redis — a raw UPDATE leaves that stale 0 in the cache and
	// relay's GetUserQuota keeps 402-ing (second live probe proved it: DB
	// said 10000, relay said "available $0.000000").
	if grant := repo.GetTenantConfigInt(user.TenantId, "quota.new_user_quota", 10000); grant > user.Quota {
		if err := repo.IncreaseUserQuota(user.Id, grant-user.Quota, true); err != nil {
			common.SysError(fmt.Sprintf("zita-bootstrap: welcome quota grant failed (user=%d): %v", user.Id, err))
		}
	}
	// Re-read by lurus_account_id to pick up DB-assigned id and defaults
	// applied by the Insert path (quota grant, sidebar config init).
	return repo.GetUserByLurusAccountID(accountID)
}
