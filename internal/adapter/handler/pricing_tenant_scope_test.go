package handler

// pricing_tenant_scope_test.go pins the tenant projection of the two public
// pricing catalogues (cycle-12 plan §L5):
//
//   - GET /api/pricing            (api-router.go:38, no auth middleware)
//   - GET /api/v2/switch/pricing  (api-v2-router.go:363, no auth middleware)
//
// Both are built from repo.GetPricing(), which before this cycle listed every
// enabled ability row of every tenant's channels — so a private fine-tune's
// model name and its channel group name were readable by an anonymous caller
// on a multi-tenant host.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	pricingSharedModel  = "shared-catalogue-model"
	pricingPrivateModel = "acme-private-ft"
	pricingPrivateGroup = "acme-vip"
)

// seedPricingCatalogue seeds one platform-shared channel (tenant_id
// "default", the value entity/channel.go:16 gives a channel with no explicit
// tenant) and one channel owned by ctx.TenantID, each serving a model the
// other does not. The process-wide ~1-minute catalogue cache (repo/pricing.go)
// is dropped before and after so neither this test nor its neighbours read a
// catalogue built from another test's rows.
func seedPricingCatalogue(t *testing.T, ctx *V2TestContext) {
	t.Helper()
	seed := func(name, tenant, model, group string) {
		ch := &repo.Channel{
			Name:        name,
			TenantId:    tenant,
			Key:         "sk-" + name,
			Status:      common.ChannelStatusEnabled,
			Type:        1,
			Models:      model,
			Group:       group,
			CreatedTime: common.GetTimestamp(),
		}
		if err := ctx.DB.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %s: %v", name, err)
		}
		if err := ctx.DB.Create(&repo.Ability{
			Group: group, Model: model, ChannelId: ch.Id, Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed ability %s: %v", model, err)
		}
	}
	seed("shared-pricing-channel", "default", pricingSharedModel, "default")
	seed("acme-pricing-channel", ctx.TenantID, pricingPrivateModel, pricingPrivateGroup)
	repo.InvalidatePricingCache()
	t.Cleanup(repo.InvalidatePricingCache)
}

// pricingModelNames reads model_name out of either catalogue shape: v1
// /api/pricing puts the rows at the top level under "data"; switch puts them
// at data.pricing.
func pricingModelNames(t *testing.T, w *httptest.ResponseRecorder, nested bool) []string {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	raw := body["data"]
	if nested {
		data, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("data missing/wrong type, body: %s", w.Body.String())
		}
		raw = data["pricing"]
	}
	rows, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("pricing rows missing/wrong type, body: %s", w.Body.String())
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		row, _ := r.(map[string]interface{})
		if row == nil {
			continue
		}
		if name, ok := row["model_name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func pricingHasModel(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestV1Pricing_AnonymousSeesOnlySharedCatalogue — the public catalogue lists
// the shared channel's model and not the tenant-owned channel's.
// Mutation: drop the projection in handler.GetPricing (call repo.GetPricing()
// again) and this goes red on the private model.
func TestV1Pricing_AnonymousSeesOnlySharedCatalogue(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	r := gin.New()
	r.GET("/api/pricing", GetPricing)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	names := pricingModelNames(t, w, false)
	if !pricingHasModel(names, pricingSharedModel) {
		t.Fatalf("shared model %q missing from the anonymous catalogue: %v", pricingSharedModel, names)
	}
	if pricingHasModel(names, pricingPrivateModel) {
		t.Errorf("anonymous /api/pricing leaked tenant-private model %q: %v", pricingPrivateModel, names)
	}
}

// TestV1Pricing_TenantSessionSeesSharedPlusOwn — a session belonging to the
// tenant that owns the private channel sees shared ∪ own.
func TestV1Pricing_TenantSessionSeesSharedPlusOwn(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", ctx.AdminUser.Id)
		c.Set("role", common.RoleAdminUser)
		c.Set("tenant_id", ctx.TenantID)
		c.Next()
	})
	r.GET("/api/pricing", GetPricing)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	names := pricingModelNames(t, w, false)
	for _, want := range []string{pricingSharedModel, pricingPrivateModel} {
		if !pricingHasModel(names, want) {
			t.Errorf("tenant session catalogue missing %q: %v", want, names)
		}
	}
}

// TestSwitchPricing_OnlySharedCatalogue — the Switch desktop client's public
// catalogue is the anonymous one.
func TestSwitchPricing_OnlySharedCatalogue(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	r := gin.New()
	r.GET("/api/v2/switch/pricing", GetSwitchPricing)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/switch/pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	names := pricingModelNames(t, w, true)
	if !pricingHasModel(names, pricingSharedModel) {
		t.Fatalf("shared model %q missing from the switch catalogue: %v", pricingSharedModel, names)
	}
	if pricingHasModel(names, pricingPrivateModel) {
		t.Errorf("public switch catalogue leaked tenant-private model %q: %v", pricingPrivateModel, names)
	}
}

// TestSwitchPricing_GroupRatioNarrowedToUsableGroups — group_ratio on the
// public catalogue is narrowed to the groups an anonymous caller may actually
// use, the way v1 GetPricing (pricing.go) narrows it. Without that, a group
// name configured only for one tenant's channels is published to every
// anonymous reader through the group_ratio map.
func TestSwitchPricing_GroupRatioNarrowedToUsableGroups(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	prevRatio := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateGroupRatioByJSONString(prevRatio) })
	prevUsable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() { _ = setting.UpdateUserUsableGroupsByJSONString(prevUsable) })

	if err := ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":1.0,"` + pricingPrivateGroup + `":0.5}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`); err != nil {
		t.Fatalf("seed usable groups: %v", err)
	}

	r := gin.New()
	r.GET("/api/v2/switch/pricing", GetSwitchPricing)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/switch/pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	groupRatio, ok := data["group_ratio"].(map[string]interface{})
	if !ok {
		t.Fatalf("data.group_ratio missing/wrong type, body: %s", w.Body.String())
	}
	if _, present := groupRatio["default"]; !present {
		t.Errorf("group_ratio dropped the usable group %q: %v", "default", groupRatio)
	}
	if _, present := groupRatio[pricingPrivateGroup]; present {
		t.Errorf("public group_ratio leaked non-usable group %q: %v", pricingPrivateGroup, groupRatio)
	}
}
