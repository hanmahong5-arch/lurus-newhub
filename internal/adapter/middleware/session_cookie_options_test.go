package middleware

// session_cookie_options_test.go — L4 (cycle 12). Two guards around the
// session cookie's attributes:
//
//  1. Nobody in this package may hand session.Options a hand-built
//     sessions.Options literal. A clearing Set-Cookie whose
//     Domain/Secure/SameSite do not match the cookie the browser is holding
//     does not delete it (RFC 6265 keys a cookie by name+domain+path), so
//     `{Path:"/", MaxAge:-1}` looks like a logout and is not one.
//     SessionClearOptions is the one way to build them.
//  2. BrowserOriginGuard's idea of "this request carries a session cookie"
//     must stay equal to the cookie name cmd/server actually registers.
//
// The sibling scan for the handler package lives in
// handler/session_clear_options_test.go — one per package, because each
// scans its own directory.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
)

// sessionOptionsCallRe matches any call of the form `<x>.Options(` — the
// population the scan classifies.
var sessionOptionsCallRe = regexp.MustCompile(`\.Options\(`)

// sessionOptionsLiteralRe matches the forbidden form: a sessions.Options
// composite literal passed straight into .Options(.
var sessionOptionsLiteralRe = regexp.MustCompile(`\.Options\(\s*sessions\.Options\{`)

// scanSessionOptionsCallSites reports (files scanned, call sites found,
// offending "file:line" strings) for one package directory.
func scanSessionOptionsCallSites(t *testing.T, dir string) (int, int, []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	files, sites := 0, 0
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		raw, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if sessionOptionsCallRe.MatchString(line) {
				sites++
			}
			if sessionOptionsLiteralRe.MatchString(line) {
				offenders = append(offenders, filepath.Join(dir, name)+":"+strconv.Itoa(i+1))
			}
		}
	}
	return files, sites, offenders
}

func TestSessionOptions_NoHandBuiltLiteralsInMiddleware(t *testing.T) {
	files, sites, offenders := scanSessionOptionsCallSites(t, ".")
	if files == 0 {
		t.Fatal("scanned 0 non-test .go files — the scan is looking at the wrong directory")
	}
	if sites == 0 {
		t.Fatalf("scanned %d files and found 0 `.Options(` call sites — this gate would pass on an empty package; the regex or the directory is wrong", files)
	}
	if len(offenders) > 0 {
		t.Fatalf("hand-built sessions.Options literal at %v — use SessionClearOptions() so Domain/Secure/SameSite match the live cookie", offenders)
	}
}

// TestBrowserSessionCookieNames_MatchTheStoreSetup pins BrowserOriginGuard's
// cookie-name list against cmd/server/main.go's own sessions.Sessions(...)
// registration. Renaming the store's cookie there without touching the guard
// would silently turn the guard into a no-op for every browser request.
func TestBrowserSessionCookieNames_MatchTheStoreSetup(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "cmd", "server", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/server/main.go: %v", err)
	}
	m := regexp.MustCompile(`sessions\.Sessions\("([^"]+)"`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("cmd/server/main.go has no sessions.Sessions(\"<name>\", ...) call — this pin cannot verify anything, fix the pin rather than deleting it")
	}
	if len(browserSessionCookieNames) == 0 || browserSessionCookieNames[0] != m[1] {
		t.Fatalf("browserSessionCookieNames[0] = %q, but cmd/server registers the session cookie as %q",
			browserSessionCookieNames, m[1])
	}
}

// TestSessionClearOptions_MatchesBaseExceptMaxAge is the behavioural half:
// the clearing options must differ from the live cookie's options in MaxAge
// and nothing else.
func TestSessionClearOptions_MatchesBaseExceptMaxAge(t *testing.T) {
	t.Setenv("SESSION_COOKIE_DOMAIN", "")
	t.Setenv("SESSION_SECURE", "true")

	base := SessionCookieBaseOptions()
	clear := SessionClearOptions()

	if clear.MaxAge != -1 {
		t.Errorf("SessionClearOptions().MaxAge = %d, want -1 (that is what deletes the cookie)", clear.MaxAge)
	}
	base.MaxAge = clear.MaxAge
	if base != clear {
		t.Errorf("SessionClearOptions() = %+v, want SessionCookieBaseOptions() with MaxAge -1 (%+v) — a mismatched attribute mints a second cookie instead of clearing the stale one", clear, base)
	}
	var zero sessions.Options
	if clear == zero {
		t.Error("SessionClearOptions() returned the zero value — Path would be empty and the cookie would not be cleared")
	}
}
