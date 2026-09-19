package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

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
//     from claims (those land with v0.2.x Whoami()). An optional
//     ?invite=<code> query param is consumed (repo.ConsumeTenantInvite) ONLY
//     on this first-ever-login path — see resolveInviteTenant below — to
//     place the new user in a B-end tenant instead of the "default"
//     placeholder; a bad invite (missing/garbage/expired/already-consumed
//     code) silently falls back to "default" rather than blocking the login.
//     One thing here CAN block it: the tenant's tenants.max_users ceiling,
//     which answers 403 TENANT_SEAT_LIMIT (autoCreateBridgedUser). A valid
//     code for a full tenant is left unconsumed so it can be used once a
//     seat frees up.
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
		// Seat cap, part one: peek at the invited tenant BEFORE the code is
		// redeemed. ConsumeTenantInvite is atomic and single-use, so checking
		// afterwards would burn a one-time code on a login it is about to
		// refuse and leave the visitor waiting on an admin to reissue it.
		// repo.PendingInviteTenantID answers only for a code that would
		// actually redeem; anything else falls through to the ordinary
		// "fall back to default, still log in" path below.
		//
		// The authoritative check is inside autoCreateBridgedUser (part two) —
		// this one exists solely to protect the code.
		if code := c.Query("invite"); code != "" {
			if invitedTenantID, known := repo.PendingInviteTenantID(code); known {
				if free, used, limit := repo.TenantHasFreeSeat(invitedTenantID); !free {
					denyTenantSeatLimit(c, &seatLimitError{TenantID: invitedTenantID, Used: used, Limit: limit}, id.AccountID, "zita-bootstrap (invite left unconsumed)")
					return
				}
			}
		}

		// Invite consumption is scoped to THIS branch only — a repeat login
		// for an already-bridged user never reaches here, so an invite
		// query param on that request is simply ignored (existing users'
		// tenant is never changed by an invite, by construction).
		tenantID := resolveInviteTenant(c, id.AccountID)

		user, err = autoCreateBridgedUser(id.AccountID, tenantID)
		var seatErr *seatLimitError
		if errors.As(err, &seatErr) {
			denyTenantSeatLimit(c, seatErr, id.AccountID, "zita-bootstrap (tenant resolved)")
			return
		}
		if err != nil {
			common.SysError(fmt.Sprintf("zita-bootstrap: auto-create user failed (account_id=%d): %v", id.AccountID, err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to provision newhub account",
			})
			return
		}
		autoCreated = true
		common.SysLog(fmt.Sprintf("zita-bootstrap: auto-created user %s (id=%d, lurus_account_id=%d, tenant_id=%s)", user.Username, user.Id, id.AccountID, tenantID))
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

// resolveTenantSlug reads the routing slug of the tenant a login belongs to.
// The console stores it and puts it in the path of every
// /api/v2/:tenant_slug/* call, where middleware.TenantSlugGuard resolves that
// segment with repo.GetTenantBySlug — so the only value worth returning is
// one that lookup can find again.
//
// It used to answer the literal "default" for tenant id "default" without
// reading the row, and to fall back to "default" whenever the lookup failed.
// Both live deployments seed that tenant with slug "lurus", so the console
// was handed a slug no row carries and answered 404 TENANT_NOT_FOUND on
// every panel after an SSO login. No slug is safe to invent: when the tenant
// cannot be resolved this returns "" and the callers pass it through, which
// leaves whatever slug the browser already had alone instead of overwriting
// it with one that is known to 404. Login is never blocked on this — the
// slug is one field of the response, not a gate.
func resolveTenantSlug(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	tenant, err := repo.GetTenantByID(tenantID)
	if err != nil || tenant == nil {
		common.SysError(fmt.Sprintf("zita-bootstrap: tenant slug lookup failed for tenant_id=%s: %v", tenantID, err))
		return ""
	}
	return tenant.Slug
}

