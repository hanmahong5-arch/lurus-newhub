package handler

// session_clear_options_test.go — L4 (cycle 12). The handler-package half of
// the session-cookie scan (the middleware half is
// middleware/session_cookie_options_test.go): no handler may hand
// session.Options a hand-built sessions.Options literal.
//
// Why it matters here specifically: RevokeCurrentSessionV2 ("log out") and
// ZitaLogout ("switch account") both used `{Path:"/", MaxAge:-1}`. On
// production the live cookie is written with Domain from
// SESSION_COOKIE_DOMAIN plus Secure and SameSite (see
// middleware.SessionCookieBaseOptions), and RFC 6265 identifies a cookie by
// name+domain+path — a clearing Set-Cookie with a different Domain mints a
// second, unrelated cookie and leaves the real one in the browser. The
// logout looks like it worked. middleware.SessionClearOptions() is the one
// builder that reads the same env the store was configured from.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// handlerSessionOptionsCallRe matches any `<x>.Options(` call — the
// population this gate classifies. Counting it is what stops the gate from
// passing vacuously if the call sites are renamed or moved away.
var handlerSessionOptionsCallRe = regexp.MustCompile(`\.Options\(`)

// handlerSessionOptionsLiteralRe matches the forbidden form.
var handlerSessionOptionsLiteralRe = regexp.MustCompile(`\.Options\(\s*sessions\.Options\{`)

func TestSessionOptions_NoHandBuiltLiteralsInHandlers(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	files, sites := 0, 0
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		raw, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if handlerSessionOptionsCallRe.MatchString(line) {
				sites++
			}
			if handlerSessionOptionsLiteralRe.MatchString(line) {
				offenders = append(offenders, filepath.Join(name)+":"+strconv.Itoa(i+1))
			}
		}
	}

	if files == 0 {
		t.Fatal("scanned 0 non-test .go files — the scan is looking at the wrong directory")
	}
	if sites == 0 {
		t.Fatalf("scanned %d files and found 0 `.Options(` call sites — this gate would pass on a package that never touches the session cookie; the regex or the directory is wrong", files)
	}
	if len(offenders) > 0 {
		t.Fatalf("hand-built sessions.Options literal at %v — use middleware.SessionClearOptions() so Domain/Secure/SameSite match the cookie the browser actually holds", offenders)
	}
}
