package handler

// tenant_model_allowlist_test.go — TestTenantModelAllowlist*: the root-only
// admin surface for internal/app/tenantpolicy's per-tenant model allow-list
// (GET/PUT/DELETE /api/v2/admin/tenants/:id/model-allowlist), plus a
// full-chain proof that a PUT through this API actually drives
// middleware.Distribute's enforcement — mirrors tenant_model_limits_chain_
// test.go's PUT-drives-middleware pattern for the model-limits surface.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// setupAllowlistAdminRouter mounts the three allow-list admin routes behind
// a root-actor stub — same convention as setupModelLimitChain
// (tenant_model_limits_chain_test.go) and setupAdminInviteRouter: mimics
// RootJWTAuth's session-based actor injection without a real JWT.
func setupAllowlistAdminRouter(t *testing.T) *V2TestContext {
	t.Helper()

	// The production body-size guard defaults to 0 MB until config init runs
	// (common/init.go), which no unit test triggers — same fix as
	// relay_inband_error_test.go (this package) and cover_helpers_test.go
	// (middleware package). Without a positive cap, GetRequestBody rejects
	// every relay body as "too large" before the allow-list check ever
	// runs. Scoped to this test (t.Cleanup restore) rather than a package
	// init(), which would mutate the constant for the whole test binary.
	prev := constant.MaxRequestBodyMB
	if constant.MaxRequestBodyMB <= 0 {
		constant.MaxRequestBodyMB = 64
	}
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })

	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)

	r := ctx.Router
	admin := r.Group("/allowlist-admin")
	admin.Use(func(c *gin.Context) {
		c.Set("id", ctx.RootUser.Id)
		c.Next()
	})
	admin.GET("/tenants/:id/model-allowlist", ListTenantModelAllowlist)
	admin.PUT("/tenants/:id/model-allowlist", UpsertTenantModelAllowlist)
	admin.DELETE("/tenants/:id/model-allowlist", DeleteTenantModelAllowlist)
	return ctx
}

func manyModelNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("m-%d", i)
	}
	return out
}

func TestTenantModelAllowlist_PutValidation(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	base := "/allowlist-admin/tenants/" + ctx.TenantID + "/model-allowlist"

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"duplicate entries", map[string]interface{}{"allowed_models": []string{"gpt-4o", "gpt-4o"}}},
		{"blank entry", map[string]interface{}{"allowed_models": []string{"gpt-4o", "   "}}},
		{"oversize entry (>128 bytes)", map[string]interface{}{"allowed_models": []string{strings.Repeat("x", 129)}}},
		{"oversize list (>256 entries)", map[string]interface{}{"allowed_models": manyModelNames(257)}},
		{"leading wildcard", map[string]interface{}{"allowed_models": []string{"*-mini"}}},
		{"embedded wildcard", map[string]interface{}{"allowed_models": []string{"gpt-*-turbo"}}},
		{"missing field", map[string]interface{}{}},
		{"null field", map[string]interface{}{"allowed_models": nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := V2Request(ctx.Router, http.MethodPut, base, tc.body, nil)
			AssertV2Status(t, w, http.StatusBadRequest)
		})
	}

	// The oversize-entry 400 must not echo the offending (potentially huge)
	// entry back into the response body — the byte bound exists precisely
	// because the entry is too large to put in an error message. Use a 5 KB
	// entry (finding-scale): a 129-byte entry is oversize enough to trigger
	// the branch but too small itself to distinguish "echoes the entry"
	// from "reports the index" by body size alone.
	t.Run("oversize entry response body stays small", func(t *testing.T) {
		w := V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": []string{strings.Repeat("x", 5000)}}, nil)
		AssertV2Status(t, w, http.StatusBadRequest)
		if n := w.Body.Len(); n >= 512 {
			t.Fatalf("oversize-entry 400 body = %d bytes, want < 512 (must not echo the entry)", n)
		}
	})

	// A missing/null allowed_models field must be rejected before any row
	// is written — never silently stored as an explicit deny-all ([]).
	t.Run("missing field does not create a row", func(t *testing.T) {
		w := V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{}, nil)
		AssertV2Status(t, w, http.StatusBadRequest)

		w = V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": nil}, nil)
		AssertV2Status(t, w, http.StatusBadRequest)

		w = V2Request(ctx.Router, http.MethodGet, base, nil, nil)
		AssertV2Status(t, w, http.StatusOK)
		resp := ParseV2Response(t, w)
		data := resp["data"].(map[string]interface{})
		if data["configured"] != false {
			t.Fatalf("configured = %v after a rejected PUT, want false (no row must be written)", data["configured"])
		}
	})
}

