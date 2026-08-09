package handler

// cov_handler-pricing_edge_test.go — business-acceptance edge coverage for the
// pricing/ratio/option surface: ratio_sync.go, option.go, prefill_group.go,
// v2_models_write.go, pricing.go, v2_pricing.go, ratio_config.go,
// tenant_model_limits.go.
//
// Helpers in this file are prefixed handlerPricing to avoid collisions with
// other concurrently-written cov_*_test.go files in this package.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// handlerPricingNewCtx builds a bare gin.Context + recorder without going
// through a router, so tests can set c.Params directly (e.g. an empty
// tenant_slug that gin's router itself could never route to).
func handlerPricingNewCtx(method, target string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		data, _ := json.Marshal(body)
		req = httptest.NewRequest(method, target, bytes.NewReader(data))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func handlerPricingParseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return m
}

// ===========================================================================
// ratio_config.go — GetRatioConfig
// ===========================================================================

func TestCovHandlerPricing_GetRatioConfig_Disabled(t *testing.T) {
	prev := ratio_setting.IsExposeRatioEnabled()
	ratio_setting.SetExposeRatioEnabled(false)
	t.Cleanup(func() { ratio_setting.SetExposeRatioEnabled(prev) })

	c, w := handlerPricingNewCtx(http.MethodGet, "/ratio-config", nil)
	GetRatioConfig(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when expose-ratio disabled, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
	if resp["message"] != "倍率配置接口未启用" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestCovHandlerPricing_GetRatioConfig_Enabled(t *testing.T) {
	prev := ratio_setting.IsExposeRatioEnabled()
	ratio_setting.SetExposeRatioEnabled(true)
	t.Cleanup(func() { ratio_setting.SetExposeRatioEnabled(prev) })

	c, w := handlerPricingNewCtx(http.MethodGet, "/ratio-config", nil)
	GetRatioConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %T", resp["data"])
	}
	for _, key := range []string{"model_ratio", "completion_ratio", "cache_ratio", "model_price"} {
		if _, present := data[key]; !present {
			t.Errorf("expected exposed data to contain %q, got keys=%v", key, data)
		}
	}
}

// ===========================================================================
// option.go — UpdateOption edge cases
// ===========================================================================

func handlerPricingOptionRouter() *gin.Engine {
	r := gin.New()
	r.POST("/option", UpdateOption)
	return r
}

func TestCovHandlerPricing_UpdateOption_MalformedJSON(t *testing.T) {
	r := handlerPricingOptionRouter()
	req := httptest.NewRequest(http.MethodPost, "/option", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["message"] != "无效的参数" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestCovHandlerPricing_UpdateOption_NullValueRejected(t *testing.T) {
	r := handlerPricingOptionRouter()
	w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{"key": "SystemName", "value": nil}, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for null value, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if !strings.Contains(resp["message"].(string), "value 不能为 null") {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

// TestCovHandlerPricing_UpdateOption_EmailDomainRestriction_RejectsEmptyWhitelist
// verifies an admin cannot flip EmailDomainRestrictionEnabled on while the
// whitelist is empty (that would lock out every signup with no way in).
func TestCovHandlerPricing_UpdateOption_EmailDomainRestriction_RejectsEmptyWhitelist(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	prevList := common.EmailDomainWhitelist
	common.EmailDomainWhitelist = nil
	t.Cleanup(func() { common.EmailDomainWhitelist = prevList })

	r := handlerPricingOptionRouter()
	w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key": "EmailDomainRestrictionEnabled", "value": true,
	}, nil)

	// Handler returns 200 with success:false for this validation path (not 400).
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false, body=%s", w.Body.String())
	}
	if resp["message"] != "无法启用邮箱域名限制，请先填入限制的邮箱域名！" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
	if common.EmailDomainRestrictionEnabled {
		t.Errorf("global flag must remain false after a rejected update")
	}

	// Non-empty whitelist must be allowed to enable.
	common.EmailDomainWhitelist = []string{"acme.example"}
	w2 := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key": "EmailDomainRestrictionEnabled", "value": true,
	}, nil)
	resp2 := handlerPricingParseBody(t, w2)
	if resp2["success"] != true {
		t.Fatalf("expected success=true with a non-empty whitelist, body=%s", w2.Body.String())
	}
	t.Cleanup(func() { common.EmailDomainRestrictionEnabled = false })
}

func TestCovHandlerPricing_UpdateOption_ModelRequestRateLimitGroup_RejectsNegative(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := handlerPricingOptionRouter()
	// limits[0] (total count) negative -> CheckModelRequestRateLimitGroup rejects.
	w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key":   "ModelRequestRateLimitGroup",
		"value": `{"vip":[-1,10]}`,
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for a negative rate limit, body=%s", w.Body.String())
	}
	if !strings.Contains(resp["message"].(string), "negative") {
		t.Errorf("expected an error mentioning the negative-limit rejection, got %v", resp["message"])
	}
}

