package middleware

// Oracle for L2-REQUEST-IDENTITY step 1: an inbound X-Request-Id must be
// honoured (echoed on both the new and legacy-alias headers, and carried in
// the gin context under the unchanged RequestIdKey), a malformed/oversized
// one must be replaced by a minted id, and a valid W3C traceparent must be
// used as a fallback when no X-Request-Id was sent. Also pins that a
// middleware-stage rejection (TokenAuth's 401) still carries the header —
// mirrors rejection_envelope_wire_test.go's router shape but adds RequestId()
// ahead of TokenAuth, exactly as cmd/server/main.go registers it globally
// ahead of every router group.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func newRequestIdRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestId())
	r.GET("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"seen": c.GetString(common.RequestIdKey)})
	})
	return r
}

// TestRequestId_HonoursValidInboundHeader pins the acceptance branch: a
// well-formed caller-supplied X-Request-Id is echoed back verbatim on both
// the new header and the legacy alias, and lands in the context under the
// unchanged RequestIdKey.
func TestRequestId_HonoursValidInboundHeader(t *testing.T) {
	r := newRequestIdRouter()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(common.RequestIdHeader, "uat-abc12345")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get(common.RequestIdHeader); got != "uat-abc12345" {
		t.Errorf("X-Request-Id = %q, want echoed inbound id", got)
	}
	if got := w.Header().Get(common.RequestIdKey); got != "uat-abc12345" {
		t.Errorf("X-Oneapi-Request-Id (alias) = %q, want echoed inbound id", got)
	}
	if !strings.Contains(w.Body.String(), "uat-abc12345") {
		t.Errorf("body = %s, want the inbound id set in context (c.GetString(RequestIdKey))", w.Body.String())
	}
}

// TestRequestId_RejectsOversizedInbound proves the length/charset guard
// actually gates acceptance: a 300-char value and a value containing
// characters that would need escaping in a header must both be replaced by a
// freshly minted id, not echoed back.
func TestRequestId_RejectsOversizedInbound(t *testing.T) {
	r := newRequestIdRouter()

	cases := []string{
		strings.Repeat("a", 300),
		`<script>alert(1)</script>`,
	}
	for _, bad := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set(common.RequestIdHeader, bad)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		got := w.Header().Get(common.RequestIdHeader)
		if got == "" {
			t.Fatalf("inbound %q: X-Request-Id header not set at all", bad)
		}
		if got == bad {
			t.Errorf("inbound %q: echoed back verbatim, want a minted replacement", bad)
		}
		if strings.Contains(w.Body.String(), "script") {
			t.Errorf("inbound %q: raw value leaked into context/body", bad)
		}
	}
}

// TestRequestId_LengthCapMatchesAuditColumnWidth pins the boundary at the
// entity.AuditEvent.RequestID column width (varchar(36)): a 36-char id is
// the longest value the audit-event INSERT can accept, so it must be
// honoured verbatim; a 37-char id must be replaced by a minted id, not
// truncated or echoed — echoing an oversized id would make
// governance.NewAuditEvent's audit-event write fail on PG (22001) and
// silently suppress the caller's own audit row.
func TestRequestId_LengthCapMatchesAuditColumnWidth(t *testing.T) {
	r := newRequestIdRouter()

	ok36 := strings.Repeat("a", 36)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(common.RequestIdHeader, ok36)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get(common.RequestIdHeader); got != ok36 {
		t.Errorf("36-char inbound id: X-Request-Id = %q, want echoed %q", got, ok36)
	}

	bad37 := strings.Repeat("a", 37)
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(common.RequestIdHeader, bad37)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get(common.RequestIdHeader); got == bad37 {
		t.Errorf("37-char inbound id: echoed back verbatim, want a minted replacement (would overflow audit_events.request_id varchar(36))")
	}
}

// TestRequestId_HonoursTraceparentFallback: no X-Request-Id, but a valid W3C
// traceparent — the trace id (not the whole header) becomes the request id.
func TestRequestId_HonoursTraceparentFallback(t *testing.T) {
	r := newRequestIdRouter()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	want := "4bf92f3577b34da6a3ce929d0e0e4736"
	if got := w.Header().Get(common.RequestIdHeader); got != want {
		t.Errorf("X-Request-Id = %q, want traceparent trace-id %q", got, want)
	}
}

// TestRequestId_MintsWhenNothingUsable: neither header present -> a fresh id
// is minted (today's behaviour), never an empty string.
func TestRequestId_MintsWhenNothingUsable(t *testing.T) {
	r := newRequestIdRouter()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get(common.RequestIdHeader); got == "" {
		t.Error("X-Request-Id not set when no inbound id/traceparent present")
	}
	if got := w.Header().Get(common.RequestIdKey); got == "" {
		t.Error("X-Oneapi-Request-Id (alias) not set when no inbound id/traceparent present")
	}
}

// TestRequestId_RejectionCarriesHeader pins that a middleware-stage 401
// (TokenAuth's bad-key rejection) still carries the caller's own
// X-Request-Id — every 4xx must be correlatable, not only 200s. Mirrors
// rejection_envelope_wire_test.go's mountWireRejectionRouter shape (same
// StampRelayFormat+TokenAuth group layout) with RequestId() added ahead of
// it, matching cmd/server/main.go's global engine.Use(RequestId()) ordering.
func TestRequestId_RejectionCarriesHeader(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestId())
	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat(), TokenAuth())
	v1.POST("/chat/completions", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer nosuchtokenatall")
	req.Header.Set(common.RequestIdHeader, "uat-rejectme")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(common.RequestIdHeader); got != "uat-rejectme" {
		t.Errorf("X-Request-Id on 401 = %q, want echoed inbound id", got)
	}
}
