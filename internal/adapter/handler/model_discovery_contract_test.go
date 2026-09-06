package handler

// model_discovery_contract_test.go locks the model discovery contract:
//   - ListModels' Anthropic branch must not panic on a caller with zero
//     routable models (day-one tenant, or a token whose model allow-list is
//     empty) — it must answer 200 with an empty list instead.
//   - RetrieveModel's unknown-model branch must answer 404, not 200, and
//     must carry the caller's own wire envelope, not always the OpenAI one.
//
// Table-driven so each row is independently red on a revert of the
// corresponding guard (see the mutation notes inline below).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// modelDiscoveryNewCtx builds a detached *gin.Context/*httptest.ResponseRecorder
// pair, mirroring the r2chanNewCtx helper already used against this same
// model.go (cover_r2_channel_test.go) but kept local so this file has no
// cross-file test-helper dependency.
func modelDiscoveryNewCtx(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

// TestListModels_AnthropicBranch_EmptyCatalogueDoesNotPanic covers row 1 of
// the oracle: a caller with a model allow-list that is enabled but empty
// (ContextKeyTokenModelLimitEnabled=true, empty token_model_limit map) must
// get 200 with an empty data array, null first_id/last_id, has_more false —
// and, above all, must not panic.
//
// Mutation 1 (restore the unguarded `useranthropicModels[0].ID` /
// `useranthropicModels[len(useranthropicModels)-1].ID` reads) makes this
// panic with "index out of range [0] with length 0".
func TestListModels_AnthropicBranch_EmptyCatalogueDoesNotPanic(t *testing.T) {
	c, w := modelDiscoveryNewCtx(http.MethodGet, "/v1/models")
	// userId stays 0 (id context key unset) so ListModels' acceptUnsetRatioModel
	// probe never touches the DB — this test is DB-free by construction.
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{})

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ListModels panicked on an empty catalogue: %v", r)
			}
		}()
		ListModels(c, constant.ChannelTypeAnthropic)
	}()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data field missing or not an array; body=%s", w.Body.String())
	}
	if len(data) != 0 {
		t.Errorf("data = %v, want empty array", data)
	}
	if resp["first_id"] != nil {
		t.Errorf("first_id = %v, want null", resp["first_id"])
	}
	if resp["last_id"] != nil {
		t.Errorf("last_id = %v, want null", resp["last_id"])
	}
	if resp["has_more"] != false {
		t.Errorf("has_more = %v, want false", resp["has_more"])
	}
}

// TestListModels_AnthropicBranch_NonEmptyCatalogueUnchanged is the negative
// control: a non-empty catalogue must keep answering the exact same shape it
// does today (first_id/last_id populated from the real entries), proving the
// guard above is not a blanket rewrite of the success path.
func TestListModels_AnthropicBranch_NonEmptyCatalogueUnchanged(t *testing.T) {
	var existing string
	for id := range openAIModelsMap {
		existing = id
		break
	}
	if existing == "" {
		t.Skip("no models registered in openAIModelsMap")
	}
	// Force acceptUnsetRatioModel=true so this row's inclusion in the
	// catalogue does not depend on whether `existing` happens to have a
	// configured ratio/price in this unit-test process — that's orthogonal
	// to what this test is proving (the non-empty-catalogue shape).
	prevSelfUse := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	defer func() { operation_setting.SelfUseModeEnabled = prevSelfUse }()

	c, w := modelDiscoveryNewCtx(http.MethodGet, "/v1/models")
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{existing: true})

	ListModels(c, constant.ChannelTypeAnthropic)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("data = %v, want a single-element array; body=%s", resp["data"], w.Body.String())
	}
	if resp["first_id"] != existing {
		t.Errorf("first_id = %v, want %q", resp["first_id"], existing)
	}
	if resp["last_id"] != existing {
		t.Errorf("last_id = %v, want %q", resp["last_id"], existing)
	}
}

// TestRetrieveModel_UnknownModel_OpenAIWire covers row 2 of the oracle:
// unknown model on the OpenAI (default) wire must be a 404, not a 200 with an
// error body buried inside a 200 envelope.
//
// Mutation 2 (restore `c.JSON(200, ...)` in the else branch) makes this red
// on the status code assertion.
func TestRetrieveModel_UnknownModel_OpenAIWire(t *testing.T) {
	c, w := modelDiscoveryNewCtx(http.MethodGet, "/v1/models/no-such-model-xyz")
	c.Params = gin.Params{{Key: "model", Value: "no-such-model-xyz"}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model_not_found") {
		t.Errorf("body = %s, want the OpenAI model_not_found error code", w.Body.String())
	}
}

// TestRetrieveModel_UnknownModel_AnthropicWire covers row 3 of the oracle:
// unknown model on the Anthropic wire must be a 404 carrying that vendor's
// own error envelope (not_found_error), and the body must not leak the
// OpenAI-only "param" field.
//
// Mutation 3 (drop the modelType switch so the else branch always emits the
// OpenAI envelope) makes this red on the "param" absence check.
func TestRetrieveModel_UnknownModel_AnthropicWire(t *testing.T) {
	c, w := modelDiscoveryNewCtx(http.MethodGet, "/v1/models/no-such-model-xyz")
	c.Params = gin.Params{{Key: "model", Value: "no-such-model-xyz"}}
	RetrieveModel(c, constant.ChannelTypeAnthropic)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("body = %s, want the Claude envelope (top-level \"type\":\"error\")", body)
	}
	if !strings.Contains(body, "not_found_error") {
		t.Errorf("body = %s, want the Claude not_found_error type", body)
	}
	if strings.Contains(body, "param") {
		t.Errorf("body = %s, must not carry the OpenAI-only \"param\" field", body)
	}
}

// TestRetrieveModel_KnownModel_Unchanged is the negative control for
// RetrieveModel: an existing model on both wires must keep answering 200
// with its established shape, proving the 404 fix is scoped to the
// unknown-model branch only.
func TestRetrieveModel_KnownModel_Unchanged(t *testing.T) {
	var existing string
	for id := range openAIModelsMap {
		existing = id
		break
	}
	if existing == "" {
		t.Skip("no models registered in openAIModelsMap")
	}

	c, w := modelDiscoveryNewCtx(http.MethodGet, fmt.Sprintf("/v1/models/%s", existing))
	c.Params = gin.Params{{Key: "model", Value: existing}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)
	if w.Code != http.StatusOK {
		t.Fatalf("OpenAI wire, known model: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	c2, w2 := modelDiscoveryNewCtx(http.MethodGet, fmt.Sprintf("/v1/models/%s", existing))
	c2.Params = gin.Params{{Key: "model", Value: existing}}
	RetrieveModel(c2, constant.ChannelTypeAnthropic)
	if w2.Code != http.StatusOK {
		t.Fatalf("Anthropic wire, known model: status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
}