// TestCovHandlerPricing_UpdateOption_ConsoleSettings_InvalidJSON exercises the
// four console_setting.* validation branches with malformed input each.
func TestCovHandlerPricing_UpdateOption_ConsoleSettings_InvalidJSON(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	cases := []string{
		"console_setting.api_info",
		"console_setting.announcements",
		"console_setting.faq",
		"console_setting.uptime_kuma_groups",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			r := handlerPricingOptionRouter()
			w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
				"key":   key,
				"value": "not-a-json-array-at-all",
			}, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			resp := handlerPricingParseBody(t, w)
			if resp["success"] != false {
				t.Fatalf("expected success=false for malformed %s, body=%s", key, w.Body.String())
			}
			if resp["message"] == "" || resp["message"] == nil {
				t.Errorf("expected a non-empty validation error message")
			}
		})
	}
}

// TestCovHandlerPricing_UpdateOption_UnknownKeyPersistsGenerically covers the
// pass-through path: keys the switch doesn't special-case still get validated
// as strings and written straight through to repo.UpdateOption / OptionMap.
func TestCovHandlerPricing_UpdateOption_UnknownKeyPersistsGenerically(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := handlerPricingOptionRouter()

	// bool -> "true"/"false" string coercion.
	w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key": "CovPricingBoolOpt", "value": true,
	}, nil)
	AssertV2Success(t, w)
	if got := common.OptionMap["CovPricingBoolOpt"]; got != "true" {
		t.Errorf("expected OptionMap[key]='true', got %q", got)
	}

	// float64 -> string coercion, and a long unicode value round-trips intact.
	longUnicode := strings.Repeat("配置-测试-🚀-", 200)
	w2 := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key": "CovPricingUnicodeOpt", "value": longUnicode,
	}, nil)
	AssertV2Success(t, w2)
	var stored repo.Option
	if err := ctx.DB.Where("key = ?", "CovPricingUnicodeOpt").First(&stored).Error; err != nil {
		t.Fatalf("expected option row persisted: %v", err)
	}
	if stored.Value != longUnicode {
		t.Errorf("expected long unicode value to round-trip exactly, got len=%d want len=%d", len(stored.Value), len(longUnicode))
	}

	// GetOptions must never leak this generic option's *Key* filtering (sanity:
	// a key not ending in Token/Secret/Key/secret/api_key must be listed).
	c2, w3 := handlerPricingNewCtx(http.MethodGet, "/options", nil)
	GetOptions(c2)
	resp3 := handlerPricingParseBody(t, w3)
	items, _ := resp3["data"].([]interface{})
	var found bool
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["key"] == "CovPricingUnicodeOpt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CovPricingUnicodeOpt to be listed by GetOptions")
	}
}

// UpdateModelRatioByJSONString (reached transitively from repo.UpdateOption for
// key "ModelRatio") used to reset the live in-memory modelRatioMap to an EMPTY
// map *before* parsing the caller-supplied JSON, so one malformed admin request
// zeroed every model's billing ratio while reporting success:false — implying
// nothing had changed. A failed update must be a no-op.
//
// fix_ratio_bad_json_test.go pins that contract by calling ratio_setting
// directly; this test keeps the whole admin round trip
// (POST /option -> handler.UpdateOption -> repo.UpdateOption -> ratio_setting)
// under coverage, because that is the path an operator actually takes.
func TestCovHandlerPricing_UpdateOption_ModelRatio_MalformedJSON_KeepsLiveState(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Snapshot + restore the real global model-ratio map around this test.
	snapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(snapshot)
	})

	// Seed a known-good ratio so the wipe is observable.
	const knownModel = "cov-pricing-finding-model"
	if err := ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":7.5}`, knownModel)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got, ok := ratio_setting.GetModelRatioCopy()[knownModel]; !ok || got != 7.5 {
		t.Fatalf("precondition failed: expected seeded ratio 7.5, got %v (present=%v)", got, ok)
	}

	r := handlerPricingOptionRouter()
	w := V2Request(r, http.MethodPost, "/option", map[string]interface{}{
		"key":   "ModelRatio",
		"value": "this is not valid json {{{",
	}, nil)

	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected the malformed update to report failure, body=%s", w.Body.String())
	}

	after := ratio_setting.GetModelRatioCopy()
	got, stillPresent := after[knownModel]
	if !stillPresent {
		t.Fatalf("the seeded model ratio was wiped by a failed update — a malformed admin payload must not zero live billing ratios")
	}
	if got != 7.5 {
		t.Fatalf("model ratio for %s = %v, want the pre-update 7.5", knownModel, got)
	}
}

// ===========================================================================
// ratio_sync.go — nearlyEqual / buildDifferences pruning edge cases
// ===========================================================================

func TestCovHandlerPricing_NearlyEqual_AGreaterThanB(t *testing.T) {
	// The a > b branch is otherwise never exercised elsewhere in the suite.
	if !nearlyEqual(2.0, 2.0-1e-12) {
		t.Error("expected a>b branch to treat a tiny delta as equal")
	}
	if nearlyEqual(3.0, 1.0) {
		t.Error("expected a>b branch to treat a large delta as unequal")
	}
}

