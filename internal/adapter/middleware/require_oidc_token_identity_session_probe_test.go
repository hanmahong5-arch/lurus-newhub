package middleware

// Adversarial probes for the identity-session fallback added to
// RequireOIDCToken. Each one asks "can this branch be turned into an
// open door?" — an unconfigured secret, an expired session, a foreign
// issuer, or an alg/shape trick must all still 401.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// probeMintSession mints an HS256 session token with caller-chosen issuer,
// subject and expiry so the probes can vary one claim at a time.
func probeMintSession(secret, issuer, sub string, exp int64) string {
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header := enc([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(map[string]any{"iss": issuer, "sub": sub, "exp": exp})
	body := header + "." + enc(payloadJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + enc(mac.Sum(nil))
}

func probeCall(t *testing.T, token string) int {
	t.Helper()
	router := requireTokenRouter()
	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

// PROBE 1 (the big one): IDENTITY_SESSION_SECRET unset. An HMAC over an
// empty key is still a well-formed MAC an attacker can compute offline, so
// if the fallback ever validated with an empty secret, anybody could mint
// themselves a session token and drain the shared Tavily budget.
func TestProbe_RequireOIDCToken_EmptyIdentitySecret_Rejects(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = ""
	defer func() { common.IdentitySessionSecret = prev }()

	// Attacker mints against the empty secret they know the server holds.
	token := probeMintSession("", "lurus-platform", "lurus:99001", time.Now().Add(time.Hour).Unix())
	if code := probeCall(t, token); code != http.StatusUnauthorized {
		t.Fatalf("empty IDENTITY_SESSION_SECRET must fail closed, got %d", code)
	}
}

// PROBE 2: an expired session token must not be admitted by the fallback.
func TestProbe_RequireOIDCToken_ExpiredIdentitySession_Rejects(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = "probe-secret"
	defer func() { common.IdentitySessionSecret = prev }()

	token := probeMintSession("probe-secret", "lurus-platform", "lurus:99002", time.Now().Add(-time.Minute).Unix())
	if code := probeCall(t, token); code != http.StatusUnauthorized {
		t.Fatalf("expired identity session token must be rejected, got %d", code)
	}
}

// PROBE 3: a correctly-signed token from some *other* issuer must not pass.
func TestProbe_RequireOIDCToken_ForeignIssuerSession_Rejects(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = "probe-secret"
	defer func() { common.IdentitySessionSecret = prev }()

	token := probeMintSession("probe-secret", "evil-idp", "lurus:99003", time.Now().Add(time.Hour).Unix())
	if code := probeCall(t, token); code != http.StatusUnauthorized {
		t.Fatalf("foreign-issuer session token must be rejected, got %d", code)
	}
}

// PROBE 4: alg=none / unsigned shape must not slip through either verifier.
func TestProbe_RequireOIDCToken_AlgNoneSession_Rejects(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = "probe-secret"
	defer func() { common.IdentitySessionSecret = prev }()

	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	payload, _ := json.Marshal(map[string]any{
		"iss": "lurus-platform",
		"sub": "lurus:99004",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token := fmt.Sprintf("%s.%s.", enc([]byte(`{"alg":"none","typ":"JWT"}`)), enc(payload))
	if code := probeCall(t, token); code != http.StatusUnauthorized {
		t.Fatalf("alg=none token must be rejected, got %d", code)
	}
}

// PROBE 5: sub must be a positive lurus account. "lurus:0" / negative ids
// must not be admitted (the middleware also guards accountID > 0).
func TestProbe_RequireOIDCToken_ZeroAccountSession_Rejects(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = "probe-secret"
	defer func() { common.IdentitySessionSecret = prev }()

	for _, sub := range []string{"lurus:0", "lurus:-5", "0", "admin"} {
		token := probeMintSession("probe-secret", "lurus-platform", sub, time.Now().Add(time.Hour).Unix())
		if code := probeCall(t, token); code != http.StatusUnauthorized {
			t.Fatalf("sub=%q must be rejected, got %d", sub, code)
		}
	}
}

// PROBE 6: the gate must stay shut when OIDC is disabled — the new branch
// must not be reachable ahead of the oidcEnabled check.
func TestProbe_RequireOIDCToken_DisabledIgnoresSessionToken(t *testing.T) {
	prevEnabled := oidcEnabled
	oidcEnabled = false
	defer func() { oidcEnabled = prevEnabled }()

	prev := common.IdentitySessionSecret
	common.IdentitySessionSecret = "probe-secret"
	defer func() { common.IdentitySessionSecret = prev }()

	token := probeMintSession("probe-secret", "lurus-platform", "lurus:99005", time.Now().Add(time.Hour).Unix())
	if code := probeCall(t, token); code != http.StatusServiceUnavailable {
		t.Fatalf("OIDC disabled must 503 regardless of session token, got %d", code)
	}
}
