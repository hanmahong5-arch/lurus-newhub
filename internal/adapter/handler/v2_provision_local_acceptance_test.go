package handler

// Cross-service local acceptance: a token minted by a REAL locally running
// lurus-platform (its own signing key, its own JWKS) is handed to newhub's real
// provision handler and DB. Opt-in — it needs that platform running:
//
//	LURUS_LOCAL_PLATFORM=http://127.0.0.1:18104
//	LURUS_LOCAL_UTOK=<platform session token of an account holding a cc_* plan>
//
// Recipe: 2l-svc-platform/scripts/acceptance/entitlement-local/README.md.
// It is skipped (and says so) without those variables; it is NOT part of the
// hermetic suite and its absence from CI is deliberate.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-entkit/entverify"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-gonic/gin"
)

func localPlatformToken(t *testing.T, base, utok, product string) (string, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/entitlements/"+product, nil)
	req.Header.Set("Authorization", "Bearer "+utok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("local platform unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("local platform token %s: HTTP %d %s", product, resp.StatusCode, body)
	}
	var r struct {
		Tok string `json:"entitlement_token"`
	}
	_ = json.Unmarshal(body, &r)
	p := strings.Split(r.Tok, ".")[1]
	raw, _ := base64.RawURLEncoding.DecodeString(p)
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	return r.Tok, claims
}

func TestProvisionV2_LocalPlatformAcceptance(t *testing.T) {
	base, utok := os.Getenv("LURUS_LOCAL_PLATFORM"), os.Getenv("LURUS_LOCAL_UTOK")
	if base == "" || utok == "" {
		t.Skip("LURUS_LOCAL_PLATFORM / LURUS_LOCAL_UTOK not set — local cross-service acceptance not run")
	}
	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)
	setProvisionVerifier(entverify.New(base+"/api/v1/entitlements/jwks", entverify.WithInsecureJWKS()))
	t.Cleanup(func() { setProvisionVerifier(nil) })
	r := gin.New()
	r.POST("/api/v2/:tenant_slug/provision", ProvisionV2)

	tok, claims := localPlatformToken(t, base, utok, "llm-api")
	ent, _ := claims["ent"].(map[string]any)
	plan, _ := ent["plan_code"].(string)
	if !strings.HasPrefix(plan, "cc_") {
		t.Fatalf("local account must hold a cc_* plan for this acceptance, token plan_code=%q", plan)
	}
	t.Logf("platform token: sub=%v aud=%v plan=%s ent=%v", claims["sub"], claims["aud"], plan, ent)

	// 1. real platform token → real provision → relay key
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok, "fingerprint": "r36-local"})
	if code != http.StatusOK {
		t.Fatalf("provision: HTTP %d %v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	key1, _ := data["token"].(string)
	if !strings.HasPrefix(key1, "sk-") || data["plan_code"] != plan || data["replayed"] != false {
		t.Fatalf("provision data unexpected: %v", data)
	}
	t.Logf("relay key minted for plan %s (replayed=false)", plan)

	// 2. the bridged hub user is keyed by the platform account id
	sub, _ := claims["sub"].(string)
	var accountID int64
	for _, ch := range sub {
		accountID = accountID*10 + int64(ch-'0')
	}
	user, err := repo.GetUserByLurusAccountID(accountID)
	if err != nil || user == nil {
		t.Fatalf("bridged user for platform account %d missing: %v", accountID, err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-"+plan).First(&row).Error; err != nil {
		t.Fatalf("relay token row missing: %v", err)
	}
	t.Logf("hub user=%s token row: unlimited_quota=%v remain_quota=%d model_limits=%q expired_time=%d",
		user.Username, row.UnlimitedQuota, row.RemainQuota, row.ModelLimits, row.ExpiredTime)

	// 3. a second provision with a FRESH platform token for the same plan
	//    replays the same relay key — a user re-installing does not multiply keys.
	tok2, _ := localPlatformToken(t, base, utok, "llm-api")
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	data, _ = resp["data"].(map[string]any)
	if code != http.StatusOK || data["replayed"] != true || data["token"] != key1 {
		t.Fatalf("second provision must replay the same key: HTTP %d %v", code, resp)
	}
	var n int64
	repo.DB.Model(&repo.Token{}).Where("user_id = ? AND name = ?", user.Id, "switch-provision-"+plan).Count(&n)
	if n != 1 {
		t.Fatalf("relay tokens for the plan = %d, want 1", n)
	}

	// 4. tampered token → TOKEN_INVALID
	parts := strings.Split(tok, ".")
	sig := []byte(parts[2])
	sig[5] ^= 1
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": parts[0] + "." + parts[1] + "." + string(sig)})
	if code != http.StatusUnauthorized || resp["error_code"] != "TOKEN_INVALID" {
		t.Fatalf("tampered: HTTP %d %v", code, resp)
	}

	// 5. a genuine token for ANOTHER product → AUD_MISMATCH (no cross-product reuse)
	other, _ := localPlatformToken(t, base, utok, "creator")
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": other})
	if code != http.StatusForbidden || resp["error_code"] != "AUD_MISMATCH" {
		t.Fatalf("creator token at llm-api provision: HTTP %d %v", code, resp)
	}
}
