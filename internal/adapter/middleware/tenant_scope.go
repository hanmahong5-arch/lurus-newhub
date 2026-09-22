package middleware

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// SelfTenantSlug is the reserved :tenant_slug that means "the tenant the
// caller authenticated into". The console builds every tenant-scoped path
// with it, so it never has to know, store or guess its own slug.
//
// Why the console should not know it: this guard already refuses every slug
// except the caller's own (the tenant.Id != tenantCtx.TenantID check below),
// so the slug in the URL never selected anything — it was a claim the server
// checked against the session. Making the browser repeat that claim is what
// broke the console twice: 2026-09-10 (the slug a login returned was the
// tenant id) and 2026-09-22 (a browser that never stored one asked for
// /api/v2/default/... and every panel 404'd). A value the browser cannot
// supply cannot be supplied wrongly.
//
// '~' is outside the slug alphabet every write path validates
// ([A-Za-z0-9_-], handler.isValidTenantSlug), and URL-safe without
// escaping (RFC 3986 unreserved). Even a tenant row that somehow carried it
// loses nothing: the alias only ever resolves to the caller's own tenant,
// which is the only tenant any slug could reach here.
const SelfTenantSlug = "~"

// TenantSlugGuard enforces that the :tenant_slug in the URL matches the
// tenant the caller actually authenticated into, and that the resolved
// tenant is still enabled. Without this, authHelper (auth.go) only ever
// resolves the CALLER's own tenant — it never looks at the URL slug — so
// every /api/v2/:tenant_slug/* route silently acted on the caller's own
// tenant regardless of what slug was typed in, and a disabled/suspended
// tenant kept working as long as its members' sessions stayed valid.
//
// SelfTenantSlug is resolved from the tenant context instead of the URL, and
// the :tenant_slug param is then rewritten to the real slug, so every handler
// behind the guard that reads c.Param("tenant_slug") itself sees exactly what
// it would have seen had the caller typed its own slug.
//
// Must be mounted AFTER UserAuth/AdminAuth so tenant_context is populated
// (mirrors the per-endpoint idiom already used by GetCreditPoolForEndUser
// in tenant_credit_pool.go).
func TenantSlugGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		slug := c.Param("tenant_slug")
		if slug == "" {
			// Slug-less v2 routes (e.g. /api/v2/user/*, /api/v2/admin/*) don't
			// carry a tenant in the URL — nothing to compare against.
			c.Next()
			return
		}

		var tenant *repo.Tenant
		if slug == SelfTenantSlug {
			tenantCtx, err := GetTenantContext(c)
			if err != nil || tenantCtx == nil {
				abortTenantUnauthenticated(c)
				return
			}
			tenant, err = repo.GetTenantByID(tenantCtx.TenantID)
			if err != nil || tenant == nil || tenant.Slug == "" {
				abortTenantNotFound(c)
				return
			}
			setTenantSlugParam(c, tenant.Slug)
		} else {
			var err error
			tenant, err = repo.GetTenantBySlug(slug)
			if err != nil || tenant == nil {
				abortTenantNotFound(c)
				return
			}

			tenantCtx, err := GetTenantContext(c)
			if err != nil || tenantCtx == nil {
				abortTenantUnauthenticated(c)
				return
			}

			if tenant.Id != tenantCtx.TenantID {
				c.JSON(http.StatusForbidden, gin.H{
					"success":    false,
					"message":    "Authenticated tenant does not match URL slug",
					"error_code": "TENANT_MISMATCH",
				})
				c.Abort()
				return
			}
		}

		if tenant.IsDisabled() {
			c.JSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "Tenant is disabled or suspended",
				"error_code": "TENANT_DISABLED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func abortTenantNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{
		"success":    false,
		"message":    "Tenant not found",
		"error_code": "TENANT_NOT_FOUND",
	})
	c.Abort()
}

func abortTenantUnauthenticated(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"success":    false,
		"message":    "Tenant context not found",
		"error_code": "UNAUTHENTICATED",
	})
	c.Abort()
}

// setTenantSlugParam replaces the matched :tenant_slug value in place.
// c.Params is this request's own slice (gin resets it per request), and
// c.Param reads it on every call, so every later handler sees the new value.
func setTenantSlugParam(c *gin.Context, slug string) {
	for i := range c.Params {
		if c.Params[i].Key == "tenant_slug" {
			c.Params[i].Value = slug
			return
		}
	}
}
