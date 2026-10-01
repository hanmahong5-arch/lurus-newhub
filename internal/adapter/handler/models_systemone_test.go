package handler

// models_systemone_test.go — System One models in /v1/models and the pricing
// catalogue: they are listed (OpenAI-compatible wire shape unchanged) and each
// carries supported_endpoint_types=["systemone"] so a chat picker keyed on the
// endpoint type does not offer them.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

type listedModel struct {
	Id                     string   `json:"id"`
	Object                 string   `json:"object"`
	OwnedBy                string   `json:"owned_by"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
}

func seedTypedChannel(t *testing.T, id, channelType int, model string) {
	t.Helper()
	ch := &repo.Channel{Id: id, Type: channelType, Status: common.ChannelStatusEnabled, Name: "so" + model, Models: model, Group: "default", TenantId: "default"}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := repo.DB.Create(&repo.Ability{Group: "default", Model: model, ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

// migrateCatalogueTables adds the model-meta and vendor tables the pricing
// build reads, so it runs without logging a missing-table error per query.
func migrateCatalogueTables(t *testing.T) {
	t.Helper()
	if err := repo.DB.AutoMigrate(&repo.Model{}, &repo.Vendor{}); err != nil {
		t.Fatalf("automigrate catalogue tables: %v", err)
	}
}

func TestListModels_SystemOneModelsCarrySystemOneEndpoint(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	seedTenantScopeUser(t, 1, "default")
	seedTypedChannel(t, 1, constant.ChannelTypeTypeSafe, "jev-latest")
	seedTypedChannel(t, 2, constant.ChannelTypeSystemOneCompatible, "laya-english")
	seedTypedChannel(t, 3, constant.ChannelTypeOpenAI, "chat-model-a")
	migrateCatalogueTables(t)
	repo.InvalidatePricingCache()
	t.Cleanup(repo.InvalidatePricingCache)
	repo.GetPricing() // ListModels reads the endpoint map the pricing build fills

	c, w := modelTenantScopeCtx("/v1/models", 1, "default")
	ListModels(c, constant.ChannelTypeOpenAI)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool          `json:"success"`
		Object  string        `json:"object"`
		Data    []listedModel `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if !resp.Success || resp.Object != "list" {
		t.Errorf("envelope changed: success=%v object=%q", resp.Success, resp.Object)
	}
	byID := map[string]listedModel{}
	for _, m := range resp.Data {
		byID[m.Id] = m
	}
	want := map[string]string{"jev-latest": "systemone", "laya-english": "systemone", "chat-model-a": "openai"}
	for id, endpoint := range want {
		m, ok := byID[id]
		if !ok {
			t.Errorf("%s missing from /v1/models: %v", id, resp.Data)
			continue
		}
		if m.Object != "model" {
			t.Errorf("%s object = %q, want model", id, m.Object)
		}
		if len(m.SupportedEndpointTypes) != 1 || m.SupportedEndpointTypes[0] != endpoint {
			t.Errorf("%s supported_endpoint_types = %v, want [%s]", id, m.SupportedEndpointTypes, endpoint)
		}
	}
}

func TestPricing_SystemOneModelsCarrySystemOneEndpoint(t *testing.T) {
	cleanup := setupModelTenantScopeDB(t)
	defer cleanup()
	seedTypedChannel(t, 1, constant.ChannelTypeTypeSafe, "jev-latest")
	seedTypedChannel(t, 2, constant.ChannelTypeSystemOneCompatible, "laya-english")
	migrateCatalogueTables(t)
	repo.InvalidatePricingCache()
	t.Cleanup(repo.InvalidatePricingCache)

	got := map[string][]constant.EndpointType{}
	for _, p := range repo.GetPricing() {
		got[p.ModelName] = p.SupportedEndpointTypes
	}
	for _, model := range []string{"jev-latest", "laya-english"} {
		eps, ok := got[model]
		if !ok {
			t.Errorf("%s missing from the pricing catalogue", model)
			continue
		}
		if len(eps) != 1 || eps[0] != constant.EndpointTypeSystemOne {
			t.Errorf("%s supported_endpoint_types = %v, want [systemone]", model, eps)
		}
	}
	if info := repo.GetSupportedEndpointMap()["systemone"]; info.Path != "/v1/systemone" || info.Method != "POST" {
		t.Errorf("endpoint map entry = %+v, want POST /v1/systemone", info)
	}
}
