package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// oidc_session_fallback.go — the browser-cookie arm of OIDCAuth: admit a
// request that carries a console session instead of a bearer token, put the
// resolved account id on the context, and refuse it when the session's
// tenant is disabled. Split out of oidc_auth.go by the cycle-13 wiring pass
// as a pure move (all three functions are byte-identical to the ones that
// stood there); internal/pkg/gates' source-size ratchet holds oidc_auth.go
// at its measured line count, so this cycle's additions to that file are
// paid for by a move rather than by raising the ceiling.
//
// The three belong together: handleSessionFallback is the only caller of the
// other two, and the tenant check it performs is what the v2 routes that do
// not mount TenantSlugGuard rely on (see the exemption table in
// internal/adapter/handler/router/tenant_slug_guard_completeness_test.go).

// handleSessionFallback checks for valid session-based OAuth data when no Bearer token is present.
// Returns true if it handled the request (either successfully authenticated via session, or aborted).
// Returns false if no session auth data is available and caller should try other methods.
// injectSessionAccountID copies the platform account linkage from the gin
// SESSION (set by zita-bootstrap / the legacy OAuth callback) into the gin
// CONTEXT, falling back to the user row's lurus_account_id column. Without
// this, every session-cookie (browser) caller of /api/v2/user/billing/*
// read as "platform account not linked" (503) even though the linkage was
// sitting in both the session and the users table — the Bearer-JWT path was
// the only one that ever populated the context key (live-probed 2026-08-31:
// fresh bridged user, summary+checkout both 503).
func injectSessionAccountID(c *gin.Context, session sessions.Session, user *repo.User) {
	if acct, ok := session.Get("identity_account_id").(int64); ok && acct > 0 {
		c.Set("identity_account_id", acct)
		return
	}
	if user != nil && user.LurusAccountID != nil && *user.LurusAccountID > 0 {
		c.Set("identity_account_id", *user.LurusAccountID)
	}
}

// sessionTenantAdmitted runs the shared tenant gate for the cookie arm of
// OIDCAuth. It returns true when the request may proceed; when it returns
// false it has already written the 403 and aborted, so the caller must report
// the request as handled.
//
// Shape matches authHelper's refusal (auth.go) so a console client sees one
// error_code for "your tenant is locked" no matter which authentication arm
// answered it.
func sessionTenantAdmitted(c *gin.Context, tenantID string) bool {
	if ok, reason := repo.TenantGate(tenantID); !ok {
		common.SysLog("oidc session fallback: refused, tenant gate closed tenant=" +
			tenantID + " reason=" + reason)
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "owning tenant is disabled or suspended",
			"error_code": "TENANT_DISABLED",
		})
		c.Abort()
		return false
	}
	return true
}

func handleSessionFallback(c *gin.Context) bool {
	// Check if session middleware is available (prevents panic when session store not configured)
	if _, exists := c.Get(sessions.DefaultKey); !exists {
		return false
	}

	session := sessions.Default(c)

	// Check for session user ID (set during OAuth callback)
	sessionID := session.Get("id")
	if sessionID == nil {
		return false // No session, let caller handle
	}

	userID, ok := sessionID.(int)
	if !ok || userID == 0 {
		return false
	}

	// Check if session has OAuth access token
	accessToken, _ := session.Get("oauth_access_token").(string)
	expiresAt, _ := session.Get("oauth_token_expires_at").(int64)

	// If access token exists and is not expired, use it to validate
	if accessToken != "" && expiresAt > time.Now().Unix() {
		// Valid session with non-expired OAuth token
		// Build tenant context from session data
		user, err := repo.GetUserById(userID, false)
		if err != nil {
			common.SysError(fmt.Sprintf("Session fallback: failed to get user %d: %v", userID, err))
			return false
		}

		// SECURITY: reject a disabled/banned user even though their session
		// cookie is still valid — mirrors authHelper (auth.go).
		if user.Status == common.UserStatusDisabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "account disabled",
			})
			c.Abort()
			return true
		}

		// SECURITY (cross-tenant isolation): scope the tenant to the
		// authenticated user's OWN record — never the URL :tenant_slug.
		// Deriving it from the slug let any session-authenticated user
		// assume another tenant's context just by changing the path; worse,
		// downstream guards compare tenantCtx.TenantID against that same
		// slug (e.g. GetCreditPoolForEndUser's TENANT_MISMATCH check), so
		// the slug was being compared to itself and always passed. Mirrors
		// authHelper (auth.go), which scopes tenant via the user's TenantId.
		tenantID := user.TenantId
		if tenantID == "" {
			tenantID = "default"
		}

		// Tenant gate (cycle-13 L9): the Bearer arm of OIDCAuth refuses a
		// suspended tenant inside mapOIDCUserToLurus, and authHelper refuses
		// one on the session/access-token path, but this arm — the browser
		// cookie arm every console page uses once the OAuth token is in the
		// session — checked only the USER's status. A tenant an operator
		// suspended kept serving its members' console reads (the credit-pool
		// readback among them) until their session expired. repo.TenantGate
		// holds the shared rules: "default"/"" exempt, fail-OPEN on a
		// transient lookup fault, TENANT_MISSING_MODE for a deleted row.
		if !sessionTenantAdmitted(c, tenantID) {
			return true
		}

		// Construct tenant context from session
		tenantCtx := &TenantContext{
			TenantID:   tenantID,
			UserID:     user.Id,
			IDPSubject: "", // Not available in session
			Email:      user.Email,
			Username:   user.Username,
			Roles:      []string{},
		}

		// Inject into gin context
		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", user.Id)
		c.Set("id", user.Id)
		injectSessionAccountID(c, session, user)

		c.Next()
		return true
	}

	// Session exists but token expired or missing — try constructing from session data directly
	if userID > 0 {
		user, err := repo.GetUserById(userID, false)
		if err != nil {
			return false
		}

		// SECURITY: reject a disabled/banned user (see non-expired branch above).
		if user.Status == common.UserStatusDisabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "account disabled",
			})
			c.Abort()
			return true
		}

		// SECURITY (cross-tenant isolation): see the non-expired branch
		// above — tenant comes from the user's own record, not the URL
		// :tenant_slug, so a session user cannot read another tenant.
		tenantID := user.TenantId
		if tenantID == "" {
			tenantID = "default"
		}

		// Tenant gate: see the non-expired branch above. Both arms need it —
		// this one serves the same browser with the same cookie once the
		// stored OAuth token has aged out.
		if !sessionTenantAdmitted(c, tenantID) {
			return true
		}

		tenantCtx := &TenantContext{
			TenantID:   tenantID,
			UserID:     user.Id,
			IDPSubject: "",
			Email:      user.Email,
			Username:   user.Username,
			Roles:      []string{},
		}

		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", user.Id)
		c.Set("id", user.Id)
		injectSessionAccountID(c, session, user)

		c.Next()
		return true
	}

	return false
}
