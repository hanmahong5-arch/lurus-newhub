package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

func publicModelsRouter() *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/models", ListPublicModels)
	r.GET("/api/v1/models/*model", GetPublicModel)
	return r
}

func getPublicModels(t *testing.T, path string) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	publicModelsRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (body %s)", path, err, w.Body.String())
	}
	return w.Code, body
}

// The catalogue converts the same rows /api/pricing publishes into
// OpenRouter's shape: USD per token as a decimal string, priced at the
// "default" group, unpriced models left out. Mutation: drop PricedOnly → the
// unpriced row appears; forget to divide by QuotaPerUnit → prompt is "2.5".
func TestListPublicModels_OpenRouterShape(t *testing.T) {
	ctx := setupPricingRouter(t)
	priced, unpriced := "pm/priced-model", "pm-unpriced-model"
	seedPricedAndUnpriced(t, ctx, priced, unpriced)

	code, body := getPublicModels(t, "/api/v1/models")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	rows, _ := body["data"].([]interface{})
	byID := map[string]map[string]interface{}{}
	for _, r := range rows {
		m := r.(map[string]interface{})
		byID[m["id"].(string)] = m
	}
	if byID[unpriced] != nil {
		t.Errorf("unpriced model published; a 0 price reads as free")
	}
	row := byID[priced]
	if row == nil {
		t.Fatalf("priced model missing from catalogue: %v", body)
	}

	pricing := row["pricing"].(map[string]interface{})
	// model_ratio 2.5 × group_ratio 1 / 500000 quota-per-USD.
	if got := pricing["prompt"]; got != "0.000005" {
		t.Errorf("pricing.prompt = %v, want \"0.000005\"", got)
	}
	if got := pricing["request"]; got != "0" {
		t.Errorf("pricing.request = %v, want \"0\" for a per-token model", got)
	}
	if _, ok := row["context_length"]; !ok || row["context_length"] != nil {
		t.Errorf("context_length = %v, want present and null (not recorded, never invented)", row["context_length"])
	}
	arch := row["architecture"].(map[string]interface{})
	if arch["modality"] != "text->text" {
		t.Errorf("architecture.modality = %v, want text->text", arch["modality"])
	}

	lurus := row["lurus"].(map[string]interface{})
	if lurus["group"] != "default" {
		t.Errorf("lurus.group = %v, want default", lurus["group"])
	}
	// 0.000005 USD/token × 1e6 × exchange rate.
	want := 5 * operation_setting.USDExchangeRate
	if got, _ := lurus["prompt_cny_per_mtok"].(float64); got != want {
		t.Errorf("lurus.prompt_cny_per_mtok = %v, want %v", lurus["prompt_cny_per_mtok"], want)
	}
}

// Model names may contain "/"; the single-model route is a catch-all.
func TestGetPublicModel_SlashInNameAndNotFound(t *testing.T) {
	ctx := setupPricingRouter(t)
	priced := "pm/priced-model"
	seedPricedAndUnpriced(t, ctx, priced, "pm-unpriced-model")

	code, body := getPublicModels(t, "/api/v1/models/"+priced)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", code, body)
	}
	if got := body["data"].(map[string]interface{})["id"]; got != priced {
		t.Errorf("data.id = %v, want %s", got, priced)
	}

	code, _ = getPublicModels(t, "/api/v1/models/pm-unpriced-model")
	if code != http.StatusNotFound {
		t.Errorf("unpriced model: status = %d, want 404", code)
	}
}

func TestPublicPriceGroup(t *testing.T) {
	usable := map[string]string{"default": "", "vip": "", "cheap": ""}
	if g, _, ok := publicPriceGroup([]string{"vip", "default"}, usable); !ok || g != "default" {
		t.Errorf("default present: got %q ok=%v, want default", g, ok)
	}
	if _, _, ok := publicPriceGroup([]string{"internal-only"}, usable); ok {
		t.Errorf("no anonymously usable group: want ok=false")
	}
}
