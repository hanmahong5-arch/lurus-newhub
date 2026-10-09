package handler

import (
	"fmt"

	"github.com/gin-contrib/sessions"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// sessionTenantSlug returns the session's routing slug, recovering it from
// the user's own tenant when the session carries none (moved verbatim from
// GetSessionInfo to keep oauth.go under its source-size ceiling).
func sessionTenantSlug(session sessions.Session, tenantID string, userID int) string {
	// Get tenant_slug from session (stored by the OAuth callback above and,
	// since the same cycle as this fallback, by ZitaBootstrap).
	//
	// Sessions minted before that were saved without the key, so this
	// endpoint — the console's only way to learn its routing slug that does
	// not need a platform cookie — answered "" for every one of them, and
	// the console fell back to a literal that TenantSlugGuard 404s. Resolve
	// it from the user's own tenant instead, and write it back so an
	// existing login pays the lookup once rather than on every call. A
	// tenant that cannot be resolved leaves the field "" (resolveTenantSlug
	// never invents a slug) and nothing is written.
	tenantSlug, _ := session.Get("tenant_slug").(string)
	if tenantSlug == "" {
		if resolved := resolveTenantSlug(tenantID); resolved != "" {
			tenantSlug = resolved
			session.Set("tenant_slug", tenantSlug)
			if err := session.Save(); err != nil {
				common.SysError(fmt.Sprintf("sessionTenantSlug: failed to persist recovered tenant_slug for user %d: %v", userID, err))
			}
		}
	}
	return tenantSlug
}