// resolveInviteTenant reads an optional ?invite=<code> query param off the
// bootstrap request and, if it redeems successfully, returns the invite's
// bound tenant id; otherwise (param absent, code unknown/expired/already
// consumed/revoked, or a DB error) it returns "default" — the exact
// pre-invite behavior — and logs why. Never returns an error: this sits on
// the login critical path and an invite is a nice-to-have, not a
// precondition for logging in.
func resolveInviteTenant(c *gin.Context, accountID int64) string {
	code := c.Query("invite")
	if code == "" {
		return "default"
	}
	tenant, err := repo.ConsumeTenantInvite(code, accountID)
	if err != nil {
		common.SysLog(fmt.Sprintf("zita-bootstrap: invite consume failed (account_id=%d): %v — falling back to default tenant", accountID, err))
		return "default"
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorSystem, 0,
		governance.ActionTenantInviteConsumed, governance.ResourceTenant, 0,
		fmt.Sprintf(`{"tenant_id":%q,"account_id":%d}`, tenant.Id, accountID),
	))
	return tenant.Id
}

// seatLimitError is what autoCreateBridgedUser returns instead of creating a
// user when the target tenant is already at tenants.max_users. It carries the
// seat numbers behind the refusal so the denial record can quote them without
// re-querying.
//
// Callers, enumerated 2026-09-19 by grepping autoCreateBridgedUser across
// non-test internal/ sources: ZitaBootstrap (this file) and ProvisionV2
// (v2_provision.go). Both map it through denyTenantSeatLimit to 403
// TENANT_SEAT_LIMIT.
type seatLimitError struct {
	TenantID string
	Used     int64
	Limit    int
}

func (e *seatLimitError) Error() string {
	return fmt.Sprintf("tenant %s is at its user limit (%d/%d)", e.TenantID, e.Used, e.Limit)
}

// denyTenantSeatLimit writes the whole refusal record for a seat-capped login
// — system log, metric, audit row — and answers 403 TENANT_SEAT_LIMIT. `stage`
// names the caller and the point it refused at, which is the only way to tell
// from the logs whether a one-time invite code survived the refusal.
//
// The audit row is the operator's only durable record of the denial, so it
// carries the reason, the tenant and the seat numbers; the metric is the
// countable half (lurus_gateway_tenant_gate_total{outcome="seat_limit_denied"}).
// Both are asserted by TestZitaBootstrap_SeatCapReached_Rejected and
// TestProvisionV2_SeatCapReached_Rejected.
func denyTenantSeatLimit(c *gin.Context, e *seatLimitError, accountID int64, stage string) {
	common.SysLog(fmt.Sprintf("%s: tenant seat limit reached for tenant %s (%d/%d) — refusing to provision account_id=%d",
		stage, e.TenantID, e.Used, e.Limit, accountID))
	metrics.RecordTenantGate(metrics.TenantGateOutcomeSeatLimitDenied)
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorSystem, 0,
		governance.ActionAuthFailed, governance.ResourceTenant, 0,
		fmt.Sprintf(`{"reason":"tenant_seat_limit","tenant_id":%q,"used":%d,"max_users":%d,"account_id":%d}`,
			e.TenantID, e.Used, e.Limit, accountID),
	))
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "该租户席位已满，请联系管理员 / This tenant has reached its user limit",
		"error_code": "TENANT_SEAT_LIMIT",
	})
}

// autoCreateBridgedUser provisions a minimum-viable newhub user for a
// platform-authenticated visitor with no existing binding. Username is
// derived from account_id (guaranteed unique by the platform); email and
// display_name stay empty until the SDK Whoami() ships and the bridge
// can backfill them. tenantID is "default" unless a valid tenant invite
// was just consumed for this login (resolveInviteTenant).
//
// Seat cap: tenants.max_users is the ceiling an admin set on the tenant. The
// OIDC provisioning path honours it (repo.CreateUserFromIDPClaims →
// TenantCanAddUser); neither bridge path did, so an invite code — or a
// switch-issued entitlement token — could fill a tenant past its plan. The
// check lives HERE, at the single insert, rather than at one of the two call
// sites, so a third caller cannot be added without it. It fails open when no
// ceiling is configured or the tenant row cannot be read (repo.TenantHasFreeSeat)
// and it is not a reservation — see that function on the race.
func autoCreateBridgedUser(accountID int64, tenantID string) (*repo.User, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	if free, used, limit := repo.TenantHasFreeSeat(tenantID); !free {
		return nil, &seatLimitError{TenantID: tenantID, Used: used, Limit: limit}
	}
	username := fmt.Sprintf("lurus_%d", accountID)
	user := &repo.User{
		Username:       username,
		DisplayName:    username,
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
		Group:          "default",
		TenantId:       tenantID,
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
