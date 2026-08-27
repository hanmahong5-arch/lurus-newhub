package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestSetUpLogger_JSONMode_TokenAuthPath_EmitsBothIdentityKeysWithoutCollision
// locks G3's fix on the identity path that matters most for money/abuse
// investigations: TokenAuth and flexAuthViaToken each call c.Set("id", ...)
// AND common.WithUserID(ctx, ...) on the same request, which is why this test
// simulates both writes instead of only the gin-context one. Anchors
// re-checked 2026-08-27 (an earlier draft cited auth.go:216+222, which is
// authHelper — the console-session path, not TokenAuth):
//   - TokenAuth (auth.go:308): WithUserID at auth.go:525, c.Set("id") at
//     auth.go:564 inside SetupContextForToken, which TokenAuth calls. This is
//     the relay/billing path.
//   - flexAuthViaToken (flex_auth.go:48): flex_auth.go:81 + :86.
//   - authHelper (auth.go:36, backing UserAuth/AdminAuth/RootAuth):
//     auth.go:216 + :222 — same both-keys shape, different path. Before the fix both writes landed on the same
// "user_id" key (once as int, once as string) — this asserts they now land on
// two distinctly-named keys ("auth_user_id" int, "user_id" string) with no
// duplicate key in the raw JSON. Mutation proof: reverting jsonAccessLogger's
// "auth_user_id" key back to "user_id" makes the strings.Count assertion
// below fail (2 occurrences of `"user_id"` instead of 1).
func TestSetUpLogger_JSONMode_TokenAuthPath_EmitsBothIdentityKeysWithoutCollision(t *testing.T) {
	buf := &bytes.Buffer{}
	common.InitSlog(&common.SlogConfig{JSONFormat: true, Writer: buf, ErrWriter: buf})
	// Restore the safe default (text mode, os.Stdout/os.Stderr) afterward —
	// leaving the global logger pointed at this test's local buf/nil writer
	// would corrupt unrelated tests elsewhere in this package that log
	// through common.LogInfo/LogError during teardown.
	t.Cleanup(func() { common.InitSlog(nil) })

	r := gin.New()
	r.Use(RequestId())
	SetUpLogger(r)
	r.GET("/x", func(c *gin.Context) {
		// Identity keys are populated by auth middleware during the handler
		// chain in production. TokenAuth and flexAuthViaToken do both of these
		// writes on the same request (auth.go:525 + :564, flex_auth.go:81 +
		// :86 — see this test's doc comment); simulate both here, not just the
		// gin-set one, since jsonAccessLogger reads the request context AFTER
		// c.Next().
		c.Set("id", 42)
		c.Set("tenant_id", "acme")
		c.Set("token_id", 7)
		c.Request = c.Request.WithContext(common.WithUserID(c.Request.Context(), fmt.Sprintf("%d", 42)))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	got := buf.String()
	if strings.Contains(got, "[GIN] ") {
		t.Fatalf("JSON mode must not emit the legacy [GIN] line, got %q", got)
	}

	var lines []string
	for _, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly one structured record, got %d: %q", len(lines), got)
	}
	line := lines[0]

	if n := strings.Count(line, `"user_id"`); n != 1 {
		t.Fatalf(`raw JSON must contain "user_id" exactly once, found %d: %q`, n, line)
	}
	if n := strings.Count(line, `"auth_user_id"`); n != 1 {
		t.Fatalf(`raw JSON must contain "auth_user_id" exactly once, found %d: %q`, n, line)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("access log JSON record does not parse: %v\nline=%q", err, line)
	}
	if record["msg"] != "http_request" {
		t.Errorf("record msg = %v, want %q", record["msg"], "http_request")
	}
	// json.Unmarshal decodes JSON numbers into float64; the string-typed
	// user_id (from contextHandler's WithUserID injection) stays a string.
	if got, want := record["auth_user_id"], float64(42); got != want {
		t.Errorf("record auth_user_id = %v, want %v", got, want)
	}
	if got, want := record["user_id"], "42"; got != want {
		t.Errorf("record user_id = %v, want %q", got, want)
	}
	if got, want := record["tenant_id"], "acme"; got != want {
		t.Errorf("record tenant_id = %v, want %q", got, want)
	}
	if got, want := record["token_id"], float64(7); got != want {
		t.Errorf("record token_id = %v, want %v", got, want)
	}
	if got, want := record["status"], float64(http.StatusOK); got != want {
		t.Errorf("record status = %v, want %v", got, want)
	}
}

// TestSetUpLogger_JSONMode_OIDCSessionPath_EmitsAuthUserIDOnly locks the
// other identity path: oidc_auth.go:589 and :632 call c.Set("id", ...) but
// never common.WithUserID, so an OIDC-session request must still carry
// "auth_user_id" (proving that path isn't silently dropped) while carrying no
// "user_id" key at all (proving the two keys really are independent writes,
// not one value duplicated under two names). Mutation proof: if
// jsonAccessLogger's auth_user_id write were removed to "fix" the duplicate
// instead of being renamed, this test's auth_user_id assertion would fail —
// OIDC-session requests would lose numeric identity entirely.
func TestSetUpLogger_JSONMode_OIDCSessionPath_EmitsAuthUserIDOnly(t *testing.T) {
	buf := &bytes.Buffer{}
	common.InitSlog(&common.SlogConfig{JSONFormat: true, Writer: buf, ErrWriter: buf})
	t.Cleanup(func() { common.InitSlog(nil) })

	r := gin.New()
	r.Use(RequestId())
	SetUpLogger(r)
	r.GET("/x", func(c *gin.Context) {
		// oidc_auth.go's session path only does this one write — no
		// common.WithUserID call anywhere in that file.
		c.Set("id", 99)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var lines []string
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly one structured record, got %d: %q", len(lines), buf.String())
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("access log JSON record does not parse: %v\nline=%q", err, lines[0])
	}
	if got, want := record["auth_user_id"], float64(99); got != want {
		t.Errorf("record auth_user_id = %v, want %v", got, want)
	}
	if _, present := record["user_id"]; present {
		t.Errorf(`record must not contain "user_id" when common.WithUserID was never called, got %v`, record["user_id"])
	}
}

// TestSetUpLogger_TextMode_StillEmitsLegacyGINLine locks the other half:
// in text mode (the non-live default) the access log must keep the exact
// "[GIN] " layout operators already grep for in `kubectl logs`.
func TestSetUpLogger_TextMode_StillEmitsLegacyGINLine(t *testing.T) {
	// common.InitSlog(nil) (rather than a bare &SlogConfig{JSONFormat: false})
	// applies DefaultSlogConfig's safe os.Stdout/os.Stderr writers — a literal
	// &SlogConfig{JSONFormat: false} leaves Writer/ErrWriter nil, which
	// panics the next unrelated test in this package that logs through
	// common.LogInfo/LogError while this test's global state is still live.
	common.InitSlog(nil)

	origOut := gin.DefaultWriter
	buf := &bytes.Buffer{}
	gin.DefaultWriter = buf
	t.Cleanup(func() { gin.DefaultWriter = origOut })

	r := gin.New()
	r.Use(RequestId())
	SetUpLogger(r)
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	got := buf.String()
	if !strings.Contains(got, "[GIN] ") {
		t.Errorf("text mode must keep the legacy [GIN] line, got %q", got)
	}
}

// TestSetUpLogger_TextMode_AnonymousRequestDoesNotPanic is the regression
// test for the unchecked `param.Keys[common.RequestIdKey].(string)` type
// assertion that used to live in the text-mode formatter. It deliberately
// omits RequestId() so no request-id key is ever set, while another
// middleware still calls c.Set (making param.Keys non-nil but missing the
// RequestIdKey entry) — exactly the state that panicked before the fix used
// the comma-ok form. Mutation proof: reverting logger.go's requestID
// extraction to the bare `.(string)` assertion makes this test panic instead
// of passing.
func TestSetUpLogger_TextMode_AnonymousRequestDoesNotPanic(t *testing.T) {
	common.InitSlog(nil) // see TestSetUpLogger_TextMode_StillEmitsLegacyGINLine for why nil, not a bare struct literal

	r := gin.New()
	// No RequestId() middleware registered: common.RequestIdKey is never Set.
	SetUpLogger(r)
	r.GET("/x", func(c *gin.Context) {
		// Some other middleware/handler sets an unrelated key, which makes
		// gin's c.Keys map non-nil without ever touching RequestIdKey.
		c.Set("id", 0)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil)) // must not panic
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
