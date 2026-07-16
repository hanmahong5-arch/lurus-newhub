package handler

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/entverify"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Entitlement-token test minter — mirrors platform entsign's wire format
// (RS256 compact JWS, base64url, kid header), same approach as the entverify
// package's own tests.
// ---------------------------------------------------------------------------

func provB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func provGenKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return k
}

func provSignRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	pb, _ := json.Marshal(claims)
	in := provB64(hb) + "." + provB64(pb)
	digest := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return in + "." + provB64(sig)
}

func provJWKSDoc(kid string, pub *rsa.PublicKey) []byte {
	type jwk struct{ Kty, Use, Alg, Kid, N, E string }
	body, _ := json.Marshal(map[string]any{"keys": []jwk{{
		"RSA", "sig", "RS256", kid,
		provB64(pub.N.Bytes()), provB64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
	return body
}

// provClaims returns a valid llm-api entitlement claim set; callers mutate.
func provClaims(sub string, ent map[string]string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": "https://identity.lurus.cn",
		"sub": sub,
		"aud": entitlementExpectedAud,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(24 * time.Hour).Unix(),
		"ent": ent,
	}
}

// setupProvisionTest boots the shared V2 test DB, points the package verifier
// at a local JWKS stub carrying the test key, and returns a router exposing
// only the provision route (no rate-limit middleware — unit scope).
func setupProvisionTest(t *testing.T) (*gin.Engine, *V2TestContext, *rsa.PrivateKey) {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)

	key := provGenKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(provJWKSDoc("test-kid", &key.PublicKey))
	}))
	t.Cleanup(srv.Close)

	setProvisionVerifier(entverify.New(srv.URL, entverify.WithInsecureJWKS()))
	t.Cleanup(func() { setProvisionVerifier(nil) }) // next test/prod path rebuilds from env

	r := gin.New()
	r.POST("/api/v2/:tenant_slug/provision", ProvisionV2)
	return r, ctx, key
}

func provisionReq(t *testing.T, r *gin.Engine, slug string, body map[string]any) (int, map[string]any) {
	t.Helper()
	w := V2Request(r, http.MethodPost, "/api/v2/"+slug+"/provision", body, nil)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v raw=%s", err, w.Body.String())
	}
	return w.Code, resp
}

// TestProvisionV2_HappyAndIdempotent: first call auto-creates the bridged
// user, mints a bounded relay token from ent.quota, and returns sk-…; the
// second call with the same entitlement token replays the SAME token without
// minting a duplicate.
func TestProvisionV2_HappyAndIdempotent(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990001", map[string]string{"plan_code": "cc_pro", "quota": "120000"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{
		"entitlement_token": tok,
		"fingerprint":       "dev-fp-001",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("missing data: %v", resp)
	}
	if data["replayed"] != false {
		t.Errorf("expected replayed=false, got %v", data["replayed"])
	}
	if data["plan_code"] != "cc_pro" {
		t.Errorf("expected plan_code=cc_pro, got %v", data["plan_code"])
	}
	if data["base_url"] != defaultRelayBaseURL {
		t.Errorf("expected base_url=%s, got %v", defaultRelayBaseURL, data["base_url"])
	}
	firstToken, _ := data["token"].(string)
	if len(firstToken) < 4 || firstToken[:3] != "sk-" {
		t.Fatalf("expected sk- prefixed token, got %q", firstToken)
	}

	// Bridged user exists with the platform account id as the idempotency key.
	user, err := repo.GetUserByLurusAccountID(990001)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	if user.Username != "lurus_990001" {
		t.Errorf("username=%q want lurus_990001", user.Username)
	}

	// Relay token persisted with the quota claim as a bound.
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.UnlimitedQuota {
		t.Error("expected bounded token (unlimited_quota=false) when ent.quota present")
	}
	if row.RemainQuota != 120000 {
		t.Errorf("remain_quota=%d want 120000", row.RemainQuota)
	}
	if "sk-"+row.Key != firstToken {
		t.Errorf("response token does not match persisted key")
	}

	// Replay: same entitlement token → same relay token, no duplicate row.
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d body=%v", code, resp)
	}
	data, _ = resp["data"].(map[string]any)
	if data["replayed"] != true {
		t.Errorf("replay: expected replayed=true, got %v", data["replayed"])
	}
	if data["token"] != firstToken {
		t.Errorf("replay returned a different token")
	}
	var n int64
	repo.DB.Model(&repo.Token{}).Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").Count(&n)
	if n != 1 {
		t.Errorf("replay minted a duplicate relay token: count=%d want 1", n)
	}
}

