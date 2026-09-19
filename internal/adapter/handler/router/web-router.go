package router

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// assetURLPrefix is where Vite writes its hashed, content-addressed build
// output (web/vite.config.js). Everything under it is immutable: the hash
// changes when the bytes change, so a stale copy is never served under an old
// name. Both of this file's departures from "one chain for the whole SPA" are
// scoped to this prefix.
const assetURLPrefix = "/assets/"

// staticAssetCacheControl repeats what middleware.Cache() puts on every path
// other than "/" — one week. precompressedAssets answers before that middleware
// runs (so that the on-the-fly compressor never sees an already-compressed
// body), so it has to set the header itself; the value is deliberately the same
// one, not a new policy.
const staticAssetCacheControl = "max-age=604800"

// precompressedEncodings pairs an Accept-Encoding token with the sibling file
// vite-plugin-compression writes for it (web/vite.config.js: brotliCompress ->
// .br, gzip -> .gz, both above a 10 KB threshold). Ordered best-first: brotli
// is roughly 30% smaller than gzip on these chunks (measured on the 2026-09-19
// build: the entry chunk is 6,571,292 B raw, 1,027,972 B .br, 1,485,039 B .gz).
var precompressedEncodings = []struct {
	token  string
	suffix string
}{
	{token: "br", suffix: ".br"},
	{token: "gzip", suffix: ".gz"},
}

// precompressedContentTypes is an explicit table rather than
// mime.TypeByExtension because that function consults the Windows registry and
// the local /etc/mime.types, so the same request answers with a different
// Content-Type on a developer machine than in the container. The keys cover
// every extension the frontend build emits: `ls web/dist/assets | sed
// 's/.*\.//' | sort | uniq -c` on the 2026-09-19 build reports js, css, ttf,
// woff and woff2 (plus the .br/.gz siblings); svg, json, map and wasm are here
// because Vite emits them for other asset kinds. An extension that is not in
// this table is not served from here at all — it falls through to static.Serve,
// which answers uncompressed with its own content sniffing.
var precompressedContentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".wasm":  "application/wasm",
	".ttf":   "font/ttf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// acceptsEncoding reports whether the Accept-Encoding header offers token with
// a usable quality value. RFC 9110 gives "q=0" the meaning "not acceptable",
// which is how a client asks for one encoding while refusing another.
func acceptsEncoding(header, token string) bool {
	for _, candidate := range strings.Split(header, ",") {
		fields := strings.Split(candidate, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), token) {
			continue
		}
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if !strings.HasPrefix(strings.ToLower(param), "q=") {
				continue
			}
			q, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
			if err == nil && q <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

// serveEncoded writes the precompressed sibling if it exists, and reports
// whether it did.
func serveEncoded(c *gin.Context, distFS fs.FS, name, contentType, token, suffix string) bool {
	file, err := distFS.Open(name + suffix)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	c.Header("Content-Encoding", token)
	c.Header("Vary", "Accept-Encoding")
	c.Header("Cache-Control", staticAssetCacheControl)
	c.DataFromReader(http.StatusOK, info.Size(), contentType, file, nil)
	c.Abort()
	return true
}

// precompressedAssets serves the .br / .gz copies the frontend build already
// produced, instead of re-compressing the same immutable bytes on every
// request. Those copies are embedded in the binary (web/embed.go embeds all of
// dist) and, until this middleware existed, were carried around and never
// served: 5.75 MB of the binary that nothing could reach.
//
// It is mounted ahead of the on-the-fly gzip middleware on purpose. A body that
// is already brotli must not be handed to a gzip writer — the client would be
// told "br" and given gzip(brotli) — and aborting here is what keeps that from
// being possible. An asset with no precompressed sibling (the build only writes
// them above 10 KB) simply falls through and gets compressed on the fly exactly
// as before.
func precompressedAssets(distFS fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		if distFS == nil {
			return
		}
		// HEAD as well as GET: a HEAD response has to carry the same headers a
		// GET would, and `curl -I` is how the deploy probe checks that the
		// precompressed copy is really being served. net/http drops the body
		// for a HEAD request on its own.
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			return
		}
		urlPath := c.Request.URL.Path
		if !strings.HasPrefix(urlPath, assetURLPrefix) {
			return
		}
		contentType, known := precompressedContentTypes[strings.ToLower(path.Ext(urlPath))]
		if !known {
			return
		}
		name := strings.TrimPrefix(urlPath, "/")
		// Rejects "..", "//" and the like before they reach the FS. embed.FS
		// would reject them too; this keeps the decision visible here.
		if !fs.ValidPath(name) {
			return
		}
		accept := c.GetHeader("Accept-Encoding")
		for _, encoding := range precompressedEncodings {
			if !acceptsEncoding(accept, encoding.token) {
				continue
			}
			if serveEncoded(c, distFS, name, contentType, encoding.token, encoding.suffix) {
				return
			}
		}
	}
}

// webRateLimitOutsideAssets applies GlobalWebRateLimit to everything the SPA
// router serves EXCEPT the hashed asset files.
//
// The budget is per client IP, and a first paint is not one request: index.html
// pulls the entry chunk, its modulepreloads and the stylesheets, and every
// lazily routed page adds another hashed chunk. Counting those against the same
// budget as the HTML document means one visitor on a cold cache can 429 their
// own page into a white screen — which is what happened on the UAT instance on
// 2026-08-30, worked around there by raising the limit to 600 rather than by
// scoping it. The documents, and every other path this router answers, keep the
// limiter unchanged.
func webRateLimitOutsideAssets() gin.HandlerFunc {
	limit := middleware.GlobalWebRateLimit()
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, assetURLPrefix) {
			// Returning without c.Next() continues the chain: gin's Next loop
			// advances past a handler that returns on its own. That is how the
			// limiters in middleware/rate-limit.go pass a request too.
			return
		}
		limit(c)
	}
}

func SetWebRouter(router *gin.Engine, buildFS embed.FS, indexPage []byte) {
	// fs.Sub with a constant, valid path cannot fail; common.EmbedFolder makes
	// the same call below and panics if it ever does.
	distFS, _ := fs.Sub(buildFS, "dist")
	setWebRoutes(router, distFS, common.EmbedFolder(buildFS, "dist"), indexPage)
}

// setWebRoutes is SetWebRouter with the embedded filesystem already opened, so
// that the mounted chain — not a copy of it — can be exercised against a
// synthetic dist (web_router_precompressed_test.go). The Go CI jobs stub
// web/dist down to a single index.html, so a test that needs real hashed
// assets cannot use web.BuildFS.
func setWebRoutes(router *gin.Engine, distFS fs.FS, staticFS static.ServeFileSystem, indexPage []byte) {
	router.Use(precompressedAssets(distFS))
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(webRateLimitOutsideAssets())
	router.Use(middleware.Cache())
	router.Use(static.Serve("/", staticFS))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
			handler.RelayNotFound(c)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
	})
}
