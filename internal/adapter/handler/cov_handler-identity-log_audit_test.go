package handler

// ============================================================================
// v2_admin_audit.go gaps beyond v2_admin_audit_test.go: CSV export's
// X-Next-Cursor pagination header, and the JSON export's zero-match path
// (a filter that legitimately matches nothing must return an empty array,
// not a JSON null that would break naive frontend .map() calls).
// Reuses setupAuditExportRouter / seedAuditEvent / parseJSON
// (v2_admin_audit_test.go, same package).
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandlerIdentityLog_ExportAuditEventsV2_CSVSetsNextCursorHeader proves
// a CSV export page that doesn't exhaust the result set carries
// X-Next-Cursor so a curl-style consumer can keep paging a compliance
// export without switching to JSON.
func TestHandlerIdentityLog_ExportAuditEventsV2_CSVSetsNextCursorHeader(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	for i := 0; i < 5; i++ {
		seedAuditEvent(t, ctx.db, "token.created", "token", 1, -time.Duration(i)*time.Minute)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=csv&limit=2", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Next-Cursor"); got == "" {
		t.Error("CSV page with more rows remaining must set X-Next-Cursor")
	}
}

// TestHandlerIdentityLog_ExportAuditEventsV2_CSVLastPageOmitsCursor proves
// the terminal page (fewer rows than the limit) does NOT advertise a next
// cursor — otherwise a paging consumer would loop forever re-requesting an
// empty page.
func TestHandlerIdentityLog_ExportAuditEventsV2_CSVLastPageOmitsCursor(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	seedAuditEvent(t, ctx.db, "token.created", "token", 1, -1*time.Minute)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=csv&limit=1000", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Next-Cursor"); got != "" {
		t.Errorf("last page must not set X-Next-Cursor, got %q", got)
	}
}

// TestHandlerIdentityLog_ExportAuditEventsV2_ZeroMatchYieldsEmptyArray
// proves a filter that legitimately matches nothing returns events:[] (not
// a JSON null) so naive frontend consumers don't have to null-guard.
func TestHandlerIdentityLog_ExportAuditEventsV2_ZeroMatchYieldsEmptyArray(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	// Seed one event of a DIFFERENT action so the table isn't empty, only the filter is.
	seedAuditEvent(t, ctx.db, "token.created", "token", 1, -1*time.Minute)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=json&action=redemption.redeemed", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data := body["data"].(map[string]interface{})
	events, ok := data["events"].([]interface{})
	if !ok {
		t.Fatalf("events field must decode as a JSON array even when empty, got %T: %v", data["events"], data["events"])
	}
	if len(events) != 0 {
		t.Errorf("expected zero events for the unmatched filter, got %d", len(events))
	}
}
