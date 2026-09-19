package handler

// v2_models_routable_test.go — L1 oracle for GET /api/v2/:tenant_slug/models/routable
// (v2_models_routable.go). Reuses the fixtures from model_tenant_scope_test.go
// (same package) — setupModelTenantScopeDB, seedTenantScopeUser,
// seedTenantScopeChannel, containsID, decodeModelIDs — since ListRoutableModelsV2
// is built on the exact same tenant-scoped core (tenantRoutableModels +
// narrowByTenantAllowlist) that visibleModels/ListModels use for /v1/models.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// seedRoutableChannel is seedTenantScopeChannel with an explicit channel
// type, so the endpoint-types test can seed an Anthropic-type channel
// without disturbing seedTenantScopeChannel's default (Type: 1) used by
// every other test in this package.
func seedRoutableChannel(t *testing.T, id int, tenantID, group, model string, channelType int) {
	t.Helper()
	ch := &repo.Channel{Id: id, Type: channelType, Status: common.ChannelStatusEnabled, Name: "rtc" + model, Models: model, Group: group, TenantId: tenantID}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := repo.DB.Create(&repo.Ability{Group: group, Model: model, ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

func routableModelsCtx(userID int, tenantID string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/t/models/routable", nil)
	c.Set("id", userID)
	c.Set("tenant_context", &middleware.TenantContext{TenantID: tenantID, UserID: userID})
	return c, w
}

type routableModelItem struct {
	Id                     string   `json:"id"`
	OwnedBy                string   `json:"owned_by"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
}

func decodeRoutableItems(t *testing.T, body []byte) []routableModelItem {
	t.Helper()
	var resp struct {
		Data struct {
			Items []routableModelItem `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, body)
	}
	return resp.Data.Items
}

func decodeRoutableIDs(t *testing.T, body []byte) []string {
	t.Helper()
	items := decodeRoutableItems(t, body)
	ids := make([]string, len(items))
	for i, m := range items {
		ids[i] = m.Id
	}
	return ids
}

// TestListRoutableModelsV2_TenantScope is the RED-on-HEAD oracle: a
// tenant-b caller must not see tenant-a's private model, mirroring
// TestListModels_TenantScope for /v1/models.
func TestListRoutableModelsV2_TenantScope(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	repo.InvalidatePricingCache()
	// GetPricing()'s cache is process-wide (pricing.go) and keyed off a
	// 1-minute TTL, not per-test — without this, whatever this test just
	// built (from its own throwaway sqlite DB) would leak into the NEXT
	// test's GetPricingV2 call within that same minute (caught live:
	// TestGetPricingV2_CacheRatioPrefill failing only in full-package runs,
	// never in isolation).
	t.Cleanup(repo.InvalidatePricingCache)

	seedTenantScopeChannel(t, 9901, "tenant-a", "default", "rt-alpha")
	seedTenantScopeChannel(t, 9902, "default", "default", "rt-shared")
	seedTenantScopeUser(t, 1, "default")

	c, w := routableModelsCtx(1, "tenant-b")
	ListRoutableModelsV2(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	ids := decodeRoutableIDs(t, w.Body.Bytes())
	if containsID(ids, "rt-alpha") {
		t.Errorf("tenant-b caller sees tenant-a's private model: data=%v", ids)
	}
	if !containsID(ids, "rt-shared") {
		t.Errorf("tenant-b caller does not see the platform-shared model: data=%v", ids)
	}
}

// TestListRoutableModelsV2_EmptyIsEmptyList: a tenant with nothing routable
// must get back "items":[] — never a JSON null the console's .map() would
// crash on.
func TestListRoutableModelsV2_EmptyIsEmptyList(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	repo.InvalidatePricingCache()
	// GetPricing()'s cache is process-wide (pricing.go) and keyed off a
	// 1-minute TTL, not per-test — without this, whatever this test just
	// built (from its own throwaway sqlite DB) would leak into the NEXT
	// test's GetPricingV2 call within that same minute (caught live:
	// TestGetPricingV2_CacheRatioPrefill failing only in full-package runs,
	// never in isolation).
	t.Cleanup(repo.InvalidatePricingCache)

	seedTenantScopeUser(t, 1, "default")

	c, w := routableModelsCtx(1, "tenant-empty")
	ListRoutableModelsV2(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf(`body = %s, want literal "items":[] (not null)`, w.Body.String())
	}
}

// TestListRoutableModelsV2_SameSetAsV1Models: for the same DB state, same
// user and same tenant, the v2 routable endpoint and /v1/models must answer
// the same set of ids — in both observe (default) and
// TENANT_MODEL_ALLOWLIST_MODE=enforce with a configured allow-list. This is
// the operator decision that the two lists are the same set for the same
// user; the only difference the console applies on top is per-token
// model_limit (intersectTokenLimits, client-side).
func TestListRoutableModelsV2_SameSetAsV1Models(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	repo.InvalidatePricingCache()
	// GetPricing()'s cache is process-wide (pricing.go) and keyed off a
	// 1-minute TTL, not per-test — without this, whatever this test just
	// built (from its own throwaway sqlite DB) would leak into the NEXT
	// test's GetPricingV2 call within that same minute (caught live:
	// TestGetPricingV2_CacheRatioPrefill failing only in full-package runs,
	// never in isolation).
	t.Cleanup(repo.InvalidatePricingCache)

	seedTenantScopeChannel(t, 9911, "tenant-a", "default", "rt-alpha")
	seedTenantScopeChannel(t, 9912, "default", "default", "rt-shared")
	seedTenantScopeUser(t, 1, "default")

	compareOnce := func(t *testing.T) {
		vc, vw := modelTenantScopeCtx("/v1/models", 1, "tenant-a")
		ListModels(vc, constant.ChannelTypeOpenAI)
		if vw.Code != http.StatusOK {
			t.Fatalf("/v1/models status = %d, want 200; body=%s", vw.Code, vw.Body.String())
		}
		v1IDs := decodeModelIDs(t, vw.Body.Bytes())

		rc, rw := routableModelsCtx(1, "tenant-a")
		ListRoutableModelsV2(rc)
		if rw.Code != http.StatusOK {
			t.Fatalf("routable status = %d, want 200; body=%s", rw.Code, rw.Body.String())
		}
		v2IDs := decodeRoutableIDs(t, rw.Body.Bytes())

		sort.Strings(v1IDs)
		sort.Strings(v2IDs)
		if !reflect.DeepEqual(v1IDs, v2IDs) {
			t.Errorf("/v1/models ids = %v, routable ids = %v, want equal", v1IDs, v2IDs)
		}
	}

	t.Run("observe (default)", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "")
		compareOnce(t)
	})

	t.Run("enforce with configured allow-list", func(t *testing.T) {
		if err := repo.SetTenantConfigJSON("tenant-a", tenantpolicy.ModelAllowlistConfigKey, []string{"rt-shared"}, "test"); err != nil {
			t.Fatalf("seed allow-list: %v", err)
		}
		tenantpolicy.Invalidate("tenant-a")
		t.Cleanup(func() { tenantpolicy.Invalidate("tenant-a") })
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		compareOnce(t)
	})
}

