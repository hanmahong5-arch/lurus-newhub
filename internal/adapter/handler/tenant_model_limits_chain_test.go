package handler

// tenant_model_limits_chain_test.go — full-chain hermetic proof that the
// per-model admin surface actually drives enforcement:
//
//	PUT /tenants/:id/model-limits (admin API)
//	  → real sqlite model_rate_limits row (migration 026 shape via AutoMigrate)
//	    → middleware.BusinessModelRateLimit reads the SAME row (no fetch seams)
//	      → 429 / 200 on the relay route.
//
// The relay route stubs exactly what the production chain provides upstream:
// TokenAuth's "token_id" (X-Test-Token-ID) and Distribute's "original_model"
// (X-Test-Model); BusinessRateLimit runs first, as in relay-router.go, so the
// tenant stash path is the one exercised. Redis is off (memory windows); the
// TPM window is fed through app.RecordBusinessTPMModelUsage — the exact
// function the settlement path calls.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

func setupModelLimitChain(t *testing.T) (*V2TestContext, *gin.Engine) {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)
	if err := ctx.DB.AutoMigrate(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("auto migrate model_rate_limits: %v", err)
	}

	r := ctx.Router
	authed := r.Group("/chain")
	authed.Use(func(c *gin.Context) {
		c.Set("id", ctx.RootUser.Id)
		c.Set("tenant_id", ctx.TenantID)
		c.Next()
	})
	authed.GET("/tenants/:id/model-limits", ListTenantModelLimits)
	authed.PUT("/tenants/:id/model-limits", UpsertTenantModelLimit)
	authed.DELETE("/tenants/:id/model-limits", DeleteTenantModelLimit)

	r.POST("/chain/relay",
		func(c *gin.Context) {
			if id, err := strconv.Atoi(c.GetHeader("X-Test-Token-ID")); err == nil && id > 0 {
				c.Set("token_id", id)
			}
			if m := c.GetHeader("X-Test-Model"); m != "" {
				c.Set("original_model", m)
			}
			c.Next()
		},
		middleware.BusinessRateLimit(),
		middleware.BusinessModelRateLimit(),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	return ctx, r
}

func chainModelRelay(r *gin.Engine, tokenID int, model string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chain/relay", nil)
	req.Header.Set("X-Test-Token-ID", strconv.Itoa(tokenID))
	req.Header.Set("X-Test-Model", model)
	r.ServeHTTP(w, req)
	return w
}

func TestModelRateLimitChain_AdminAPIDrivesMiddleware(t *testing.T) {
	ctx, r := setupModelLimitChain(t)
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "model-chain-token")
	base := "/chain/tenants/" + ctx.TenantID + "/model-limits"

	// Baseline: no limit rows → relay flows freely.
	for i := 0; i < 3; i++ {
		if w := chainModelRelay(r, tok.Id, "m-capped"); w.Code != http.StatusOK {
			t.Fatalf("baseline relay %d = %d, want 200", i+1, w.Code)
		}
	}

	// Set rpm_limit=1 for (tenant, m-capped) through the admin API.
	w := V2Request(r, http.MethodPut, base, map[string]interface{}{
		"model":          "m-capped",
		"rate_limit_rpm": 1,
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert HTTP = %d, body %s", w.Code, w.Body.String())
	}
	rows, err := repo.ListModelRateLimits(ctx.TenantID)
	if err != nil || len(rows) != 1 || rows[0].Model != "m-capped" || rows[0].RateLimitRPM != 1 {
		t.Fatalf("persisted rows = %+v (err %v), want one m-capped row with rpm 1", rows, err)
	}

	// The middleware reads that same row: 1st in-window request passes, 2nd 429s.
	if w := chainModelRelay(r, tok.Id, "m-capped"); w.Code != http.StatusOK {
		t.Fatalf("first limited relay = %d, want 200", w.Code)
	}
	lw := chainModelRelay(r, tok.Id, "m-capped")
	if lw.Code != http.StatusTooManyRequests {
		t.Fatalf("second relay under rpm_limit=1 = %d, want 429", lw.Code)
	}
	if ra := lw.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q, want positive seconds", ra)
	}

	// Another model is not affected by m-capped's limit row.
	if w := chainModelRelay(r, tok.Id, "m-free"); w.Code != http.StatusOK {
		t.Errorf("other model relay = %d, want 200 (limits isolate per model)", w.Code)
	}

	// Upsert to 0 → unlimited again even with a hot window.
	V2Request(r, http.MethodPut, base, map[string]interface{}{
		"model":          "m-capped",
		"rate_limit_rpm": 0,
	}, nil)
	if w := chainModelRelay(r, tok.Id, "m-capped"); w.Code != http.StatusOK {
		t.Errorf("relay after reset to 0 = %d, want 200 (0 = unlimited)", w.Code)
	}

	// DELETE removes the row and the dimension entirely.
	dw := V2Request(r, http.MethodDelete, base+"?model=m-capped", nil, nil)
	if dw.Code != http.StatusOK {
		t.Fatalf("delete HTTP = %d, body %s", dw.Code, dw.Body.String())
	}
	if rows, _ := repo.ListModelRateLimits(ctx.TenantID); len(rows) != 0 {
		t.Fatalf("rows after delete = %d, want 0", len(rows))
	}
	if w := chainModelRelay(r, tok.Id, "m-capped"); w.Code != http.StatusOK {
		t.Errorf("relay after delete = %d, want 200", w.Code)
	}
}

