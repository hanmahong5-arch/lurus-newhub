package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

const (
	legacyIndexHTML = "<!doctype html><title>legacy console</title>"
	nextIndexHTML   = "<!doctype html><title>next console</title>"
)

// newConsoleEngine mounts the real web chain over a synthetic dist. The Go CI
// jobs stub web/dist down to a lone index.html, so a /next build has to be
// supplied here.
func newConsoleEngine(t *testing.T, dist fstest.MapFS) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevEnable := common.GlobalWebRateLimitEnable
	prevRedis := common.RedisEnabled
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable = prevEnable
		common.RedisEnabled = prevRedis
	})
	common.GlobalWebRateLimitEnable = false
	common.RedisEnabled = false

	engine := gin.New()
	setWebRoutes(engine, dist, mapServeFS{http.FS(dist)}, []byte(legacyIndexHTML))
	return engine
}

func nextDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                        {Data: []byte(legacyIndexHTML)},
		"assets/legacy-DEADBEEF.js":         {Data: []byte("console.log('legacy');")},
		"next/index.html":                   {Data: []byte(nextIndexHTML)},
		"next/logo.png":                     {Data: []byte("\x89PNG not really a png")},
		"next/static/js/index.abc123.js":    {Data: []byte("console.log('next');")},
		"next/static/js/index.abc123.js.br": {Data: []byte("<<brotli bytes for next>>")},
	}
}

func getPath(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestWebRouter_NextConsoleRoutesGetTheNextDocument(t *testing.T) {
	engine := newConsoleEngine(t, nextDist())

	for _, path := range []string{"/next", "/next/", "/next/index.html", "/next/keys", "/next/usage-logs?p=2", "/next/models/deep/link"} {
		w := getPath(engine, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if got := w.Body.String(); got != nextIndexHTML {
			t.Fatalf("GET %s served %q, want the new console document", path, got)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache: the document must revalidate so a deploy takes effect", path, got)
		}
	}
}

func TestWebRouter_LegacyConsoleKeepsTheLegacyDocument(t *testing.T) {
	engine := newConsoleEngine(t, nextDist())

	// "/nextfoo" and "/console/next" share the substring but not the mount.
	for _, path := range []string{"/", "/console", "/console/v2/dashboard", "/login", "/nextfoo", "/console/next/keys"} {
		w := getPath(engine, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if got := w.Body.String(); got != legacyIndexHTML {
			t.Fatalf("GET %s served %q, want the legacy document", path, got)
		}
	}
}

func TestWebRouter_NextFallsBackToLegacyWithoutANextBuild(t *testing.T) {
	// CI stubs dist to a single index.html; /next/* must not 404 or panic.
	engine := newConsoleEngine(t, fstest.MapFS{
		"index.html": {Data: []byte(legacyIndexHTML)},
	})

	for _, path := range []string{"/next", "/next/", "/next/keys"} {
		w := getPath(engine, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if got := w.Body.String(); got != legacyIndexHTML {
			t.Fatalf("GET %s served %q, want the legacy document", path, got)
		}
	}
}

func TestWebRouter_NextStaticAssetsAreServedAndMissesAre404(t *testing.T) {
	engine := newConsoleEngine(t, nextDist())

	t.Run("a built chunk is served as itself", func(t *testing.T) {
		w := getPath(engine, "/next/static/js/index.abc123.js")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if got := w.Body.String(); got != "console.log('next');" {
			t.Fatalf("body = %q, want the chunk, not an HTML document", got)
		}
		if got := w.Header().Get("ETag"); got == "" {
			t.Fatal("no ETag: a hashed /next/static chunk must carry a validator like /assets does")
		}
	})

	t.Run("the precompressed sibling is preferred when offered", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/next/static/js/index.abc123.js", nil)
		req.Header.Set("Accept-Encoding", "br")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if got := w.Header().Get("Content-Encoding"); got != "br" {
			t.Fatalf("Content-Encoding = %q, want br", got)
		}
		if got := w.Body.String(); got != "<<brotli bytes for next>>" {
			t.Fatalf("body = %q, want the stored .br bytes", got)
		}
	})

	t.Run("a missing chunk is a 404, never the SPA document", func(t *testing.T) {
		w := getPath(engine, "/next/static/js/gone.deadbeef.js")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		if strings.Contains(w.Body.String(), "<title>") {
			t.Fatalf("body = %q: a miss under /next/static/ must not be answered with an HTML document (the browser would parse it as script)", w.Body.String())
		}
	})

	t.Run("dist-root files of the next build are served", func(t *testing.T) {
		w := getPath(engine, "/next/logo.png")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "\x89PNG") {
			t.Fatalf("GET /next/logo.png = %d %q, want the file", w.Code, w.Body.String())
		}
	})
}

func TestWebRouter_NextDocumentIsBehindTheSameRateLimitAsTheLegacyOne(t *testing.T) {
	budgetOfTwo(t)
	dist := nextDist()
	engine := gin.New()
	setWebRoutes(engine, dist, mapServeFS{http.FS(dist)}, []byte(legacyIndexHTML))

	const clientIP = "198.51.100.77:40000"

	// Static chunks do not spend budget...
	for i := 0; i < 5; i++ {
		if w := getFrom(engine, clientIP, "/next/static/js/index.abc123.js"); w.Code != http.StatusOK {
			t.Fatalf("chunk request %d = %d, want 200", i+1, w.Code)
		}
	}
	// ...the document does.
	for i := 1; i <= 2; i++ {
		if w := getFrom(engine, clientIP, "/next/keys"); w.Code != http.StatusOK {
			t.Fatalf("document request %d = %d, want 200", i, w.Code)
		}
	}
	if w := getFrom(engine, clientIP, "/next/keys"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("third document request = %d, want 429: /next must not be a way around the SPA document budget", w.Code)
	}
}
