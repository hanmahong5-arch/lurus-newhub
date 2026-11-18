package router

import (
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	assetURLPrefix          = "/assets/"
	nextMountPrefix         = "/next"
	nextAssetURLPrefix      = nextMountPrefix + "/static/"
	staticAssetCacheControl = "max-age=604800"
)

var precompressedEncodings = []struct {
	token  string
	suffix string
}{
	{token: "br", suffix: ".br"},
	{token: "gzip", suffix: ".gz"},
}

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

func isHashedAssetPath(urlPath string) bool {
	return strings.HasPrefix(urlPath, assetURLPrefix) || strings.HasPrefix(urlPath, nextAssetURLPrefix)
}

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

func etagFor(fileName string) string {
	return `W/"` + fileName + `"`
}

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

func notModified(c *gin.Context, etag string) {
	c.Header("ETag", etag)
	c.Header("Vary", "Accept-Encoding")
	c.Header("Cache-Control", staticAssetCacheControl)
	c.Status(http.StatusNotModified)
	c.Abort()
}

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

func hashedAssetTransport(distFS fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		if distFS == nil || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			return
		}
		urlPath := c.Request.URL.Path
		if !isHashedAssetPath(urlPath) {
			return
		}
		name := strings.TrimPrefix(urlPath, "/")
		if !fs.ValidPath(name) {
			return
		}
		if contentType, known := precompressedContentTypes[strings.ToLower(path.Ext(urlPath))]; known {
			accept := c.GetHeader("Accept-Encoding")
			for _, encoding := range precompressedEncodings {
				if acceptsEncoding(accept, encoding.token) && serveEncoded(c, distFS, name, contentType, encoding.token, encoding.suffix) {
					return
				}
			}
		}
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