func TestModelRateLimitChain_TPMFromSettlementWrite(t *testing.T) {
	ctx, r := setupModelLimitChain(t)
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "model-chain-tpm-token")
	base := "/chain/tenants/" + ctx.TenantID + "/model-limits"

	V2Request(r, http.MethodPut, base, map[string]interface{}{
		"model":          "m-tpm",
		"rate_limit_tpm": 50,
	}, nil)

	// Quiet window → admitted (single spikes allowed by design).
	if w := chainModelRelay(r, tok.Id, "m-tpm"); w.Code != http.StatusOK {
		t.Fatalf("relay on quiet TPM window = %d, want 200", w.Code)
	}

	// 60 settled tokens land in the (tenant, model) window via the production
	// write path → the next relay for THAT model is throttled…
	app.RecordBusinessTPMModelUsage(ctx.TenantID, "m-tpm", 60)
	if w := chainModelRelay(r, tok.Id, "m-tpm"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("relay over model tpm_limit = %d, want 429", w.Code)
	}
	// …while other models flow on.
	if w := chainModelRelay(r, tok.Id, "m-elsewhere"); w.Code != http.StatusOK {
		t.Errorf("other model relay = %d, want 200 (TPM windows isolate per model)", w.Code)
	}
}

func TestModelRateLimitChain_CrossTenantIsolation(t *testing.T) {
	ctx, r := setupModelLimitChain(t)
	tokA := SeedV2Token(t, ctx, ctx.NormalUser.Id, "model-chain-tenant-a")

	// A second tenant with its own token, sharing the model name.
	otherTenant := &repo.Tenant{
		Id:       "model-chain-tenant-b",
		Name:     "Model Chain B",
		Slug:     "model-chain-b",
		Status:   repo.TenantStatusEnabled,
		IDPOrgID: "org_model_chain_b",
	}
	if err := ctx.DB.Create(otherTenant).Error; err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}
	tokB := &repo.Token{
		UserId:         ctx.NormalUser.Id,
		TenantId:       otherTenant.Id,
		Key:            "model-chain-b-" + strconv.Itoa(ctx.NormalUser.Id),
		Status:         1,
		Name:           "model-chain-tenant-b-token",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := ctx.DB.Create(tokB).Error; err != nil {
		t.Fatalf("seed token B: %v", err)
	}

	// Cap the model for tenant A only.
	V2Request(r, http.MethodPut, "/chain/tenants/"+ctx.TenantID+"/model-limits", map[string]interface{}{
		"model":          "m-shared",
		"rate_limit_rpm": 1,
	}, nil)

	if w := chainModelRelay(r, tokA.Id, "m-shared"); w.Code != http.StatusOK {
		t.Fatalf("tenant A first = %d, want 200", w.Code)
	}
	if w := chainModelRelay(r, tokA.Id, "m-shared"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("tenant A second = %d, want 429", w.Code)
	}
	// Tenant B's token relays the same model without a limit row → free.
	for i := 0; i < 3; i++ {
		if w := chainModelRelay(r, tokB.Id, "m-shared"); w.Code != http.StatusOK {
			t.Errorf("tenant B relay %d = %d, want 200 (limits must isolate per tenant)", i+1, w.Code)
		}
	}
}

func TestModelLimitAdminAPI_ValidationAndErrors(t *testing.T) {
	ctx, r := setupModelLimitChain(t)
	base := "/chain/tenants/" + ctx.TenantID + "/model-limits"

	// Missing / blank model and out-of-range limits are 400s.
	for _, body := range []map[string]interface{}{
		{"rate_limit_rpm": 1},
		{"model": "   ", "rate_limit_rpm": 1},
		{"model": "m-bad", "rate_limit_rpm": -1},
		{"model": "m-bad", "rate_limit_tpm": app.MaxRateLimitPerMinute + 1},
	} {
		w := V2Request(r, http.MethodPut, base, body, nil)
		AssertV2Status(t, w, http.StatusBadRequest)
	}

	// Unknown tenant → 404; the bootstrap "default" tenant is allowed without
	// a tenants row (this harness seeds no 'default' row — the carve-out is
	// exactly what makes this succeed).
	w := V2Request(r, http.MethodPut, "/chain/tenants/no-such-tenant/model-limits",
		map[string]interface{}{"model": "m-x", "rate_limit_rpm": 1}, nil)
	AssertV2Status(t, w, http.StatusNotFound)
	w = V2Request(r, http.MethodPut, "/chain/tenants/default/model-limits",
		map[string]interface{}{"model": "m-x", "rate_limit_rpm": 1}, nil)
	AssertV2Status(t, w, http.StatusOK)

	// DELETE without ?model= → 400; DELETE of an absent row → 404.
	w = V2Request(r, http.MethodDelete, base, nil, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	w = V2Request(r, http.MethodDelete, base+"?model=never-configured", nil, nil)
	AssertV2Status(t, w, http.StatusNotFound)

	// Upsert twice converges to ONE row with the latest limits; GET lists it.
	V2Request(r, http.MethodPut, base, map[string]interface{}{"model": "m-up", "rate_limit_rpm": 5}, nil)
	V2Request(r, http.MethodPut, base, map[string]interface{}{"model": "m-up", "rate_limit_rpm": 9, "rate_limit_tpm": 900}, nil)
	rows, err := repo.ListModelRateLimits(ctx.TenantID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows after double upsert = %+v (err %v), want exactly 1", rows, err)
	}
	if rows[0].RateLimitRPM != 9 || rows[0].RateLimitTPM != 900 {
		t.Errorf("row limits = (%d,%d), want (9,900) — upsert must overwrite", rows[0].RateLimitRPM, rows[0].RateLimitTPM)
	}
	gw := V2Request(r, http.MethodGet, base, nil, nil)
	AssertV2Status(t, gw, http.StatusOK)
	resp := ParseV2Response(t, gw)
	if data, ok := resp["data"].([]interface{}); !ok || len(data) != 1 {
		t.Errorf("GET data = %v, want list of 1", resp["data"])
	}
}
