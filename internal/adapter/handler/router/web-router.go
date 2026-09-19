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
// name — which is what lets hashedAssetTransport use the file name as a
// validator and serve a precompressed sibling under it. It does NOT scope the
// rate limiter: that is bound to the SPA document instead (see setWebRoutes),
// so dist-root files like /logo.png are outside the budget too.
const assetURLPrefix = "/assets/"

// staticAssetCacheControl repeats what middleware.Cache() puts on every path
// other than "/" — one week. hashedAssetTransport answers before that middleware
// runs (so that the on-the-fly compressor never sees an already-compressed
// body), so it has to set the header itself; the value is deliberately the same
// one, not a new policy.
const staticAssetCacheControl = "max-age=604800"

// precompressedEncodings pairs an Accept-Encoding token with the sibling file
// vite-plugin-compression writes for it (web/vite.config.js: brotliCompress ->
// .br, gzip -> .gz, both above a 10 KB threshold). Ordered best-first: brotli
// is roughly 30% smaller than gzip on these chunks (measured on the pre-change
// 2026-09-19 build, where the entry chunk was 6,571,292 B raw / 1,027,972 B .br
// / 1,485,039 B .gz).
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

// etagFor builds the response validator for one stored representation of a
// hashed asset. The file name IS the validator: Vite writes a content hash into
// it, so two builds whose bytes differ cannot share a name. Without one these
// responses have no validator at all — embed.FS reports a zero ModTime, so
// http.ServeContent emits no Last-Modified either, and a reload re-downloads
// the whole build as 200s.
//
// Weak (W/) because the tag is per representation by construction: the plain,
// .br and .gz copies of one asset each carry their own file name here, and a
// weak tag is the honest strength for a validator that was not computed over
// the bytes being sent.
func etagFor(fileName string) string {
	return `W/"` + fileName + `"`
}

// ifNoneMatch reports whether the client already holds this representation.
// RFC 9110 §13.1.2 specifies the WEAK comparison function for If-None-Match, so
// the W/ prefix is stripped from both sides before the opaque tags are
// compared, and "*" matches anything that exists.
func ifNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == want {
			return true
		}
	}
	return false
}

// notModified answers a conditional request for a representation the client
// already has. It sets the headers RFC 9110 §15.4.5 asks a 304 to carry for a
// cache to refresh its entry, and aborts — which also means middleware.Cache()
// downstream never runs, hence the explicit Cache-Control.
func notModified(c *gin.Context, etag string) {
	c.Header("ETag", etag)
	c.Header("Vary", "Accept-Encoding")
	c.Header("Cache-Control", staticAssetCacheControl)
	c.Status(http.StatusNotModified)
	c.Abort()
}

// serveEncoded writes the precompressed sibling if it exists, and reports
// whether it did. A conditional request that already holds this exact
// representation is answered 304 here rather than re-sending it, which also
// counts as "did".
func serveEncoded(c *gin.Context, distFS fs.FS, name, contentType, token, suffix string) bool {
	encodedName := name + suffix
	file, err := distFS.Open(encodedName)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	etag := etagFor(path.Base(encodedName))
	if ifNoneMatch(c.GetHeader("If-None-Match"), etag) {
		notModified(c, etag)
		return true
	}
	c.Header("Content-Encoding", token)
	c.Header("Vary", "Accept-Encoding")
	c.Header("Cache-Control", staticAssetCacheControl)
	c.Header("ETag", etag)
	c.DataFromReader(http.StatusOK, info.Size(), contentType, file, nil)
	c.Abort()
	return true
}