// TestCovHandlerPricing_BuildDifferences_ChannelPruning exercises the
// cross-channel pruning pass: a channel whose only contribution to the
// overall diff set is "same"/absent values must be scrubbed from every
// entry's Upstreams/Confidence maps, while a channel with at least one real
// difference survives everywhere it appears.
func TestCovHandlerPricing_BuildDifferences_ChannelPruning(t *testing.T) {
	localData := map[string]any{
		"model_ratio": map[string]float64{"m-a": 1.0},
	}
	channels := []struct {
		name string
		data map[string]any
	}{
		{
			name: "chan-diff",
			data: map[string]any{
				"model_ratio": map[string]any{"m-a": float64(2.0), "m-b": float64(5.0)},
			},
		},
		{
			name: "chan-same",
			data: map[string]any{
				"model_ratio": map[string]any{"m-a": float64(1.0)},
			},
		},
	}

	diffs := buildDifferences(localData, channels)

	itemA, ok := diffs["m-a"]["model_ratio"]
	if !ok {
		t.Fatalf("expected m-a/model_ratio in differences, got %#v", diffs)
	}
	if _, present := itemA.Upstreams["chan-same"]; present {
		t.Errorf("expected chan-same pruned from m-a (it never disagrees anywhere), got %v", itemA.Upstreams)
	}
	if v, present := itemA.Upstreams["chan-diff"]; !present || v.(float64) != 2.0 {
		t.Errorf("expected chan-diff=2.0 to survive pruning for m-a, got %v (present=%v)", v, present)
	}

	// m-b has no local value at all: inclusion via the hasUpstreamValue path
	// (localValue==nil), not the hasDifference path.
	itemB, ok := diffs["m-b"]["model_ratio"]
	if !ok {
		t.Fatalf("expected m-b/model_ratio in differences (local absent, upstream present), got %#v", diffs)
	}
	if v, present := itemB.Upstreams["chan-diff"]; !present || v.(float64) != 5.0 {
		t.Errorf("expected chan-diff=5.0 for m-b, got %v (present=%v)", v, present)
	}
	if _, present := itemB.Upstreams["chan-same"]; present {
		t.Errorf("expected chan-same pruned from m-b too (it has no m-b entry at all), got %v", itemB.Upstreams)
	}
}

// ===========================================================================
// ratio_sync.go — GetSyncableChannels edge cases
// ===========================================================================

func TestCovHandlerPricing_GetSyncableChannels_EmptyBaseURLExcluded(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Type 0 has an empty default base URL (constant.ChannelBaseURLs[0]=="");
	// this channel's GetBaseURL() resolves to "" and must be excluded.
	excluded := &repo.Channel{Name: "no-base-url", TenantId: ctx.TenantID, Key: "sk-x", Status: 1, Type: 0, CreatedTime: common.GetTimestamp()}
	if err := ctx.DB.Create(excluded).Error; err != nil {
		t.Fatalf("seed excluded channel: %v", err)
	}
	included := &repo.Channel{Name: "has-base-url", TenantId: ctx.TenantID, Key: "sk-y", Status: 1, Type: 0, CreatedTime: common.GetTimestamp()}
	if err := ctx.DB.Create(included).Error; err != nil {
		t.Fatalf("seed included channel: %v", err)
	}
	explicitURL := "https://upstream.example.com"
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", included.Id).Update("base_url", explicitURL).Error; err != nil {
		t.Fatalf("set base_url: %v", err)
	}

	c, w := handlerPricingNewCtx(http.MethodGet, "/syncable", nil)
	GetSyncableChannels(c)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	items, _ := resp["data"].([]interface{})

	var sawExcluded, sawIncluded, sawPreset bool
	for _, it := range items {
		m := it.(map[string]interface{})
		switch m["name"] {
		case "no-base-url":
			sawExcluded = true
		case "has-base-url":
			sawIncluded = true
			if m["base_url"] != explicitURL {
				t.Errorf("expected base_url=%q, got %v", explicitURL, m["base_url"])
			}
		}
		if int(m["id"].(float64)) == -100 {
			sawPreset = true
		}
	}
	if sawExcluded {
		t.Error("channel with empty resolved base_url must be excluded from the syncable list")
	}
	if !sawIncluded {
		t.Error("channel with an explicit base_url must be included")
	}
	if !sawPreset {
		t.Error("expected the -100 official-preset entry to always be present")
	}
}

// ===========================================================================
// ratio_sync.go — FetchUpstreamRatios (network-facing handler, driven via a
// local httptest.Server so no real external network is touched)
// ===========================================================================

func handlerPricingUpstreamServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/ratio_config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"model_ratio":{"cov-fetch-diff-model":2.0},"completion_ratio":{"cov-fetch-diff-model":1.0}}}`))
	})
	mux.HandleFunc("/type2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"model_name":"cov-fetch-type2-ratio","quota_type":0,"model_ratio":3.0,"completion_ratio":2.0},
			{"model_name":"cov-fetch-type2-price","quota_type":1,"model_price":0.002}
		]`))
		// NOTE: this is a bare array; the handler's own {success,data} envelope
		// still applies to the wrapping response below via the pricing-list path.
	})
	mux.HandleFunc("/type2-wrapped", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[
			{"model_name":"cov-fetch-type2-ratio","quota_type":0,"model_ratio":3.0,"completion_ratio":2.0},
			{"model_name":"cov-fetch-type2-price","quota_type":1,"model_price":0.002}
		]}`))
	})
	mux.HandleFunc("/upstream-fail", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"upstream auth failed"}`))
	})
	mux.HandleFunc("/bad-json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not-json-at-all`))
	})
	mux.HandleFunc("/server-error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	mux.HandleFunc("/custom-path", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"model_ratio":{"cov-fetch-custom-path":9.0}}}`))
	})
	mux.HandleFunc("/wrong-content-type", func(w http.ResponseWriter, r *http.Request) {
		// Valid JSON body, but a non-json Content-Type header — the handler
		// only logs a warning and still parses the body.
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"success":true,"data":{"model_ratio":{"cov-fetch-wrong-ct":4.0}}}`))
	})
	mux.HandleFunc("/unparseable-shape", func(w http.ResponseWriter, r *http.Request) {
		// success:true but data is neither a type1 map nor a type2 list —
		// both fallback parses must fail.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":12345}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCovHandlerPricing_FetchUpstreamRatios_Type1Happy_ProducesDifference(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)

	snapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(snapshot) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"cov-fetch-diff-model":1.0}`); err != nil {
		t.Fatalf("seed local ratio: %v", err)
	}

	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "srv1", "base_url": srv.URL},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 test result, got %d", len(results))
	}
	r0 := results[0].(map[string]interface{})
	if r0["name"] != "srv1" {
		t.Errorf("expected uniqueName='srv1' (no channel id suffix for upstreams-path entries), got %v", r0["name"])
	}
	if r0["status"] != "success" {
		t.Errorf("expected status=success, got %v (body=%s)", r0["status"], w.Body.String())
	}

	diffs := data["differences"].(map[string]interface{})
	modelDiff, ok := diffs["cov-fetch-diff-model"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a difference entry for cov-fetch-diff-model, got %#v", diffs)
	}
	ratioDiff := modelDiff["model_ratio"].(map[string]interface{})
	if ratioDiff["current"].(float64) != 1.0 {
		t.Errorf("expected current=1.0, got %v", ratioDiff["current"])
	}
	upstreams := ratioDiff["upstreams"].(map[string]interface{})
	if upstreams["srv1"].(float64) != 2.0 {
		t.Errorf("expected upstream srv1=2.0, got %v", upstreams["srv1"])
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_Type2PricingList(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)

	// Neither cov-fetch-type2-* model exists locally, so both must surface via
	// the hasUpstreamValue (localValue==nil) inclusion path once converted
	// from the type2 pricing-list shape into type1 maps.
	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "pricingsrv", "base_url": srv.URL, "endpoint": "/type2-wrapped"},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)

	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	diffs := data["differences"].(map[string]interface{})

	ratioEntry, ok := diffs["cov-fetch-type2-ratio"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected type2 model_ratio conversion to surface a difference entry, got %#v", diffs)
	}
	mr := ratioEntry["model_ratio"].(map[string]interface{})
	if mr["upstreams"].(map[string]interface{})["pricingsrv"].(float64) != 3.0 {
		t.Errorf("expected converted model_ratio=3.0, got %v", mr["upstreams"])
	}

	priceEntry, ok := diffs["cov-fetch-type2-price"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected type2 model_price conversion to surface a difference entry, got %#v", diffs)
	}
	mp := priceEntry["model_price"].(map[string]interface{})
	if mp["upstreams"].(map[string]interface{})["pricingsrv"].(float64) != 0.002 {
		t.Errorf("expected converted model_price=0.002, got %v", mp["upstreams"])
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_ErrorPaths(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)

	cases := []struct {
		name       string
		endpoint   string
		wantErrSub string
	}{
		{"upstream_reports_failure", "/upstream-fail", "upstream auth failed"},
		{"malformed_json_body", "/bad-json", ""},
		{"non_200_status", "/server-error", "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"upstreams": []map[string]interface{}{
					{"name": "errsrv", "base_url": srv.URL, "endpoint": tc.endpoint},
				},
			}
			c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
			FetchUpstreamRatios(c)
			resp := handlerPricingParseBody(t, w)
			data := resp["data"].(map[string]interface{})
			results := data["test_results"].([]interface{})
			if len(results) != 1 {
				t.Fatalf("expected 1 test result, got %d", len(results))
			}
			r0 := results[0].(map[string]interface{})
			if r0["status"] != "error" {
				t.Fatalf("expected status=error for %s, got %v", tc.name, r0["status"])
			}
			errMsg, _ := r0["error"].(string)
			if tc.wantErrSub != "" && !strings.Contains(errMsg, tc.wantErrSub) {
				t.Errorf("expected error to contain %q, got %q", tc.wantErrSub, errMsg)
			}
		})
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_ConnectionRefused_Retries(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// A server we immediately close: the URL is well-formed but nothing is
	// listening, forcing the handler's 3-attempt retry loop to exhaust and
	// report a connection error.
	deadSrv := httptest.NewServer(http.NewServeMux())
	deadURL := deadSrv.URL
	deadSrv.Close()

	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "deadsrv", "base_url": deadURL},
		},
		"timeout": 2,
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	start := time.Now()
	FetchUpstreamRatios(c)
	elapsed := time.Since(start)

	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r0 := results[0].(map[string]interface{})
	if r0["status"] != "error" {
		t.Fatalf("expected status=error for an unreachable upstream, got %v", r0["status"])
	}
	// The retry loop sleeps 200ms+400ms+800ms=1.4s between 3 attempts.
	if elapsed < 1300*time.Millisecond {
		t.Errorf("expected the 3-attempt retry backoff (~1.4s) to have elapsed, only took %v", elapsed)
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_CustomEndpointNoLeadingSlash(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)

	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			// No leading "/" — handler must prefix one before building fullURL.
			{"name": "custom", "base_url": srv.URL, "endpoint": "custom-path"},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	r0 := results[0].(map[string]interface{})
	if r0["status"] != "success" {
		t.Fatalf("expected the no-leading-slash endpoint to still resolve correctly, got %v body=%s", r0["status"], w.Body.String())
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_InvalidBaseURLFiltered(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// base_url not starting with "http" must be filtered out entirely, leaving
	// zero valid upstreams even though the request body isn't empty.
	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "ftp-upstream", "base_url": "ftp://not-http.example.com"},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when every upstream is filtered out, body=%s", w.Body.String())
	}
	if resp["message"] != "无有效上游渠道" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_ChannelIDsPath(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)

	ch := &repo.Channel{Name: "chan-sync-src", TenantId: ctx.TenantID, Key: "sk-chan", Status: 1, Type: 1, CreatedTime: common.GetTimestamp()}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("base_url", srv.URL).Error; err != nil {
		t.Fatalf("set base_url: %v", err)
	}

	body := map[string]interface{}{
		"channel_ids": []int64{int64(ch.Id)},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result from the channel_ids path, got %d", len(results))
	}
	r0 := results[0].(map[string]interface{})
	wantName := fmt.Sprintf("%s(%d)", "chan-sync-src", ch.Id)
	if r0["name"] != wantName {
		t.Errorf("expected uniqueName=%q (name+channel id suffix), got %v", wantName, r0["name"])
	}
	if r0["status"] != "success" {
		t.Errorf("expected status=success, got %v", r0["status"])
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_ChannelIDs_NoMatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"channel_ids": []int64{999999},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for channel_ids matching nothing, body=%s", w.Body.String())
	}
	if resp["message"] != "无有效上游渠道" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

// ===========================================================================
// tenant_model_limits.go — DB failure + unknown-tenant branches
// ===========================================================================

func handlerPricingModelLimitCtx(t *testing.T) *V2TestContext {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("automigrate ModelRateLimit: %v", err)
	}
	return ctx
}

