package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// SetApiV2Router mounts the `/api/v2` middleware chain and delegates route registration
// to vertical domain mount functions so concurrent feature work avoids central router conflicts.
// Security invariants:
//   - CORS -> BrowserOriginGuard -> RequestBodySizeLimit -> OptionalZitaIdentity -> GlobalV2RateLimit -> Gzip
//   - Gzip excludes streaming CSV exports and pre-signed Switch teampack archives.
func SetApiV2Router(router *gin.Engine) {
	apiV2 := router.Group("/api/v2")
	apiV2.Use(middleware.CORS())
	apiV2.Use(middleware.BrowserOriginGuard())
	apiV2.Use(middleware.RequestBodySizeLimit())
	apiV2.Use(middleware.OptionalZitaIdentity())
	apiV2.Use(middleware.GlobalV2RateLimit())
	apiV2.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPathsRegexs([]string{
			`^/api/v2/(?:[^/]+/logs|admin/audit)/export$`,
			`^/api/v2/[^/]+/switch/teampack(?:\.sig)?$`,
		}),
	))

	mountV2AuthAndBridge(apiV2)
	mountV2TenantWorkspace(apiV2)
	mountV2TenantChannelsAndLogs(apiV2)
	mountV2TenantCatalogAndBilling(apiV2)
	mountV2TenantAccountAndChat(apiV2)
	mountV2SwitchAndPublic(apiV2)
	mountV2PlatformUserBilling(apiV2)
	mountAdminStaffFacade(apiV2)
	mountV2AdminRoot(apiV2)
	mountV2DelegatedAudit(apiV2)
	mountV2LutuSearch(apiV2)
}
