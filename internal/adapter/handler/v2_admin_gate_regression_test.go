package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestV2AdminGate_SessionAdminWithoutStringRoles locks the contract that the
// channel/redemption admin endpoints accept a site admin authenticated via
// session/access token — whose TenantContext.Roles is empty (only the integer
// "role" gin key is set, see middleware.authHelper) — and still reject a
// common user with no roles. Before the fix these handlers checked only the
// Zitadel string roles (hasRole), so access-token admins got 403.
func TestV2AdminGate_SessionAdminWithoutStringRoles(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	endpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"list channels", http.MethodGet, "/api/v2/test-tenant/channels"},
		{"get channel", http.MethodGet, "/api/v2/test-tenant/channels/99999"},
		{"create channel", http.MethodPost, "/api/v2/test-tenant/channels"},
		{"update channel", http.MethodPut, "/api/v2/test-tenant/channels/99999"},
		{"delete channel", http.MethodDelete, "/api/v2/test-tenant/channels/99999"},
		{"test channel", http.MethodPost, "/api/v2/test-tenant/channels/99999/test"},
		{"upstream models", http.MethodGet, "/api/v2/test-tenant/channels/99999/upstream-models"},
		{"list redemptions", http.MethodGet, "/api/v2/test-tenant/redemptions"},
		{"create redemption", http.MethodPost, "/api/v2/test-tenant/redemptions"},
		{"delete redemption", http.MethodDelete, "/api/v2/test-tenant/redemptions/99999"},
	}

	// The admin gate runs before body/ID validation, so a 403 means the gate
	// rejected the caller; any other status means the gate let them through.
	assertGate := func(t *testing.T, user *repo.User, roles []string, wantForbidden bool) {
		t.Helper()
		for _, ep := range endpoints {
			var w *httptest.ResponseRecorder
			if ep.method == http.MethodPost || ep.method == http.MethodPut {
				w = V2RequestAsUser(ctx, user, ep.method, ep.path, map[string]interface{}{}, roles)
			} else {
				w = V2RequestAsUser(ctx, user, ep.method, ep.path, nil, roles)
			}
			if wantForbidden && w.Code != http.StatusForbidden {
				t.Errorf("%s: expected 403 for non-admin, got %d, body: %s", ep.name, w.Code, w.Body.String())
			}
			if !wantForbidden && w.Code == http.StatusForbidden {
				t.Errorf("%s: session admin (no string roles) was rejected with 403, body: %s", ep.name, w.Body.String())
			}
		}
	}

	t.Run("session admin no string roles passes gate", func(t *testing.T) {
		assertGate(t, ctx.AdminUser, nil, false)
	})
	t.Run("session root no string roles passes gate", func(t *testing.T) {
		assertGate(t, ctx.RootUser, nil, false)
	})
	t.Run("common user no string roles is rejected", func(t *testing.T) {
		assertGate(t, ctx.NormalUser, nil, true)
	})
	// Channels and redemption codes are the platform's supply chain, not a
	// tenant's (enterprise hub plan D5): an OIDC "admin" claim is tenant-scoped
	// and an IdP may issue it to a customer organisation, so it must not pass.
	// Only "root" on the JWT path is platform staff.
	t.Run("common user with jwt admin string role is rejected", func(t *testing.T) {
		assertGate(t, ctx.NormalUser, []string{"admin"}, true)
	})
	t.Run("common user with jwt root string role passes gate", func(t *testing.T) {
		assertGate(t, ctx.NormalUser, []string{"root"}, false)
	})
}