func TestCovHandlerPricing_ListTenantModelLimits_UnknownTenant404(t *testing.T) {
	ctx := handlerPricingModelLimitCtx(t)
	defer ctx.Cleanup()

	c, w := handlerPricingNewCtx(http.MethodGet, "/model-limits", nil)
	c.Params = gin.Params{{Key: "id", Value: "no-such-tenant-xyz"}}
	ListTenantModelLimits(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCovHandlerPricing_ListTenantModelLimits_DBError(t *testing.T) {
	ctx := handlerPricingModelLimitCtx(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	c, w := handlerPricingNewCtx(http.MethodGet, "/model-limits", nil)
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	ListTenantModelLimits(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the underlying table is gone, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
}

func TestCovHandlerPricing_UpsertTenantModelLimit_DBError(t *testing.T) {
	ctx := handlerPricingModelLimitCtx(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	c, w := handlerPricingNewCtx(http.MethodPut, "/model-limits", map[string]interface{}{
		"model": "some-model", "rate_limit_rpm": 5,
	})
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	UpsertTenantModelLimit(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCovHandlerPricing_DeleteTenantModelLimit_UnknownTenant404(t *testing.T) {
	ctx := handlerPricingModelLimitCtx(t)
	defer ctx.Cleanup()

	c, w := handlerPricingNewCtx(http.MethodDelete, "/model-limits?model=x", nil)
	c.Params = gin.Params{{Key: "id", Value: "no-such-tenant-xyz"}}
	c.Request.URL.RawQuery = "model=x"
	DeleteTenantModelLimit(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCovHandlerPricing_DeleteTenantModelLimit_DBError(t *testing.T) {
	ctx := handlerPricingModelLimitCtx(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	c, w := handlerPricingNewCtx(http.MethodDelete, "/model-limits?model=x", nil)
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	c.Request.URL.RawQuery = "model=x"
	DeleteTenantModelLimit(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// ===========================================================================
// v2_models_write.go — CreateModelV2 / DeleteModelV2 edge cases
// ===========================================================================

type handlerPricingModelsCtx struct {
	db      *repo.Tenant
	cleanup func()
}

// handlerPricingModelsSetup mirrors v2_models_write_test.go's setupModelsWriteRouter
// but returns raw ingredients (no router) so tests can drive the handlers
// directly via gin.CreateTestContext with hand-picked context state.
func handlerPricingModelsSetup(t *testing.T) (*V2TestContext, *repo.Tenant) {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&repo.Model{}, &repo.Vendor{}); err != nil {
		t.Fatalf("automigrate Model/Vendor: %v", err)
	}
	tenant, err := repo.GetTenantByID(ctx.TenantID)
	if err != nil {
		t.Fatalf("load seeded tenant: %v", err)
	}
	return ctx, tenant
}

func handlerPricingAdminModelCtx(method, target string, body interface{}, tenant *repo.Tenant) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := handlerPricingNewCtx(method, target, body)
	c.Params = gin.Params{{Key: "tenant_slug", Value: tenant.Slug}}
	c.Set("id", 1)
	c.Set("role", common.RoleAdminUser)
	c.Set("tenant_context", &middleware.TenantContext{TenantID: tenant.Id, UserID: 1})
	return c, w
}

func TestCovHandlerPricing_CreateModelV2_EmptySlug(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()
	_ = tenant

	c, w := handlerPricingNewCtx(http.MethodPost, "/models", map[string]interface{}{"model_name": "x"})
	c.Params = gin.Params{{Key: "tenant_slug", Value: ""}}
	CreateModelV2(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["error_code"] != "INVALID_TENANT_SLUG" {
		t.Errorf("unexpected error_code: %v", resp["error_code"])
	}
}

func TestCovHandlerPricing_CreateModelV2_MissingTenantContext(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	c, w := handlerPricingNewCtx(http.MethodPost, "/models", map[string]interface{}{"model_name": "x"})
	c.Params = gin.Params{{Key: "tenant_slug", Value: tenant.Slug}}
	// Deliberately no tenant_context set.
	CreateModelV2(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing tenant context, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCovHandlerPricing_CreateModelV2_MalformedBody(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	c, w := handlerPricingAdminModelCtx(http.MethodPost, "/models", nil, tenant)
	c.Request = httptest.NewRequest(http.MethodPost, "/models", strings.NewReader("{bad json"))
	c.Request.Header.Set("Content-Type", "application/json")
	CreateModelV2(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["error_code"] != "INVALID_REQUEST" {
		t.Errorf("unexpected error_code: %v", resp["error_code"])
	}
}

func TestCovHandlerPricing_CreateModelV2_DuplicateCheckDBError(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Model{}); err != nil {
		t.Fatalf("drop Model table: %v", err)
	}

	c, w := handlerPricingAdminModelCtx(http.MethodPost, "/models", map[string]interface{}{"model_name": "whatever"}, tenant)
	CreateModelV2(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the Model table itself is gone, got %d body=%s", w.Code, w.Body.String())
	}
}

// Submitted pricing used to be accepted and then silently discarded (repo.Model
// has no ratio columns); it now lands in ratio_setting — see
// fix_v2_models_pricing_test.go, which pins where each field goes. What is
// asserted here instead is the shape of the response: modelV2View is a
// catalogue view with no pricing fields, so a 201 must not echo the submitted
// ratios back as if the view carried them, and enable_groups (derived from the
// serving channels, never writable) must come back null.
func TestCovHandlerPricing_CreateModelV2_PricingNotEchoedInCatalogueView(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	c, w := handlerPricingAdminModelCtx(http.MethodPost, "/models", map[string]interface{}{
		"model_name":       "cov-pricing-view-shape-model",
		"model_ratio":      999.5,
		"completion_ratio": 2.5,
	}, tenant)
	CreateModelV2(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// modelV2View has no pricing fields at all — they must not appear as keys.
	for _, forbidden := range []string{"model_ratio", "completion_ratio", "model_price"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response echoes %q, which modelV2View does not carry — the view contract changed, update this test and the console accordingly. body=%s", forbidden, body)
		}
	}

	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	// enable_groups exists in the view but is derived from the channels serving
	// the model, so a freshly created model has none.
	if eg, present := data["enable_groups"]; !present || eg != nil {
		t.Fatalf("expected enable_groups=null on a freshly created model, got present=%v value=%v", present, eg)
	}
	id := int(data["id"].(float64))
	var stored repo.Model
	if err := ctx.DB.First(&stored, id).Error; err != nil {
		t.Fatalf("reload created model: %v", err)
	}
	if stored.ModelName != "cov-pricing-view-shape-model" {
		t.Fatalf("unexpected stored model_name: %q", stored.ModelName)
	}
}

func TestCovHandlerPricing_DeleteModelV2_EmptySlug(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()
	_ = tenant

	c, w := handlerPricingNewCtx(http.MethodDelete, "/models/1", nil)
	c.Params = gin.Params{{Key: "tenant_slug", Value: ""}, {Key: "id", Value: "1"}}
	DeleteModelV2(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["error_code"] != "INVALID_TENANT_SLUG" {
		t.Errorf("unexpected error_code: %v", resp["error_code"])
	}
}

func TestCovHandlerPricing_DeleteModelV2_UnknownTenant(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()
	_ = tenant

	c, w := handlerPricingNewCtx(http.MethodDelete, "/models/1", nil)
	c.Params = gin.Params{{Key: "tenant_slug", Value: "no-such-slug"}, {Key: "id", Value: "1"}}
	DeleteModelV2(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("unexpected error_code: %v", resp["error_code"])
	}
}

func TestCovHandlerPricing_DeleteModelV2_MissingTenantContext(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	c, w := handlerPricingNewCtx(http.MethodDelete, "/models/1", nil)
	c.Params = gin.Params{{Key: "tenant_slug", Value: tenant.Slug}, {Key: "id", Value: "1"}}
	DeleteModelV2(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCovHandlerPricing_DeleteModelV2_InvalidModelID(t *testing.T) {
	ctx, tenant := handlerPricingModelsSetup(t)
	defer ctx.Cleanup()

	cases := []string{"not-a-number", "0", "-5"}
	for _, idStr := range cases {
		t.Run(idStr, func(t *testing.T) {
			c, w := handlerPricingAdminModelCtx(http.MethodDelete, "/models/"+idStr, nil, tenant)
			c.Params = gin.Params{{Key: "tenant_slug", Value: tenant.Slug}, {Key: "id", Value: idStr}}
			DeleteModelV2(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for id=%q, got %d body=%s", idStr, w.Code, w.Body.String())
			}
			resp := handlerPricingParseBody(t, w)
			if resp["error_code"] != "INVALID_MODEL_ID" {
				t.Errorf("unexpected error_code for id=%q: %v", idStr, resp["error_code"])
			}
		})
	}
}

// ===========================================================================
// v2_pricing.go — GetPricingV2 edge case
// ===========================================================================

func TestCovHandlerPricing_GetPricingV2_EmptySlug(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := handlerPricingNewCtx(http.MethodGet, "/pricing", nil)
	c.Params = gin.Params{{Key: "tenant_slug", Value: ""}}
	GetPricingV2(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["error_code"] != "INVALID_TENANT_SLUG" {
		t.Errorf("unexpected error_code: %v", resp["error_code"])
	}
}

// ===========================================================================
// prefill_group.go — DB-failure branches (table dropped mid-flow)
// ===========================================================================

func handlerPricingPrefillCtx(t *testing.T) *V2TestContext {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	return ctx
}

func TestCovHandlerPricing_GetPrefillGroups_DBError(t *testing.T) {
	ctx := handlerPricingPrefillCtx(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	r := gin.New()
	r.GET("/prefill-groups", GetPrefillGroups)
	w := V2Request(r, http.MethodGet, "/prefill-groups", nil, nil)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when the table is gone, body=%s", w.Body.String())
	}
}

func TestCovHandlerPricing_CreatePrefillGroup_DupCheckDBError(t *testing.T) {
	ctx := handlerPricingPrefillCtx(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	r := gin.New()
	r.POST("/prefill-groups", CreatePrefillGroup)
	w := V2Request(r, http.MethodPost, "/prefill-groups", map[string]interface{}{"name": "n", "type": "t"}, nil)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when the table is gone, body=%s", w.Body.String())
	}
}

func TestCovHandlerPricing_UpdatePrefillGroup_DupCheckDBError(t *testing.T) {
	ctx := handlerPricingPrefillCtx(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	r := gin.New()
	r.PUT("/prefill-groups", UpdatePrefillGroup)
	w := V2Request(r, http.MethodPut, "/prefill-groups", map[string]interface{}{"id": 1, "name": "n", "type": "t"}, nil)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when the table is gone, body=%s", w.Body.String())
	}
}

func TestCovHandlerPricing_DeletePrefillGroup_DBError(t *testing.T) {
	ctx := handlerPricingPrefillCtx(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	r := gin.New()
	r.DELETE("/prefill-groups/:id", DeletePrefillGroup)
	w := V2Request(r, http.MethodDelete, "/prefill-groups/1", nil, nil)
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when the table is gone, body=%s", w.Body.String())
	}
}

// ===========================================================================
// pricing.go — ResetModelRatio: repo.UpdateOption's DB-write errors must
// reach the caller
// ===========================================================================

// repo.UpdateOption used to discard the *gorm.DB errors from FirstOrCreate and
// Save, so an admin resetting model ratios during a DB outage got a green
// "重置模型倍率成功" while nothing was persisted. fix_option_write_error_test.go
// pins the repo-level contract; this test covers ResetModelRatio's own error
// branch (pricing.go:55-61), which only exists because the error now propagates.
func TestCovHandlerPricing_ResetModelRatio_DBWriteFailureReported(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	snapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(snapshot) })

	if err := ctx.DB.Migrator().DropTable(&repo.Option{}); err != nil {
		t.Fatalf("drop Option table: %v", err)
	}

	r := gin.New()
	r.POST("/reset", ResetModelRatio)
	w := V2Request(r, http.MethodPost, "/reset", nil, nil)
	resp := handlerPricingParseBody(t, w)

	if resp["success"] != false {
		t.Fatalf("a reset that could not be persisted reported success=%v — the DB write error was swallowed again. body=%s", resp["success"], w.Body.String())
	}
	if msg, _ := resp["message"].(string); msg == "" || msg == "重置模型倍率成功" {
		t.Errorf("message = %q, want the underlying DB error", msg)
	}
	// The envelope is still HTTP 200 with success=false — that is this API's
	// established error shape, and the console keys off `success`.
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ===========================================================================
// ratio_sync.go — remaining gaps: content-type mismatch, unparseable data
// shape, GetAllChannels/GetChannelsByIds DB errors, confidence downgrade rule
// ===========================================================================

func TestCovHandlerPricing_FetchUpstreamRatios_WrongContentTypeStillParses(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)
	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "ctsrv", "base_url": srv.URL, "endpoint": "/wrong-content-type"},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	r0 := results[0].(map[string]interface{})
	if r0["status"] != "success" {
		t.Fatalf("expected a text/plain Content-Type to still be parsed as JSON (warning-only), got status=%v body=%s", r0["status"], w.Body.String())
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_UnparseableDataShape(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := handlerPricingUpstreamServer(t)
	body := map[string]interface{}{
		"upstreams": []map[string]interface{}{
			{"name": "shapesrv", "base_url": srv.URL, "endpoint": "/unparseable-shape"},
		},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	resp := handlerPricingParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	results := data["test_results"].([]interface{})
	r0 := results[0].(map[string]interface{})
	if r0["status"] != "error" {
		t.Fatalf("expected status=error for a data shape matching neither type1 nor type2, got %v", r0["status"])
	}
	if r0["error"] != "无法解析上游返回数据" {
		t.Errorf("unexpected error message: %v", r0["error"])
	}
}

func TestCovHandlerPricing_FetchUpstreamRatios_GetChannelsByIdsDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Channel{}); err != nil {
		t.Fatalf("drop Channel table: %v", err)
	}

	body := map[string]interface{}{
		"channel_ids": []int64{1},
	}
	c, w := handlerPricingNewCtx(http.MethodPost, "/fetch", body)
	FetchUpstreamRatios(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the channels table is gone, got %d body=%s", w.Code, w.Body.String())
	}
	resp := handlerPricingParseBody(t, w)
	if resp["message"] != "查询渠道失败" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestCovHandlerPricing_GetSyncableChannels_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Channel{}); err != nil {
		t.Fatalf("drop Channel table: %v", err)
	}

	c, w := handlerPricingNewCtx(http.MethodGet, "/syncable", nil)
	GetSyncableChannels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (handler reports failure in-body, not via status), got %d", w.Code)
	}
	resp := handlerPricingParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when the channels table is gone, body=%s", w.Body.String())
	}
}

// TestCovHandlerPricing_BuildDifferences_ConfidenceDowngrade locks in the
// "known unreliable pricing-endpoint value" heuristic: when a channel's data
// came from the /api/pricing (type2) shape and a model's converted
// model_ratio==37.5 with completion_ratio==1.0 exactly (the placeholder pair
// many upstreams emit for a model they don't actually meter), the model is
// flagged low-confidence for THAT channel so operators don't blindly trust
// it in a sync decision.
func TestCovHandlerPricing_BuildDifferences_ConfidenceDowngrade(t *testing.T) {
	localData := map[string]any{}
	channels := []struct {
		name string
		data map[string]any
	}{
		{
			name: "suspect-chan",
			data: map[string]any{
				"model_ratio":      map[string]any{"m-suspect": float64(37.5), "m-normal": float64(2.0)},
				"completion_ratio": map[string]any{"m-suspect": float64(1.0), "m-normal": float64(1.5)},
			},
		},
	}
	diffs := buildDifferences(localData, channels)

	suspect, ok := diffs["m-suspect"]["model_ratio"]
	if !ok {
		t.Fatalf("expected m-suspect/model_ratio present, got %#v", diffs)
	}
	if suspect.Confidence["suspect-chan"] {
		t.Errorf("expected confidence=false for the 37.5/1.0 placeholder pair, got true")
	}

	normal, ok := diffs["m-normal"]["model_ratio"]
	if !ok {
		t.Fatalf("expected m-normal/model_ratio present, got %#v", diffs)
	}
	if !normal.Confidence["suspect-chan"] {
		t.Errorf("expected confidence=true for a model that isn't the 37.5/1.0 placeholder pair, got false")
	}
}
