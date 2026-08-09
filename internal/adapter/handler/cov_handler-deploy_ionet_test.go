package handler

// cov_handler-deploy_ionet_test.go — business-acceptance tests for deployment.go
// (io.net model-deployment integration). Scope: the feature-flag gate
// (getIoAPIKey / getIoEnterpriseClient / getIoClient), the pure response
// mappers (mapIoNetDeployment / computeStatusCounts), and every handler's
// parameter-validation guard clause that returns BEFORE any outbound network
// call is made.
//
// Deliberately NOT covered: the actual io.net HTTP round-trip (ListDeployments,
// GetDeployment, DeployContainer, ...). Reaching those lines would require
// either a live io.net account or rewriting deployment.go to accept an
// injectable base URL — both out of scope (no real external network in this
// tier, no non-test-file edits allowed). See honest_notes.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/pkg/ionet"

	"github.com/gin-gonic/gin"
)

// handlerDeployIonetOptionSnapshot saves/restores the two OptionMap keys the
// io.net gate reads, so this file's mutations never leak into other tests in
// the package (OptionMap is a package-level global in common).
func handlerDeployIonetOptionSnapshot(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	prevEnabled, hadEnabled := common.OptionMap["model_deployment.ionet.enabled"]
	prevKey, hadKey := common.OptionMap["model_deployment.ionet.api_key"]
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadEnabled {
			common.OptionMap["model_deployment.ionet.enabled"] = prevEnabled
		} else {
			delete(common.OptionMap, "model_deployment.ionet.enabled")
		}
		if hadKey {
			common.OptionMap["model_deployment.ionet.api_key"] = prevKey
		} else {
			delete(common.OptionMap, "model_deployment.ionet.api_key")
		}
	})
}

func handlerDeploySetIonetOption(enabled bool, apiKey string) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	if enabled {
		common.OptionMap["model_deployment.ionet.enabled"] = "true"
	} else {
		common.OptionMap["model_deployment.ionet.enabled"] = "false"
	}
	common.OptionMap["model_deployment.ionet.api_key"] = apiKey
}

func handlerDeployNewRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func handlerDeployDo(r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func handlerDeployParseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse response: %v raw=%s", err, w.Body.String())
	}
	return out
}

// ─── GetModelDeploymentSettings ────────────────────────────────────────────

func TestGetModelDeploymentSettings_Combinations(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)

	cases := []struct {
		name        string
		enabled     bool
		apiKey      string
		wantEnabled bool
		wantConfig  bool
		wantConnect bool
	}{
		{"disabled_no_key", false, "", false, false, false},
		{"enabled_no_key", true, "", true, false, false},
		// enabled=false but a key IS stored: configured must reflect key
		// presence independent of the enabled flag; can_connect requires BOTH.
		{"disabled_with_key", false, "sk-stored", false, true, false},
		{"enabled_with_key", true, "sk-stored", true, true, true},
		{"enabled_whitespace_key_only", true, "   ", true, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlerDeploySetIonetOption(tc.enabled, tc.apiKey)
			r := handlerDeployNewRouter()
			r.GET("/settings", GetModelDeploymentSettings)
			w := handlerDeployDo(r, http.MethodGet, "/settings", nil)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			resp := handlerDeployParseBody(t, w)
			data, _ := resp["data"].(map[string]interface{})
			if data == nil {
				t.Fatalf("missing data: %v", resp)
			}
			if data["provider"] != "io.net" {
				t.Errorf("provider = %v, want io.net", data["provider"])
			}
			if data["enabled"] != tc.wantEnabled {
				t.Errorf("enabled = %v, want %v", data["enabled"], tc.wantEnabled)
			}
			if data["configured"] != tc.wantConfig {
				t.Errorf("configured = %v, want %v", data["configured"], tc.wantConfig)
			}
			if data["can_connect"] != tc.wantConnect {
				t.Errorf("can_connect = %v, want %v", data["can_connect"], tc.wantConnect)
			}
		})
	}
}

