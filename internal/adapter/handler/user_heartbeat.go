package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================================
// V2 User Heartbeat
//
// Switch EndUser mode polls this endpoint every 5 minutes to verify that its
// stored user_token is still valid. The Switch client at
//   2c-gui-switch/internal/redemption/heartbeat.go
// expects:
//
//   - HTTP 401 → token permanently revoked (UI locks immediately)
//   - HTTP 404 → legacy Hub (Switch treats as active, soft-fallback)
//   - HTTP 200 with envelope {success, message, data} where data is
//     {status: "active"|"expired"|"revoked", expires_at, quota}
//
// Added cycle-13 L9: HTTP 403 with error_code TENANT_DISABLED and data
// {status:"suspended"} when the token's owning tenant is suspended. Today's
// Switch client treats any non-401/404 rejection as a transient failure it
// does not latch on, which is the intended "locked, not revoked" reading;
// giving it its own UI state is the switch owner's call (O-heartbeat).
//
// Authentication is a raw user_token (Token.Key) in the `Authorization`
// header. We deliberately do NOT use middleware.UserAuth() here — that
// middleware resolves the user's *access_token* (User.AccessToken), not a
// per-token API key (Token.Key). UserAuth would 401 every heartbeat from
// Switch, defeating the entire flow.
//
// Inline auth keeps the change surgical: we only validate what we need
// (token exists + user enabled + tenant matches), without forking the
// middleware or polluting other endpoints.
// ============================================================================

// HeartbeatRequest is the JSON body Switch sends on every tick. Both fields
// are advisory — we accept any non-empty fingerprint/app_version so we don't
// reject legacy clients during rolling upgrades.
type HeartbeatRequest struct {
	Fingerprint string `json:"fingerprint"`
	AppVersion  string `json:"app_version"`
}

// heartbeatStatus is the typed status string returned in the response data.
// Kept as a typed constant block so a typo in a handler branch fails at
// compile time, not at JSON parse time on the Switch side.
const (
	heartbeatStatusActive  = "active"
	heartbeatStatusExpired = "expired"
	heartbeatStatusRevoked = "revoked"
	// heartbeatStatusSuspended is reported when the token itself is fine but
	// an operator suspended the owning tenant. It rides in a 403 envelope
	// with success=false, so a Switch build that only knows
	// active/expired/revoked never parses it (its client short-circuits on
	// the envelope) — it is there for operators and for a client that grows
	// a branch for it (contract note O-heartbeat).
	heartbeatStatusSuspended = "suspended"
)

