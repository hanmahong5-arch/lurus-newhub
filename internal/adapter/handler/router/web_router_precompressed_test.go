package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// mapServeFS adapts an fs.FS to what static.Serve wants. It mirrors
// common.EmbedFolder's embedFileSystem, including its one special case: "/"
// reports as missing so the request reaches NoRoute and is answered with the
// index bytes SetWebRouter was handed.
type mapServeFS struct{ http.FileSystem }

func (m mapServeFS) Exists(_ string, filepath string) bool {
	if filepath == "/" {
		return false
	}
	file, err := m.Open(filepath)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func (m mapServeFS) Open(name string) (http.File, error) {
	if name == "/" {
		return nil, os.ErrNotExist
	}
	return m.FileSystem.Open(name)
}

// The bodies are deliberately not real brotli/gzip streams: the assertion is
// byte identity with what is stored on disk, which is exactly the property that
// fails if anything in the chain re-compresses or re-encodes the response.
const (
	plainJS   = "console.log('cycle12 plain');"
	brotliJS  = "<<brotli bytes for x.js>>"
	gzipJS    = "<<gzip bytes for x.js>>"
	plainCSS  = ".cycle12{color:red}"
	gzipCSS   = "<<gzip bytes for y.css>>"
	uncompJS  = "console.log('too small to precompress');"
	indexHTML = "<!doctype html><title>spa</title>"
)

func newPrecompressedEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevEnable := common.GlobalWebRateLimitEnable
	prevRedis := common.RedisEnabled
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable = prevEnable
		common.RedisEnabled = prevRedis
	})
	// This file is about transport, not budgets; the limiter has its own test.
	common.GlobalWebRateLimitEnable = false
	common.RedisEnabled = false

	dist := fstest.MapFS{
		// A chunk with both precompressed siblings, as the build writes them
		// for anything over 10 KB.
		"assets/x.js":    {Data: []byte(plainJS)},
		"assets/x.js.br": {Data: []byte(brotliJS)},
		"assets/x.js.gz": {Data: []byte(gzipJS)},
		// A stylesheet with only the gzip sibling.
		"assets/y.css":    {Data: []byte(plainCSS)},
		"assets/y.css.gz": {Data: []byte(gzipCSS)},
		// A chunk under the build's compression threshold: no siblings at all.
		"assets/small.js": {Data: []byte(uncompJS)},
		"index.html":      {Data: []byte(indexHTML)},
	}

	engine := gin.New()
	setWebRoutes(engine, dist, mapServeFS{http.FS(dist)}, []byte(indexHTML))
	return engine
}

