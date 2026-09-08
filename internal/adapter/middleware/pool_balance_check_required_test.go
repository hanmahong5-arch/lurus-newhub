package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestPoolBalanceCheck_CreditPoolRequired covers the CREDIT_POOL_REQUIRED
// three-state gate added on top of the pre-existing ErrPoolNotFound bypass.
// Each sub-test uses setupCoverDB (hermetic sqlite, no mocks) so
// repo.GetTenantCreditPool genuinely returns repo.ErrPoolNotFound for a
// tenant with no tenant_credit_pools row — the real code path, not a fake.

// buildPoolGateRouter wires PoolBalanceCheck behind a stand-in for TokenAuth
// that injects tenant_context + the relay group context key exactly as the
// production chain does (TokenAuth runs before PoolBalanceCheck).
func buildPoolGateRouter(tenantID, group string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: tenantID})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		c.Next()
	})
	r.Use(PoolBalanceCheck())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestPoolBalanceCheck_EnforceWithoutPool_Returns402 is the RED case: with
// CREDIT_POOL_REQUIRED=enforce and no pool row, the gate must 402 instead of
// silently bypassing (the pre-flag legacy behaviour that lets unbilled
// consumption through).
func TestPoolBalanceCheck_EnforceWithoutPool_Returns402(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", setting.CreditPoolRequiredEnforce)

	r := buildPoolGateRouter("t-enforce-nopool", "default")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "pool_not_configured") {
		t.Errorf("body missing pool_not_configured error code: %s", w.Body.String())
	}
}

// TestPoolBalanceCheck_EnforceWithoutPool_ClaudeWire is
// TestPoolBalanceCheck_EnforceWithoutPool_Returns402's Claude-wire sibling
// (L3-CONTRACT-TAXONOMY item 4): before the root-mapping fix this 402 was
// built via types.WithOpenAIError, so ToClaudeError's ErrorTypeOpenAIError
// branch stamped the raw Code string ("pool_not_configured") into
// error.type instead of the Anthropic vendor taxonomy — a Claude SDK would
// see an unrecognised type value where it expects billing_error.
func TestPoolBalanceCheck_EnforceWithoutPool_ClaudeWire(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", setting.CreditPoolRequiredEnforce)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(StampRelayFormat())
	r.Use(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "t-enforce-nopool-claude"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	r.Use(PoolBalanceCheck())
	r.POST("/v1/messages", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf(`body = %s, want a Claude envelope ("type":"error")`, body)
	}
	if !strings.Contains(body, `"type":"billing_error"`) {
		t.Errorf(`body = %s, want the nested error.type "billing_error" (not the leaked pool_not_configured code)`, body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}

// TestPoolBalanceCheck_OffWithoutPool_Bypasses guards the default ("off",
// including unset env) path: request must pass through unchanged — the
// legacy bypass behaviour must be byte-identical.
func TestPoolBalanceCheck_OffWithoutPool_Bypasses(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", "")

	r := buildPoolGateRouter("t-off-nopool", "default")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (off must bypass); body=%s", w.Code, w.Body.String())
	}
}

// TestPoolBalanceCheck_LogWithoutPool_BypassesAndCounts guards the "log"
// path: the request still passes through (no behavioural change to relay
// traffic), but CreditPoolNotConfiguredTotal{action="log"} is incremented so
// ops can size the blast radius before flipping to enforce.
func TestPoolBalanceCheck_LogWithoutPool_BypassesAndCounts(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", setting.CreditPoolRequiredLog)

	tenantID := "t-log-nopool"
	before := testutil.ToFloat64(metrics.CreditPoolNotConfiguredTotal.WithLabelValues(tenantID, "log"))

	r := buildPoolGateRouter(tenantID, "default")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (log must bypass); body=%s", w.Code, w.Body.String())
	}
	after := testutil.ToFloat64(metrics.CreditPoolNotConfiguredTotal.WithLabelValues(tenantID, "log"))
	if after != before+1 {
		t.Errorf("CreditPoolNotConfiguredTotal{tenant=%s,action=log} = %v, want %v", tenantID, after, before+1)
	}
}

// TestPoolBalanceCheck_UnknownFlagValue_BehavesLikeOff guards the
// never-fail-open contract: an unrecognized CREDIT_POOL_REQUIRED value must
// degrade to "off" (bypass), never to "enforce".
func TestPoolBalanceCheck_UnknownFlagValue_BehavesLikeOff(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", "banana")

	r := buildPoolGateRouter("t-unknown-flag-nopool", "default")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (unknown value must degrade to off, never enforce); body=%s", w.Code, w.Body.String())
	}
}

// TestPoolBalanceCheck_EnforceMode_DBTransientError_StillBypasses guards the
// deliberate residual fail-open: a hard DB error resolving the pool row (NOT
// ErrPoolNotFound) must still bypass even under CREDIT_POOL_REQUIRED=enforce,
// so a DB blip cannot 402 the entire tenant base.
func TestPoolBalanceCheck_EnforceMode_DBTransientError_StillBypasses(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", setting.CreditPoolRequiredEnforce)

	// Force every subsequent query against repo.DB to fail with a generic
	// driver error (not gorm.ErrRecordNotFound), so GetTenantCreditPool takes
	// the "hard DB error" branch instead of ErrPoolNotFound.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	r := buildPoolGateRouter("t-db-error", "default")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (DB transient error must stay fail-open even in enforce); body=%s", w.Code, w.Body.String())
	}
}