// ─── Feature-flag gate: every list/read/write endpoint must refuse to touch
//     io.net at all when the integration is off or unconfigured. This is the
//     dominant, always-reachable branch of every handler in deployment.go. ───

func TestDeploymentEndpoints_IonetDisabled_RejectBeforeNetwork(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(false, "")

	r := handlerDeployNewRouter()
	r.GET("/deployments", GetAllDeployments)
	r.GET("/deployments/search", SearchDeployments)
	r.GET("/deployments/:id", GetDeployment)
	r.PUT("/deployments/:id/name", UpdateDeploymentName)
	r.PUT("/deployments/:id", UpdateDeployment)
	r.POST("/deployments/:id/extend", ExtendDeployment)
	r.DELETE("/deployments/:id", DeleteDeployment)
	r.POST("/deployments", CreateDeployment)
	r.GET("/hardware-types", GetHardwareTypes)
	r.GET("/locations", GetLocations)
	r.GET("/replicas", GetAvailableReplicas)
	r.POST("/price", GetPriceEstimation)
	r.GET("/cluster-name", CheckClusterNameAvailability)
	r.GET("/deployments/:id/logs", GetDeploymentLogs)
	r.GET("/deployments/:id/containers", ListDeploymentContainers)
	r.GET("/deployments/:id/containers/:container_id", GetContainerDetails)

	reqs := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/deployments", nil},
		{http.MethodGet, "/deployments/search", nil},
		{http.MethodGet, "/deployments/dep-1", nil},
		{http.MethodPut, "/deployments/dep-1/name", []byte(`{"name":"x"}`)},
		{http.MethodPut, "/deployments/dep-1", []byte(`{}`)},
		{http.MethodPost, "/deployments/dep-1/extend", []byte(`{}`)},
		{http.MethodDelete, "/deployments/dep-1", nil},
		{http.MethodPost, "/deployments", []byte(`{}`)},
		{http.MethodGet, "/hardware-types", nil},
		{http.MethodGet, "/locations", nil},
		{http.MethodGet, "/replicas?hardware_id=1", nil},
		{http.MethodPost, "/price", []byte(`{}`)},
		{http.MethodGet, "/cluster-name?name=foo", nil},
		{http.MethodGet, "/deployments/dep-1/logs?container_id=c1", nil},
		{http.MethodGet, "/deployments/dep-1/containers", nil},
		{http.MethodGet, "/deployments/dep-1/containers/c1", nil},
	}

	for _, rq := range reqs {
		t.Run(rq.method+"_"+rq.path, func(t *testing.T) {
			w := handlerDeployDo(r, rq.method, rq.path, rq.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (envelope always 200)", w.Code)
			}
			resp := handlerDeployParseBody(t, w)
			if resp["success"] != false {
				t.Errorf("success = %v, want false (io.net disabled must block before any network call)", resp["success"])
			}
			msg, _ := resp["message"].(string)
			if msg == "" {
				t.Errorf("expected a non-empty rejection message")
			}
		})
	}
}

// ─── TestIoNetConnection: this endpoint validates a caller-SUPPLIED api_key
//     independent of the enabled flag (you must be able to test connectivity
//     before flipping the feature on). ──────────────────────────────────────

func TestIoNetConnection_MissingKey_NoStoredFallback(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(false, "") // no stored key, enabled irrelevant here

	r := handlerDeployNewRouter()
	r.POST("/test-conn", TestIoNetConnection)
	w := handlerDeployDo(r, http.MethodPost, "/test-conn", []byte(`{}`))

	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false, got %v (body=%s)", resp["success"], w.Body.String())
	}
	if resp["message"] != "api_key is required" {
		t.Errorf("message = %v, want %q", resp["message"], "api_key is required")
	}
}

// NOTE: TestIoNetConnection's "empty body falls back to the stored api_key"
// branch (deployment.go:75-85) is real but untestable here: past the guard
// clause it immediately calls the live io.net API
// (ionet.NewEnterpriseClient(apiKey).GetMaxGPUsPerContainer(), no injectable
// base URL) — exercising it would require real external network, which this
// tier explicitly disallows. See honest_notes.

