package handler

// token_rate_limit_chain_test.go — full-chain hermetic proof that the
// rate-limit management surface actually drives enforcement:
//
//	token/tenant API write (rate_limit_rpm / rate_limit_tpm)
//	  → real sqlite row (rpm_limit / tpm_limit columns)
//	    → middleware.BusinessRateLimit reads the SAME row (no fetch seams)
//	      → 429 / 200 on the relay route.
//
// Redis is off (memory window backends); the TPM windows are fed through
// app.RecordBusinessTPMUsage — the exact function the settlement path calls.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// setupRateLimitChain wires the v1 token handlers, the tenant admin handlers
// and a BusinessRateLimit-guarded relay route onto one router backed by the
// shared hermetic DB harness. The relay route picks its token from the
// X-Test-Token-ID header (mirroring what TokenAuth puts in the context).
func setupRateLimitChain(t *testing.T) (*V2TestContext, *gin.Engine) {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)

	r := ctx.Router
	authed := r.Group("/chain")
	authed.Use(func(c *gin.Context) {
		c.Set("id", ctx.NormalUser.Id)
		c.Set("tenant_id", ctx.TenantID)
		c.Next()
	})
	authed.POST("/token", AddToken)
	authed.PUT("/token", UpdateToken)
	authed.POST("/tenants", CreateTenant)
	authed.PUT("/tenants/:id", UpdateTenant)

	r.POST("/chain/relay",
		func(c *gin.Context) {
			if id, err := strconv.Atoi(c.GetHeader("X-Test-Token-ID")); err == nil && id > 0 {
				c.Set("token_id", id)
			}
			c.Next()
		},
		middleware.BusinessRateLimit(),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	return ctx, r
}

func chainRelay(r *gin.Engine, tokenID int) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chain/relay", nil)
	req.Header.Set("X-Test-Token-ID", strconv.Itoa(tokenID))
	r.ServeHTTP(w, req)
	return w
}

// updateChainToken PUTs a whole-object token update (the v1 endpoint's
// semantics) carrying the given rpm/tpm limits.
func updateChainToken(t *testing.T, r *gin.Engine, tok *repo.Token, rpm, tpm int) map[string]interface{} {
	t.Helper()
	w := V2Request(r, http.MethodPut, "/chain/token", map[string]interface{}{
		"id":              tok.Id,
		"name":            tok.Name,
		"remain_quota":    tok.RemainQuota,
		"unlimited_quota": tok.UnlimitedQuota,
		"expired_time":    tok.ExpiredTime,
		"group":           tok.Group,
		"rate_limit_rpm":  rpm,
		"rate_limit_tpm":  tpm,
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("update token HTTP = %d, body %s", w.Code, w.Body.String())
	}
	return ParseV2Response(t, w)
}

func TestTokenRateLimitChain_UpdateAPIDrivesMiddleware(t *testing.T) {
	ctx, r := setupRateLimitChain(t)
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "chain-rpm-token")

	// Baseline: no limits → relay flows freely.
	for i := 0; i < 3; i++ {
		if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
			t.Fatalf("baseline relay %d = %d, want 200", i+1, w.Code)
		}
	}

	// Set rpm_limit=1 through the UPDATE API and verify the row.
	resp := updateChainToken(t, r, tok, 1, 0)
	if ok, _ := resp["success"].(bool); !ok {
		t.Fatalf("update success=false: %s", resp["message"])
	}
	stored, err := repo.GetTokenById(tok.Id)
	if err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if stored.RateLimitRPM != 1 || stored.RateLimitTPM != 0 {
		t.Fatalf("stored limits = (%d,%d), want (1,0) — update API must persist rpm_limit",
			stored.RateLimitRPM, stored.RateLimitTPM)
	}

	// The middleware reads that same row: 1st request in the minute passes,
	// 2nd is rejected.
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Fatalf("first limited relay = %d, want 200", w.Code)
	}
	w := chainRelay(r, tok.Id)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second relay under rpm_limit=1 = %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q, want positive seconds", ra)
	}

	// Reset to 0 through the API → unlimited again, end to end.
	updateChainToken(t, r, tok, 0, 0)
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Errorf("relay after reset to 0 = %d, want 200 (0 = unlimited)", w.Code)
	}
}

func TestTokenRateLimitChain_TPMUpdateDrivesMiddleware(t *testing.T) {
	ctx, r := setupRateLimitChain(t)
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "chain-tpm-token")

	// tpm_limit=50 via the update API.
	updateChainToken(t, r, tok, 0, 50)
	stored, err := repo.GetTokenById(tok.Id)
	if err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if stored.RateLimitTPM != 50 {
		t.Fatalf("stored tpm_limit = %d, want 50", stored.RateLimitTPM)
	}

	// Quiet window → admitted (single spikes allowed by design).
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Fatalf("relay on quiet TPM window = %d, want 200", w.Code)
	}

	// 60 settled tokens land in the window (production write path fn) —
	// 60 > 50 → the next relay is throttled.
	app.RecordBusinessTPMUsage(tok.Id, ctx.TenantID, 60)
	if w := chainRelay(r, tok.Id); w.Code != http.StatusTooManyRequests {
		t.Fatalf("relay over tpm_limit = %d, want 429", w.Code)
	}

	// Reset tpm to 0 via the API → unlimited even with a hot window.
	updateChainToken(t, r, tok, 0, 0)
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Errorf("relay after tpm reset = %d, want 200 (0 = unlimited)", w.Code)
	}
}

