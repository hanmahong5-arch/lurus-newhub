/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// redisDeleteSessionKey deletes the Redis-backed session store's own key for
// sessionKey ("session_"+key — the boj/redistore default prefix; pinned by
// cmd/server's TestRedisSessionKeyFormat_Canary). This is the authoritative
// logout for the Redis session store: the row's revoked_at flag (defence in
// depth, checked in authHelper) only covers the brief window between this
// call and a replica actually seeing the deletion. A no-op when Redis is not
// configured (cookie-only deployments never register a row in the first
// place, so there is nothing to delete) or sessionKey is empty.
func redisDeleteSessionKey(c *gin.Context, sessionKey string) {
	if sessionKey == "" || !common.RedisEnabled || common.RDB == nil {
		return
	}
	if err := common.RedisDel(c.Request.Context(), "session_"+sessionKey); err != nil {
		common.SysLog("session revoke: RedisDel failed for session_" + sessionKey + ": " + err.Error())
	}
}

// RevokeCurrentSessionV2 terminates the calling user's current session.
// Route (registered in api-v2-router.go):
//
//	DELETE /api/v2/:tenant_slug/sessions/current
//	Auth: UserAuth middleware
//
// Revocation clears the gin session cookie and the platform lurus_session
// cookie: RediStore.Save with MaxAge<=0 (what middleware.SessionClearOptions
// below triggers) deletes the store's own Redis key itself, and both UserAuth (v1
// session) and the OIDC bridge read the session from the same cookie, so
// clearing it makes every subsequent authenticated endpoint return 401 until
// the user logs in again. When SESSION_REGISTRY_ENABLED is on, this
// additionally marks the row's own reason as "logout" (best-effort — a
// failure here must never block the logout response) purely so the
// registry/audit trail records WHY the session ended, distinct from a
// user-initiated revoke-by-id of a DIFFERENT device.
func RevokeCurrentSessionV2(c *gin.Context) {
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	session := sessions.Default(c)
	sessionKey := currentSessionID(c)

	// Clear the gin session store entry so middleware.UserAuth() rejects the
	// next request from this browser. The attributes come from
	// middleware.SessionClearOptions, not a {Path,MaxAge} literal: a clearing
	// Set-Cookie whose Domain/Secure/SameSite differ from the live cookie's
	// mints a second cookie instead of deleting the session one.
	session.Clear()
	session.Options(middleware.SessionClearOptions())
	_ = session.Save()

	if repo.SessionRegistryEnabled() && sessionKey != "" {
		if err := repo.RevokeUserSessionByKey(sessionKey, entity.SessionRevokeReasonLogout); err != nil {
			common.SysLog("session revoke (logout): RevokeUserSessionByKey failed: " + err.Error())
		}
	}

	// Expire the platform lurus_session cookie on both production (.lurus.cn)
	// and dev/local (host-only) variants. This mirrors ZitaLogout so the
	// identity bridge does not auto-re-login the user on the next /login visit.
	c.SetCookie(zita.SessionCookieName, "", -1, "/", ".lurus.cn", true, true)
	c.SetCookie(zita.SessionCookieName, "", -1, "/", "", true, true)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"redirect": "/login",
		},
	})
}

