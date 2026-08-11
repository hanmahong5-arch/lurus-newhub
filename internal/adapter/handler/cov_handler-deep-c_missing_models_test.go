package handler

// cov_handler-deep-c_missing_models_test.go — business-acceptance coverage
// for missing_models.go's GetMissingModels (0% before this file): the admin
// diagnostic that reports which models are wired into channels (via the
// abilities table) but have no corresponding entry in the models metadata
// table, so operators can spot unconfigured/undocumented models.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

func handlerDeepCMissingModelsRouter(ctx *V2TestContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil {
		panic(err)
	}
	r := gin.New()
	r.GET("/missing-models", GetMissingModels)
	return r
}

func TestGetMissingModels_NoEnabledAbilities_ReturnsEmptyWithoutTouchingModelsTable(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r := handlerDeepCMissingModelsRouter(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing-models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data is not a list: %T (%v)", resp["data"], resp["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty missing-models list when no abilities are enabled, got %v", data)
	}
}

func TestGetMissingModels_MixOfConfiguredAndMissing(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r := handlerDeepCMissingModelsRouter(ctx)

	priorityConfigured := int64(1)
	priorityMissing := int64(1)
	priorityDisabled := int64(1)
	abilities := []repo.Ability{
		{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true, Priority: &priorityConfigured},
		{Group: "default", Model: "claude-3-opus", ChannelId: 2, Enabled: true, Priority: &priorityMissing},
		{Group: "default", Model: "gpt-4o", ChannelId: 3, Enabled: true, Priority: &priorityConfigured}, // duplicate model, different channel: must not double-count
		{Group: "default", Model: "disabled-model", ChannelId: 4, Enabled: false, Priority: &priorityDisabled},
	}
	for i := range abilities {
		if err := ctx.DB.Create(&abilities[i]).Error; err != nil {
			t.Fatalf("seed ability %d: %v", i, err)
		}
	}
	if err := ctx.DB.Create(&repo.Model{ModelName: "gpt-4o", Status: 1}).Error; err != nil {
		t.Fatalf("seed configured model: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing-models", nil))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data is not a list: %T", resp["data"])
	}
	missing := make(map[string]bool, len(data))
	for _, v := range data {
		missing[v.(string)] = true
	}
	if !missing["claude-3-opus"] {
		t.Errorf("expected 'claude-3-opus' (enabled ability, no models row) to be reported missing, got %v", data)
	}
	if missing["gpt-4o"] {
		t.Errorf("'gpt-4o' has a models row, must NOT be reported missing, got %v", data)
	}
	if missing["disabled-model"] {
		t.Errorf("'disabled-model' ability is disabled, must NOT be scanned at all, got %v", data)
	}
	if len(data) != 1 {
		t.Errorf("expected exactly 1 missing model (deduped), got %d: %v", len(data), data)
	}
}
