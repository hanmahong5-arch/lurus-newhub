package handler

// l5_v2_token_update_semantics_test.go — regression coverage for D6
// (2026-08-26 live business acceptance): UpdateTokenV2 leaked the raw token
// Key in its PUT response body, and treated explicit zero-values on
// allow_ips / model_limits / remain_quota as "field omitted" so callers
// could never clear them even though the UI offers exactly that action.
//
// Reuses SetupV2TestRouter / V2RequestAsUser / SeedV2Token / AssertV2Status /
// AssertV2Success / ParseV2Response from v2_testutil_test.go per "先读既有
// 测试...复用脚手架".

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestL5UpdateTokenV2_ResponseNeverLeaksRawKey covers the leak in D6: the PUT
// response's "data" field used to be the raw repo.Token (unmasked Key), while
// every other v2 token endpoint (list, create, rotate) either masks the key
// or returns it exactly once on creation. A plain no-op rename must never
// surface the bearer secret.
func TestL5UpdateTokenV2_ResponseNeverLeaksRawKey(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "leak-check-target")
	rawKey := token.Key
	if len(rawKey) != 32 {
		t.Fatalf("test fixture assumption broken: seeded key length = %d, want 32", len(rawKey))
	}

	body := map[string]interface{}{"name": "leak-check-target-renamed"}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %v", resp["data"])
	}
	keyVal, ok := data["key"].(string)
	if !ok {
		t.Fatalf("data.key missing or not a string: %v", data["key"])
	}
	if keyVal == rawKey {
		t.Fatalf("data.key leaked the raw bearer secret: %q", keyVal)
	}
	if len(keyVal) == 32 {
		t.Errorf("data.key length = %d (looks unmasked, raw keys are 32 chars): %q", len(keyVal), keyVal)
	}
	if !strings.Contains(keyVal, "****") {
		t.Errorf("data.key = %q, want a masked value containing '****'", keyVal)
	}
}

// TestL5UpdateTokenV2_ClearsAllowIps mirrors the real console action: the
// "unrestricted IP" toggle in web/src/pages/v2/Token/index.jsx sends
// allow_ips:"" to clear a previously-set allowlist. Before the fix this was
// silently ignored (empty string treated as "field omitted") while the
// handler still returned 200 success.
func TestL5UpdateTokenV2_ClearsAllowIps(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "clear-allowips-target")

	seedIP := "1.2.3.4"
	token.AllowIps = &seedIP
	if err := ctx.DB.Save(token).Error; err != nil {
		t.Fatalf("seed allow_ips: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{"allow_ips": ""}, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.AllowIps == nil {
		t.Fatalf("allow_ips = nil, want cleared to empty string (not left unset)")
	}
	if *reloaded.AllowIps != "" {
		t.Errorf("allow_ips = %q, want cleared to \"\"", *reloaded.AllowIps)
	}
}

// TestL5UpdateTokenV2_ClearsModelLimits mirrors clearing a previously-set
// model-limits JSON blob via an explicit empty string.
func TestL5UpdateTokenV2_ClearsModelLimits(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "clear-modellimits-target")

	token.ModelLimits = `{"gpt-4":100}`
	if err := ctx.DB.Save(token).Error; err != nil {
		t.Fatalf("seed model_limits: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{"model_limits": ""}, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.ModelLimits != "" {
		t.Errorf("model_limits = %q, want cleared to \"\"", reloaded.ModelLimits)
	}
}

// TestL5UpdateTokenV2_SetsRemainQuotaZero mirrors
// web/src/pages/v2/Token/index.jsx's Math.max(0, ...) path, which can and
// does submit remain_quota:0 for a metered (non-unlimited) token.
func TestL5UpdateTokenV2_SetsRemainQuotaZero(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "zero-remain-quota-target")

	token.UnlimitedQuota = false
	token.RemainQuota = 1000
	if err := ctx.DB.Save(token).Error; err != nil {
		t.Fatalf("seed remain_quota: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{"remain_quota": 0}, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.RemainQuota != 0 {
		t.Errorf("remain_quota = %d, want 0", reloaded.RemainQuota)
	}
}