func TestTokenRateLimitAPI_ValidationAndCreatePersists(t *testing.T) {
	ctx, r := setupRateLimitChain(t)

	// Negative and over-cap limits are rejected by both write endpoints.
	for _, body := range []map[string]interface{}{
		{"name": "bad-rpm", "rate_limit_rpm": -1},
		{"name": "bad-tpm", "rate_limit_tpm": app.MaxRateLimitPerMinute + 1},
	} {
		w := V2Request(r, http.MethodPost, "/chain/token", body, nil)
		resp := ParseV2Response(t, w)
		if ok, _ := resp["success"].(bool); ok {
			t.Errorf("AddToken with %v: success=true, want validation rejection", body)
		}
	}

	// A valid create persists both columns.
	w := V2Request(r, http.MethodPost, "/chain/token", map[string]interface{}{
		"name":            "chain-create-limits",
		"unlimited_quota": true,
		"rate_limit_rpm":  7,
		"rate_limit_tpm":  7000,
	}, nil)
	resp := ParseV2Response(t, w)
	if ok, _ := resp["success"].(bool); !ok {
		t.Fatalf("AddToken failed: %s", resp["message"])
	}
	var created repo.Token
	if err := ctx.DB.Where("name = ?", "chain-create-limits").First(&created).Error; err != nil {
		t.Fatalf("load created token: %v", err)
	}
	if created.RateLimitRPM != 7 || created.RateLimitTPM != 7000 {
		t.Errorf("created limits = (%d,%d), want (7,7000)", created.RateLimitRPM, created.RateLimitTPM)
	}

	// UpdateToken rejects invalid limits without touching the row.
	uw := V2Request(r, http.MethodPut, "/chain/token", map[string]interface{}{
		"id":              created.Id,
		"name":            created.Name,
		"unlimited_quota": true,
		"rate_limit_rpm":  -5,
	}, nil)
	uresp := ParseV2Response(t, uw)
	if ok, _ := uresp["success"].(bool); ok {
		t.Errorf("UpdateToken with negative rpm: success=true, want rejection")
	}
	var after repo.Token
	if err := ctx.DB.First(&after, created.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if after.RateLimitRPM != 7 {
		t.Errorf("rpm_limit after rejected update = %d, want unchanged 7", after.RateLimitRPM)
	}
}

func TestTenantRateLimitAPI_PersistsResetsAndEnforces(t *testing.T) {
	ctx, r := setupRateLimitChain(t)

	// Create a tenant carrying aggregate limits.
	w := V2Request(r, http.MethodPost, "/chain/tenants", map[string]interface{}{
		"zitadel_org_id": "org_ratelimit_chain",
		"slug":           "ratelimit-chain",
		"name":           "RateLimit Chain",
		"rate_limit_rpm": 0,
		"rate_limit_tpm": 50,
	}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create tenant HTTP = %d, body %s", w.Code, w.Body.String())
	}
	var createResp struct {
		Data repo.Tenant `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	tenantID := createResp.Data.Id
	if tenantID == "" {
		t.Fatalf("create response carries no tenant id: %s", w.Body.String())
	}
	if createResp.Data.RateLimitTPM != 50 {
		t.Errorf("create response rate_limit_tpm = %d, want 50", createResp.Data.RateLimitTPM)
	}
	stored, err := repo.GetTenantByID(tenantID)
	if err != nil {
		t.Fatalf("reload tenant: %v", err)
	}
	if stored.RateLimitRPM != 0 || stored.RateLimitTPM != 50 {
		t.Fatalf("stored tenant limits = (%d,%d), want (0,50)", stored.RateLimitRPM, stored.RateLimitTPM)
	}

	// A token of that tenant hits the tenant TPM window end to end.
	tok := &repo.Token{
		UserId:         ctx.NormalUser.Id,
		TenantId:       tenantID,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "tenant-chain-token",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := ctx.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed tenant token: %v", err)
	}
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Fatalf("relay on quiet tenant window = %d, want 200", w.Code)
	}
	app.RecordBusinessTPMUsage(tok.Id, tenantID, 60) // aggregate 60 > 50
	if w := chainRelay(r, tok.Id); w.Code != http.StatusTooManyRequests {
		t.Fatalf("relay over tenant tpm_limit = %d, want 429", w.Code)
	}

	// Explicit 0 through the UPDATE API resets to unlimited (pointer field —
	// distinguishable from omitted).
	uw := V2Request(r, http.MethodPut, "/chain/tenants/"+tenantID, map[string]interface{}{
		"rate_limit_tpm": 0,
	}, nil)
	if uw.Code != http.StatusOK {
		t.Fatalf("update tenant HTTP = %d, body %s", uw.Code, uw.Body.String())
	}
	stored, err = repo.GetTenantByID(tenantID)
	if err != nil {
		t.Fatalf("reload tenant after reset: %v", err)
	}
	if stored.RateLimitTPM != 0 {
		t.Fatalf("tenant tpm_limit after explicit-0 update = %d, want 0", stored.RateLimitTPM)
	}
	if w := chainRelay(r, tok.Id); w.Code != http.StatusOK {
		t.Errorf("relay after tenant tpm reset = %d, want 200", w.Code)
	}

	// Validation: negative tenant limits are a 400 on create and update.
	bw := V2Request(r, http.MethodPost, "/chain/tenants", map[string]interface{}{
		"zitadel_org_id": "org_ratelimit_bad",
		"slug":           "ratelimit-bad",
		"name":           "Bad",
		"rate_limit_rpm": -1,
	}, nil)
	AssertV2Status(t, bw, http.StatusBadRequest)
	bu := V2Request(r, http.MethodPut, "/chain/tenants/"+tenantID, map[string]interface{}{
		"rate_limit_rpm": -1,
	}, nil)
	AssertV2Status(t, bu, http.StatusBadRequest)
}
