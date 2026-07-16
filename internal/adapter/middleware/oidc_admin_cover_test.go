package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// These tests reuse the OIDC integration harness (setupIntegrationTest /
// ctx.createJWT in oidc_auth_integration_test.go) which stands up a real
// JWKS server, RSA signing key, and wires oidcEnabled/jwksManager/oidcIssuers.
// That lets the admin JWT path and VerifyIDTokenWithJWKS run against genuinely
// signed tokens rather than mocks.

func adminClaims(issuer string, roles map[string]interface{}) OIDCClaims {
	return OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:             "integration@test.local",
		PreferredUsername: "integration_user",
		Roles:             roles,
	}
}

func mountJWT(mw gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/admin", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func doBearer(r *gin.Engine, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAdminJWTAuth_ValidAdminRole_Passes(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"admin": map[string]interface{}{}}))
	w := doBearer(mountJWT(AdminJWTAuth()), token)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for valid admin JWT; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminJWTAuth_NonAdminRole_403(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"viewer": map[string]interface{}{}}))
	w := doBearer(mountJWT(AdminJWTAuth()), token)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for non-admin role; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminJWTAuth_InvalidIssuer_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims("https://evil.issuer", map[string]interface{}{"admin": map[string]interface{}{}}))
	w := doBearer(mountJWT(AdminJWTAuth()), token)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for issuer mismatch; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminJWTAuth_TamperedToken_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"admin": map[string]interface{}{}}))
	// Corrupt the signature segment.
	w := doBearer(mountJWT(AdminJWTAuth()), token+"tampered")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for tampered signature; body=%s", w.Code, w.Body.String())
	}
}

func TestRootJWTAuth_ValidRoot_Passes(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"root": map[string]interface{}{}}))
	w := doBearer(mountJWT(RootJWTAuth()), token)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for valid root JWT; body=%s", w.Code, w.Body.String())
	}
}

func TestRootJWTAuth_NonRoot_403(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	// admin role is NOT sufficient for RootJWTAuth.
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"admin": map[string]interface{}{}}))
	w := doBearer(mountJWT(RootJWTAuth()), token)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for admin-only token on root route; body=%s", w.Code, w.Body.String())
	}
}

func TestVerifyIDTokenWithJWKS_Valid(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	token := ctx.createJWT(t, adminClaims(ctx.Issuer, nil))
	var claims OIDCClaims
	tok, err := VerifyIDTokenWithJWKS(token, &claims)
	if err != nil {
		t.Fatalf("VerifyIDTokenWithJWKS returned error for valid token: %v", err)
	}
	if tok == nil || !tok.Valid {
		t.Errorf("expected valid token")
	}
	if claims.Email != "integration@test.local" {
		t.Errorf("claims not populated: email=%q", claims.Email)
	}
}

func TestVerifyIDTokenWithJWKS_BadToken(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	var claims OIDCClaims
	if _, err := VerifyIDTokenWithJWKS("not-a-jwt", &claims); err == nil {
		t.Errorf("expected error for malformed token")
	}
}

func TestCheckAudience(t *testing.T) {
	prevAud := oidcAllowedAudiences
	prevClient := oidcClientID
	defer func() {
		oidcAllowedAudiences = prevAud
		oidcClientID = prevClient
	}()

	oidcClientID = "svc-client"

	// This service's own client_id is always an accepted audience, even with no
	// extra allow-list configured — a legitimate newhub token still passes.
	oidcAllowedAudiences = nil
	if !checkAudience(jwt.ClaimStrings{"svc-client"}) {
		t.Errorf("client_id audience must be accepted without any allow-list")
	}
	// SECURITY: a token minted for a DIFFERENT client under the same issuer,
	// with no allow-list configured, must be rejected (regression guard for the
	// default-open audience bypass).
	if checkAudience(jwt.ClaimStrings{"other-client"}) {
		t.Errorf("foreign audience must be rejected when no allow-list is set")
	}
	// A token carrying no audience at all is rejected.
	if checkAudience(jwt.ClaimStrings{}) {
		t.Errorf("empty audience must be rejected")
	}

	// With an extra allow-list, both the client_id and listed audiences pass;
	// anything else is rejected.
	oidcAllowedAudiences = []string{"aud-a", "aud-b"}
	if !checkAudience(jwt.ClaimStrings{"aud-b"}) {
		t.Errorf("allow-listed audience must be accepted")
	}
	if !checkAudience(jwt.ClaimStrings{"svc-client"}) {
		t.Errorf("client_id must still be accepted alongside an allow-list")
	}
	if checkAudience(jwt.ClaimStrings{"aud-x"}) {
		t.Errorf("audience matching neither client_id nor allow-list must be rejected")
	}

	// Defensive: an empty client_id must not degrade into silent accept-all.
	oidcClientID = ""
	oidcAllowedAudiences = nil
	if checkAudience(jwt.ClaimStrings{"anything"}) {
		t.Errorf("empty client_id + empty allow-list must fail closed, not accept all")
	}
}

func TestResolveOIDCClaims_CustomKeys(t *testing.T) {
	prev := oidcClaimKeys
	defer func() { oidcClaimKeys = prev }()
	// Point the org-id key at a non-default claim name so resolveOIDCClaims
	// actually reads from the raw map.
	oidcClaimKeys = claimKeySet{
		OrgID:             "custom_org",
		OrgDomain:         "org_domain",
		ResourceOwnerID:   "resource_owner_id",
		ResourceOwnerName: "resource_owner_name",
		Roles:             "roles",
	}
	var c OIDCClaims
	resolveOIDCClaims(&c, map[string]interface{}{"custom_org": "org-xyz"})
	if c.OrgID != "org-xyz" {
		t.Errorf("OrgID = %q, want org-xyz (resolved from custom claim key)", c.OrgID)
	}

	// nil guards must not panic.
	resolveOIDCClaims(nil, map[string]interface{}{"x": "y"})
	resolveOIDCClaims(&c, nil)
}