// hashedAssetTransport handles the two things a content-addressed asset needs
// that the generic static chain does not give it.
//
// (1) It serves the .br / .gz copies the frontend build already produced,
// instead of re-compressing the same immutable bytes on every request. Those
// copies are embedded in the binary (web/embed.go embeds all of dist) and,
// before this middleware existed, no client ever requested those paths — they
// were shipped inside every image and never used, while static.Serve answered
// the plain name and the gzip middleware compressed it again per request. (Size
// of the dead weight, for scale only: 5.75 MB in the pre-change build recorded
// in the cycle-12 plan, 5,334,015 B over 126 files in the build measured while
// this was written. Both move with every build; neither is a contract.)
//
// It is mounted ahead of the on-the-fly gzip middleware on purpose. A body that
// is already brotli must not be handed to a gzip writer — the client would be
// told "br" and given gzip(brotli) — and aborting here is what keeps that from
// being possible. An asset with no precompressed sibling (the build only writes
// them above 10 KB) simply falls through and gets compressed on the fly exactly
// as before.
//
// (2) It attaches a validator. embed.FS reports a zero ModTime, so
// http.ServeContent sends neither ETag nor Last-Modified for these files: a
// reload re-downloads the entire build as 200s no matter how long the
// Cache-Control lives. The file name is already a content hash, so it is the
// validator (etagFor), and a conditional request for a representation the
// client holds is answered 304.
func hashedAssetTransport(distFS fs.FS) gin.HandlerFunc {
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
		name := strings.TrimPrefix(urlPath, "/")
		// Rejects "..", "//" and the like before they reach the FS. embed.FS
		// would reject them too; this keeps the decision visible here.
		if !fs.ValidPath(name) {
			return
		}
		// The content-type table gates only the precompressed branch, which has
		// to name a type because it writes the body itself. The validator below
		// applies to every hashed asset, including the extensions Vite emits
		// that are not worth pre-compressing (images, for one).
		if contentType, known := precompressedContentTypes[strings.ToLower(path.Ext(urlPath))]; known {
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
		// No precompressed copy was used, so the plain file is what the rest of
		// the chain will answer with — on-the-fly gzip included. Only the
		// validator is attached here; the body is deliberately left to
		// static.Serve so that a chunk under the build's 10 KB compression
		// threshold keeps the compression it has always had.
		//
		// Matching against the representation that WOULD be selected, not
		// against any stored copy: a client holding the plain bytes and asking
		// for brotli is answered 200 with the brotli representation and its own
		// tag, never 304 for a representation it does not have.
		plain, err := distFS.Open(name)
		if err != nil {
			return
		}
		info, err := plain.Stat()
		_ = plain.Close()
		if err != nil || info.IsDir() {
			return
		}
		etag := etagFor(path.Base(name))
		c.Header("ETag", etag)
		c.Header("Vary", "Accept-Encoding")
		if ifNoneMatch(c.GetHeader("If-None-Match"), etag) {
			notModified(c, etag)
		}
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
	router.Use(hashedAssetTransport(distFS))
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(middleware.Cache())
	router.Use(static.Serve("/", staticFS))

	// GlobalWebRateLimit budgets REQUESTS PER CLIENT IP, and a first paint is
	// not one request: index.html pulls the entry chunk, its modulepreloads,
	// the stylesheets, the favicon, and every lazily routed page adds another
	// hashed chunk. While the limiter sat on the whole engine, all of those
	// counted against the same 60-per-180s budget as the document, so one
	// visitor on a cold cache could 429 their own page into a white screen —
	// which is what happened on the UAT instance on 2026-08-30 and was worked
	// around there by raising the number to 600 rather than by scoping it. A
	// whole office behind one NAT egress IP is the same arithmetic, multiplied.
	//
	// So it is bound HERE, to the one response this router generates rather
	// than reads out of the build output: the SPA HTML document. Everything
	// static.Serve can answer — /assets/* and the dist-root files such as
	// /logo.png, /favicon.ico — is served before NoRoute is ever reached and is
	// therefore outside the budget, which is the whole point: a cold first
	// paint now costs exactly one token. The 404 branch below is also outside
	// it; a missing asset is answered by handler.RelayNotFound, which is
	// cheaper than the document and is not what the budget is protecting.
	//
	// The budget itself is unchanged, and both the value and the per-IP keying
	// are still GlobalWebRateLimit's (this captures it at mount time, exactly
	// as router.Use did).
	spaDocumentRateLimit := middleware.GlobalWebRateLimit()

	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
			handler.RelayNotFound(c)
			return
		}
		// Set BEFORE the limiter, not after. middleware.Cache() has already run
		// by the time NoRoute is reached and has put max-age=604800 on this
		// response — so a 429 written below would go out with a week of
		// explicit freshness, which is exactly what makes a response cacheable
		// regardless of its status code (RFC 9111 §3). A shared cache could then
		// answer a whole NAT-ed office with a stored 429 for a week. While the
		// limiter was an engine-level middleware it aborted before Cache() ran
		// and the question did not arise.
		c.Header("Cache-Control", "no-cache")
		// The limiters in middleware/rate-limit.go signal refusal by writing
		// the 429 and calling c.Abort(); on the allowed path they simply
		// return (or call c.Next(), which is a no-op from the last handler).
		spaDocumentRateLimit(c)
		if c.IsAborted() {
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
	})
}
