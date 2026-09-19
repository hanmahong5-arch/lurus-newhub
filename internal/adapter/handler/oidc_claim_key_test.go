package handler

// oidc_claim_key_test.go — L9 (cycle 12): the browser OIDC callback must read
// the organization claim through the SAME deploy-configured key resolver the
// bearer-JWT middleware uses.
//
// IDTokenClaims' doc comment (oauth.go) says provider-specific
// claim keys "are injected at deploy time via OIDC_CLAIM_* env vars and
// overlaid by the middleware's configurable-claim resolver". That was true for
// middleware.OIDCAuth (applyConfigurableClaims) and false here:
// validateIDToken decoded the token into IDTokenClaims and stopped, so on a
// deploy whose IdP advertises the organization under a URN key,
// claims.OrgID was empty on every browser login — which silently disables the
// org checks (auto-create tenant, and the org-binding guard added by this
// lane) rather than failing loudly.

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/golang-jwt/jwt/v5"
)

// signMapClaimsIDToken signs an arbitrary claim map with the harness key/kid,
// so a test can put a claim under a key no Go struct tag names.
func (h *oidcCallbackTestHarness) signMapClaimsIDToken(t *testing.T, extra map[string]interface{}) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": h.issuer,
		"aud": h.clientID,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = oidcCallbackTestKid
	signed, err := token.SignedString(h.privateKey)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	return signed
}

// TestValidateIDToken_OrgIDFromConfiguredClaimKey is the headline lock: with
// OIDC_CLAIM_ORG_ID pointed at a URN key, the org id in the token under THAT
// key is what validateIDToken returns.
func TestValidateIDToken_OrgIDFromConfiguredClaimKey(t *testing.T) {
	// Registered before the first t.Setenv so it runs LAST (cleanups are LIFO):
	// the env must already be restored when the claim keys are re-read.
	t.Cleanup(middleware.ReloadOIDCClaimKeys)
	t.Setenv("OIDC_CLAIM_ORG_ID", "urn:example:org:id")

	h := setupOIDCCallbackHarness(t, 5150)
	defer h.cleanup()

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":                "claim-key-subject-1",
		"email":              "claim-key-1@example.com",
		"urn:example:org:id": "org-X",
	})

	claims, err := validateIDToken(signed, "")
	if err != nil {
		t.Fatalf("validateIDToken: %v", err)
	}
	if claims.OrgID != "org-X" {
		t.Errorf("claims.OrgID = %q, want %q — the configured OIDC_CLAIM_ORG_ID key was not read on the callback path",
			claims.OrgID, "org-X")
	}
}

// TestValidateIDToken_ConfiguredKeyWins pins precedence when a token carries
// BOTH the neutral key and the configured one: the deploy-configured key is
// the authority, matching middleware.applyConfigurableClaims.
func TestValidateIDToken_ConfiguredKeyWins(t *testing.T) {
	t.Cleanup(middleware.ReloadOIDCClaimKeys)
	t.Setenv("OIDC_CLAIM_ORG_ID", "urn:example:org:id")

	h := setupOIDCCallbackHarness(t, 5151)
	defer h.cleanup()

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":                "claim-key-subject-2",
		"email":              "claim-key-2@example.com",
		"org_id":             "org-neutral",
		"urn:example:org:id": "org-configured",
	})

	claims, err := validateIDToken(signed, "")
	if err != nil {
		t.Fatalf("validateIDToken: %v", err)
	}
	if claims.OrgID != "org-configured" {
		t.Errorf("claims.OrgID = %q, want %q (configured key must win over the neutral one)",
			claims.OrgID, "org-configured")
	}
}

// TestValidateIDToken_NeutralClaimKeyStillDecoded is the negative control: on
// a deploy that configures nothing, the neutral struct tag keeps populating
// OrgID. Without this case the overlay could be made to pass by always
// clearing the field.
func TestValidateIDToken_NeutralClaimKeyStillDecoded(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 5152)
	defer h.cleanup()

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":    "claim-key-subject-3",
		"email":  "claim-key-3@example.com",
		"org_id": "org-neutral",
	})

	claims, err := validateIDToken(signed, "")
	if err != nil {
		t.Fatalf("validateIDToken: %v", err)
	}
	if claims.OrgID != "org-neutral" {
		t.Errorf("claims.OrgID = %q, want %q (neutral key path must keep working)", claims.OrgID, "org-neutral")
	}
}
