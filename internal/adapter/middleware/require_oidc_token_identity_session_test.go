package middleware

// require_oidc_token_identity_session_test.go — pins the fix for the lutu
// search gate rejecting phone/password-login lurus-platform callers.
//
// Background (contracts.md:572): a caller who logged into lutu via phone
// verification code or account/password holds a lurus-platform HS256 session
// token (common.ValidateIdentitySessionToken), not an OIDC-issued JWT — there
// is no JWKS keypair to verify it against. With OIDC_ENABLED=true,
// RequireOIDCToken previously only tried JWKS verification, so those callers
// got 401 on POST /api/v2/lutu/search even though they are legitimately
// logged-in Lurus users. Owner-decided fix: after JWKS verification fails,
// also try the identity session token verifier before rejecting; either path
// succeeding admits the request, and both failing still 401s (fail-closed).
//
// TestRequireOIDCToken_ValidToken_NoTenantMapping_Passes (require_oidc_token_test.go)
// already pins case (a): a legitimate OIDC JWT keeps working. This file pins
// (b) and (c).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// (b) A valid lurus-platform identity session token now passes the gate.
// Before this fix, this exact request got 401 — see the "before" run recorded
// in the task report; this test would have failed (expected 200, got 401)
// against the pre-fix middleware.
func TestRequireOIDCToken_ValidIdentitySessionToken_Passes(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	prevSecret := common.IdentitySessionSecret
	common.IdentitySessionSecret = "session-secret-lutu-search-gate"
	defer func() { common.IdentitySessionSecret = prevSecret }()

	const acct = int64(88012)
	token := mintIdentitySessionToken(common.IdentitySessionSecret, acct)

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid identity session token (phone/password lutu login), got %d, body: %s",
			w.Code, w.Body.String())
	}
}

// (c) Neither a valid OIDC token nor a valid identity session token: still 401.
// This is the fail-closed guard — the new fallback branch must not turn into
// an "anything goes" path.
func TestRequireOIDCToken_NeitherOIDCNorIdentitySession_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	prevSecret := common.IdentitySessionSecret
	common.IdentitySessionSecret = "session-secret-lutu-search-gate"
	defer func() { common.IdentitySessionSecret = prevSecret }()

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt-and-not-a-session-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when neither verifier accepts the token, got %d, body: %s",
			w.Code, w.Body.String())
	}
}

// Guard: an identity session token signed with the WRONG secret must still be
// rejected — the fallback must verify the signature, not merely try to parse
// the token shape.
func TestRequireOIDCToken_IdentitySessionToken_WrongSecret_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	prevSecret := common.IdentitySessionSecret
	common.IdentitySessionSecret = "session-secret-lutu-search-gate"
	defer func() { common.IdentitySessionSecret = prevSecret }()

	token := mintIdentitySessionToken("attacker-controlled-secret", 88013)

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an identity session token signed with the wrong secret, got %d, body: %s",
			w.Code, w.Body.String())
	}
}
