package handler

// cov_h5c_deploy_network_test.go — business-acceptance tests for the io.net
// network-touching deployment handlers in deployment.go: GetAllDeployments,
// SearchDeployments, GetDeployment, DeleteDeployment, GetHardwareTypes,
// GetLocations, ListDeploymentContainers, GetContainerDetails.
//
// These handlers construct an *ionet.Client via the package-private
// getIoClient/getIoEnterpriseClient helpers, which hardcode io.net's
// production base URLs (no injectable base URL). To exercise their response
// mapping / partial-success / malformed-payload branches without any real
// external network call, this file swaps the process-wide
// http.DefaultTransport for the duration of each test with a RoundTripper
// that redirects requests whose Host is api.io.solutions to a local
// httptest.Server, then restores the original transport. All exercised
// handler calls are synchronous (no goroutines survive past the request), so
// this is safe as long as no test in this file calls t.Parallel().

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// h5c_ioNetProxyTransport redirects any request bound for io.net's real host
// to a local fixture server while leaving everything else (there should be
// nothing else during these tests) untouched.
type h5c_ioNetProxyTransport struct {
	targetHost string
	base       http.RoundTripper
}

func (t *h5c_ioNetProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.io.solutions" {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = t.targetHost
		clone.Host = t.targetHost
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}

// h5c_installIoNetFixture points every outbound io.net client call made
// during the rest of the test at srv, restoring the previous
// http.DefaultTransport on cleanup.
func h5c_installIoNetFixture(t *testing.T, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fixture server URL: %v", err)
	}
	prev := http.DefaultTransport
	http.DefaultTransport = &h5c_ioNetProxyTransport{targetHost: u.Host, base: prev}
	t.Cleanup(func() { http.DefaultTransport = prev })
}

// h5c_ionetOptionSnapshot isolates this file's mutation of the shared
// OptionMap gate keys from the rest of the package's tests.
func h5c_ionetOptionSnapshot(t *testing.T) {
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

func h5c_enableIonet(t *testing.T) {
	t.Helper()
	h5c_ionetOptionSnapshot(t)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["model_deployment.ionet.enabled"] = "true"
	common.OptionMap["model_deployment.ionet.api_key"] = "h5c-test-key"
	common.OptionMapRWMutex.Unlock()
}

func h5c_router() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func h5c_do(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func h5c_parse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse response: %v raw=%s", err, w.Body.String())
	}
	return out
}

// h5c_jsonHandler builds an http.HandlerFunc that serves a fixed status code
// and body for a fixed path, used to compose the fixture server's mux.
func h5c_jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// ─── GetAllDeployments ──────────────────────────────────────────────────────

func TestH5c_GetAllDeployments_HappyPath_MapsAndCountsStatuses(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployments", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"deployments": [
				{"id":"d1","status":"RUNNING","name":"alpha","hardware_quantity":2,"brand_name":"NVIDIA","hardware_name":"H100","created_at":"2026-01-01T00:00:00Z"},
				{"id":"d2","status":"STOPPED","name":"beta","hardware_quantity":1,"brand_name":"AMD","hardware_name":"MI300","created_at":"2026-01-02T00:00:00Z"}
			],
			"total": 2,
			"statuses": ["running","stopped"]
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployments", GetAllDeployments)
	w := h5c_do(r, http.MethodGet, "/deployments")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	resp := h5c_parse(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v (body=%s)", resp["success"], w.Body.String())
	}
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v", resp)
	}
	items, _ := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 mapped items, got %d (%v)", len(items), items)
	}
	first, _ := items[0].(map[string]interface{})
	if first["id"] != "d1" || first["status"] != "running" {
		t.Errorf("unexpected mapped first item: %v", first)
	}
	if first["hardware_name"] != "H100" || first["brand_name"] != "NVIDIA" {
		t.Errorf("expected hardware/brand passthrough, got %v", first)
	}
	counts, _ := data["status_counts"].(map[string]interface{})
	if counts == nil {
		t.Fatalf("missing status_counts: %v", data)
	}
}