// RevokeSessionByIDV2 revokes ONE of the caller's own registered devices.
// Route: DELETE /api/v2/:tenant_slug/sessions/:id — Auth: UserAuth middleware.
// With SESSION_REGISTRY_ENABLED off this 404s SESSION_NOT_FOUND without
// touching the DB — a rollback or a row left over from a prior flag-on
// soak cannot make this endpoint act on anything.
//
// Ownership is enforced with the same not-found-shaped IDOR pattern
// repo.GetUserSessionByID documents (mirrors ConsumeTenantInvite/project.go):
// a row belonging to a different user 404s exactly like a row that does not
// exist, so a cross-account probe of session ids learns nothing. Defence in
// depth (the mutation this pairs with): BOTH the DB revoke (checked by
// authHelper's revoked_at lookup) AND the Redis key deletion happen — either
// one alone leaves a real window for the still-valid other mechanism to keep
// admitting the revoked device. Revoking the caller's OWN current device
// additionally clears this request's session cookie (same as
// RevokeCurrentSessionV2) so this browser is not locked out on its next
// request by the very row it just revoked.
func RevokeSessionByIDV2(c *gin.Context) {
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	// With the flag off no row was ever registered — 404 without touching
	// the DB, so a rollback (or a soak-period row left over from a prior
	// flag-on window) cannot make this endpoint act on anything.
	if !repo.SessionRegistryEnabled() {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Session not found",
			"error_code": "SESSION_NOT_FOUND",
		})
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid session id",
			"error_code": "INVALID_SESSION_ID",
		})
		return
	}

	row, err := repo.GetUserSessionByID(id)
	if err != nil {
		common.SysLog("RevokeSessionByIDV2: lookup failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to look up session",
			"error_code": "GATEWAY_INTERNAL",
		})
		return
	}
	if row == nil || row.UserId != userID {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Session not found",
			"error_code": "SESSION_NOT_FOUND",
		})
		return
	}

	if err := repo.RevokeUserSessionRow(row.Id, entity.SessionRevokeReasonUserRevoked); err != nil {
		common.SysLog("RevokeSessionByIDV2: revoke failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to revoke session",
			"error_code": "GATEWAY_INTERNAL",
		})
		return
	}
	redisDeleteSessionKey(c, row.SessionKey)

	// If the caller just revoked THEIR OWN current device (as opposed to a
	// different one of their own devices), also clear this very request's
	// cookie — same as RevokeCurrentSessionV2 — so this browser is not left
	// holding a cookie whose id now maps to a permanently-revoked row (the
	// re-login lockout this pairs with in authHelper's revoked_at check).
	if sid := currentSessionID(c); sid != "" && sid == row.SessionKey {
		session := sessions.Default(c)
		session.Clear()
		session.Options(middleware.SessionClearOptions())
		_ = session.Save()
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userID,
		governance.ActionAuthSessionRevoked, governance.ResourceUser, userID,
		fmt.Sprintf(`{"session_id":%d,"reason":%q}`, row.Id, entity.SessionRevokeReasonUserRevoked)))

	c.Status(http.StatusNoContent)
}

// RevokeOtherSessionsV2 revokes every OTHER active session of the caller —
// "sign out other devices". Route: DELETE /api/v2/:tenant_slug/sessions/others
// — Auth: UserAuth middleware. Registered before /:id in api-v2-router.go
// (a literal path segment, not an addressable resource id — mirrors
// /sessions/current's own exemption in v2_completeness_test.go). With
// SESSION_REGISTRY_ENABLED off this refuses with 409
// SESSION_REGISTRY_DISABLED without touching the DB — a rollback or rows
// left over from a prior flag-on soak cannot make this endpoint revoke
// anything, and "sign out other devices" no longer reports success on a
// deployment where it cannot work (cycle-12 L4).
func RevokeOtherSessionsV2(c *gin.Context) {
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	// With the flag off no rows were ever registered — refuse without
	// touching the DB, so a rollback (or leftover rows from a prior flag-on
	// soak) cannot make this endpoint revoke anything, and the browser is
	// told the feature is off rather than that every other device is gone.
	if !repo.SessionRegistryEnabled() {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "Session registry is disabled on this deployment",
			"error_code": "SESSION_REGISTRY_DISABLED",
		})
		return
	}

	currentKey := currentSessionID(c)
	rows, err := repo.RevokeOtherUserSessions(userID, currentKey, entity.SessionRevokeReasonUserRevokedOthers)
	if err != nil {
		common.SysLog("RevokeOtherSessionsV2: failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to revoke other sessions",
			"error_code": "GATEWAY_INTERNAL",
		})
		return
	}
	for _, r := range rows {
		redisDeleteSessionKey(c, r.SessionKey)
	}

	if len(rows) > 0 {
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userID,
			governance.ActionAuthSessionRevoked, governance.ResourceUser, userID,
			fmt.Sprintf(`{"revoked":%d,"reason":%q}`, len(rows), entity.SessionRevokeReasonUserRevokedOthers)))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"revoked": len(rows),
		},
	})
}
