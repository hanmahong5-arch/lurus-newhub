package handler

// cov_handler-deploy_uptimekuma_test.go — business-acceptance tests for
// uptime_kuma.go: GET /console status widget that fans out to N configured
// Uptime Kuma status-page groups and merges monitor status+uptime. Edge focus:
// malformed/partial upstream payloads must degrade to an empty group rather
// than erroring the whole console page, and per-monitor fields must fall back
// to zero values when the heartbeat/uptime maps don't carry an entry.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/console_setting"

	"github.com/gin-gonic/gin"
)

func handlerDeployUKSaveGroups(t *testing.T) {
	t.Helper()
	cs := console_setting.GetConsoleSetting()
	prev := cs.UptimeKumaGroups
	t.Cleanup(func() { cs.UptimeKumaGroups = prev })
}

func handlerDeployUKSetGroups(t *testing.T, groupsJSON string) {
	t.Helper()
	handlerDeployUKSaveGroups(t)
	console_setting.GetConsoleSetting().UptimeKumaGroups = groupsJSON
}

func handlerDeployUKRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/status", GetUptimeKumaStatus)
	return r
}

func handlerDeployUKDo(r *gin.Engine) (*httptest.ResponseRecorder, map[string]interface{}) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	r.ServeHTTP(w, req)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestGetUptimeKumaStatus_NoGroupsConfigured(t *testing.T) {
	handlerDeployUKSetGroups(t, "")

	w, body := handlerDeployUKDo(handlerDeployUKRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body["success"] != true {
		t.Errorf("success = %v, want true", body["success"])
	}
	data, ok := body["data"].([]interface{})
	if !ok {
		t.Fatalf("data not an array: %v", body["data"])
	}
	if len(data) != 0 {
		t.Errorf("len(data) = %d, want 0", len(data))
	}
}

// handlerDeployUKStub stands in for one Uptime Kuma status-page host: it
// serves /api/status-page/<slug> and /api/status-page/heartbeat/<slug> with
// caller-supplied bodies (or a configurable non-200 to simulate the "group
// unreachable" degrade path).
func handlerDeployUKStub(t *testing.T, slug string, statusCode int, statusBody, heartbeatBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status-page/"+slug, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(statusBody))
	})
	mux.HandleFunc("/api/status-page/heartbeat/"+slug, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(heartbeatBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGetUptimeKumaStatus_HappyPath_MergesUptimeAndHeartbeatStatus(t *testing.T) {
	statusBody := `{
		"publicGroupList": [
			{"id": 1, "name": "Core", "monitorList": [
				{"id": 101, "name": "api"},
				{"id": 102, "name": "relay"}
			]}
		]
	}`
	heartbeatBody := `{
		"heartbeatList": {
			"101": [{"status": 1}],
			"102": [{"status": 0}]
		},
		"uptimeList": {
			"101_24": 99.95,
			"102_24": 42.0
		}
	}`
	srv := handlerDeployUKStub(t, "core-slug", http.StatusOK, statusBody, heartbeatBody)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "core-slug", "categoryName": "Core Services"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	w, body := handlerDeployUKDo(handlerDeployUKRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	data, ok := body["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected exactly 1 group result, got %v", body["data"])
	}
	group, _ := data[0].(map[string]interface{})
	if group["categoryName"] != "Core Services" {
		t.Errorf("categoryName = %v, want 'Core Services'", group["categoryName"])
	}
	monitors, _ := group["monitors"].([]interface{})
	if len(monitors) != 2 {
		t.Fatalf("expected 2 monitors, got %d: %v", len(monitors), monitors)
	}
	byName := map[string]map[string]interface{}{}
	for _, m := range monitors {
		mm := m.(map[string]interface{})
		byName[mm["name"].(string)] = mm
	}
	api, ok := byName["api"]
	if !ok {
		t.Fatalf("missing 'api' monitor in %v", monitors)
	}
	if api["group"] != "Core" {
		t.Errorf("api.group = %v, want Core", api["group"])
	}
	if api["status"] != float64(1) {
		t.Errorf("api.status = %v, want 1", api["status"])
	}
	if api["uptime"] != 99.95 {
		t.Errorf("api.uptime = %v, want 99.95", api["uptime"])
	}
	relay := byName["relay"]
	if relay["status"] != float64(0) {
		t.Errorf("relay.status = %v, want 0", relay["status"])
	}
	if relay["uptime"] != 42.0 {
		t.Errorf("relay.uptime = %v, want 42.0", relay["uptime"])
	}
}

func TestGetUptimeKumaStatus_MissingHeartbeatAndUptimeEntries_DefaultToZero(t *testing.T) {
	statusBody := `{
		"publicGroupList": [
			{"id": 1, "name": "Core", "monitorList": [
				{"id": 999, "name": "orphan-monitor"}
			]}
		]
	}`
	// heartbeatList/uptimeList carry NO entry for monitor 999 at all.
	heartbeatBody := `{"heartbeatList": {}, "uptimeList": {}}`
	srv := handlerDeployUKStub(t, "orphan-slug", http.StatusOK, statusBody, heartbeatBody)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "orphan-slug", "categoryName": "Orphans"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	_, body := handlerDeployUKDo(handlerDeployUKRouter())
	data := body["data"].([]interface{})
	group := data[0].(map[string]interface{})
	monitors := group["monitors"].([]interface{})
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monitors))
	}
	m := monitors[0].(map[string]interface{})
	if m["status"] != float64(0) {
		t.Errorf("status = %v, want 0 (zero-value default when no heartbeat entry)", m["status"])
	}
	if m["uptime"] != float64(0) {
		t.Errorf("uptime = %v, want 0 (zero-value default when no uptime entry)", m["uptime"])
	}
}

