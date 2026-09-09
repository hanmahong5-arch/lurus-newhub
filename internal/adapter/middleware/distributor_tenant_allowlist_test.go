package middleware

// distributor_tenant_allowlist_test.go — L1: the per-tenant model
// allow-list check inserted into Distribute() between "original_model" being
// set and the pinned-vs-selection branch, so both paths are covered. Default
// mode only counts denials (observed); enforce mode actually aborts with the
// typed 403 model_blocked. RED on HEAD (no such check exists): (b)'s counter
// stays 0 and (c)/(e)/(f)'s enforce cases return 200 instead of 403.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// mountDistributeBothFormats registers Distribute() on both the OpenAI and
// the Claude relay paths — the allow-list check must apply identically to
// both, since getModelRequest reads {"model": ...} from either body shape.
func mountDistributeBothFormats(ctxSetup func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if ctxSetup != nil {
			ctxSetup(c)
		}
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	r.POST("/v1/chat/completions", Distribute(), pass)
	r.POST("/v1/messages", Distribute(), pass)
	return r
}

func doDistributePath(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func tenantAllowlistCounter(t *testing.T, tenantID, action string) float64 {
	t.Helper()
	return testutil.ToFloat64(metrics.TenantModelDeniedTotal.WithLabelValues(tenantID, action))
}

func decodeErrorCode(t *testing.T, w *httptest.ResponseRecorder) (code, errType string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, w.Body.String())
	}
	return body.Error.Code, body.Error.Type
}