func TestH5c_GetAllDeployments_MalformedUpstreamJSON_DegradesToApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployments", h5c_jsonHandler(http.StatusOK, `{not-json`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployments", GetAllDeployments)
	w := h5c_do(r, http.MethodGet, "/deployments")

	if w.Code != http.StatusOK {
		t.Fatalf("handler always responds 200 with success=false envelope, got %d", w.Code)
	}
	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for malformed upstream JSON, got %v", resp)
	}
	if msg, _ := resp["message"].(string); msg == "" {
		t.Errorf("expected a non-empty error message, got %v", resp)
	}
}

func TestH5c_GetAllDeployments_UpstreamNon2xx_PropagatesApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployments", h5c_jsonHandler(http.StatusServiceUnavailable, `{"detail":"upstream down"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployments", GetAllDeployments)
	w := h5c_do(r, http.MethodGet, "/deployments")

	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false when io.net returns 503, got %v (body=%s)", resp, w.Body.String())
	}
}

// ─── SearchDeployments ──────────────────────────────────────────────────────

func TestH5c_SearchDeployments_KeywordFiltersAndRecomputesTotal(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployments", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"deployments": [
				{"id":"d1","status":"RUNNING","name":"prod-alpha"},
				{"id":"d2","status":"RUNNING","name":"prod-beta"},
				{"id":"d3","status":"RUNNING","name":"staging-gamma"}
			],
			"total": 3
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/search", SearchDeployments)
	w := h5c_do(r, http.MethodGet, "/search?keyword=prod")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v", resp)
	}
	// total must reflect the post-filter count (2), NOT the upstream total (3).
	totalF, ok := data["total"].(float64)
	if !ok || int(totalF) != 2 {
		t.Fatalf("expected filtered total=2, got %v", data["total"])
	}
	items, _ := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 filtered items, got %d", len(items))
	}
}

func TestH5c_SearchDeployments_EmptyKeyword_UsesUpstreamTotalUnfiltered(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployments", h5c_jsonHandler(http.StatusOK, `{
		"data": {"deployments": [{"id":"d1","status":"RUNNING","name":"only-one"}], "total": 99}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/search", SearchDeployments)
	w := h5c_do(r, http.MethodGet, "/search")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	totalF, _ := data["total"].(float64)
	if int(totalF) != 99 {
		t.Fatalf("expected unfiltered total to pass through upstream total (99), got %v", data["total"])
	}
}

// ─── GetDeployment ──────────────────────────────────────────────────────────

func TestH5c_GetDeployment_HappyPath_MapsDetailFields(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"id":"dep-1","status":"RUNNING","total_containers":3,"hardware_id":42,
			"amount_paid":12.5,"completed_percent":80,"total_gpus":6,"gpus_per_container":2,
			"hardware_name":"H100","brand_name":"NVIDIA","created_at":"2026-01-01T00:00:00Z"
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id", GetDeployment)
	w := h5c_do(r, http.MethodGet, "/deployment/dep-1")

	resp := h5c_parse(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, got %v (body=%s)", resp, w.Body.String())
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["id"] != "dep-1" || data["status"] != "running" {
		t.Errorf("unexpected id/status: %v", data)
	}
	if data["hardware_id"] != float64(42) {
		t.Errorf("expected hardware_id=42, got %v", data["hardware_id"])
	}
	rc, _ := data["resource_config"].(map[string]interface{})
	if rc["gpu"] != "6" {
		t.Errorf("expected resource_config.gpu to be strconv of total_gpus (6), got %v", rc)
	}
}

func TestH5c_GetDeployment_UpstreamNotFound_PropagatesApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/missing-dep", h5c_jsonHandler(http.StatusNotFound, `{"detail":"deployment not found"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id", GetDeployment)
	w := h5c_do(r, http.MethodGet, "/deployment/missing-dep")

	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false on upstream 404, got %v (body=%s)", resp, w.Body.String())
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "deployment not found") {
		t.Errorf("expected upstream detail surfaced in message, got %q", msg)
	}
}

// ─── DeleteDeployment ───────────────────────────────────────────────────────