func TestTenantModelAllowlist_UnknownTenant404(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	base := "/allowlist-admin/tenants/no-such-tenant/model-allowlist"

	w := V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusNotFound)

	w = V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": []string{"m1"}}, nil)
	AssertV2Status(t, w, http.StatusNotFound)

	w = V2Request(ctx.Router, http.MethodDelete, base, nil, nil)
	AssertV2Status(t, w, http.StatusNotFound)
}

func TestTenantModelAllowlist_GetRoundTripAndDelete(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	base := "/allowlist-admin/tenants/" + ctx.TenantID + "/model-allowlist"

	// Never configured: unrestricted.
	w := V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data = %v, want an object", resp["data"])
	}
	if data["configured"] != false {
		t.Errorf("configured = %v, want false before any PUT", data["configured"])
	}

	// PUT persists and echoes propagation_seconds.
	w = V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": []string{"gpt-4o", "claude-*"}}, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp = ParseV2Response(t, w)
	data = resp["data"].(map[string]interface{})
	if data["propagation_seconds"] != float64(30) {
		t.Errorf("propagation_seconds = %v, want 30", data["propagation_seconds"])
	}
	if data["configured"] != true {
		t.Errorf("configured = %v, want true after PUT", data["configured"])
	}

	// GET reflects the persisted value.
	w = V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp = ParseV2Response(t, w)
	data = resp["data"].(map[string]interface{})
	allowed, ok := data["allowed_models"].([]interface{})
	if !ok || len(allowed) != 2 || allowed[0] != "gpt-4o" || allowed[1] != "claude-*" {
		t.Errorf("allowed_models = %v, want [gpt-4o claude-*]", data["allowed_models"])
	}
	if data["mode"] != "observe" {
		t.Errorf("mode = %v, want observe (default)", data["mode"])
	}

	// DELETE returns the tenant to unrestricted.
	w = V2Request(ctx.Router, http.MethodDelete, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp = ParseV2Response(t, w)
	data = resp["data"].(map[string]interface{})
	if data["configured"] != false {
		t.Errorf("configured after DELETE = %v, want false", data["configured"])
	}

	w = V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp = ParseV2Response(t, w)
	data = resp["data"].(map[string]interface{})
	if data["configured"] != false {
		t.Errorf("configured after DELETE (via GET) = %v, want false", data["configured"])
	}
}

func TestTenantModelAllowlist_EmptyListIsExplicitDenyAll(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	base := "/allowlist-admin/tenants/" + ctx.TenantID + "/model-allowlist"

	w := V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": []string{}}, nil)
	AssertV2Status(t, w, http.StatusOK)

	w = V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	data := resp["data"].(map[string]interface{})
	if data["configured"] != true {
		t.Errorf("configured = %v, want true for an explicit empty list", data["configured"])
	}
	allowed, ok := data["allowed_models"].([]interface{})
	if !ok || len(allowed) != 0 {
		t.Errorf("allowed_models = %v, want an empty (non-nil) list", data["allowed_models"])
	}
}

