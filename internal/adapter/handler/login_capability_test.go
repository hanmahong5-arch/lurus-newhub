package handler

// login_capability_test.go — what /api/status tells a browser about how it
// can sign in.
//
// The console has to decide, before it navigates anywhere, which sign-in
// route this deployment actually wired. It cannot use login_methods.oidc for
// that: that entry projects the legacy OAuth settings block, and both live
// deployments report it false — including the one whose SSO works — so a
// console reading it would send every visitor into ZitaLogin's 503 with no
// way back. These lock the two flags the console does read, each against the
// same condition the thing it describes checks for itself.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// statusLoginMethods drives GetStatus and returns its login_methods map.
func statusLoginMethods(t *testing.T) map[string]interface{} {
	t.Helper()
	c, w := r2authReq(http.MethodGet, "/api/status", nil)
	GetStatus(c)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authAssertSuccess(t, w, true)
	data, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body=%s", w.Body.String())
	}
	lm, ok := data["login_methods"].(map[string]interface{})
	if !ok {
		t.Fatalf("login_methods missing or wrong shape, body=%s", w.Body.String())
	}
	return lm
}

func statusFlag(t *testing.T, lm map[string]interface{}, group string) bool {
	t.Helper()
	entry, ok := lm[group].(map[string]interface{})
	if !ok {
		t.Fatalf("login_methods.%s missing or wrong shape, got %T", group, lm[group])
	}
	enabled, ok := entry["enabled"].(bool)
	if !ok {
		t.Fatalf("login_methods.%s.enabled missing or not a bool, got %T", group, entry["enabled"])
	}
	return enabled
}

// TestStatusSSOFlagTracksTheLoginHandlersOwnCheck: login_methods.sso.enabled
// must agree with common.ZitaClient != nil, which is the condition ZitaLogin
// branches on. Both states are exercised — a flag that is hardwired to
// either one would still satisfy a single-state test.
func TestStatusSSOFlagTracksTheLoginHandlersOwnCheck(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	prev := common.ZitaClient
	t.Cleanup(func() { common.ZitaClient = prev })

	common.ZitaClient = nil
	if statusFlag(t, statusLoginMethods(t), "sso") {
		t.Error("login_methods.sso.enabled = true with no identity client — the console would send visitors to a 503")
	}

	t.Setenv("IDENTITY_PUBLIC_URL", "https://identity.invalid.test")
	t.Setenv("IDENTITY_SESSION_SECRET", "login-capability-test-secret-not-a-real-one")
	common.InitZitaClient()
	if common.ZitaClient == nil {
		t.Fatal("InitZitaClient left ZitaClient nil with both settings supplied — cannot exercise the enabled state")
	}
	if !statusFlag(t, statusLoginMethods(t), "sso") {
		t.Error("login_methods.sso.enabled = false while the identity client is wired — the console would hide the only way in")
	}
}

// TestStatusBridgeFlagTracksRouteRegistration: login_methods.bridge.enabled
// must agree with BridgeEnabled(), the same predicate the router consults
// before registering POST /api/v2/bridge/exchange. A console that offered
// the bridge form where the route is absent would collect a token and get
// a 404.
func TestStatusBridgeFlagTracksRouteRegistration(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Setenv("E2E_BRIDGE_TOKEN", "")
	if BridgeEnabled() {
		t.Fatal("BridgeEnabled() = true with an empty token — test premise broken")
	}
	if statusFlag(t, statusLoginMethods(t), "bridge") {
		t.Error("login_methods.bridge.enabled = true where the route is not registered")
	}

	t.Setenv("E2E_BRIDGE_TOKEN", "login-capability-test-bridge-token")
	if !BridgeEnabled() {
		t.Fatal("BridgeEnabled() = false with a token set — test premise broken")
	}
	if !statusFlag(t, statusLoginMethods(t), "bridge") {
		t.Error("login_methods.bridge.enabled = false while the route is registered")
	}
}

// TestZitaLoginUnavailableBodyNamesNoSettings: the endpoint is
// unauthenticated, so its failure body is public. It may say that sign-on is
// off; it may not enumerate the settings that would turn it on.
func TestZitaLoginUnavailableBodyNamesNoSettings(t *testing.T) {
	prev := common.ZitaClient
	t.Cleanup(func() { common.ZitaClient = prev })
	common.ZitaClient = nil

	c, w := r2authReq(http.MethodGet, "/api/v2/auth/zita-login", nil)
	ZitaLogin(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	if got := body["error"]; got != "sso_not_configured" {
		t.Errorf("error = %v, want sso_not_configured", got)
	}
	for _, leak := range []string{"IDENTITY_", "SESSION_SECRET", "PUBLIC_URL"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("public 503 body names the deployment setting %q: %s", leak, w.Body.String())
		}
	}
}