func TestGetUptimeKumaStatus_UpstreamNon200_DegradesToEmptyGroupNotError(t *testing.T) {
	srv := handlerDeployUKStub(t, "flaky-slug", http.StatusInternalServerError, `{}`, `{}`)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "flaky-slug", "categoryName": "Flaky"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	w, body := handlerDeployUKDo(handlerDeployUKRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (one flaky monitoring backend must not fail the console page)", w.Code)
	}
	data := body["data"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("expected 1 group result (degraded, not dropped), got %d", len(data))
	}
	group := data[0].(map[string]interface{})
	monitors, _ := group["monitors"].([]interface{})
	if len(monitors) != 0 {
		t.Errorf("monitors = %v, want empty (upstream failure degrades to zero monitors)", monitors)
	}
	// categoryName is still populated before the fetch is attempted.
	if group["categoryName"] != "Flaky" {
		t.Errorf("categoryName = %v, want Flaky (set before the failing fetch)", group["categoryName"])
	}
}

func TestGetUptimeKumaStatus_MalformedJSONBody_DegradesToEmptyGroup(t *testing.T) {
	srv := handlerDeployUKStub(t, "garbage-slug", http.StatusOK, `not-json-at-all{{{`, `{}`)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "garbage-slug", "categoryName": "Garbage"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	w, body := handlerDeployUKDo(handlerDeployUKRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	data := body["data"].([]interface{})
	group := data[0].(map[string]interface{})
	monitors, _ := group["monitors"].([]interface{})
	if len(monitors) != 0 {
		t.Errorf("monitors = %v, want empty (JSON decode failure degrades silently)", monitors)
	}
}

func TestGetUptimeKumaStatus_GroupMissingURLOrSlug_SkippedWithoutNetworkCall(t *testing.T) {
	dialed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		dialed = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "", "categoryName": "NoSlug"},      // slug missing
		{"url": "", "slug": "no-url-slug", "categoryName": "NoURL"}, // url missing
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	w, body := handlerDeployUKDo(handlerDeployUKRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	data := body["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("expected 2 group results (both degrade-empty, not dropped), got %d", len(data))
	}
	for _, d := range data {
		group := d.(map[string]interface{})
		monitors, _ := group["monitors"].([]interface{})
		if len(monitors) != 0 {
			t.Errorf("monitors = %v, want empty for a group missing url/slug", monitors)
		}
	}
	if dialed {
		t.Errorf("a group missing slug must never reach the network — fetchGroupData should short-circuit on empty url/slug")
	}
}

func TestGetUptimeKumaStatus_EmptyMonitorList_ProducesNoMonitorEntries(t *testing.T) {
	statusBody := `{
		"publicGroupList": [
			{"id": 1, "name": "Empty", "monitorList": []}
		]
	}`
	srv := handlerDeployUKStub(t, "empty-slug", http.StatusOK, statusBody, `{}`)

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv.URL, "slug": "empty-slug", "categoryName": "EmptyCat"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	_, body := handlerDeployUKDo(handlerDeployUKRouter())
	data := body["data"].([]interface{})
	group := data[0].(map[string]interface{})
	monitors, _ := group["monitors"].([]interface{})
	if len(monitors) != 0 {
		t.Errorf("monitors = %v, want empty (group.monitorList empty is skipped entirely)", monitors)
	}
}

func TestGetUptimeKumaStatus_MultipleGroups_FetchedConcurrentlyAndPreserveOrder(t *testing.T) {
	mk := func(id int, slug string) *httptest.Server {
		statusBody := `{"publicGroupList": [{"id": ` + strconv.Itoa(id) + `, "name": "G", "monitorList": [{"id": ` + strconv.Itoa(id) + `, "name": "m` + strconv.Itoa(id) + `"}]}]}`
		return handlerDeployUKStub(t, slug, http.StatusOK, statusBody, `{"heartbeatList":{},"uptimeList":{}}`)
	}
	srv1 := mk(1, "s1")
	srv2 := mk(2, "s2")
	srv3 := mk(3, "s3")

	groupsJSON, _ := json.Marshal([]map[string]interface{}{
		{"url": srv1.URL, "slug": "s1", "categoryName": "Cat1"},
		{"url": srv2.URL, "slug": "s2", "categoryName": "Cat2"},
		{"url": srv3.URL, "slug": "s3", "categoryName": "Cat3"},
	})
	handlerDeployUKSetGroups(t, string(groupsJSON))

	_, body := handlerDeployUKDo(handlerDeployUKRouter())
	data := body["data"].([]interface{})
	if len(data) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(data))
	}
	// Result ordering must match input group order (results[i] indexed write),
	// even though the underlying fetches ran concurrently.
	wantCats := []string{"Cat1", "Cat2", "Cat3"}
	for i, want := range wantCats {
		got := data[i].(map[string]interface{})["categoryName"]
		if got != want {
			t.Errorf("data[%d].categoryName = %v, want %v (result order must match config order)", i, got, want)
		}
	}
}
