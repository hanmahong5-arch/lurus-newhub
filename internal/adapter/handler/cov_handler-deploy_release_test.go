package handler

// cov_handler-deploy_release_test.go — business-acceptance tests for
// release.go against a REAL repo.ReleaseRepository + app.ReleaseService
// wired to an isolated in-memory SQLite DB (release_test.go's existing tests
// mostly assert HTTP status shape without seeding real rows or initializing
// releaseService in a controlled way — this file closes that gap with actual
// persisted fixtures and asserts on response BODY fields, not just codes).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var handlerDeployReleaseDBCounter atomic.Int64

type handlerDeployReleaseFixture struct {
	router *gin.Engine
	db     *gorm.DB
}

func handlerDeployReleaseNewFixture(t *testing.T) *handlerDeployReleaseFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:covrelease%d?mode=memory&cache=shared", handlerDeployReleaseDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&entity.Release{}, &entity.ReleaseArtifact{}, &entity.DownloadLog{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevService := releaseService
	prevDB := repo.DB
	repo.DB = db
	releaseService = app.NewReleaseService(repo.NewReleaseRepository(db))

	t.Cleanup(func() {
		releaseService = prevService
		repo.DB = prevDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	r := gin.New()
	r.GET("/api/v1/releases", ListReleases)
	r.GET("/api/v1/releases/latest/:product_id", GetLatestRelease)
	r.GET("/api/v1/releases/:id", GetReleaseByID)
	r.GET("/api/v1/releases/:id/download/:artifact_id", DownloadArtifact)
	r.GET("/api/v1/releases/:id/changelog", GetChangelog)
	return &handlerDeployReleaseFixture{router: r, db: db}
}

func handlerDeployReleaseDo(f *handlerDeployReleaseFixture, path string) (*httptest.ResponseRecorder, map[string]interface{}) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	f.router.ServeHTTP(w, req)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func handlerDeploySeedRelease(t *testing.T, f *handlerDeployReleaseFixture, r *entity.Release) *entity.Release {
	t.Helper()
	if err := f.db.Create(r).Error; err != nil {
		t.Fatalf("seed release: %v", err)
	}
	return r
}

// ─── ListReleases ───────────────────────────────────────────────────────────

func TestListReleases_OnlyPublishedNonPrereleaseByDefault(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()

	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "1.0.0", Title: "v1", ReleaseType: "stable", IsPublished: true, IsPrerelease: false, PublishedAt: &now})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "0.9.0-beta", Title: "beta", ReleaseType: "stable", IsPublished: true, IsPrerelease: true, PublishedAt: &now})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "0.8.0", Title: "draft", ReleaseType: "stable", IsPublished: false, IsPrerelease: false})

	w, body := handlerDeployReleaseDo(f, "/api/v1/releases?product_id=lurus-cli")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	data, _ := body["data"].(map[string]interface{})
	if data["total"] != float64(1) {
		t.Fatalf("total = %v, want 1 (draft + prerelease excluded by default), body=%v", data["total"], body)
	}
	releases, _ := data["releases"].([]interface{})
	if len(releases) != 1 {
		t.Fatalf("len(releases) = %d, want 1", len(releases))
	}
	rel := releases[0].(map[string]interface{})
	if rel["version"] != "1.0.0" {
		t.Errorf("version = %v, want 1.0.0", rel["version"])
	}
}

func TestListReleases_IncludePrereleaseTrue_SurfacesBoth(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "1.0.0", Title: "v1", ReleaseType: "stable", IsPublished: true, IsPrerelease: false, PublishedAt: &now})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "1.1.0-rc1", Title: "rc", ReleaseType: "stable", IsPublished: true, IsPrerelease: true, PublishedAt: &now})

	_, body := handlerDeployReleaseDo(f, "/api/v1/releases?product_id=lurus-cli&include_prerelease=true")
	data, _ := body["data"].(map[string]interface{})
	if data["total"] != float64(2) {
		t.Fatalf("total = %v, want 2 with include_prerelease=true, body=%v", data["total"], body)
	}
}

func TestListReleases_ProductIDFilter_ExcludesOtherProducts(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-cli", Version: "1.0.0", ReleaseType: "stable", IsPublished: true, PublishedAt: &now})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "lurus-switch", Version: "2.0.0", ReleaseType: "stable", IsPublished: true, PublishedAt: &now})

	_, body := handlerDeployReleaseDo(f, "/api/v1/releases?product_id=lurus-switch")
	data, _ := body["data"].(map[string]interface{})
	releases, _ := data["releases"].([]interface{})
	if len(releases) != 1 {
		t.Fatalf("expected 1 release for lurus-switch, got %d", len(releases))
	}
	if releases[0].(map[string]interface{})["product_id"] != "lurus-switch" {
		t.Errorf("leaked a release from another product_id filter")
	}
}