func getAsset(t *testing.T, engine *gin.Engine, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// vite-plugin-compression has been writing .br and .gz next to every chunk over
// 10 KB for as long as the plugin has been configured, and web/embed.go embeds
// all of dist — so 5.75 MB of compressed copies shipped inside the binary while
// static.Serve, which only ever looks up the exact request path, could not
// reach a single one of them. Every asset was re-compressed on the fly instead.
func TestPrecompressedAssets_ServesTheBuildOutput(t *testing.T) {
	engine := newPrecompressedEngine(t)

	t.Run("prefers brotli when the client offers both", func(t *testing.T) {
		w := getAsset(t, engine, "/assets/x.js", "br, gzip")
		if got := w.Header().Get("Content-Encoding"); got != "br" {
			t.Fatalf("Content-Encoding = %q, want br", got)
		}
		if got := w.Body.String(); got != brotliJS {
			t.Fatalf("body = %q, want the stored .br bytes %q", got, brotliJS)
		}
		if got := w.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Fatalf("Vary = %q, want Accept-Encoding: a cache keyed only on the URL would hand this body to a client that cannot decode it", got)
		}
		if got := w.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
			t.Fatalf("Content-Type = %q, want text/javascript; charset=utf-8", got)
		}
		if got := w.Header().Get("Cache-Control"); got != staticAssetCacheControl {
			t.Fatalf("Cache-Control = %q, want %q (the same value middleware.Cache gives every other asset)", got, staticAssetCacheControl)
		}
	})

	t.Run("falls back to gzip when brotli is not offered", func(t *testing.T) {
		w := getAsset(t, engine, "/assets/x.js", "gzip, deflate")
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		// Byte identity also proves the on-the-fly gzip middleware never ran on
		// top of this body: it would have produced gzip(gzip(...)) under the
		// same header.
		if got := w.Body.String(); got != gzipJS {
			t.Fatalf("body = %q, want the stored .gz bytes %q", got, gzipJS)
		}
	})

	t.Run("honours q=0 as a refusal", func(t *testing.T) {
		w := getAsset(t, engine, "/assets/x.js", "br;q=0, gzip")
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip: br;q=0 means the client cannot take brotli", got)
		}
	})

	t.Run("uses the only sibling the build produced", func(t *testing.T) {
		w := getAsset(t, engine, "/assets/y.css", "br, gzip")
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip: y.css has no .br sibling", got)
		}
		if got := w.Body.String(); got != gzipCSS {
			t.Fatalf("body = %q, want the stored .gz bytes %q", got, gzipCSS)
		}
		if got := w.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
			t.Fatalf("Content-Type = %q, want text/css; charset=utf-8", got)
		}
	})

	t.Run("serves the plain file when the client asks for no encoding", func(t *testing.T) {
		w := getAsset(t, engine, "/assets/x.js", "")
		if got := w.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Content-Encoding = %q, want none", got)
		}
		if got := w.Body.String(); got != plainJS {
			t.Fatalf("body = %q, want the uncompressed file %q", got, plainJS)
		}
	})

	t.Run("falls through for an asset with no precompressed sibling", func(t *testing.T) {
		// Still compressed, just on the fly: the gzip middleware behind this
		// one is untouched, which is why small chunks did not lose anything.
		w := getAsset(t, engine, "/assets/small.js", "br, gzip")
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip from the on-the-fly middleware", got)
		}
		if got := w.Body.String(); got == uncompJS {
			t.Fatal("body is the raw file: the on-the-fly gzip middleware no longer runs for assets that have no precompressed sibling")
		}
	})

	t.Run("ignores paths outside the hashed asset prefix", func(t *testing.T) {
		w := getAsset(t, engine, "/console/v2/dashboard", "br, gzip")
		if got := w.Header().Get("Content-Encoding"); got == "br" {
			t.Fatal("the SPA document was answered with a precompressed body")
		}
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 from the SPA fallback", w.Code)
		}
	})

	t.Run("answers HEAD with the same headers as GET", func(t *testing.T) {
		// `curl -sI ... -H 'Accept-Encoding: br'` is the deploy probe for this
		// feature, and curl -I sends HEAD. net/http drops the body itself.
		req := httptest.NewRequest(http.MethodHead, "/assets/x.js", nil)
		req.Header.Set("Accept-Encoding", "br")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if got := w.Header().Get("Content-Encoding"); got != "br" {
			t.Fatalf("Content-Encoding = %q on HEAD, want br", got)
		}
		if got := w.Header().Get("Content-Length"); got != "25" {
			t.Fatalf("Content-Length = %q on HEAD, want the size of the .br file (25)", got)
		}
	})

	t.Run("ignores a non-GET method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/assets/x.js", nil)
		req.Header.Set("Accept-Encoding", "br")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if got := w.Header().Get("Content-Encoding"); got == "br" {
			t.Fatal("a POST was answered from the precompressed store")
		}
	})

	t.Run("does not clean a dot-dot segment into a lookup", func(t *testing.T) {
		// path.Clean would turn this into assets/x.js and serve x.js.br with a
		// 200; fs.ValidPath refuses it, so it falls through to the 404 the rest
		// of the router already gives an unresolvable /assets/ path. Only brotli
		// is offered so that the on-the-fly gzip middleware stays out of the
		// response headers being read here.
		w := getAsset(t, engine, "/assets/../assets/x.js", "br")
		if got := w.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Content-Encoding = %q: a path with .. was resolved by the precompressed lookup", got)
		}
		if w.Code == http.StatusOK {
			t.Fatalf("status = 200 for a path with ..; want the 404 an unresolvable asset path gets")
		}
	})
}
