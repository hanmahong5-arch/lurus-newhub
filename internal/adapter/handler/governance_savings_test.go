package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// The savings headline must be derived from real aggregation, never a constant.
// Seed gpt-4o spend, then assert the endpoint reports exactly the hand-computed
// re-priced figure (see TestCompute_NormalSaving for the arithmetic).
func TestGetGovernanceSavings_HeadlineFromRealRows(t *testing.T) {
	ratio_setting.InitRatioSettings()
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	// seedConsumeLog fixes prompt=100, completion=200. quota=1000 → cons 940.
	seedConsumeLog(t, ctx.db, "gpt-4o", 1, 1000, 0, "", 1)

	w := doGET(ctx.router, "/api/v2/admin/governance/savings?hours=168")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v body=%s", resp["success"], w.Body.String())
	}
	data := resp["data"].(map[string]interface{})

	if int(data["total_spend_quota"].(float64)) != 1000 {
		t.Errorf("total_spend_quota = %v, want 1000", data["total_spend_quota"])
	}
	if data["heuristic"] != true {
		t.Errorf("expected heuristic=true, got %v", data["heuristic"])
	}
	if data["caveat"] == "" || data["caveat"] == nil {
		t.Errorf("expected a non-empty caveat")
	}

	scenarios := data["scenarios"].(map[string]interface{})
	cons := scenarios["conservative"].(map[string]interface{})
	if int(cons["total_savings_quota"].(float64)) != 940 {
		t.Errorf("conservative total_savings_quota = %v, want 940", cons["total_savings_quota"])
	}
	// spend_by_model surfaces the real row.
	models := data["spend_by_model"].([]interface{})
	if len(models) != 1 {
		t.Fatalf("expected 1 model row, got %d", len(models))
	}
}

// Empty DB must yield a genuine zero — not a fallback constant.
func TestGetGovernanceSavings_EmptyIsZero(t *testing.T) {
	ratio_setting.InitRatioSettings()
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/admin/governance/savings?hours=24")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	if int(data["total_spend_quota"].(float64)) != 0 {
		t.Errorf("total_spend_quota = %v, want 0", data["total_spend_quota"])
	}
	scenarios := data["scenarios"].(map[string]interface{})
	cons := scenarios["conservative"].(map[string]interface{})
	if int(cons["total_savings_quota"].(float64)) != 0 {
		t.Errorf("conservative savings = %v, want 0", cons["total_savings_quota"])
	}
}

// spend_by_product reads the source_product tag out of the log's Other JSON and
// folds untagged rows into the default product.
func TestGetGovernanceSavings_SpendByProduct(t *testing.T) {
	ratio_setting.InitRatioSettings()
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	// One tagged row (kova) and one untagged row (folds into lurus-api).
	tagged := &entity.Log{
		UserId: 1, TenantId: "test-tenant", CreatedAt: time.Now().Add(-time.Hour).Unix(),
		Type: entity.LogTypeConsume, ModelName: "gpt-4o", Quota: 500,
		PromptTokens: 100, CompletionTokens: 200,
		Other: `{"source_product":"kova"}`,
	}
	if err := ctx.db.Create(tagged).Error; err != nil {
		t.Fatalf("seed tagged: %v", err)
	}
	seedConsumeLog(t, ctx.db, "gpt-4o", 1, 500, 0, "", 1) // Other="" → lurus-api

	w := doGET(ctx.router, "/api/v2/admin/governance/savings?hours=168")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	products := data["spend_by_product"].([]interface{})

	got := map[string]int{}
	for _, p := range products {
		row := p.(map[string]interface{})
		got[row["product"].(string)] = int(row["total_quota"].(float64))
	}
	if got["kova"] != 500 {
		t.Errorf("kova spend = %d, want 500 (got products=%v)", got["kova"], got)
	}
	// DefaultSourceProduct 归一为 llm-api 后(2026-08-22,platform
	// identity.products 里从来只有 llm-api,lurus-api 是从未存在过的标签),
	// 无 product 归属的 relay 支出落到 llm-api。
	if got["llm-api"] != 500 {
		t.Errorf("llm-api spend = %d, want 500 (got products=%v)", got["llm-api"], got)
	}
}
