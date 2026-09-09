package handler

// model_tenant_scope_test.go — L2 oracle: ListModels/RetrieveModel must
// answer from the caller's tenant-routable set (repo.GetGroupEnabledModelsForTenant,
// the same scope channel/ability selection already applies) instead of every
// model any tenant's channel happens to serve, and — only under
// TENANT_MODEL_ALLOWLIST_MODE=enforce with a configured allow-list — that set
// is further narrowed by internal/app/tenantpolicy.ModelAllowed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelTenantScopeDBCounter atomic.Int64

// setupModelTenantScopeDB wires an isolated in-memory SQLite DB into repo.DB
// with the tables ListModels' tenant-scoped path and the allow-list touch,
// and forces acceptUnsetRatioModel=true so a seeded model's inclusion never
// depends on an unrelated ratio/price fixture.
func setupModelTenantScopeDB(t *testing.T) func() {
	t.Helper()
	dsn := fmt.Sprintf("file:modeltenantscope%d?mode=memory&cache=shared", modelTenantScopeDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Channel{}, &repo.Ability{}, &repo.TenantConfig{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	prevDB := repo.DB
	prevRedis := common.RedisEnabled
	prevSelfUse := operation_setting.SelfUseModeEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false
	operation_setting.SelfUseModeEnabled = true

	return func() {
		repo.DB = prevDB
		common.RedisEnabled = prevRedis
		operation_setting.SelfUseModeEnabled = prevSelfUse
		if sqlDB, sErr := db.DB(); sErr == nil {
			_ = sqlDB.Close()
		}
	}
}

func seedTenantScopeUser(t *testing.T, id int, group string) {
	t.Helper()
	if err := repo.DB.Create(&repo.User{Id: id, Username: fmt.Sprintf("u%d", id), Status: common.UserStatusEnabled, Group: group}).Error; err != nil {
		t.Fatalf("seed user %d: %v", id, err)
	}
}

// seedTenantScopeChannel mirrors repo's seedTenantRelayChannel
// (channel_cache_tenant_test.go) — kept local because this is a different
// package and the oracle must stay a single self-contained file per L2's
// file list.
func seedTenantScopeChannel(t *testing.T, id int, tenantID, group, model string) {
	t.Helper()
	ch := &repo.Channel{Id: id, Type: 1, Status: common.ChannelStatusEnabled, Name: "tsc" + model, Models: model, Group: group, TenantId: tenantID}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := repo.DB.Create(&repo.Ability{Group: group, Model: model, ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

func modelTenantScopeCtx(target string, userId int, tenantID string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set("id", userId)
	c.Set("tenant_id", tenantID)
	return c, w
}

func decodeModelIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var resp struct {
		Data []struct {
			Id string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, body)
	}
	ids := make([]string, len(resp.Data))
	for i, m := range resp.Data {
		ids[i] = m.Id
	}
	return ids
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestListModels_TenantScope is the RED-on-HEAD oracle: a tenant-b caller
// (no channel of its own, only the platform-shared one) must list only the
// shared model, not tenant-a's private one — before this lane, ListModels
// called the tenant-blind repo.GetGroupEnabledModels and every tenant saw
// every model.
func TestListModels_TenantScope(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	seedTenantScopeChannel(t, 9701, "tenant-a", "default", "a-only")
	seedTenantScopeChannel(t, 9702, "default", "default", "shared-model")
	seedTenantScopeUser(t, 1, "default")

	c, w := modelTenantScopeCtx("/v1/models", 1, "tenant-b")
	ListModels(c, constant.ChannelTypeOpenAI)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	ids := decodeModelIDs(t, w.Body.Bytes())
	if containsID(ids, "a-only") {
		t.Errorf("tenant-b caller sees tenant-a's private model: data=%v", ids)
	}
	if !containsID(ids, "shared-model") {
		t.Errorf("tenant-b caller does not see the platform-shared model: data=%v", ids)
	}

	// Positive control: the owning tenant keeps seeing its own model.
	c2, w2 := modelTenantScopeCtx("/v1/models", 1, "tenant-a")
	ListModels(c2, constant.ChannelTypeOpenAI)
	ids2 := decodeModelIDs(t, w2.Body.Bytes())
	if !containsID(ids2, "a-only") {
		t.Errorf("tenant-a caller does not see its own model: data=%v", ids2)
	}
	if !containsID(ids2, "shared-model") {
		t.Errorf("tenant-a caller does not see the platform-shared model: data=%v", ids2)
	}
}

// TestListModels_TenantScope_EnforceAllowlist covers the flag half: under
// TENANT_MODEL_ALLOWLIST_MODE=enforce with a configured allow-list, the
// tenant-routable set is further narrowed; under the default (observe) the
// same allow-list row has no effect.
func TestListModels_TenantScope_EnforceAllowlist(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	seedTenantScopeChannel(t, 9711, "tenant-a", "default", "a-only")
	seedTenantScopeChannel(t, 9712, "default", "default", "shared-model")
	seedTenantScopeUser(t, 1, "default")

	if err := repo.SetTenantConfigJSON("tenant-a", tenantpolicy.ModelAllowlistConfigKey, []string{"shared-model"}, "test"); err != nil {
		t.Fatalf("seed allow-list: %v", err)
	}
	tenantpolicy.Invalidate("tenant-a")
	t.Cleanup(func() { tenantpolicy.Invalidate("tenant-a") })

	t.Run("observe (default) ignores the allow-list", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "")
		c, w := modelTenantScopeCtx("/v1/models", 1, "tenant-a")
		ListModels(c, constant.ChannelTypeOpenAI)
		ids := decodeModelIDs(t, w.Body.Bytes())
		if !containsID(ids, "a-only") {
			t.Errorf("observe mode: a-only hidden, want visible; data=%v", ids)
		}
	})

	t.Run("enforce hides the model the allow-list excludes", func(t *testing.T) {
		t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", "enforce")
		tenantpolicy.Invalidate("tenant-a")
		c, w := modelTenantScopeCtx("/v1/models", 1, "tenant-a")
		ListModels(c, constant.ChannelTypeOpenAI)
		ids := decodeModelIDs(t, w.Body.Bytes())
		if containsID(ids, "a-only") {
			t.Errorf("enforce mode: a-only visible, want hidden by the tenant allow-list; data=%v", ids)
		}
		if !containsID(ids, "shared-model") {
			t.Errorf("enforce mode: shared-model (on the allow-list) hidden, want visible; data=%v", ids)
		}
	})
}

// TestRetrieveModel_TenantScope_UnroutableStaticModel404s covers RetrieveModel's
// half: a model present in the static per-vendor catalogue (openAIModelsMap)
// but not routable for this caller's tenant must now 404, not 200.
func TestRetrieveModel_TenantScope_UnroutableStaticModel404s(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	seedTenantScopeChannel(t, 9721, "default", "default", "shared-model")
	seedTenantScopeUser(t, 1, "default")

	var staticModel string
	for id := range openAIModelsMap {
		staticModel = id
		break
	}
	if staticModel == "" {
		t.Skip("no models registered in openAIModelsMap")
	}

	c, w := modelTenantScopeCtx("/v1/models/"+staticModel, 1, "tenant-b")
	c.Params = gin.Params{{Key: "model", Value: staticModel}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unroutable static-catalogue model; body=%s", w.Code, w.Body.String())
	}
}

// TestRetrieveModel_TenantScope_RoutableModel200s is the positive control: a
// non-catalogue ("custom") model that IS routable for the caller's tenant
// answers 200 with the owned_by "custom" shape ListModels already emits for
// it, via an ability row rather than the static map.
func TestRetrieveModel_TenantScope_RoutableModel200s(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	seedTenantScopeChannel(t, 9731, "tenant-a", "default", "custom-routable-model")
	seedTenantScopeUser(t, 1, "default")

	c, w := modelTenantScopeCtx("/v1/models/custom-routable-model", 1, "tenant-a")
	c.Params = gin.Params{{Key: "model", Value: "custom-routable-model"}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a tenant-routable custom model; body=%s", w.Code, w.Body.String())
	}
	if want := `"owned_by":"custom"`; !strings.Contains(w.Body.String(), want) {
		t.Errorf("body = %s, want %s", w.Body.String(), want)
	}
}

// TestRetrieveModel_BackendFaultIs500 covers the visibleModels error path:
// a genuine backend fault (DB unreachable, here forced by closing the
// underlying sql.DB) must answer 500 with the caller's own wire-native
// envelope, not 404 — before this lane's fix, RetrieveModel's
// `if models, err := visibleModels(c); err == nil` guard swallowed the
// error and fell through to the not-found branch, telling an SDK the model
// was deleted when the real cause was this gateway's own state being
// unreachable.
func TestRetrieveModel_BackendFaultIs500(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	c, w := modelTenantScopeCtx("/v1/models/whatever-model", 1, "tenant-a")
	c.Params = gin.Params{{Key: "model", Value: "whatever-model"}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a backend fault; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	if resp.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error; body=%s", resp.Error.Type, w.Body.String())
	}
	if resp.Error.Code != "gateway_internal" {
		t.Errorf("error.code = %q, want gateway_internal; body=%s", resp.Error.Code, w.Body.String())
	}
}

// TestListModels_TenantScope_AutoGroup covers the model.go tokenGroup=="auto"
// call site specifically — it is the only test here that reaches that branch,
// so a lone revert of the auto-group call
// site back to the tenant-blind repo.GetGroupEnabledModels would leave the
// rest of this file green while leaking another tenant's models to every
// token whose group is "auto".
func TestListModels_TenantScope_AutoGroup(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()

	prevAutoGroups := setting.AutoGroups2JsonString()
	if err := setting.UpdateAutoGroupsByJsonString(`["default"]`); err != nil {
		t.Fatalf("seed auto groups: %v", err)
	}
	t.Cleanup(func() {
		if err := setting.UpdateAutoGroupsByJsonString(prevAutoGroups); err != nil {
			t.Fatalf("restore auto groups: %v", err)
		}
	})

	seedTenantScopeChannel(t, 9741, "tenant-a", "default", "a-only")
	seedTenantScopeChannel(t, 9742, "default", "default", "shared-model")
	seedTenantScopeUser(t, 1, "default")

	c, w := modelTenantScopeCtx("/v1/models", 1, "tenant-b")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
	ListModels(c, constant.ChannelTypeOpenAI)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	ids := decodeModelIDs(t, w.Body.Bytes())
	if containsID(ids, "a-only") {
		t.Errorf("tenant-b caller (auto group) sees tenant-a's private model: data=%v", ids)
	}
	if !containsID(ids, "shared-model") {
		t.Errorf("tenant-b caller (auto group) does not see the platform-shared model: data=%v", ids)
	}
}
