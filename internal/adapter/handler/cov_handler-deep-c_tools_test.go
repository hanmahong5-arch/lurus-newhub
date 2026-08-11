package handler

// cov_handler-deep-c_tools_test.go — business-acceptance coverage for
// tool_manifest.go's GetToolDownloadManifest and tool_version.go's
// GetToolVersions, both at 0% before this file.
//
// GetToolDownloadManifest reuses handlerDeployReleaseNewFixture (from
// cov_handler-deploy_release_test.go) to get a real *app.ReleaseService
// wired to an isolated SQLite DB — MinIO is never configured in this tier,
// so BuildToolManifest takes its documented npm-only fast path with no
// outbound network call.
//
// GetToolVersions reuses the miniredis pattern already established by
// cov_handler-deploy_health_test.go for common.RDB.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// ─── GetToolDownloadManifest ────────────────────────────────────────────

func TestGetToolDownloadManifest_ServiceNotInitialized_503(t *testing.T) {
	prev := releaseService
	releaseService = nil
	t.Cleanup(func() { releaseService = prev })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/manifest", GetToolDownloadManifest)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/manifest", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	if resp["error"] != "release service not initialized" {
		t.Errorf("error = %v, want 'release service not initialized'", resp["error"])
	}
}

func TestGetToolDownloadManifest_NpmOnlyFastPath_ReturnsCacheableManifest(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	f.router.GET("/manifest", GetToolDownloadManifest)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	f.router.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=300'", cc)
	}
	var resp struct {
		GeneratedAt string                    `json:"generated_at"`
		Tools       map[string]map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse manifest body: %v raw=%s", err, w.Body.String())
	}
	if resp.GeneratedAt == "" {
		t.Error("expected non-empty generated_at timestamp")
	}
	if len(resp.Tools) == 0 {
		t.Error("expected at least the static npm tool entries in the manifest")
	}
}

// TestGetToolDownloadManifest_SecondCall_ServesFromCache exercises the
// fast-path branch of BuildToolManifest itself (cache hit) via two
// back-to-back handler calls sharing the same releaseService instance —
// generated_at must be identical across both responses since the second
// call must not rebuild.
func TestGetToolDownloadManifest_SecondCall_ServesFromCache(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	f.router.GET("/manifest", GetToolDownloadManifest)

	do := func() string {
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/manifest", nil))
		var resp struct {
			GeneratedAt string `json:"generated_at"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("parse manifest body: %v", err)
		}
		return resp.GeneratedAt
	}
	first := do()
	second := do()
	if first != second {
		t.Errorf("expected cached generated_at to be stable across calls: first=%s second=%s", first, second)
	}
}

// ─── GetToolVersions ────────────────────────────────────────────────────

func handlerDeepCToolVersionsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/tool-versions", GetToolVersions)
	return r
}

func handlerDeepCSnapshotRedis(t *testing.T) {
	t.Helper()
	prevEnabled, prevRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = prevEnabled, prevRDB
	})
}

func TestGetToolVersions_RedisDisabled_ReturnsEmptyMap(t *testing.T) {
	handlerDeepCSnapshotRedis(t)
	common.RedisEnabled = false
	common.RDB = nil

	r := handlerDeepCToolVersionsRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tool-versions", nil))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok || len(data) != 0 {
		t.Errorf("data = %v, want empty map when Redis disabled", resp["data"])
	}
}

func TestGetToolVersions_RedisEnabledButKeyMissing_ReturnsEmptyMap(t *testing.T) {
	handlerDeepCSnapshotRedis(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})

	r := handlerDeepCToolVersionsRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tool-versions", nil))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok || len(data) != 0 {
		t.Errorf("data = %v, want empty map when the hash key has never been written", resp["data"])
	}
}

func TestGetToolVersions_CachedVersions_ValidAndCorruptEntriesFiltered(t *testing.T) {
	handlerDeepCSnapshotRedis(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})

	ctx := context.Background()
	valid, err := json.Marshal(entity.ToolVersion{Tool: "shot-console", Version: "1.4.2", Source: "npm", UpdatedAt: time.Now()})
	if err != nil {
		t.Fatalf("marshal valid entry: %v", err)
	}
	emptyVersion, err := json.Marshal(entity.ToolVersion{Tool: "empty-version-tool", Version: "", Source: "npm"})
	if err != nil {
		t.Fatalf("marshal empty-version entry: %v", err)
	}
	if err := common.RDB.HSet(ctx, toolVersionRedisKey, map[string]interface{}{
		"shot-console":        string(valid),
		"empty-version-tool":  string(emptyVersion),
		"corrupt-json-tool":   "{not-json",
	}).Err(); err != nil {
		t.Fatalf("seed redis hash: %v", err)
	}

	r := handlerDeepCToolVersionsRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tool-versions", nil))
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}
	if data["shot-console"] != "1.4.2" {
		t.Errorf("shot-console version = %v, want 1.4.2", data["shot-console"])
	}
	if _, present := data["empty-version-tool"]; present {
		t.Errorf("entry with empty Version must be filtered out, got %v", data)
	}
	if _, present := data["corrupt-json-tool"]; present {
		t.Errorf("entry with corrupt JSON must be filtered out, got %v", data)
	}
	if len(data) != 1 {
		t.Errorf("expected exactly 1 surfaced version, got %d: %v", len(data), data)
	}
}