// TestTenantModelAllowlist_GetBypassesCache locks item 4: GET must not read
// through tenantpolicy's 30s replica cache. Warm the cache with an initial
// GET, then write a new list directly through the repo layer — as a write
// landing on a *different* replica would, i.e. without this process's
// Invalidate call — and assert the next GET reflects it immediately.
func TestTenantModelAllowlist_GetBypassesCache(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	tenantID := ctx.TenantID
	base := "/allowlist-admin/tenants/" + tenantID + "/model-allowlist"
	t.Cleanup(func() { tenantpolicy.Invalidate(tenantID) })

	// Warm the cache for this replica: no row yet, so a cached load would
	// remember (nil, false, nil) for cacheTTL.
	w := V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	data := resp["data"].(map[string]interface{})
	if data["configured"] != false {
		t.Fatalf("configured = %v before any write, want false", data["configured"])
	}

	// Simulate a write landing on a different replica: bypass this
	// process's Invalidate call entirely.
	if err := repo.SetTenantConfigJSON(tenantID, tenantpolicy.ModelAllowlistConfigKey, []string{"other-model"}, "test"); err != nil {
		t.Fatalf("SetTenantConfigJSON: %v", err)
	}

	w = V2Request(ctx.Router, http.MethodGet, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp = ParseV2Response(t, w)
	data = resp["data"].(map[string]interface{})
	if data["configured"] != true {
		t.Fatalf("configured = %v after an out-of-band write with a warm cache, want true (GET must not read through the cache)", data["configured"])
	}
}

// TestTenantModelAllowlist_ChainDrivesDistribute proves the admin API is not
// just a database write: PUT through the handler, then a real
// middleware.Distribute() call for the same tenant/model actually denies
// under enforce and allows again after DELETE.
func TestTenantModelAllowlist_ChainDrivesDistribute(t *testing.T) {
	ctx := setupAllowlistAdminRouter(t)
	tenantID := ctx.TenantID
	base := "/allowlist-admin/tenants/" + tenantID + "/model-allowlist"

	relay := ctx.Router.Group("/chain-relay")
	relay.Use(func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{TenantID: tenantID})
		c.Next()
	})
	relay.POST("/v1/chat/completions", middleware.Distribute(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Returns the recorder's status directly rather than w.Result(): the
	// assertions below only need the code, and an *http.Response here would
	// be an unclosed body on every call.
	doRelayStatus := func() int {
		w := V2Request(ctx.Router, http.MethodPost, "/chain-relay/v1/chat/completions", map[string]interface{}{"model": "gpt-4o"}, nil)
		return w.Code
	}

	// Baseline: unconfigured tenant, unrestricted (no channel exists for
	// gpt-4o in this DB, so a genuinely-open request would 503 "no channel"
	// rather than 403 model_blocked — the allow-list check must not be what
	// blocks it).
	status := doRelayStatus()
	if status == http.StatusForbidden {
		t.Fatalf("baseline (unconfigured) status = 403, want anything but a model_blocked 403")
	}

	// PUT an allow-list that excludes gpt-4o, then flip to enforce.
	w := V2Request(ctx.Router, http.MethodPut, base, map[string]interface{}{"allowed_models": []string{"other-model"}}, nil)
	AssertV2Status(t, w, http.StatusOK)
	t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")

	status = doRelayStatus()
	if status != http.StatusForbidden {
		t.Fatalf("after PUT + enforce, relay status = %d, want 403", status)
	}

	// DELETE the allow-list; the tenant is unrestricted again even though
	// enforce mode is still on.
	w = V2Request(ctx.Router, http.MethodDelete, base, nil, nil)
	AssertV2Status(t, w, http.StatusOK)

	status = doRelayStatus()
	if status == http.StatusForbidden {
		t.Fatalf("after DELETE (still enforce mode), relay status = 403, want anything but model_blocked")
	}

	// tenantpolicy is package-global cache state; make sure this test does
	// not leak an allow-list into any test that runs after it in the same
	// process.
	tenantpolicy.Invalidate(tenantID)
}
