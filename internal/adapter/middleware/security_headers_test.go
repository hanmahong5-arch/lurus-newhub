package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func mountSecurityHeadersEcho() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/echo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestSecurityHeaders_AlwaysOnHeaders pins the three headers that must be set
// on every response regardless of transport, plus the deliberate absence of
// Content-Security-Policy (the design explicitly excludes it — see the doc
// comment on SecurityHeaders — so a future accidental addition that breaks
// the SPA should show up here as a failing assertion, not silence).
func TestSecurityHeaders_AlwaysOnHeaders(t *testing.T) {
	r := mountSecurityHeadersEcho()
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from the wrapped handler, got %d", w.Code)
	}

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options: expected %q, got %q", "nosniff", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options: expected %q, got %q", "DENY", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy: expected %q, got %q", "strict-origin-when-cross-origin", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("Content-Security-Policy: expected absent by design, got %q", got)
	}
}

// TestSecurityHeaders_HSTS_AbsentWithoutTLSSignal proves a plain-HTTP request
// with no TLS and no X-Forwarded-Proto: https (e.g. local `go run`, no proxy
// in front) never gets told to force HTTPS — that would break access to a
// host that doesn't actually serve TLS.
func TestSecurityHeaders_HSTS_AbsentWithoutTLSSignal(t *testing.T) {
	r := mountSecurityHeadersEcho()
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("Strict-Transport-Security: expected absent without a TLS/X-Forwarded-Proto signal, got %q", got)
	}
}

// TestSecurityHeaders_HSTS_PresentBehindTLSProxy proves the R6 topology (host
// nginx terminates TLS, sets X-Forwarded-Proto: https on every request) does
// get the header, with the exact conservative value (no includeSubDomains,
// no preload — see the doc comment on SecurityHeaders for why).
func TestSecurityHeaders_HSTS_PresentBehindTLSProxy(t *testing.T) {
	r := mountSecurityHeadersEcho()
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=15552000" {
		t.Fatalf("Strict-Transport-Security: expected %q behind an X-Forwarded-Proto: https proxy, got %q", "max-age=15552000", got)
	}
}