func TestIoNetConnection_MalformedJSON_Rejected(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(false, "")

	r := handlerDeployNewRouter()
	r.POST("/test-conn", TestIoNetConnection)
	w := handlerDeployDo(r, http.MethodPost, "/test-conn", []byte(`{"api_key": not-json}`))

	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for malformed JSON, got %v", resp["success"])
	}
	if resp["message"] != "invalid request payload" {
		t.Errorf("message = %v, want %q", resp["message"], "invalid request payload")
	}
}

// ─── Parameter-validation guard clauses reached AFTER the enabled-gate but
//     BEFORE any outbound network call. ─────────────────────────────────────

func TestUpdateDeploymentName_EmptyNameAfterTrim_RejectedWithoutNetwork(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.PUT("/deployments/:id/name", UpdateDeploymentName)

	w := handlerDeployDo(r, http.MethodPut, "/deployments/dep-1/name", []byte(`{"name":"   "}`))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for whitespace-only name, got %v", resp["success"])
	}
	if resp["message"] != "deployment name cannot be empty" {
		t.Errorf("message = %v, want %q", resp["message"], "deployment name cannot be empty")
	}
}

func TestUpdateDeploymentName_MissingRequiredField_BindError(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.PUT("/deployments/:id/name", UpdateDeploymentName)

	w := handlerDeployDo(r, http.MethodPut, "/deployments/dep-1/name", []byte(`{}`))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when required 'name' field is absent, got %v", resp["success"])
	}
}

func TestGetAvailableReplicas_HardwareIDValidation(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	cases := []struct {
		name    string
		query   string
		wantMsg string
	}{
		{"missing", "", "hardware_id parameter is required"},
		{"non_numeric", "?hardware_id=abc", "invalid hardware_id parameter"},
		{"zero", "?hardware_id=0", "invalid hardware_id parameter"},
		{"negative", "?hardware_id=-5", "invalid hardware_id parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := handlerDeployNewRouter()
			r.GET("/replicas", GetAvailableReplicas)
			w := handlerDeployDo(r, http.MethodGet, "/replicas"+tc.query, nil)
			resp := handlerDeployParseBody(t, w)
			if resp["success"] != false {
				t.Fatalf("expected success=false, got %v", resp["success"])
			}
			if resp["message"] != tc.wantMsg {
				t.Errorf("message = %v, want %q", resp["message"], tc.wantMsg)
			}
		})
	}
}

func TestCheckClusterNameAvailability_MissingName(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.GET("/cluster-name", CheckClusterNameAvailability)
	w := handlerDeployDo(r, http.MethodGet, "/cluster-name?name=%20", nil) // trims to empty
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false || resp["message"] != "name parameter is required" {
		t.Errorf("got success=%v message=%v, want false / 'name parameter is required'", resp["success"], resp["message"])
	}
}

func TestGetDeploymentLogs_MissingContainerID(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.GET("/deployments/:id/logs", GetDeploymentLogs)
	w := handlerDeployDo(r, http.MethodGet, "/deployments/dep-1/logs", nil) // no container_id query
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false || resp["message"] != "container_id parameter is required" {
		t.Errorf("got success=%v message=%v, want false / 'container_id parameter is required'", resp["success"], resp["message"])
	}
}

func TestRequireDeploymentID_EmptyAfterTrim(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.GET("/deployments/:id", GetDeployment)
	// %20 decodes to a single space; requireDeploymentID trims it to "".
	w := handlerDeployDo(r, http.MethodGet, "/deployments/%20", nil)
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false || resp["message"] != "deployment ID is required" {
		t.Errorf("got success=%v message=%v, want false / 'deployment ID is required'", resp["success"], resp["message"])
	}
}

