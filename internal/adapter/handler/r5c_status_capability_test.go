package handler

// r5c_status_capability_test.go — locks G4d in the 2026-08-26 live UAT gap
// report: GetStatus's public /api/status payload advertised
// login_methods.password.enabled=true and registration.enabled=true /
// mode="open", steering third-party clients that do capability discovery
// toward routes that do not exist in this router. Two things are checked:
//
//  1. the JSON projection itself now reports the closed state, and
//  2. the *reason* it is safe to report closed — that this router really
//     has no password register/login route — by scanning the real router
//     source (not a mock) at test time. If either the projection or the
//     router source drifts (someone re-adds password auth, or someone
//     re-flips these two fields without re-adding routes), this file is the
//     tripwire.
//
// This cannot import internal/adapter/handler/router directly (it imports
// this package, which would be an import cycle), so #2 is a static text
// scan of the router package's own .go source files rather than a live
// gin.Engine walk. That is deliberately closer to "observe the real wiring"
// than a hardcoded assumption: it re-derives the route list from source on
// every run, the same way the grep in misc.go's comment was produced.
//
// G4c wiring gap (added 2026-08-27, R6-D lane): operation_setting's own test
// (r5c_general_setting_default_test.go) only locks the package-level default
// of GeneralSetting.DocsLink; nothing observed the actual GetStatus HTTP
// projection at misc.go:146. Proven by mutation: hardcoding
// `"docs_link": "https://docs.newapi.pro"` at that call site (instead of
// reading operation_setting.GetGeneralSetting().DocsLink) left
// `go test ./internal/adapter/handler/ ./internal/pkg/setting/operation_setting/`
// fully green. The assertion below closes that gap by reading data["docs_link"]
// from the same decoded /api/status payload this test already has in hand.

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestR5CStatusCapability_LoginMethodsAndRegistrationReportClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authReq(http.MethodGet, "/api/status", nil)
	GetStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authAssertSuccess(t, w, true)
	data, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body=%s", w.Body.String())
	}

	loginMethods, ok := data["login_methods"].(map[string]interface{})
	if !ok {
		t.Fatalf("login_methods missing or wrong shape, body=%s", w.Body.String())
	}
	password, ok := loginMethods["password"].(map[string]interface{})
	if !ok {
		t.Fatalf("login_methods.password missing or wrong shape, body=%s", w.Body.String())
	}
	if enabled, _ := password["enabled"].(bool); enabled {
		t.Errorf("login_methods.password.enabled = true, want false (no POST /api/user/login route exists — see comment in misc.go)")
	}

	registration, ok := data["registration"].(map[string]interface{})
	if !ok {
		t.Fatalf("registration missing or wrong shape, body=%s", w.Body.String())
	}
	if enabled, _ := registration["enabled"].(bool); enabled {
		t.Errorf("registration.enabled = true, want false (no POST /api/user/register route exists)")
	}
	if mode, _ := registration["mode"].(string); mode != "closed" {
		t.Errorf("registration.mode = %q, want %q", mode, "closed")
	}

	// G4c: the projection surface, not just operation_setting's own default —
	// see the file header comment. A hardcoded upstream docs URL at misc.go's
	// call site would pass every other assertion here and still leak.
	if docsLink, _ := data["docs_link"].(string); docsLink != "" {
		t.Errorf("docs_link = %q, want empty string (white-label leak: GetStatus must not surface a hardcoded upstream docs URL — see general_setting.go's DocsLink default comment)", docsLink)
	}

	// N5: passkey was settings-only theater — a registered config struct with
	// zero backend handlers (no /webauthn or /passkey route ever existed).
	// system_setting/passkey.go and its projection here were deleted outright
	// (root contracts.md has zero passkey consumers). This pins the response
	// shape so a re-add doesn't silently resurrect dead keys.
	if _, ok := loginMethods["passkey"]; ok {
		t.Errorf("login_methods.passkey key present, want removed (passkey config surface was deleted, see N5)")
	}
	securityBlock, ok := data["security"].(map[string]interface{})
	if !ok {
		t.Fatalf("security missing or wrong shape, body=%s", w.Body.String())
	}
	if _, ok := securityBlock["passkey_available"]; ok {
		t.Errorf("security.passkey_available key present, want removed (passkey config surface was deleted, see N5)")
	}
	for _, key := range []string{
		"passkey_login", "passkey_display_name", "passkey_rp_id",
		"passkey_origins", "passkey_allow_insecure",
		"passkey_user_verification", "passkey_attachment",
	} {
		if _, ok := data[key]; ok {
			t.Errorf("top-level %q key present, want removed (passkey config surface was deleted, see N5)", key)
		}
	}
}

// routeRegistrationPattern matches gin route registration calls of the form
// `<receiver>.<METHOD>("<path>", ...)` as they appear in the router package
// source (e.g. `apiV2.GET("/:tenant_slug/auth/login", handler.OIDCLoginRedirect)`).
var routeRegistrationPattern = regexp.MustCompile(`\.(GET|POST|PUT|DELETE|PATCH|Any)\(\s*"([^"]*)"`)

// r5cScanRouterRouteRegistrations reads every non-test .go file directly
// under dir and returns "METHOD PATH" for each gin route registration call
// found in it. It is a plain text/regex scan of the router package's own
// source (not an import of that package), specifically to avoid the
// router->handler->router import cycle while still observing real wiring.
func r5cScanRouterRouteRegistrations(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read router dir %q: %v (this test's relative path assumes `go test` cwd = internal/adapter/handler)", dir, err)
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range routeRegistrationPattern.FindAllStringSubmatch(string(src), -1) {
			found = append(found, m[1]+" "+m[2])
		}
	}
	if len(found) == 0 {
		t.Fatalf("scanned %s and found zero route registrations — the regex or the relative path is broken, this must not silently pass", dir)
	}
	return found
}

// TestR5CStatusCapability_NoPasswordAuthRoutesRegistered is the oracle for
// the test above: it re-runs (in code, at test time) the same grep that
// justified hardcoding login_methods.password.enabled=false and
// registration.enabled=false in misc.go. If a future change re-adds a
// POST /api/user/register or POST /api/user/login route, this test fails
// and tells the reader to flip GetStatus's projection back to the live
// common.* flags.
func TestR5CStatusCapability_NoPasswordAuthRoutesRegistered(t *testing.T) {
	routes := r5cScanRouterRouteRegistrations(t, "router")

	knownSafeLoginRoutes := map[string]bool{
		"GET /:tenant_slug/auth/login": true, // OIDC redirect, api-v2-router.go
		"GET /auth/zita-login":         true, // Zita SDK login, api-v2-router.go
	}

	for _, r := range routes {
		lower := strings.ToLower(r)
		if strings.Contains(lower, "register") {
			t.Errorf("found a %q route registration containing \"register\" — password registration routes must not exist in this router; if this is intentional, flip registration.enabled/mode back in misc.go", r)
			continue
		}
		if strings.Contains(lower, "login") && !knownSafeLoginRoutes[r] {
			t.Errorf("found unexpected login route %q (not in the known-safe OIDC/Zita set) — if a password login route was added, flip login_methods.password.enabled back in misc.go", r)
		}
	}

	// Confirm the two known-safe routes are actually there (not just that
	// nothing unexpected was found) — otherwise a broken regex/path would
	// pass this test vacuously.
	for want := range knownSafeLoginRoutes {
		hit := false
		for _, r := range routes {
			if r == want {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("expected known-safe route %q not found among scanned registrations — scan may be broken (found %d total)", want, len(routes))
		}
	}
}
