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

// TestV1Pricing_SharedCatalogueForEveryCaller — /api/pricing answers the
// platform-shared catalogue to EVERY caller, session or not, root or not.
// The route carries no auth middleware (api-router.go, "Public routes"), so a
// per-caller branch here could never execute in production; the tenant-aware
// price list is GET /api/v2/:tenant_slug/pricing. This pins that decision:
// re-introducing "root sees the global catalogue" or "a session sees its own
// tenant's models too" turns the second half of this test red.
func TestV1Pricing_SharedCatalogueForEveryCaller(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	call := func(t *testing.T, decorate func(c *gin.Context)) []string {
		t.Helper()
		r := gin.New()
		if decorate != nil {
			r.Use(func(c *gin.Context) { decorate(c); c.Next() })
		}
		r.GET("/api/pricing", GetPricing)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
		}
		return pricingModelNames(t, w, false)
	}

	cases := []struct {
		name     string
		decorate func(c *gin.Context)
	}{
		{"tenant_admin_session", func(c *gin.Context) {
			c.Set("id", ctx.AdminUser.Id)
			c.Set("role", common.RoleAdminUser)
			c.Set("tenant_id", ctx.TenantID)
		}},
		{"root_session", func(c *gin.Context) {
			c.Set("id", ctx.RootUser.Id)
			c.Set("role", common.RoleRootUser)
			c.Set("tenant_id", ctx.TenantID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names := call(t, tc.decorate)
			if !pricingHasModel(names, pricingSharedModel) {
				t.Fatalf("shared model %q missing: %v", pricingSharedModel, names)
			}
			if pricingHasModel(names, pricingPrivateModel) {
				t.Errorf("/api/pricing answered %s with a tenant-owned model %q: %v",
					tc.name, pricingPrivateModel, names)
			}
		})
	}
}

// TestPricingCatalogue_OrphanAbilityIsNotShared — an enabled ability whose
// channel row is gone (the deletion paths in repo/channel.go can leave one
// behind) is routable by nobody, so it must not reach the catalogue either.
// With the left join this used to use, such a row arrived with an empty
// channel tenant id and was published as platform-shared.
// Mutation: put `left` back in repo.GetAllEnableAbilityWithChannels's join.
func TestPricingCatalogue_OrphanAbilityIsNotShared(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	const orphanModel = "orphan-ability-model"
	orphan := &repo.Channel{
		Name: "orphan-pricing-channel", TenantId: "acme-orphan", Key: "sk-orphan",
		Status: common.ChannelStatusEnabled, Type: 1, Models: orphanModel,
		Group: "default", CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(orphan).Error; err != nil {
		t.Fatalf("seed orphan channel: %v", err)
	}
	if err := ctx.DB.Create(&repo.Ability{
		Group: "default", Model: orphanModel, ChannelId: orphan.Id, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed orphan ability: %v", err)
	}
	// Hard delete: the ability row survives, its channel row does not.
	if err := ctx.DB.Exec("DELETE FROM channels WHERE id = ?", orphan.Id).Error; err != nil {
		t.Fatalf("delete the channel row: %v", err)
	}
	repo.InvalidatePricingCache()

	r := gin.New()
	r.GET("/api/pricing", GetPricing)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing", nil))
	names := pricingModelNames(t, w, false)
	if !pricingHasModel(names, pricingSharedModel) {
		t.Fatalf("fixture broken: shared model %q missing: %v", pricingSharedModel, names)
	}
	if pricingHasModel(names, orphanModel) {
		t.Errorf("anonymous catalogue published an orphan ability's model %q as platform-shared: %v",
			orphanModel, names)
	}
	if got := repo.GetPricingForTenant("acme-orphan"); pricingHasModel(pricingNamesOf(got), orphanModel) {
		t.Errorf("the orphan's own tenant must not see it either (nobody can route it): %v", pricingNamesOf(got))
	}
}

// pricingNamesOf lists the model names of a catalogue slice.
func pricingNamesOf(catalogue []repo.Pricing) []string {
	out := make([]string, 0, len(catalogue))
	for _, p := range catalogue {
		out = append(out, p.ModelName)
	}
	return out
}

// TestV2Pricing_TenantSlugSeesSharedPlusOwn — the tenant-aware price list
// (GET /api/v2/:tenant_slug/pricing, behind UserAuth) is the surface that
// does show a tenant its own models, and still not another tenant's.
func TestV2Pricing_TenantSlugSeesSharedPlusOwn(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedPricingCatalogue(t, ctx)

	const otherModel = "other-tenant-private-model"
	other := &repo.Channel{
		Name: "other-pricing-channel", TenantId: "other-tenant-xyz-pricing", Key: "sk-other-pricing",
		Status: common.ChannelStatusEnabled, Type: 1, Models: otherModel,
		Group: "default", CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("seed other-tenant channel: %v", err)
	}
	if err := ctx.DB.Create(&repo.Ability{
		Group: "default", Model: otherModel, ChannelId: other.Id, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed other-tenant ability: %v", err)
	}
	repo.InvalidatePricingCache()

	r := gin.New()
	r.GET("/api/v2/:tenant_slug/pricing", GetPricingV2)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/"+ctx.TenantID+"/pricing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	names := pricingModelNames(t, w, true)
	for _, want := range []string{pricingSharedModel, pricingPrivateModel} {
		if !pricingHasModel(names, want) {
			t.Errorf("tenant price list missing %q: %v", want, names)
		}
	}
	if pricingHasModel(names, otherModel) {
		t.Errorf("tenant price list leaked another tenant's model %q: %v", otherModel, names)
	}
}

// TestV2Pricing_GroupRatioNarrowedToUsableGroups — group_ratio on the
// tenant price list is narrowed to the caller's usable groups, like v1 and
// switch. The raw map is process-global, so publishing it handed every
// authenticated user the group names configured for one tenant's channels —
// the disclosure the model list above no longer carries.
func TestV2Pricing_GroupRatioNarrowedToUsableGroups(t *testing.T) {
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
	r.GET("/api/v2/:tenant_slug/pricing", GetPricingV2)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/"+ctx.TenantID+"/pricing", nil))
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
		t.Errorf("tenant price list group_ratio leaked non-usable group %q: %v", pricingPrivateGroup, groupRatio)
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
