package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// seedPricedAndUnpriced serves two models through one channel: one with a
// configured ratio and one nobody priced. The ratio table is restored on
// cleanup.
func seedPricedAndUnpriced(t *testing.T, ctx *pricingCtx, priced, unpriced string) {
	t.Helper()
	ch := &repo.Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "pc-channel", Models: priced + "," + unpriced, Group: "default"}
	if err := ctx.db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, m := range []string{priced, unpriced} {
		if err := ctx.db.Create(&repo.Ability{Group: "default", Model: m, ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}
	prev := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(prev)
		repo.InvalidatePricingCache()
	})
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + priced + `":2.5}`); err != nil {
		t.Fatalf("seed ratio: %v", err)
	}
	repo.InvalidatePricingCache()
}

func pricingRowsByName(t *testing.T, w *httptest.ResponseRecorder) map[string]map[string]interface{} {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	data, _ := parsePricing(t, w)["data"].(map[string]interface{})
	rows, _ := data["pricing"].([]interface{})
	out := map[string]map[string]interface{}{}
	for _, r := range rows {
		if m, _ := r.(map[string]interface{}); m != nil {
			out[m["model_name"].(string)] = m
		}
	}
	return out
}

// The tenant pricing view lists an unpriced model flagged, with no invented
// ratio, so an admin can price it. The public catalogues (v1 /api/pricing and
// the switch catalogue) leave it out: their older readers only see
// model_ratio, and a 0 there reads as "free" (or the cheapest pick).
// Mutation: drop repo.PricedOnly from GetSwitchPricing or GetPricing and the
// public half goes red; set PriceConfigured unconditionally and both go red.
func TestPricingCatalogue_PriceConfigured_V2AndSwitch(t *testing.T) {
	ctx := setupPricingRouter(t)
	priced, unpriced := "pc-priced-model", "pc-unpriced-model"
	seedPricedAndUnpriced(t, ctx, priced, unpriced)

	v2 := pricingRowsByName(t, getPricing(ctx, ctx.tenantSlug))
	if got := v2[priced]["price_configured"]; got != true {
		t.Errorf("v2: priced model price_configured = %v, want true", got)
	}
	if got, _ := v2[priced]["model_ratio"].(float64); got != 2.5 {
		t.Errorf("v2: priced model_ratio = %v, want 2.5", v2[priced]["model_ratio"])
	}
	if v2[unpriced] == nil {
		t.Fatalf("v2: unpriced model missing; the admin view must list it so it can be priced")
	}
	if got := v2[unpriced]["price_configured"]; got != false {
		t.Errorf("v2: unpriced model price_configured = %v, want false", got)
	}
	if got, _ := v2[unpriced]["model_ratio"].(float64); got != 0 {
		t.Errorf("v2: unpriced model_ratio = %v, want 0 (not the 37.5 fallback)", v2[unpriced]["model_ratio"])
	}

	sw := httptest.NewRecorder()
	switchPricingRouter().ServeHTTP(sw, httptest.NewRequest(http.MethodGet, "/api/v2/switch/pricing", nil))
	switchRows := pricingRowsByName(t, sw)
	if got := switchRows[priced]["price_configured"]; got != true {
		t.Errorf("switch: priced model price_configured = %v, want true", got)
	}
	if switchRows[unpriced] != nil {
		t.Errorf("switch: unpriced model listed as %v; the desktop client would rank its 0 ratio as the cheapest", switchRows[unpriced])
	}

	r := gin.New()
	r.GET("/api/pricing", GetPricing)
	v1 := httptest.NewRecorder()
	r.ServeHTTP(v1, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	if v1.Code != http.StatusOK {
		t.Fatalf("v1: status = %d, body: %s", v1.Code, v1.Body.String())
	}
	var body struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(v1.Body.Bytes(), &body); err != nil {
		t.Fatalf("v1: decode: %v", err)
	}
	seen := map[string]bool{}
	for _, row := range body.Data {
		name, _ := row["model_name"].(string)
		seen[name] = true
	}
	if !seen[priced] {
		t.Errorf("v1: priced model missing from /api/pricing")
	}
	if seen[unpriced] {
		t.Errorf("v1: unpriced model listed; peer gateways sync ratios from /api/pricing and would copy a 0 as free")
	}
}