// TestListRoutableModelsV2_EndpointTypesFromChannelType: a routable model
// served only through an Anthropic-type channel must carry "anthropic" in
// supported_endpoint_types — proving the handler calls repo.GetPricing()
// (pricing.go's process-wide modelSupportEndpointTypes map is otherwise
// empty for a model no earlier call has warmed).
func TestListRoutableModelsV2_EndpointTypesFromChannelType(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	repo.InvalidatePricingCache()
	// GetPricing()'s cache is process-wide (pricing.go) and keyed off a
	// 1-minute TTL, not per-test — without this, whatever this test just
	// built (from its own throwaway sqlite DB) would leak into the NEXT
	// test's GetPricingV2 call within that same minute (caught live:
	// TestGetPricingV2_CacheRatioPrefill failing only in full-package runs,
	// never in isolation).
	t.Cleanup(repo.InvalidatePricingCache)

	// Seeded in reverse-alphabetical order (rt-yankee BEFORE rt-anthropic).
	// This does NOT prove the handler sorts: on sqlite the underlying
	// Distinct().Pluck() already comes back alphabetical, so the order
	// assertion below only pins the contract; TestSortRoutableItems_OrdersById
	// is the oracle that goes red when the sort is removed.
	seedRoutableChannel(t, 9920, "tenant-a", "default", "rt-yankee", constant.ChannelTypeAnthropic)
	seedRoutableChannel(t, 9921, "tenant-a", "default", "rt-anthropic", constant.ChannelTypeAnthropic)
	seedTenantScopeUser(t, 1, "default")

	c, w := routableModelsCtx(1, "tenant-a")
	ListRoutableModelsV2(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	items := decodeRoutableItems(t, w.Body.Bytes())
	var found bool
	for _, it := range items {
		if it.Id == "rt-anthropic" {
			found = true
			if !containsString(it.SupportedEndpointTypes, "anthropic") {
				t.Errorf("supported_endpoint_types = %v, want to contain \"anthropic\"", it.SupportedEndpointTypes)
			}
		}
	}
	if !found {
		t.Fatalf("rt-anthropic missing from items: %v", items)
	}
	// Deterministic order (operator decision 4): sorted by id, not DB/
	// insertion order — rt-anthropic (inserted second) must come first.
	ids := decodeRoutableIDs(t, w.Body.Bytes())
	wantOrder := []string{"rt-anthropic", "rt-yankee"}
	if !reflect.DeepEqual(ids, wantOrder) {
		t.Errorf("ids = %v, want sorted order %v", ids, wantOrder)
	}
}

// TestListRoutableModelsV2_Unauthenticated401: no session in context ->
// 401 UNAUTHENTICATED, not a panic or a leaked empty-tenant answer.
func TestListRoutableModelsV2_Unauthenticated401(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/t/models/routable", nil)

	ListRoutableModelsV2(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	if resp.ErrorCode != "UNAUTHENTICATED" {
		t.Errorf("error_code = %q, want UNAUTHENTICATED; body=%s", resp.ErrorCode, w.Body.String())
	}
}

// TestVisibleModels_AcceptUnsetRatioModel_PerUser proves acceptUnsetRatioModels'
// split from visibleModels (model.go) did not lose the per-user branch: every
// other test in this file runs under setupModelTenantScopeDB, which forces
// operation_setting.SelfUseModeEnabled=true (model_tenant_scope_test.go),
// so a model with no configured ratio/price is included unconditionally and
// the per-user dto.UserSetting.AcceptUnsetRatioModel path is never
// exercised. This test turns self-use mode off and drives that path
// directly through tenantRoutableModels (via ListRoutableModelsV2): a model
// with no configured ratio/price is routable only for a caller whose own
// setting accepts it.
func TestVisibleModels_AcceptUnsetRatioModel_PerUser(t *testing.T) {
	const unsetRatioModel = "rt-unset-ratio-only"

	seedUserWithAcceptSetting := func(t *testing.T, id int, accept bool) {
		t.Helper()
		settingBytes, err := json.Marshal(dto.UserSetting{AcceptUnsetRatioModel: accept})
		if err != nil {
			t.Fatalf("marshal setting: %v", err)
		}
		if err := repo.DB.Create(&repo.User{
			Id:       id,
			Username: fmt.Sprintf("u%d", id),
			Status:   common.UserStatusEnabled,
			Group:    "default",
			Setting:  string(settingBytes),
		}).Error; err != nil {
			t.Fatalf("seed user %d: %v", id, err)
		}
	}

	run := func(t *testing.T, accept bool) []string {
		cleanup := setupModelTenantScopeDB(t)
		defer cleanup()
		// setupModelTenantScopeDB forces SelfUseModeEnabled=true; this test
		// exists specifically to exercise the branch that runs when it is
		// false, restored by cleanup() above regardless of what this does.
		operation_setting.SelfUseModeEnabled = false
		repo.InvalidatePricingCache()
		t.Cleanup(repo.InvalidatePricingCache)

		seedTenantScopeChannel(t, 9931, "tenant-a", "default", unsetRatioModel)
		seedUserWithAcceptSetting(t, 1, accept)

		c, w := routableModelsCtx(1, "tenant-a")
		ListRoutableModelsV2(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		return decodeRoutableIDs(t, w.Body.Bytes())
	}

	t.Run("user accepts unset-ratio models", func(t *testing.T) {
		ids := run(t, true)
		if !containsID(ids, unsetRatioModel) {
			t.Errorf("ids = %v, want to contain %q when the user accepts unset-ratio models", ids, unsetRatioModel)
		}
	})

	t.Run("user does not accept unset-ratio models", func(t *testing.T) {
		ids := run(t, false)
		if containsID(ids, unsetRatioModel) {
			t.Errorf("ids = %v, want NOT to contain %q when the user does not accept unset-ratio models", ids, unsetRatioModel)
		}
	})
}

// TestSortRoutableItems_OrdersById is the ordering oracle: a deliberately
// unsorted input must come out ordered by id. Replacing the comparator with
// a constant turns this red, which the handler-level fixture above cannot
// do because sqlite already returns those rows alphabetically.
func TestSortRoutableItems_OrdersById(t *testing.T) {
	items := []routableModelView{{Id: "rt-yankee"}, {Id: "rt-alpha"}, {Id: "rt-mike"}}
	sortRoutableItems(items)
	got := []string{items[0].Id, items[1].Id, items[2].Id}
	want := []string{"rt-alpha", "rt-mike", "rt-yankee"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortRoutableItems = %v, want %v", got, want)
	}
}
