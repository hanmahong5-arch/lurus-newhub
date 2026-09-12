package middleware

// root_jwt_stepup_test.go — L6 (2026-09-12): proves the composed
// RootJWTAuth() + SecureVerificationRequired() chain that guards
// POST /api/v2/admin/security/users/:id/totp/force-disable
// (router/api-v2-router.go, handler.ForceDisableTotpV2) never lets a bare
// Bearer JWT through to the step-up gate.
//
// RootJWTAuth's Bearer-JWT branch (admin_jwt_auth.go) validates the token,
// checks the "root" role, and sets admin_sub/admin_email/admin_roles — but
// never the "id" context key. SecureVerificationRequired reads exactly that
// key to find the session's step-up timestamp, so a caller who authenticates
// via a genuinely valid, correctly-signed root JWT (no session cookie) 401s
// on "未登录" (GetVerificationStatus's own message) before ever reaching the
// handler — never a session-only gate that a stolen root JWT alone could
// walk through. This reuses the real OIDC JWKS test harness
// (setupIntegrationTest / adminClaims / oidc_auth_integration_test.go,
// oidc_admin_cover_test.go) so the token is genuinely signed and validated,
// not a hand-built claims struct standing in for the real code path.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// mountRootForceDisableStepUp mirrors mountJWT but adds the cookie-session
// middleware SecureVerificationRequired needs (mountJWT's bare gin.New()
// has none, so this cannot reuse it directly).
func mountRootForceDisableStepUp() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("root-jwt-stepup-test-secret"))
	r.Use(sessions.Sessions("session", store))
	r.POST("/admin/force-disable", RootJWTAuth(), SecureVerificationRequired(),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestAdminTotpForceDisable_BearerJWTRoot401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"root": map[string]interface{}{}}))
	r := mountRootForceDisableStepUp()

	req := httptest.NewRequest(http.MethodPost, "/admin/force-disable", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a Bearer-JWT root caller with no stepped-up session; body=%s",
			w.Code, w.Body.String())
	}
	// Distinguish "RootJWTAuth itself rejected the token" (would be a
	// different message, e.g. "unauthorized" / "Root role required") from
	// "RootJWTAuth passed it through and SecureVerificationRequired refused
	// for lack of a session" — only the latter proves this test's claim.
	body := w.Body.String()
	if !strings.Contains(body, `"success":false`) || !strings.Contains(body, "未登录") {
		t.Fatalf("body = %s, want the SecureVerificationRequired session-missing message (未登录), "+
			"not a RootJWTAuth-level rejection — otherwise this test cannot tell the two apart", body)
	}
}