func TestRequireContainerID_EmptyAfterTrim(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.GET("/deployments/:id/containers/:container_id", GetContainerDetails)
	w := handlerDeployDo(r, http.MethodGet, "/deployments/dep-1/containers/%20", nil)
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != false || resp["message"] != "container ID is required" {
		t.Errorf("got success=%v message=%v, want false / 'container ID is required'", resp["success"], resp["message"])
	}
}

// ─── Pure mapper unit tests (no HTTP layer at all) ─────────────────────────

func TestMapIoNetDeployment_TimeRemainingFormatting(t *testing.T) {
	fixedCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		minutesLeft int
		wantRemain  string
	}{
		{"zero_completed", 0, "completed"},
		{"minutes_only", 45, "45 minutes"},
		{"hours_and_minutes", 125, "2 hour 5 minutes"},
		{"exact_hour_no_minutes_falls_to_minutes_branch", 60, "1 hour 0 minutes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ionet.Deployment{
				ID: "dep-x", Name: "my-dep", Status: "RUNNING",
				BrandName: "NVIDIA", HardwareName: "H100", HardwareQuantity: 3,
				ComputeMinutesRemaining: tc.minutesLeft,
				CreatedAt:               fixedCreated,
			}
			got := mapIoNetDeployment(d)
			if got["time_remaining"] != tc.wantRemain {
				t.Errorf("time_remaining = %v, want %q", got["time_remaining"], tc.wantRemain)
			}
			if got["status"] != "running" {
				t.Errorf("status = %v, want lowercased 'running'", got["status"])
			}
			if got["hardware_info"] != "NVIDIA H100 x3" {
				t.Errorf("hardware_info = %v, want 'NVIDIA H100 x3'", got["hardware_info"])
			}
			rc, _ := got["resource_config"].(map[string]interface{})
			if rc["gpu"] != "3" {
				t.Errorf("resource_config.gpu = %v, want \"3\"", rc["gpu"])
			}
			wantUnix := fixedCreated.Unix()
			if got["created_at"] != wantUnix {
				t.Errorf("created_at = %v, want %d", got["created_at"], wantUnix)
			}
		})
	}
}

func TestMapIoNetDeployment_ZeroCreatedAt_FallsBackToNow(t *testing.T) {
	before := time.Now().Unix()
	d := ionet.Deployment{ID: "dep-y", Name: "n", Status: "completed"}
	got := mapIoNetDeployment(d)
	after := time.Now().Unix()

	createdAt, ok := got["created_at"].(int64)
	if !ok {
		t.Fatalf("created_at not int64: %T", got["created_at"])
	}
	if createdAt < before || createdAt > after {
		t.Errorf("created_at = %d, want within [%d, %d] (time.Now() fallback for zero CreatedAt)", createdAt, before, after)
	}
}

func TestComputeStatusCounts_KnownAndUnknownStatuses(t *testing.T) {
	deployments := []ionet.Deployment{
		{Status: "Running"},
		{Status: "running"}, // case-insensitive aggregation
		{Status: "FAILED"},
		{Status: "  destroyed  "}, // surrounding whitespace trimmed
		{Status: "some-brand-new-status"},
	}

	counts := computeStatusCounts(42, deployments)

	if counts["all"] != 42 {
		t.Errorf("all = %d, want 42 (uses the caller-supplied total, not len(deployments))", counts["all"])
	}
	if counts["running"] != 2 {
		t.Errorf("running = %d, want 2 (case-insensitive merge)", counts["running"])
	}
	if counts["failed"] != 1 {
		t.Errorf("failed = %d, want 1", counts["failed"])
	}
	if counts["destroyed"] != 1 {
		t.Errorf("destroyed = %d, want 1 (whitespace trimmed before match)", counts["destroyed"])
	}
	if counts["completed"] != 0 {
		t.Errorf("completed = %d, want 0 (present in the fixed status set, unseen in this batch)", counts["completed"])
	}
	if counts["some-brand-new-status"] != 1 {
		t.Errorf("some-brand-new-status = %d, want 1 (unknown statuses are still tallied, just outside the fixed key set)", counts["some-brand-new-status"])
	}
}