// TestProvisionV2_NoQuotaClaim: absent ent.quota/amount → user-balance mode
// (unlimited_quota=true relay token).
func TestProvisionV2_NoQuotaClaim(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990002", map[string]string{"plan_code": "cc_basic"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(990002)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_basic").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if !row.UnlimitedQuota {
		t.Error("expected unlimited_quota=true (user-balance mode) when no quota claim")
	}
}

// TestProvisionV2_AudMismatch: a token minted for another product (aud is the
// product_id, not the tenant slug) is rejected 403 AUD_MISMATCH.
func TestProvisionV2_AudMismatch(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	claims := provClaims("990003", map[string]string{"plan_code": "cc_pro"})
	claims["aud"] = "switch" // wrong product
	tok := provSignRS256(t, key, "test-kid", claims)

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "AUD_MISMATCH" {
		t.Errorf("expected AUD_MISMATCH, got %v", resp["error_code"])
	}
}

// TestProvisionV2_PlanNotEligible: plan_code outside the cc_ allowlist → 403,
// and neither a user nor a token is created.
func TestProvisionV2_PlanNotEligible(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990004", map[string]string{"plan_code": "pro_max"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "PLAN_NOT_ELIGIBLE" {
		t.Errorf("expected PLAN_NOT_ELIGIBLE, got %v", resp["error_code"])
	}
	if _, err := repo.GetUserByLurusAccountID(990004); err == nil {
		t.Error("ineligible plan must not create a bridged user")
	}
}

// TestProvisionV2_BadToken: garbage and wrong-key signatures → 401 TOKEN_INVALID.
func TestProvisionV2_BadToken(t *testing.T) {
	r, ctx, _ := setupProvisionTest(t)

	t.Run("garbage", func(t *testing.T) {
		code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": "not.a.jws"})
		if code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%v", code, resp)
		}
		if resp["error_code"] != "TOKEN_INVALID" {
			t.Errorf("expected TOKEN_INVALID, got %v", resp["error_code"])
		}
	})

	t.Run("wrong_signing_key", func(t *testing.T) {
		rogue := provGenKey(t) // NOT in the served JWKS (kid matches, key doesn't)
		tok := provSignRS256(t, rogue, "test-kid",
			provClaims("990005", map[string]string{"plan_code": "cc_pro"}))
		code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
		if code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%v", code, resp)
		}
		if resp["error_code"] != "TOKEN_INVALID" {
			t.Errorf("expected TOKEN_INVALID, got %v", resp["error_code"])
		}
	})

	t.Run("missing_token", func(t *testing.T) {
		code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{})
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%v", code, resp)
		}
		if resp["error_code"] != "MISSING_ENTITLEMENT_TOKEN" {
			t.Errorf("expected MISSING_ENTITLEMENT_TOKEN, got %v", resp["error_code"])
		}
	})
}

// TestProvisionV2_ExpiredToken: past exp + 72h offline grace (+skew) → 401.
func TestProvisionV2_ExpiredToken(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	claims := provClaims("990006", map[string]string{"plan_code": "cc_pro"})
	hardExpired := time.Now().Add(-80 * time.Hour) // > 72h grace + 5m skew ago
	claims["iat"] = hardExpired.Add(-24 * time.Hour).Unix()
	claims["nbf"] = hardExpired.Add(-24 * time.Hour).Unix()
	claims["exp"] = hardExpired.Unix()
	tok := provSignRS256(t, key, "test-kid", claims)

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "TOKEN_INVALID" {
		t.Errorf("expected TOKEN_INVALID, got %v", resp["error_code"])
	}
}

// TestProvisionV2_UnknownTenant: bad slug → 404 before any verification.
func TestProvisionV2_UnknownTenant(t *testing.T) {
	r, _, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990007", map[string]string{"plan_code": "cc_pro"}))

	code, resp := provisionReq(t, r, "no-such-tenant", map[string]any{"entitlement_token": tok})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("expected TENANT_NOT_FOUND, got %v", resp["error_code"])
	}
}
