package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

var fixRelDLCounter atomic.Int64

// fixRelDLOpenDB opens a private in-memory sqlite database with the release
// tables migrated. Writers are serialized onto a single connection because the
// download bookkeeping runs detached from the request goroutine.
func fixRelDLOpenDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:fixreldl%d?mode=memory&cache=shared", fixRelDLCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	for _, tbl := range []interface{}{&entity.Release{}, &entity.ReleaseArtifact{}, &entity.DownloadLog{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// fixRelDLBindService points repo.DB and the package-level releaseService at db.
func fixRelDLBindService(t *testing.T, db *gorm.DB) {
	t.Helper()
	prevDB := repo.DB
	prevSvc := releaseService
	repo.DB = db
	InitReleaseService()
	t.Cleanup(func() {
		releaseService = prevSvc
		repo.DB = prevDB
	})
}

// fixRelDLSeedArtifact seeds one published release with one artifact.
func fixRelDLSeedArtifact(t *testing.T, db *gorm.DB) *entity.ReleaseArtifact {
	t.Helper()
	now := time.Now()
	rel := &entity.Release{
		ProductId: "fix-rel-dl", Version: "1.0.0", Title: "R", ChangelogMd: "# c",
		ReleaseType: "stable", IsPublished: true, CreatedAt: now, UpdatedAt: now, PublishedAt: &now,
	}
	if err := db.Create(rel).Error; err != nil {
		t.Fatalf("seed release: %v", err)
	}
	art := &entity.ReleaseArtifact{
		ReleaseId: rel.Id, Platform: "linux", Arch: "amd64",
		Filename: "f.bin", FileSize: 10, StoragePath: "p/f.bin", ChecksumSha256: "abc",
	}
	if err := db.Create(art).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	return art
}

// fixRelDLNewCtx builds a gin context for DownloadArtifact whose request carries
// reqCtx, the way net/http hands one to a handler.
func fixRelDLNewCtx(art *entity.ReleaseArtifact, reqCtx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/releases/download", nil).WithContext(reqCtx)
	c.Params = gin.Params{
		{Key: "id", Value: strconv.FormatInt(art.ReleaseId, 10)},
		{Key: "artifact_id", Value: strconv.FormatInt(art.Id, 10)},
	}
	return c, w
}

// fixRelDLWaitCount polls the artifact's download_count until it reaches want or
// the deadline expires, returning the last value read.
func fixRelDLWaitCount(t *testing.T, db *gorm.DB, artifactId int64, want int64, timeout time.Duration) int64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var got entity.ReleaseArtifact
		if err := db.First(&got, artifactId).Error; err != nil {
			t.Fatalf("reload artifact: %v", err)
		}
		if got.DownloadCount >= want || time.Now().After(deadline) {
			return got.DownloadCount
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fixRelDLAssertReached fails when the handler bailed out before reaching the
// download bookkeeping. Object storage is not configured in tests, so the
// terminal step is a 500 from the presign attempt rather than the 302 redirect.
func fixRelDLAssertReached(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code == http.StatusBadRequest || w.Code == http.StatusNotFound {
		t.Fatalf("handler rejected the request before the download bookkeeping: %d %s", w.Code, w.Body.String())
	}
}

// TestFixRelDL_CountSurvivesRequestContextCancel locks in that the download
// bookkeeping runs on a detached context. net/http cancels the request context
// as soon as the handler returns, so bookkeeping bound to it is dropped and the
// download counter silently under-counts.
func TestFixRelDL_CountSurvivesRequestContextCancel(t *testing.T) {
	db := fixRelDLOpenDB(t)
	fixRelDLBindService(t, db)
	art := fixRelDLSeedArtifact(t, db)

	const runs = 3
	for i := 1; i <= runs; i++ {
		reqCtx, cancel := context.WithCancel(context.Background())
		c, w := fixRelDLNewCtx(art, reqCtx)

		DownloadArtifact(c)
		// net/http cancels the request context the moment the handler returns.
		cancel()

		fixRelDLAssertReached(t, w)
		if got := fixRelDLWaitCount(t, db, art.Id, int64(i), 3*time.Second); got != int64(i) {
			t.Fatalf("after %d downloads download_count = %d, want %d", i, got, i)
		}
	}

	// Let the detached download-log goroutine finish before teardown.
	time.Sleep(100 * time.Millisecond)
}

// TestFixRelDL_ServiceResolvedBeforeDetaching locks in that the handler resolves
// the release service before detaching. Reading the package-level service from
// the detached goroutine makes the bookkeeping land wherever that global points
// by the time it runs — and a nil global there takes the process down.
func TestFixRelDL_ServiceResolvedBeforeDetaching(t *testing.T) {
	db := fixRelDLOpenDB(t)
	fixRelDLBindService(t, db)
	art := fixRelDLSeedArtifact(t, db)

	scratch := fixRelDLOpenDB(t)

	c, w := fixRelDLNewCtx(art, context.Background())
	DownloadArtifact(c)
	// Repoint the global the way a caller swapping the service does right after
	// the handler returns; the already-detached bookkeeping must not see it.
	releaseService = app.NewReleaseService(repo.NewReleaseRepository(scratch))

	fixRelDLAssertReached(t, w)
	if got := fixRelDLWaitCount(t, db, art.Id, 1, 3*time.Second); got != 1 {
		t.Fatalf("download_count = %d, want 1", got)
	}

	// Let the detached download-log goroutine finish before teardown.
	time.Sleep(100 * time.Millisecond)
}