func TestListReleases_PageSizeOutOfRange_DefaultsTo20(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	_, body := handlerDeployReleaseDo(f, "/api/v1/releases?page_size=500")
	data, _ := body["data"].(map[string]interface{})
	if data["page_size"] != float64(20) {
		t.Errorf("page_size = %v, want clamped-to-default 20", data["page_size"])
	}
}

func TestListReleases_NegativePageSize_DefaultsTo20(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	_, body := handlerDeployReleaseDo(f, "/api/v1/releases?page_size=-3")
	data, _ := body["data"].(map[string]interface{})
	if data["page_size"] != float64(20) {
		t.Errorf("page_size = %v, want default 20 for a negative input", data["page_size"])
	}
}

func TestListReleases_ReleaseTypeFilter(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", ReleaseType: "stable", IsPublished: true, PublishedAt: &now})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.1.0", ReleaseType: "beta", IsPublished: true, PublishedAt: &now})

	_, body := handlerDeployReleaseDo(f, "/api/v1/releases?product_id=p&release_type=beta")
	data, _ := body["data"].(map[string]interface{})
	releases, _ := data["releases"].([]interface{})
	if len(releases) != 1 || releases[0].(map[string]interface{})["release_type"] != "beta" {
		t.Fatalf("release_type filter did not isolate the beta release: %v", releases)
	}
}

// ─── GetLatestRelease ───────────────────────────────────────────────────────

func TestGetLatestRelease_NoReleases_404(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	w, body := handlerDeployReleaseDo(f, "/api/v1/releases/latest/nonexistent-product")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%v", w.Code, body)
	}
	if body["success"] != false {
		t.Errorf("success = %v, want false", body["success"])
	}
}

func TestGetLatestRelease_PicksMostRecentlyPublished(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, IsPrerelease: false, PublishedAt: &older})
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.1.0", IsPublished: true, IsPrerelease: false, PublishedAt: &newer})

	w, body := handlerDeployReleaseDo(f, "/api/v1/releases/latest/p")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%v", w.Code, body)
	}
	data, _ := body["data"].(map[string]interface{})
	rel, _ := data["release"].(map[string]interface{})
	if rel["version"] != "1.1.0" {
		t.Errorf("release.version = %v, want 1.1.0 (most recently published)", rel["version"])
	}
}

func TestGetLatestRelease_HasUpdateComparesAgainstCurrentVersion(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "2.0.0", IsPublished: true, PublishedAt: &now})

	w, body := handlerDeployReleaseDo(f, "/api/v1/releases/latest/p?current_version=1.0.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%v", w.Code, body)
	}
	data, _ := body["data"].(map[string]interface{})
	if data["has_update"] != true {
		t.Errorf("has_update = %v, want true (1.0.0 -> 2.0.0)", data["has_update"])
	}

	w2, body2 := handlerDeployReleaseDo(f, "/api/v1/releases/latest/p?current_version=3.0.0")
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d", w2.Code)
	}
	data2, _ := body2["data"].(map[string]interface{})
	if data2["has_update"] != false {
		t.Errorf("has_update = %v, want false (caller already ahead: 3.0.0 > 2.0.0)", data2["has_update"])
	}
}

// ─── GetReleaseByID ─────────────────────────────────────────────────────────

func TestGetReleaseByID_UnpublishedRelease_404(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "0.1.0", IsPublished: false})

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d", rel.Id))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (draft release must not be readable by id), body=%v", w.Code, body)
	}
}

func TestGetReleaseByID_PublishedRelease_200(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", Title: "GA", IsPublished: true, PublishedAt: &now})

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d", rel.Id))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%v", w.Code, body)
	}
	data, _ := body["data"].(map[string]interface{})
	if data["title"] != "GA" {
		t.Errorf("title = %v, want GA", data["title"])
	}
}

// ─── GetChangelog ───────────────────────────────────────────────────────────

func TestGetChangelog_Empty_404(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, PublishedAt: &now, ChangelogMd: ""})

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d/changelog", rel.Id))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an empty changelog, body=%v", w.Code, body)
	}
}

