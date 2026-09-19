package router

// r11_channel_root_only_test.go — cycle-11 L3/L8/W: GET /api/channel/test,
// GET /api/channel/update_balance and POST /api/channel/fix are now
// middleware.RootAuth()-gated (api-router.go), where before any tenant admin
// could trigger a platform-wide channel probe / balance refresh / ability
// rebuild. Drives the real SetApiRouter mount, reusing
// setupChannelSensitiveWriteRealChain's fixture rather than re-deriving a
// harness.
//
// Mutation: dropping middleware.RootAuth() from any one of the three lines
// in api-router.go turns that route's sub-test red (role-10 admin gets
// success:true instead of the "权限不足" refusal).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestChannelRootOnlyRoutes_RealChain(t *testing.T) {
	f, cleanup := setupChannelSensitiveWriteRealChain(t)
	defer cleanup()

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"test", http.MethodGet, "/api/channel/test"},
		{"update_balance", http.MethodGet, "/api/channel/update_balance"},
		{"fix", http.MethodPost, "/api/channel/fix"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wAdmin := f.serveAs(t, f.adminID, common.RoleAdminUser, tc.method, tc.path, nil)
			if !strings.Contains(wAdmin.Body.String(), permissionDenied) {
				t.Fatalf("non-root admin %s %s: want refusal containing %q, got body=%s", tc.method, tc.path, permissionDenied, wAdmin.Body.String())
			}

			wRoot := f.serveAs(t, f.rootID, common.RoleRootUser, tc.method, tc.path, nil)
			if strings.Contains(wRoot.Body.String(), permissionDenied) {
				t.Fatalf("root %s %s: wrongly refused, body=%s", tc.method, tc.path, wRoot.Body.String())
			}
			if wRoot.Code != http.StatusOK {
				t.Fatalf("root %s %s: status = %d, want 200; body=%s", tc.method, tc.path, wRoot.Code, wRoot.Body.String())
			}
		})
	}
}
