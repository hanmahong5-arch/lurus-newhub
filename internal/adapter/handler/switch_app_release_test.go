package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Registers a fresh no-tenant engine (static path must not collide with the
// ":tenant_slug" wildcard — same reasoning as switch_pricing_test.go).
func switchAppReleaseRouter() *gin.Engine {
	r := gin.New()
	r.GET("/api/v2/switch/app/releases/latest", GetSwitchAppRelease)
	return r
}

func setReleaseOptions(t *testing.T, kv map[string]string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev := map[string]string{}
	keys := []string{optSwitchAppVersion, optSwitchAppNotes, optSwitchAppDownloadURL, optSwitchAppSHA256, optSwitchAppSigURL}
	for _, k := range keys {
		prev[k] = common.OptionMap[k]
		delete(common.OptionMap, k)
	}
	for k, v := range kv {
		common.OptionMap[k] = v
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		for _, k := range keys {
			if prev[k] == "" {
				delete(common.OptionMap, k)
			} else {
				common.OptionMap[k] = prev[k]
			}
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func getRelease(t *testing.T) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	r := switchAppReleaseRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/switch/app/releases/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v, body: %s", err, w.Body.String())
	}
	return w, body
}

func TestGetSwitchAppRelease_NotConfiguredIs404(t *testing.T) {
	setReleaseOptions(t, nil)
	w, body := getRelease(t)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when unpublished, body: %s", w.Code, w.Body.String())
	}
	if body["success"] != false {
		t.Errorf("success = %v, want false", body["success"])
	}
}

func TestGetSwitchAppRelease_PublishedHappyPath(t *testing.T) {
	setReleaseOptions(t, map[string]string{
		optSwitchAppVersion:     "1.4.2",
		optSwitchAppNotes:       "fixes",
		optSwitchAppDownloadURL: "https://hub.lurus.cn/dl/lurus-switch-windows-x64.exe",
		optSwitchAppSHA256:      "abc123",
		optSwitchAppSigURL:      "https://hub.lurus.cn/dl/lurus-switch-windows-x64.exe.sig",
	})
	w, body := getRelease(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if body["success"] != true {
		t.Errorf("success = %v, want true", body["success"])
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	want := map[string]string{
		"version":      "1.4.2",
		"notes":        "fixes",
		"download_url": "https://hub.lurus.cn/dl/lurus-switch-windows-x64.exe",
		"sha256":       "abc123",
		"sig_url":      "https://hub.lurus.cn/dl/lurus-switch-windows-x64.exe.sig",
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data.%s = %v, want %q", k, data[k], v)
		}
	}
}

func TestGetSwitchAppRelease_VersionWithoutURLIs404(t *testing.T) {
	// Half-configured must read as "unpublished", not as a 200 the client
	// would then reject — keep the fallback path clean.
	setReleaseOptions(t, map[string]string{optSwitchAppVersion: "1.4.2"})
	w, _ := getRelease(t)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when download_url missing", w.Code)
	}
}