func TestDistribute_TenantAllowlist(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9701, "t-allow", 100)
	repo.InitChannelCache()

	tenantSetup := func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "t-allow"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	}

	t.Run("a_no_row_unrestricted", func(t *testing.T) {
		tenantpolicy.Invalidate("t-allow")
		before := tenantAllowlistCounter(t, "t-allow", "observed")
		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for an unconfigured tenant; body=%s", w.Code, w.Body.String())
		}
		if after := tenantAllowlistCounter(t, "t-allow", "observed"); after != before {
			t.Errorf("observed counter moved from %v to %v with no allow-list row configured", before, after)
		}
	})

	t.Run("b_observe_mode_denies_silently_but_counts", func(t *testing.T) {
		if err := repo.SetTenantConfigJSON("t-allow", tenantpolicy.ModelAllowlistConfigKey, []string{"other-*"}, ""); err != nil {
			t.Fatalf("seed allow-list: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")
		before := tenantAllowlistCounter(t, "t-allow", "observed")

		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 in observe mode; body=%s", w.Code, w.Body.String())
		}
		if after := tenantAllowlistCounter(t, "t-allow", "observed"); after != before+1 {
			t.Errorf("observed counter = %v, want %v", after, before+1)
		}
	})

	t.Run("c_enforce_mode_blocks_both_wire_formats", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		// allow-list still ["other-*"] from the previous subtest.
		tenantpolicy.Invalidate("t-allow")

		before := tenantAllowlistCounter(t, "t-allow", "enforced")
		r := mountDistributeBothFormats(tenantSetup)
		for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
			w := doDistributePath(r, path, `{"model":"gpt-4o"}`)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 403; body=%s", path, w.Code, w.Body.String())
			}
			code, errType := decodeErrorCode(t, w)
			if code != "model_blocked" {
				t.Errorf("%s: error.code = %q, want model_blocked", path, code)
			}
			if errType != "permission_error" {
				t.Errorf("%s: error.type = %q, want permission_error", path, errType)
			}
		}
		// Two denied requests (one per wire format) must each record their
		// own "enforced" increment — the counter write is not a decoration.
		if after := tenantAllowlistCounter(t, "t-allow", "enforced"); after != before+2 {
			t.Errorf("enforced counter = %v, want %v (one per denied request)", after, before+2)
		}
	})

	t.Run("d_enforce_mode_wildcard_allows", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		if err := repo.SetTenantConfigJSON("t-allow", tenantpolicy.ModelAllowlistConfigKey, []string{"gpt-4*"}, ""); err != nil {
			t.Fatalf("seed allow-list: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")

		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for a wildcard-matched model under enforce; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("e_enforce_mode_blocks_pinned_channel_path", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		if err := repo.SetTenantConfigJSON("t-allow", tenantpolicy.ModelAllowlistConfigKey, []string{"other-*"}, ""); err != nil {
			t.Fatalf("seed allow-list: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")

		before := tenantAllowlistCounter(t, "t-allow", "enforced")
		r := mountDistribute(func(c *gin.Context) {
			c.Set("tenant_context", &TenantContext{TenantID: "t-allow"})
			common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(9701))
		})
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a denied model on the pinned-channel path; body=%s", w.Code, w.Body.String())
		}
		code, _ := decodeErrorCode(t, w)
		if code != "model_blocked" {
			t.Errorf("error.code = %q, want model_blocked", code)
		}
		if after := tenantAllowlistCounter(t, "t-allow", "enforced"); after != before+1 {
			t.Errorf("enforced counter = %v, want %v (the pinned-channel path must also record the denial)", after, before+1)
		}
	})

	t.Run("f_empty_list_denies_all_only_under_enforce", func(t *testing.T) {
		if err := repo.SetTenantConfigJSON("t-allow", tenantpolicy.ModelAllowlistConfigKey, []string{}, ""); err != nil {
			t.Fatalf("seed empty allow-list: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")

		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("enforce: status = %d, want 403 for an explicit deny-all list; body=%s", w.Code, w.Body.String())
		}

		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "")
		tenantpolicy.Invalidate("t-allow")
		before := tenantAllowlistCounter(t, "t-allow", "observed")
		w = doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("observe: status = %d, want 200 for a deny-all list; body=%s", w.Code, w.Body.String())
		}
		if after := tenantAllowlistCounter(t, "t-allow", "observed"); after != before+1 {
			t.Errorf("observed counter = %v, want %v", after, before+1)
		}
	})

	t.Run("g_capitalised_enforce_only_observes", func(t *testing.T) {
		if err := repo.SetTenantConfigJSON("t-allow", tenantpolicy.ModelAllowlistConfigKey, []string{"other-*"}, ""); err != nil {
			t.Fatalf("seed allow-list: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "Enforce")

		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — only the literal lowercase \"enforce\" enforces; body=%s", w.Code, w.Body.String())
		}
	})

	// h) The fail-open branch has no other oracle in this suite: a row that
	// exists but cannot be parsed as the allow-list JSON (GetTenantConfigJSON
	// returns "config type is not JSON") must not turn into a 500 — the
	// request proceeds and is logged, matching the "must never turn a read
	// fault into an outage" contract at distributor.go's allow-list block.
	// Neither counter moves: this is a read failure, not a denial.
	t.Run("h_unreadable_row_fails_open_not_500", func(t *testing.T) {
		if err := repo.SetTenantConfig("t-allow", tenantpolicy.ModelAllowlistConfigKey, "not-json", repo.ConfigTypeString, "", false); err != nil {
			t.Fatalf("seed unreadable row: %v", err)
		}
		tenantpolicy.Invalidate("t-allow")
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")

		beforeObserved := tenantAllowlistCounter(t, "t-allow", "observed")
		beforeEnforced := tenantAllowlistCounter(t, "t-allow", "enforced")

		r := mountDistribute(tenantSetup)
		w := doDistribute(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (fail open) for an unreadable allow-list row; body=%s", w.Code, w.Body.String())
		}
		if after := tenantAllowlistCounter(t, "t-allow", "observed"); after != beforeObserved {
			t.Errorf("observed counter moved from %v to %v on a read failure, want unchanged", beforeObserved, after)
		}
		if after := tenantAllowlistCounter(t, "t-allow", "enforced"); after != beforeEnforced {
			t.Errorf("enforced counter moved from %v to %v on a read failure, want unchanged", beforeEnforced, after)
		}
	})
}
