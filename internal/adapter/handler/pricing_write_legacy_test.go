package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// callLegacyRootHandler drives a legacy root handler (UpdateOption /
// ResetModelRatio) with an authenticated root context, the way the
// /api/option/ routes see it after RootAuth.
func callLegacyRootHandler(t *testing.T, method, path string, body interface{}, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, &buf)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 1)
	h(c)
	return w
}

func readPricingVersionRow(t *testing.T) int64 {
	t.Helper()
	raw, found, err := repo.GetOptionValue(repo.DB, "PricingVersion")
	if err != nil {
		t.Fatalf("read PricingVersion: %v", err)
	}
	if !found {
		return 0
	}
	v, _ := strconv.ParseInt(raw, 10, 64)
	return v
}

// The legacy PUT /api/option/ route rewrites a whole ratio map. For the four
// pricing keys it must go through the same version bump and audit row the
// console batch write uses, or "every pricing change is traceable" is false
// for the oldest root API in the tree.
func TestLegacyOptionWrite_PricingKeyBumpsVersionAndAudits(t *testing.T) {
	setupPricingWriteRouter(t)
	seedPricingVersion(t, 5)
	model := "legacy-option-probe-model"

	current := ratio_setting.GetModelRatioCopy()
	current[model] = 9.5
	raw, _ := json.Marshal(current)

	w := callLegacyRootHandler(t, http.MethodPut, "/api/option/",
		map[string]interface{}{"key": "ModelRatio", "value": string(raw)}, UpdateOption)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	if got := readPricingVersionRow(t); got != 6 {
		t.Errorf("PricingVersion row = %d, want 6 (legacy write must bump the version)", got)
	}
	if v, _, _ := ratio_setting.GetModelRatio(model); v != 9.5 {
		t.Errorf("in-memory model ratio = %v, want 9.5 after the legacy write", v)
	}

	ev := pollAuditRow(t, governance.ActionPricingUpdated, 2*time.Second)
	if ev == nil {
		t.Fatal("no pricing.updated audit row for the legacy option write")
	}
	var details struct {
		Source      string `json:"source"`
		Key         string `json:"key"`
		FromVersion int64  `json:"from_version"`
		ToVersion   int64  `json:"to_version"`
	}
	if err := json.Unmarshal([]byte(ev.Details), &details); err != nil {
		t.Fatalf("unmarshal details: %v — raw: %s", err, ev.Details)
	}
	if details.Source != "legacy_option_api" || details.Key != "ModelRatio" {
		t.Errorf("details = %+v, want source legacy_option_api / key ModelRatio", details)
	}
	if details.FromVersion != 5 || details.ToVersion != 6 {
		t.Errorf("versions = %d→%d, want 5→6", details.FromVersion, details.ToVersion)
	}
}

// A malformed map must be rejected before anything is persisted: the version
// row and the in-memory map stay as they were.
func TestLegacyOptionWrite_MalformedPricingMapRejectedWithoutWrite(t *testing.T) {
	setupPricingWriteRouter(t)
	seedPricingVersion(t, 5)
	before, _, _ := ratio_setting.GetModelRatio("legacy-option-probe-model")

	w := callLegacyRootHandler(t, http.MethodPut, "/api/option/",
		map[string]interface{}{"key": "ModelRatio", "value": "{not-json"}, UpdateOption)
	if w.Code == http.StatusOK {
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["success"] == true {
			t.Fatalf("malformed map accepted: %s", w.Body.String())
		}
	}
	if got := readPricingVersionRow(t); got != 5 {
		t.Errorf("PricingVersion row = %d, want 5 (nothing may be written)", got)
	}
	if after, _, _ := ratio_setting.GetModelRatio("legacy-option-probe-model"); after != before {
		t.Errorf("in-memory ratio changed %v → %v on a rejected write", before, after)
	}
}

// Keys outside the pricing set keep the plain repo.UpdateOption path: no
// version bump, no pricing.updated row.
func TestLegacyOptionWrite_NonPricingKeyDoesNotBumpVersion(t *testing.T) {
	setupPricingWriteRouter(t)
	seedPricingVersion(t, 5)

	w := callLegacyRootHandler(t, http.MethodPut, "/api/option/",
		map[string]interface{}{"key": "Notice", "value": "hello"}, UpdateOption)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if got := readPricingVersionRow(t); got != 5 {
		t.Errorf("PricingVersion row = %d, want 5 (non-pricing key must not bump)", got)
	}
}

// POST /api/option/rest_model_ratio resets ModelRatio to the shipped
// defaults; it is a pricing change and must be versioned and audited too.
func TestResetModelRatio_BumpsVersionAndAudits(t *testing.T) {
	setupPricingWriteRouter(t)
	seedPricingVersion(t, 5)
	model := "legacy-reset-probe-model"
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `": 4.2}`); err != nil {
		t.Fatalf("seed ratio: %v", err)
	}

	w := callLegacyRootHandler(t, http.MethodPost, "/api/option/rest_model_ratio", nil, ResetModelRatio)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["success"] != true {
		t.Fatalf("reset failed: %s", w.Body.String())
	}

	if got := readPricingVersionRow(t); got != 6 {
		t.Errorf("PricingVersion row = %d, want 6", got)
	}
	if _, found, _ := ratio_setting.GetModelRatio(model); found {
		t.Errorf("model %q still has an explicit ratio after reset", model)
	}
	ev := pollAuditRow(t, governance.ActionPricingUpdated, 2*time.Second)
	if ev == nil {
		t.Fatal("no pricing.updated audit row for the reset")
	}
	var details struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal([]byte(ev.Details), &details)
	if details.Source != "legacy_reset" {
		t.Errorf("audit source = %q, want legacy_reset", details.Source)
	}
}

// repo.GetPricing keeps its own ~1-minute cache; a versioned write must drop
// it so the catalogue the console re-fetches right after saving shows the
// new ratio instead of the pre-write one.
func TestVersionedPricingWrite_InvalidatesPricingCache(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	seedPricingVersion(t, 5)
	// repo.GetPricing rebuilds from abilities/channels/models; the harness
	// does not create those tables, and updatePricing returns before
	// stamping the cache when its query fails, so create them empty here.
	for _, tbl := range []interface{}{&repo.Ability{}, &repo.Channel{}, &repo.Model{}, &repo.Vendor{}} {
		if err := repo.DB.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	_ = repo.GetPricing() // warm the cache
	if repo.PricingCacheAge() > time.Minute {
		t.Fatal("cache not warm after GetPricing")
	}

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": "cache-probe-model", "model_ratio": 2.5}},
		map[string]string{"If-Match-Pricing-Version": "5"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if repo.PricingCacheAge() < time.Minute {
		t.Error("pricing cache still warm after a versioned write; the next GET would serve pre-write ratios")
	}
}
