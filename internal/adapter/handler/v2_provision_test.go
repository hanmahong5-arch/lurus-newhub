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
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
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

// TestProvisionV2_GraceTokenRejected: a token past its hard exp but still
// inside entverify's 72h offline grace verifies (Freshness==Grace, nil
// error) — but bounded-staleness means ProvisionV2 must hard-reject it
// rather than degrade-and-mint: no relay token may be issued or renewed off
// a stale entitlement claim.
func TestProvisionV2_GraceTokenRejected(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	claims := provClaims("990008", map[string]string{"plan_code": "cc_pro", "quota": "1000"})
	graceExp := time.Now().Add(-1 * time.Hour) // past exp+skew, well within the 72h grace
	claims["iat"] = graceExp.Add(-24 * time.Hour).Unix()
	claims["nbf"] = graceExp.Add(-24 * time.Hour).Unix()
	claims["exp"] = graceExp.Unix()
	tok := provSignRS256(t, key, "test-kid", claims)

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "ENTITLEMENT_STALE" {
		t.Errorf("expected ENTITLEMENT_STALE, got %v", resp["error_code"])
	}

	var n int64
	repo.DB.Model(&repo.Token{}).Where("name = ?", "switch-provision-cc_pro").Count(&n)
	if n != 0 {
		t.Errorf("grace-freshness token must not mint a relay token: count=%d want 0", n)
	}
}

// TestProvisionV2_IssuedTokenNotImmortal: the minted relay token's expiry
// must be forward-looking from the entitlement's own exp claim (exp + the
// entverify offline grace margin, never the raw claim verbatim — see
// TestProvisionV2_FreshButPastExp_NeverMintsPastExpiry for why), and never
// -1 (never expires) — an unrenewed plan must eventually stop working on its
// own.
func TestProvisionV2_IssuedTokenNotImmortal(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	claims := provClaims("990009", map[string]string{"plan_code": "cc_pro", "quota": "5000"})
	wantExp := time.Now().Add(24 * time.Hour).Unix()
	claims["exp"] = wantExp
	tok := provSignRS256(t, key, "test-kid", claims)

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(990009)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.ExpiredTime == -1 {
		t.Fatal("issued relay token must not be immortal (expired_time=-1)")
	}
	wantExpiredTime := wantExp + int64(entverify.DefaultGrace/time.Second)
	if row.ExpiredTime != wantExpiredTime {
		t.Errorf("expired_time=%d want %d (claims.exp + entverify.DefaultGrace)", row.ExpiredTime, wantExpiredTime)
	}
}

// TestProvisionV2_FreshButPastExp_NeverMintsPastExpiry: entverify grades a
// token Fresh for the WHOLE window exp <= now < exp+skew (5m) — a token can
// be a minute past its raw exp and still verify Fresh. Copying claims.exp
// verbatim into the relay token's expired_time would then mint a
// dead-on-arrival credential (already expired by the time the client makes
// its first relay call). The minted token's expiry must always be in the
// future.
func TestProvisionV2_FreshButPastExp_NeverMintsPastExpiry(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	claims := provClaims("990011", map[string]string{"plan_code": "cc_pro", "quota": "1000"})
	pastExp := time.Now().Add(-1 * time.Minute) // past exp, inside the 5m skew: Freshness=Fresh
	claims["iat"] = pastExp.Add(-24 * time.Hour).Unix()
	claims["nbf"] = pastExp.Add(-24 * time.Hour).Unix()
	claims["exp"] = pastExp.Unix()
	tok := provSignRS256(t, key, "test-kid", claims)

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200 (past-exp-but-Fresh must still provision), got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(990011)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.ExpiredTime <= time.Now().Unix() {
		t.Fatalf("minted relay token is dead-on-arrival: expired_time=%d <= now=%d", row.ExpiredTime, time.Now().Unix())
	}
}

