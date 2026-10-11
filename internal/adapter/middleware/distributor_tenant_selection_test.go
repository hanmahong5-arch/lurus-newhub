package middleware

// A tenant admin's own narrowing (models.tenant_selection) is enforced at
// relay time with the same typed 403 model_blocked as the platform allow-list,
// regardless of TENANT_MODEL_ALLOWLIST_MODE (it is the tenant's own explicit
// choice, not a platform rollout), and never widens the platform ceiling.

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func TestDistribute_TenantSelectionNarrowing(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9711, "t-sel", 100)
	repo.InitChannelCache()

	setup := func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "t-sel"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	}
	set := func(key string, v []string) {
		if err := repo.SetTenantConfigJSON("t-sel", key, v, ""); err != nil {
			t.Fatal(err)
		}
		tenantpolicy.Invalidate("t-sel")
		tenantpolicy.InvalidateSelection("t-sel")
	}

	// Default (observe) mode: selection still enforced.
	set(tenantpolicy.SelectionConfigKey, []string{"other-*"})
	r := mountDistributeBothFormats(setup)
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		w := doDistributePath(r, path, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d want 403; body=%s", path, w.Code, w.Body.String())
		}
		if code, _ := decodeErrorCode(t, w); code != "model_blocked" {
			t.Errorf("%s: code=%q want model_blocked", path, code)
		}
	}

	// Model inside the selection passes.
	set(tenantpolicy.SelectionConfigKey, []string{"gpt-4*"})
	if w := doDistribute(mountDistribute(setup), `{"model":"gpt-4o"}`); w.Code != http.StatusOK {
		t.Fatalf("selected model blocked: %d %s", w.Code, w.Body.String())
	}

	// A selection cannot reopen a model the platform ceiling (enforce) denies.
	t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
	set(tenantpolicy.ModelAllowlistConfigKey, []string{"other-*"})
	if w := doDistribute(mountDistribute(setup), `{"model":"gpt-4o"}`); w.Code != http.StatusForbidden {
		t.Fatalf("selection widened platform ceiling: %d %s", w.Code, w.Body.String())
	}
}