func TestGetChangelog_Present_ReturnsMarkdown(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, PublishedAt: &now, ChangelogMd: "# Notes\n- fixed things"})

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d/changelog", rel.Id))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%v", w.Code, body)
	}
	data, _ := body["data"].(map[string]interface{})
	if data["changelog_md"] != "# Notes\n- fixed things" {
		t.Errorf("changelog_md = %v, want the seeded markdown", data["changelog_md"])
	}
}

// ─── DownloadArtifact ───────────────────────────────────────────────────────

func TestDownloadArtifact_ArtifactBelongsToDifferentRelease_400(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	relA := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, PublishedAt: &now})
	relB := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.1.0", IsPublished: true, PublishedAt: &now})
	artifact := &entity.ReleaseArtifact{ReleaseId: relB.Id, Platform: "linux", Arch: "amd64", Filename: "f.tar.gz", StoragePath: "releases/f.tar.gz", ChecksumSha256: "abc"}
	if err := f.db.Create(artifact).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	// Address the artifact through relA's id (an IDOR-shaped mismatch), even
	// though the artifact actually belongs to relB.
	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d/download/%d", relA.Id, artifact.Id))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (artifact/release mismatch), body=%v", w.Code, body)
	}
	if body["error"] != "Artifact does not belong to this release" {
		t.Errorf("error = %v, want the mismatch message", body["error"])
	}
}

func TestDownloadArtifact_NonexistentArtifact_404(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	now := time.Now()
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, PublishedAt: &now})

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d/download/999999", rel.Id))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%v", w.Code, body)
	}
}

// TestDownloadArtifact_StorageNotConfigured_500 documents real current
// behaviour in this test tier: no MINIO_ENDPOINT is set, so
// ReleaseService.GenerateDownloadURL always errors — the redirect is never
// reached. This is a genuine (non-mocked) exercise of that failure path.
func TestDownloadArtifact_StorageNotConfigured_500(t *testing.T) {
	f := handlerDeployReleaseNewFixture(t)
	if releaseService.IsStorageConfigured() {
		t.Skip("MinIO is configured in this environment; the not-configured branch is not reachable")
	}
	now := time.Now()
	rel := handlerDeploySeedRelease(t, f, &entity.Release{ProductId: "p", Version: "1.0.0", IsPublished: true, PublishedAt: &now})
	artifact := &entity.ReleaseArtifact{ReleaseId: rel.Id, Platform: "linux", Arch: "amd64", Filename: "f.tar.gz", StoragePath: "releases/f.tar.gz", ChecksumSha256: "abc"}
	if err := f.db.Create(artifact).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	w, body := handlerDeployReleaseDo(f, fmt.Sprintf("/api/v1/releases/%d/download/%d", rel.Id, artifact.Id))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (object storage not configured), body=%v", w.Code, body)
	}
	if body["error"] != "Failed to generate download URL" {
		t.Errorf("error = %v, want the storage-not-configured message", body["error"])
	}
	handlerDeployReleaseAwaitDownloadRecorded(t, f, artifact.Id)
}

// handlerDeployReleaseAwaitDownloadRecorded blocks until DownloadArtifact's
// asynchronous bookkeeping has finished.
//
// release.go:180 fires `go releaseService.HandleDownload(...)`, and that in turn
// fires a second goroutine for the download log. Both close over the
// package-level `releaseService`, which this file's fixture swaps in and whose
// t.Cleanup restores to its pre-test nil. If the test returns while either
// goroutine is still pending, the restore wins the race and the goroutine
// dereferences a nil *ReleaseService — panicking the entire package. That made
// `go test ./internal/adapter/handler/` fail roughly half the time.
//
// Waiting on both goroutines' observable effects (the incremented counter and
// the persisted log row) removes the race without touching production code.
func handlerDeployReleaseAwaitDownloadRecorded(t *testing.T, f *handlerDeployReleaseFixture, artifactID int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var artifact entity.ReleaseArtifact
		var logs int64
		countErr := f.db.Model(&entity.DownloadLog{}).Where("artifact_id = ?", artifactID).Count(&logs).Error
		if err := f.db.First(&artifact, artifactID).Error; err == nil && countErr == nil &&
			artifact.DownloadCount > 0 && logs > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("artifact %d: download counter and/or log row never appeared within 5s; "+
		"the async HandleDownload goroutines would outlive the fixture and panic on the restored nil service", artifactID)
}