func TestH5c_DeleteDeployment_HappyPath_ReturnsTerminationMessage(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1", h5c_jsonHandler(http.StatusOK, `{"status":"terminating","deployment_id":"dep-1"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.DELETE("/deployment/:id", DeleteDeployment)
	w := h5c_do(r, http.MethodDelete, "/deployment/dep-1")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data["status"] != "terminating" || data["deployment_id"] != "dep-1" {
		t.Fatalf("unexpected delete response: %v (body=%s)", data, w.Body.String())
	}
	if data["message"] != "Deployment termination requested successfully" {
		t.Errorf("unexpected message: %v", data["message"])
	}
}

func TestH5c_DeleteDeployment_RepeatedCall_SecondCallSurfacesUpstreamError(t *testing.T) {
	// Idempotency edge: io.net has already reaped the deployment on the first
	// delete; a second delete for the same ID should surface the upstream
	// error rather than pretend success.
	h5c_enableIonet(t)

	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"terminating","deployment_id":"dep-1"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"detail":"deployment already deleted"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.DELETE("/deployment/:id", DeleteDeployment)

	w1 := h5c_do(r, http.MethodDelete, "/deployment/dep-1")
	resp1 := h5c_parse(t, w1)
	if resp1["success"] != true {
		t.Fatalf("expected first delete to succeed, got %v", resp1)
	}

	w2 := h5c_do(r, http.MethodDelete, "/deployment/dep-1")
	resp2 := h5c_parse(t, w2)
	if resp2["success"] != false {
		t.Fatalf("expected repeated delete of an already-gone deployment to surface an error, got %v", resp2)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 upstream calls, got %d", calls)
	}
}

// ─── GetHardwareTypes ───────────────────────────────────────────────────────

func TestH5c_GetHardwareTypes_TotalZero_FallsBackToAvailableSumAndNameDefault(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/hardware/max-gpus-per-container", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"hardware": [
				{"hardware_id":1,"max_gpus_per_container":8,"available":3,"hardware_name":"","brand_name":"NVIDIA"},
				{"hardware_id":2,"max_gpus_per_container":4,"available":0,"hardware_name":"MI300","brand_name":"AMD"}
			],
			"total": 0
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/hardware", GetHardwareTypes)
	w := h5c_do(r, http.MethodGet, "/hardware")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v (body=%s)", resp, w.Body.String())
	}
	// total_available must fall back to summing per-item Available (3+0=3)
	// when the upstream's own total field is 0.
	if got, _ := data["total_available"].(float64); int(got) != 3 {
		t.Fatalf("expected total_available fallback sum=3, got %v", data["total_available"])
	}
	types, _ := data["hardware_types"].([]interface{})
	if len(types) != 2 {
		t.Fatalf("expected 2 hardware types, got %d", len(types))
	}
	first, _ := types[0].(map[string]interface{})
	if first["name"] != "Hardware 1" {
		t.Errorf("expected empty hardware_name to fall back to 'Hardware 1', got %v", first["name"])
	}
	if first["available"] != true {
		t.Errorf("expected available=true when available count > 0, got %v", first["available"])
	}
	second, _ := types[1].(map[string]interface{})
	if second["available"] != false {
		t.Errorf("expected available=false when available count is 0, got %v", second["available"])
	}
}

func TestH5c_GetHardwareTypes_UpstreamError_PropagatesApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/hardware/max-gpus-per-container", h5c_jsonHandler(http.StatusInternalServerError, `{"detail":"boom"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/hardware", GetHardwareTypes)
	w := h5c_do(r, http.MethodGet, "/hardware")

	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false on upstream 500, got %v", resp)
	}
}

// ─── GetLocations ───────────────────────────────────────────────────────────

func TestH5c_GetLocations_TotalAndAvailableSumBothZero_FallsBackToLenCount(t *testing.T) {
	h5c_enableIonet(t)

	// Neither the upstream's own "total" field nor the per-location
	// "available" counts (both absent/zero) give a usable total; the
	// handler must fall back to len(locations) rather than reporting 0.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/io-cloud/caas/locations", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"locations": [
				{"id":1,"name":"us-east","iso2":"us"},
				{"id":2,"name":"eu-west","iso2":"de"}
			],
			"total": 0
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/locations", GetLocations)
	w := h5c_do(r, http.MethodGet, "/locations")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v (body=%s)", resp, w.Body.String())
	}
	if got, _ := data["total"].(float64); int(got) != 2 {
		t.Fatalf("expected total fallback to len(locations)=2, got %v", data["total"])
	}
	locs, _ := data["locations"].([]interface{})
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}
	first, _ := locs[0].(map[string]interface{})
	if first["iso2"] != "US" {
		t.Errorf("expected iso2 to be upper-cased by the client mapper, got %v", first["iso2"])
	}
}