// TestProvisionV2_ExpiredEnabledTokenRefreshedNotReminted: the entitlement
// mints on a fixed 24h cadence, so a token that has aged past its own
// expired_time while its Status is still Enabled (the Redis-on production
// shape — the auto-expire status write in repo/token.go never fires) is a
// ROUTINE re-provision, not a rare edge case. ent.quota is a static plan
// attribute that does not shrink with spend, so minting a brand new row here
// would refill remain_quota/used_quota to the full plan quota every cycle —
// a monthly cap becomes a daily one. The existing row must be refreshed in
// place: remain_quota/used_quota carry over untouched.
func TestProvisionV2_ExpiredEnabledTokenRefreshedNotReminted(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990012", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(990012)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}

	// Simulate spend (400 of the original 1000) plus natural expiry with the
	// row still Enabled.
	if err := repo.DB.Model(&repo.Token{}).
		Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").
		Updates(map[string]any{
			"remain_quota": 600,
			"used_quota":   400,
			"expired_time": time.Now().Add(-1 * time.Hour).Unix(),
		}).Error; err != nil {
		t.Fatalf("simulate spend+expiry: %v", err)
	}

	tok2 := provSignRS256(t, key, "test-kid",
		provClaims("990012", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	if code != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["replayed"] != true {
		t.Errorf("expected replayed=true on refresh, got %v", data["replayed"])
	}

	var rows []repo.Token
	repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("expired-but-enabled refresh must not mint a duplicate row: count=%d want 1", len(rows))
	}
	if rows[0].RemainQuota != 600 {
		t.Errorf("remain_quota=%d want 600 (must carry over, not refill to plan quota 1000)", rows[0].RemainQuota)
	}
	if rows[0].UsedQuota != 400 {
		t.Errorf("used_quota=%d want 400 (must carry over)", rows[0].UsedQuota)
	}
	if rows[0].ExpiredTime <= time.Now().Unix() {
		t.Errorf("expired_time=%d must be refreshed into the future", rows[0].ExpiredTime)
	}
}

// TestProvisionV2_AutoStatusNotTreatedAsRevoked: repo/token.go automatically
// flips a token to Expired(3) on natural expiry or Exhausted(4) when a
// bounded token's quota runs out — no human involved, unlike an admin
// Disabled(2). Neither automatic state may be treated as an administrative
// revocation: a paying customer whose plan renews must be able to self-heal
// via a routine provision call, not get locked out of TOKEN_REVOKED forever
// with no self-service recovery.
func TestProvisionV2_AutoStatusNotTreatedAsRevoked(t *testing.T) {
	cases := []struct {
		name   string
		status int
		sub    string
		acctID int64
	}{
		{"expired", common.TokenStatusExpired, "990020", 990020},
		{"exhausted", common.TokenStatusExhausted, "990021", 990021},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ctx, key := setupProvisionTest(t)
			tok := provSignRS256(t, key, "test-kid",
				provClaims(tc.sub, map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
			code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
			if code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%v", code, resp)
			}
			user, err := repo.GetUserByLurusAccountID(tc.acctID)
			if err != nil {
				t.Fatalf("bridged user not created: %v", err)
			}

			if err := repo.DB.Model(&repo.Token{}).
				Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").
				Update("status", tc.status).Error; err != nil {
				t.Fatalf("simulate automatic status %d: %v", tc.status, err)
			}

			tok2 := provSignRS256(t, key, "test-kid",
				provClaims(tc.sub, map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
			code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
			if resp["error_code"] == "TOKEN_REVOKED" {
				t.Fatalf("automatic status %d must not be treated as administrative revocation", tc.status)
			}
			if code != http.StatusOK {
				t.Fatalf("expected 200 (self-heal), got %d body=%v", code, resp)
			}
		})
	}
}

// TestProvisionV2_RevokedTokenNotResurrected: once an admin disables the
// relay token minted for a (user, plan), a later provision call presenting a
// perfectly valid Fresh entitlement token must NOT resurrect it by minting a
// new Enabled row under the same idempotency name — that would silently
// undo the revocation on the client's next routine provision call.
func TestProvisionV2_RevokedTokenNotResurrected(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990010", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(990010)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}

	// Simulate an administrator revoking the relay token out-of-band.
	if err := repo.DB.Model(&repo.Token{}).
		Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").
		Update("status", common.TokenStatusDisabled).Error; err != nil {
		t.Fatalf("disable token: %v", err)
	}

	tok2 := provSignRS256(t, key, "test-kid",
		provClaims("990010", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	if code != http.StatusForbidden && code != http.StatusConflict {
		t.Fatalf("expected 403/409, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "TOKEN_REVOKED" {
		t.Errorf("expected TOKEN_REVOKED, got %v", resp["error_code"])
	}

	var rows []repo.Token
	repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 token row (no resurrection), got %d", len(rows))
	}
	if rows[0].Status != common.TokenStatusDisabled {
		t.Errorf("expected token to remain disabled, got status=%d", rows[0].Status)
	}
}
