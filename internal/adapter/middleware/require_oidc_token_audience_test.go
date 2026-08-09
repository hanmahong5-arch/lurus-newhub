package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/golang-jwt/jwt/v5"
)

// The consumer gate (RequireOIDCToken) used to verify signature + issuer only.
// A valid issuer says the IdP minted the token; it does NOT say the token was
// minted FOR this service — so a token issued to ANY other client under the
// same issuer (every other Lurus product) could be replayed here and spend the
// shared upstream quota this gate protects.
//
// The fix cannot simply reuse OIDCAuth's checkAudience: consumer apps hold
// their OWN client_id, so a legitimate Lutu APP token never carries newhub's
// OIDC_CLIENT_ID. Hence OIDC_CONSUMER_AUDIENCES plus the three-state
// OIDC_CONSUMER_AUD_REQUIRED rollout flag. These tests pin both halves: the
// attack is rejected under enforce, and every legitimate caller keeps working.

// withConsumerAudiences sets the consumer allow-list for one test and restores
// whatever the previous test left behind (the package globals are shared).
func withConsumerAudiences(t *testing.T, auds ...string) {
	t.Helper()
	prev := oidcConsumerAudiences
	oidcConsumerAudiences = auds
	t.Cleanup(func() { oidcConsumerAudiences = prev })
}

func consumerTokenWithAudience(t *testing.T, ctx *integrationTestContext, aud ...string) string {
	t.Helper()
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "lutu_consumer",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "consumer@lutu.example",
	}
	if len(aud) > 0 {
		claims.Audience = jwt.ClaimStrings(aud)
	}
	return ctx.createJWT(t, claims)
}

func doConsumerRequest(t *testing.T, token string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	requireTokenRouter().ServeHTTP(w, req)
	return w.Code
}

// THE ATTACK: a token minted for an unrelated client under the same issuer.
// Under enforce it must be rejected — this is the whole point of the check.
func TestConsumerGate_Enforce_ForeignClientAudience_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "some-other-product-client-id"))
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token minted for a different client, got %d", code)
	}
}

// A token carrying no audience at all is the same class of problem and must
// not slip through the len(aud)==0 hole.
//
// This one cannot go through ctx.createJWT: that helper stamps the client_id
// whenever a test leaves "aud" empty (so the harness mints realistic tokens),
// which would make an audience-less token indistinguishable from a legitimate
// one. Sign directly to get the shape a real IdP could emit.
func TestConsumerGate_Enforce_NoAudience_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "lutu_consumer",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	token.Header["kid"] = "test-integration-kid"
	signed, err := token.SignedString(ctx.PrivateKey)
	if err != nil {
		t.Fatalf("failed to sign audience-less JWT: %v", err)
	}

	if code := doConsumerRequest(t, signed); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token with no audience, got %d", code)
	}
}

// The legitimate consumer caller: its own client_id is allow-listed via
// OIDC_CONSUMER_AUDIENCES. Enforcing must not lock out the Lutu APP.
func TestConsumerGate_Enforce_AllowListedConsumerAudience_200(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "lutu-app-client-id"))
	if code != http.StatusOK {
		t.Fatalf("expected 200 for an allow-listed consumer audience, got %d", code)
	}
}

// newhub's own client_id stays acceptable here too (checkAudience path), so a
// token issued directly to this service is not collateral damage.
func TestConsumerGate_Enforce_OwnClientIDAudience_200(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, oidcClientID))
	if code != http.StatusOK {
		t.Fatalf("expected 200 for this service's own client_id as audience, got %d", code)
	}
}

// A multi-audience token passes as long as ONE entry is allow-listed — IdPs
// routinely stamp both a project id and a client id.
func TestConsumerGate_Enforce_OneOfSeveralAudiencesAllowListed_200(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "project-1234", "lutu-app-client-id"))
	if code != http.StatusOK {
		t.Fatalf("expected 200 when one of several audiences is allow-listed, got %d", code)
	}
}

// GUARD: log mode (the default) must stay behaviourally identical to the
// pre-flag gate — it observes, it does not reject. This is what makes turning
// the check on safe before the allow-list has been populated.
func TestConsumerGate_LogMode_ForeignAudienceStillAdmitted(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t)
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredLog)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "some-other-product-client-id"))
	if code != http.StatusOK {
		t.Fatalf("log mode must admit the request (observe-only), got %d", code)
	}
}

// GUARD: off mode is the legacy issuer-only gate, byte-identical to before.
func TestConsumerGate_OffMode_ForeignAudienceStillAdmitted(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t)
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredOff)

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "some-other-product-client-id"))
	if code != http.StatusOK {
		t.Fatalf("off mode must admit the request (legacy behaviour), got %d", code)
	}
}

// GUARD: a typo in the env must degrade to log, never to enforce — a
// misconfigured deploy must not silently 401 every consumer caller.
func TestConsumerGate_UnknownFlagValue_DegradesToLogNotEnforce(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t)
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", "enfroce") // deliberate typo

	code := doConsumerRequest(t, consumerTokenWithAudience(t, ctx, "some-other-product-client-id"))
	if code != http.StatusOK {
		t.Fatalf("an unrecognized flag value must degrade to log (admit), got %d", code)
	}
}

// The issuer check must keep firing before/independently of the audience one:
// an allow-listed audience does not excuse a wrong issuer.
func TestConsumerGate_Enforce_AllowListedAudienceButWrongIssuer_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	withConsumerAudiences(t, "lutu-app-client-id")
	t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", setting.ConsumerAudRequiredEnforce)

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://evil.example",
			Subject:   "lutu_consumer",
			Audience:  jwt.ClaimStrings{"lutu-app-client-id"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	if code := doConsumerRequest(t, ctx.createJWT(t, claims)); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong issuer even with an allow-listed audience, got %d", code)
	}
}