// ─── ListDeploymentContainers ───────────────────────────────────────────────

func TestH5c_ListDeploymentContainers_HappyPath_MapsEventsAndTotal(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1/containers", h5c_jsonHandler(http.StatusOK, `{
		"data": {
			"total": 1,
			"workers": [
				{
					"container_id":"c1","device_id":"dev-1","status":"  Running ","hardware":"H100",
					"brand_name":"NVIDIA","created_at":"2026-01-01T00:00:00Z","uptime_percent":97,
					"gpus_per_container":2,"public_url":"https://c1.example",
					"container_events":[{"time":"2026-01-01T00:01:00Z","message":"container started"}]
				}
			]
		}
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id/containers", ListDeploymentContainers)
	w := h5c_do(r, http.MethodGet, "/deployment/dep-1/containers")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if got, _ := data["total"].(float64); int(got) != 1 {
		t.Fatalf("expected total=1, got %v (body=%s)", data["total"], w.Body.String())
	}
	containers, _ := data["containers"].([]interface{})
	if len(containers) != 1 {
		t.Fatalf("expected 1 mapped container, got %d", len(containers))
	}
	c1, _ := containers[0].(map[string]interface{})
	if c1["status"] != "running" {
		t.Errorf("expected status to be lower-cased and trimmed, got %q", c1["status"])
	}
	events, _ := c1["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev, _ := events[0].(map[string]interface{})
	if ev["message"] != "container started" {
		t.Errorf("unexpected event message: %v", ev)
	}
}

func TestH5c_ListDeploymentContainers_MalformedUpstreamJSON_ReturnsApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1/containers", h5c_jsonHandler(http.StatusOK, `not json at all`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id/containers", ListDeploymentContainers)
	w := h5c_do(r, http.MethodGet, "/deployment/dep-1/containers")

	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false for malformed upstream body, got %v", resp)
	}
}

// ─── GetContainerDetails ────────────────────────────────────────────────────

func TestH5c_GetContainerDetails_HappyPath_MapsFields(t *testing.T) {
	h5c_enableIonet(t)

	// Unlike the /containers list endpoint, the single-container endpoint
	// returns the container object directly, with NO {"data": ...} wrapper.
	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1/container/c1", h5c_jsonHandler(http.StatusOK, `{
		"container_id":"c1","device_id":"dev-1","status":"RUNNING","hardware":"H100",
		"brand_name":"NVIDIA","created_at":"2026-01-01T00:00:00Z","uptime_percent":100,
		"gpus_per_container":1,"public_url":"https://c1.example",
		"container_events":[{"time":"2026-01-01T00:02:00Z","message":"healthy"}]
	}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id/container/:container_id", GetContainerDetails)
	w := h5c_do(r, http.MethodGet, "/deployment/dep-1/container/c1")

	resp := h5c_parse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("missing data: %v (body=%s)", resp, w.Body.String())
	}
	if data["deployment_id"] != "dep-1" || data["container_id"] != "c1" {
		t.Errorf("unexpected ids: %v", data)
	}
	if data["status"] != "running" {
		t.Errorf("expected status lower-cased, got %v", data["status"])
	}
	events, _ := data["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestH5c_GetContainerDetails_UpstreamNon200_PropagatesApiError(t *testing.T) {
	h5c_enableIonet(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/enterprise/v1/io-cloud/caas/deployment/dep-1/container/missing", h5c_jsonHandler(http.StatusNotFound, `{"detail":"container not found"}`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h5c_installIoNetFixture(t, srv)

	r := h5c_router()
	r.GET("/deployment/:id/container/:container_id", GetContainerDetails)
	w := h5c_do(r, http.MethodGet, "/deployment/dep-1/container/missing")

	resp := h5c_parse(t, w)
	if resp["success"] != false {
		t.Fatalf("expected success=false on upstream 404, got %v", resp)
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "container not found") {
		t.Errorf("expected upstream detail surfaced, got %q", msg)
	}
}
