package router

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

var classicConsolePages = map[string]string{
	"/console/v2/dashboard":               "/dashboard",
	"/console/v2/token":                   "/keys",
	"/console/v2/log":                     "/usage-logs",
	"/console/v2/tasks":                   "/tasks",
	"/console/v2/playground":              "/playground",
	"/console/v2/chat":                    "/chat",
	"/console/v2/billing":                 "/wallet",
	"/console/v2/settings":                "/account",
	"/console/v2/models":                  "/models",
	"/console/v2/channel":                 "/ops/channels",
	"/console/v2/flows":                   "/org/flows",
	"/console/v2/pricing":                 "/org/pricing",
	"/console/v2/redemption":              "/admin/redemption-codes",
	"/console/v2/projects":                "/org/departments",
	"/console/v2/tenants":                 "/admin/tenants",
	"/console/v2/admin/users":             "/admin/users",
	"/console/v2/admin/settings":          "/admin/settings",
	"/console/v2/admin/rankings":          "/rankings",
	"/console/v2/admin/audit":             "/usage-logs/audit",
	"/console/v2/admin/model-limits":      "/admin/model-limits",
	"/console/v2/admin/authz":             "/admin/authz",
	"/console/v2/admin/model-pools":       "/admin/model-pools",
	"/console/v2/admin/gateway":           "/ops/overview",
	"/console/v2/admin/cost-intelligence": "/admin/cost-intelligence",
	"/console/v2/admin/model-performance": "/admin/model-performance",
	"/console/v2/admin/system-tasks":      "/admin/system-tasks",
	"/console/v2/admin/diagnostics":       "/admin/diagnostics",
	"/console/v2/account-disabled":        "/account-disabled",
	"/console/setting":                    "/admin/settings",
	"/console/personal":                   "/account",
	"/console/user":                       "/admin/users",
	"/console/openrouter-sync":            "/admin/openrouter-sync",
	"/console/token":                      "/keys",
	"/console/log":                        "/usage-logs",
	"/console/topup":                      "/wallet",
	"/console/redemption":                 "/admin/redemption-codes",
	"/console/channel":                    "/ops/channels",
	"/console/models":                     "/models",
	"/console/playground":                 "/playground",
	"/console/chat":                       "/chat",
	"/console/task":                       "/tasks",
	"/console/midjourney":                 "/tasks",
}

func isNextPath(urlPath string) bool {
	return urlPath == nextMountPrefix || strings.HasPrefix(urlPath, nextMountPrefix+"/")
}

func isClassicConsolePath(urlPath string) bool {
	return urlPath == "/console" || strings.HasPrefix(urlPath, "/console/")
}

func classicConsoleTarget(urlPath string) string {
	for p := strings.TrimSuffix(urlPath, "/"); strings.HasPrefix(p, "/console/"); p = path.Dir(p) {
		if target, ok := classicConsolePages[p]; ok {
			return target
		}
	}
	return "/dashboard"
}

func nextBookmarkTarget(urlPath, rawQuery string) string {
	target := strings.TrimPrefix(urlPath, nextMountPrefix)
	if target == "" {
		target = "/"
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target
}

// SetWebRouter mounts precompressed hashed assets, static serving, legacy `/console` and `/next`
// bookmark redirects, and the per-document rate-limited SPA fallback.
func SetWebRouter(router *gin.Engine, buildFS embed.FS, indexPage []byte) {
	distFS, _ := fs.Sub(buildFS, "dist")
	setWebRoutes(router, distFS, common.EmbedFolder(buildFS, "dist"), indexPage)
}

func setWebRoutes(router *gin.Engine, distFS fs.FS, staticFS static.ServeFileSystem, indexPage []byte) {
	router.GET("/desk-device", handler.DeskDevicePage)

	router.Use(hashedAssetTransport(distFS))
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(middleware.Cache())
	router.Use(static.Serve("/", staticFS))

	spaDocumentRateLimit := middleware.GlobalWebRateLimit()

	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") ||
			strings.HasPrefix(c.Request.RequestURI, "/assets") || strings.HasPrefix(c.Request.URL.Path, nextAssetURLPrefix) {
			handler.RelayNotFound(c)
			return
		}
		c.Header("Cache-Control", "no-cache")
		spaDocumentRateLimit(c)
		if c.IsAborted() {
			return
		}
		urlPath := c.Request.URL.Path
		if isNextPath(urlPath) {
			c.Redirect(http.StatusFound, nextBookmarkTarget(urlPath, c.Request.URL.RawQuery))
			return
		}
		if isClassicConsolePath(urlPath) {
			c.Redirect(http.StatusFound, classicConsoleTarget(urlPath))
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
	})
}
