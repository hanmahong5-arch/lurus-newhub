package router

// v2_gzip_mount_test.go — cycle-12 L2/W. Proves two things about the gzip
// middleware SetApiV2Router mounts, on the real route table rather than a
// hand-built engine:
//
//  1. an ordinary /api/v2 JSON response IS compressed when the client offers
//     gzip (that is the first-paint/bandwidth win the mount exists for), and
//  2. the three CSV exports are NOT, because gin-contrib/gzip buffers the
//     whole response before writing it — a streamed 50k-row export would be
//     held in memory and delivered as one block, which is exactly what the
//     streaming writer in those handlers was built to avoid.
//
// Mutation: delete the gzip.WithExcludedPathsRegexs option and the three
// export subtests go red; delete the whole apiV2.Use(gzip.Gzip(...)) and
// json_response_is_compressed goes red.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func gzipMountGet(t *testing.T, engine *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestApiV2Gzip_MountedButExcludesStreamingExports(t *testing.T) {
	engine := mountRealRouterWithSession(t, map[string]interface{}{
		"username": "gzip-mount-actor",
		"role":     common.RoleRootUser,
		"id":       1,
		"status":   common.UserStatusEnabled,
	})

	t.Run("json_response_is_compressed", func(t *testing.T) {
		// A registered, reachable /api/v2 GET (api-v2-router.go's
		// /relays/recommended). What matters here is the Content-Encoding the
		// middleware chain applies, not the handler's own status.
		w := gzipMountGet(t, engine, "/api/v2/relays/recommended")
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want \"gzip\" — the console's JSON is not being compressed; status=%d body=%s",
				got, w.Code, firstBytes(w.Body.String()))
		}
	})

	// The three CSV exports, by the path shape the exclusion regex has to
	// cover. "admin" matches the [^/]+ tenant-slug alternative too, which is
	// why one regex is enough — and why an over-broad one would silently stop
	// compressing ordinary tenant JSON.
	for _, tc := range []struct {
		name string
		path string
	}{
		{"tenant_logs_export", "/api/v2/demo-tenant/logs/export"},
		{"admin_logs_export", "/api/v2/admin/logs/export"},
		{"admin_audit_export", "/api/v2/admin/audit/export"},
	} {
		t.Run(tc.name+"_not_compressed", func(t *testing.T) {
			w := gzipMountGet(t, engine, tc.path)
			if got := w.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("%s: Content-Encoding = %q, want none — gin-contrib/gzip buffers the whole response, so compressing this cancels the streaming writer these handlers use for a 50k-row export; status=%d",
					tc.path, got, w.Code)
			}
		})
	}
}

func firstBytes(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + strings.Repeat(".", 3)
}