// UserHeartbeat handles POST /api/v2/:tenant_slug/user/heartbeat and the
// single-tenant fallback POST /api/v2/switch/heartbeat.
//
// The same handler serves both routes; tenant cross-check only fires when
// :tenant_slug is present in the URL path.
func UserHeartbeat(c *gin.Context) {
	// 1. Extract raw user_token from Authorization header.
	//    Switch sends it as-is (no "Bearer " prefix per the EndUser contract),
	//    but we accept Bearer too for forward compatibility with any client
	//    that adds it later.
	rawAuth := strings.TrimSpace(c.Request.Header.Get("Authorization"))
	if rawAuth == "" {
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "missing Authorization header")
		return
	}
	key := rawAuth
	if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	key = strings.TrimPrefix(key, "sk-")
	// sk-<key>-<channel> form (relay convention) — heartbeat ignores channel.
	if idx := strings.IndexByte(key, '-'); idx > 0 {
		key = key[:idx]
	}
	if key == "" {
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "empty Authorization token")
		return
	}

	// 2. Validate request body. Required fields are advisory — we only block
	//    on truly malformed JSON, not on missing optional fields, so legacy
	//    Switch builds don't get hard-rejected.
	var req HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "malformed request body: " + err.Error(),
		})
		return
	}

	// 3. Look up the token. Distinguish "not found" (→ revoked, hard) from
	//    "DB error" (→ 500, transient — Switch's grace period absorbs it).
	token, err := repo.GetTokenByKey(key, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeHeartbeatRevoked(c, http.StatusUnauthorized, "token not found")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "token lookup failed",
		})
		return
	}
	if token == nil {
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "token not found")
		return
	}

	// 4. Token-status branches. Order matters: disabled/exhausted before
	//    expired so the most actionable status surfaces to the user.
	switch token.Status {
	case common.TokenStatusDisabled:
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "token disabled")
		return
	case common.TokenStatusExpired:
		writeHeartbeatStatus(c, heartbeatStatusExpired, token)
		return
	case common.TokenStatusExhausted:
		writeHeartbeatStatus(c, heartbeatStatusExpired, token)
		return
	}
	// Status enabled but expired by clock — treat as expired without DB flip
	// (the relay path handles persistent status mutation; heartbeat stays
	// read-only on the hot path).
	if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
		writeHeartbeatStatus(c, heartbeatStatusExpired, token)
		return
	}
	// Quota burned through with no unlimited flag — same expired semantics.
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		writeHeartbeatStatus(c, heartbeatStatusExpired, token)
		return
	}

	// 5. User-level checks. A disabled user => all their tokens are revoked,
	//    even if the token row itself is still enabled.
	user, userErr := repo.GetUserById(token.UserId)
	if userErr != nil || user == nil {
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "user not found")
		return
	}
	if user.Status == common.UserStatusDisabled {
		writeHeartbeatRevoked(c, http.StatusUnauthorized, "user disabled")
		return
	}

	// 6. Tenant cross-check. Only enforce when the multi-tenant route is hit
	//    (`/api/v2/:tenant_slug/user/heartbeat`); the single-tenant fallback
	//    `/api/v2/switch/heartbeat` has no tenant_slug and is allowed.
	if tenantSlug := c.Param("tenant_slug"); tenantSlug != "" {
		tenant, tenantErr := repo.GetTenantBySlug(tenantSlug)
		if tenantErr != nil || tenant == nil {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "tenant not found",
			})
			return
		}
		if token.TenantId != tenant.Id {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "token does not belong to this tenant",
			})
			return
		}
	}

	// 6b. Owning-tenant gate. Applies to BOTH routes (it sits outside the
	//     slug branch above) because the single-tenant fallback carries the
	//     same token, with the same tenant on it. Before this, a suspended
	//     tenant's EndUser kept being told "active" every five minutes while
	//     every relay call it made was already refused by the token gate in
	//     middleware/auth.go — the heartbeat was the one surface that still
	//     said the account was fine. repo.TenantGate carries the shared
	//     rules ("default"/"" exempt, fail-OPEN on a transient lookup fault,
	//     TENANT_MISSING_MODE for a soft-deleted row).
	//
	//     403 is a new shape for this endpoint: 2c-gui-switch's heartbeat
	//     client (internal/redemption/heartbeat.go callHub) hard-maps 401 to
	//     "revoked" and 404 to "legacy hub, keep working"; anything else with
	//     success=false surfaces as a rejection it does not latch on, which is
	//     the intended "locked, not revoked" reading. Whether it gets its own
	//     UI state is the switch owner's call (O-heartbeat).
	if ok, reason := repo.TenantGate(token.TenantId); !ok {
		common.SysLog("heartbeat: refused, tenant gate closed tenant=" +
			token.TenantId + " reason=" + reason)
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "owning tenant is disabled or suspended",
			"error_code": switchTenantDisabledCode,
			"data": gin.H{
				"status": heartbeatStatusSuspended,
			},
		})
		return
	}

	// 7. Active path. Best-effort update accessed_time (last-seen) so the
	//    operator UI can show heartbeat liveness. The update is synchronous
	//    but errors are logged-and-swallowed — heartbeat is 5min-cadence, a
	//    single failed write must never break the EndUser session.
	token.AccessedTime = common.GetTimestamp()
	if err := token.SelectUpdate(); err != nil {
		common.SysLog("heartbeat: failed to update accessed_time: " + err.Error())
	}

	writeHeartbeatStatus(c, heartbeatStatusActive, token)
}

// writeHeartbeatStatus emits a 200 envelope with the standard data payload.
// Centralized so the field shape stays in sync across active/expired branches.
func writeHeartbeatStatus(c *gin.Context, status string, token *repo.Token) {
	data := gin.H{
		"status": status,
	}
	if token != nil {
		if token.ExpiredTime != -1 {
			data["expires_at"] = token.ExpiredTime
		}
		if !token.UnlimitedQuota {
			// Switch reads `quota` as remaining quota (its UI label) — emit
			// remain_quota, not the larger lifetime allocation.
			data["quota"] = token.RemainQuota
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// writeHeartbeatRevoked emits a 401 with the revoked sentinel. Switch's
// heartbeat client short-circuits on the HTTP status code alone, but we
// include the envelope so any debugging tool (curl, mitmproxy) sees a
// well-formed body too.
func writeHeartbeatRevoked(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"message": message,
		"data": gin.H{
			"status": heartbeatStatusRevoked,
		},
	})
}
