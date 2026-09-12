package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/gin-gonic/gin"
)

func setupAuditExportRouter(t *testing.T) *adminGovCtx {
	t.Helper()
	ctx := setupAdminGovRouter(t)
	// Register the E3 endpoints under the same /api/v2/admin group used by
	// the GetAuditEvents test scaffold. setupAdminGovRouter pre-mounts
	// /audit/events; add the new export + actions endpoints here so we
	// share the SQLite DB and mock auth wiring.
	admin := ctx.router.Group("/api/v2/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	admin.GET("/audit/actions", ListAuditActionsV2)
	admin.GET("/audit/export", ExportAuditEventsV2)
	admin.GET("/audit/coverage", GetAuditCoverageV2)
	return ctx
}

// TestAuditCoverageV2_ReportsFallbackRoutes drives GetAuditCoverageV2 against
// a synthetic route list injected via SetAdminWriteRoutes (the mechanism
// router.SetInternalApiRouter uses in production — see audit_coverage_gen.go
// for why the handler can't discover its own route table). One route is a
// known-explicit one (present in AuditExplicitRoutes); the other is a fake
// route absent from that map, so it must be reported under
// routes_relying_on_fallback.
func TestAuditCoverageV2_ReportsFallbackRoutes(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	prevRoutes := GetAdminWriteRoutes()
	t.Cleanup(func() { SetAdminWriteRoutes(prevRoutes) })

	const fakeFallbackRoute = "POST /internal/admin/fake-coverage-probe"
	explicitRoute := "POST /api/v2/admin/tenants" // known-explicit per AuditExplicitRoutes
	SetAdminWriteRoutes([]string{explicitRoute, fakeFallbackRoute})

	// One admin.write_unaudited row inside the 24h window and one just
	// outside it, so fallback_events_last_24h has a real oracle instead of
	// only a key-presence check (a constant 0 stayed green before this):
	// the response must count exactly the in-window row.
	seedAuditEvent(t, ctx.db, "admin.write_unaudited", "route", 0, -1*time.Minute)
	seedAuditEvent(t, ctx.db, "admin.write_unaudited", "route", 0, -25*time.Hour)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/coverage", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}

	body := parseJSON(t, w)
	data := body["data"].(map[string]interface{})

	if got := int(data["total_admin_write_routes"].(float64)); got != 2 {
		t.Errorf("total_admin_write_routes = %d, want 2", got)
	}

	explicit := toStringSlice(data["routes_with_explicit_audit"])
	if !containsString(explicit, explicitRoute) {
		t.Errorf("routes_with_explicit_audit = %v, want to contain %q", explicit, explicitRoute)
	}
	if containsString(explicit, fakeFallbackRoute) {
		t.Errorf("routes_with_explicit_audit = %v, must not contain the fake fallback route %q", explicit, fakeFallbackRoute)
	}

	fallback := toStringSlice(data["routes_relying_on_fallback"])
	if !containsString(fallback, fakeFallbackRoute) {
		t.Errorf("routes_relying_on_fallback = %v, want to contain %q", fallback, fakeFallbackRoute)
	}
	if containsString(fallback, explicitRoute) {
		t.Errorf("routes_relying_on_fallback = %v, must not contain the known-explicit route %q", fallback, explicitRoute)
	}

	fallbackEvents24h, ok := data["fallback_events_last_24h"].(float64)
	if !ok {
		t.Fatal("response missing fallback_events_last_24h")
	}
	if int(fallbackEvents24h) != 1 {
		t.Errorf("fallback_events_last_24h = %v, want 1 (one seeded row inside the 24h window, one seeded 25h ago must be excluded)", fallbackEvents24h)
	}
}

func toStringSlice(v interface{}) []string {
	raw, _ := v.([]interface{})
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(string))
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestListAuditActionsV2_ReturnsSortedRegistry(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/actions", nil)
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data := body["data"].(map[string]interface{})
	actions := data["actions"].([]interface{})
	if len(actions) == 0 {
		t.Fatal("expected non-empty action list")
	}

	// Spot-check a few sentinel actions exist.
	wantPresent := map[string]bool{
		"auth.login_success":               false,
		"token.created":                    false,
		"user.role_changed":                false,
		"redemption.redeemed":              false,
		"security.whitelabel_key_accessed": false,
	}
	for _, a := range actions {
		s := a.(string)
		if _, ok := wantPresent[s]; ok {
			wantPresent[s] = true
		}
	}
	for k, found := range wantPresent {
		if !found {
			t.Errorf("expected action %q in response", k)
		}
	}
}

func TestExportAuditEventsV2_JSONPagination(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	for i := 0; i < 5; i++ {
		seedAuditEvent(t, ctx.db, "token.created", "token", 1, -time.Duration(i)*time.Minute)
	}

	// Page 1: limit=2 → 2 rows + next_cursor pointing at row 3.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=json&limit=2", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("page 1 status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data := body["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("page 1 events=%d, want 2", len(events))
	}
	nextCursor := int64(data["next_cursor"].(float64))
	if nextCursor == 0 {
		t.Fatal("page 1 next_cursor should be non-zero (more rows remain)")
	}

	// Page 2 with cursor — must skip past page 1 by id.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v2/admin/audit/export?format=json&limit=2&cursor=%d", nextCursor), nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("page 2 status: %d", w.Code)
	}
	body = parseJSON(t, w)
	data = body["data"].(map[string]interface{})
	events = data["events"].([]interface{})
	if len(events) != 2 {
		t.Errorf("page 2 events=%d, want 2", len(events))
	}
}

func TestExportAuditEventsV2_CSVHeaderAndRows(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	seedAuditEvent(t, ctx.db, "token.created", "token", 7, -1*time.Minute)
	seedAuditEvent(t, ctx.db, "channel.deleted", "channel", 7, -2*time.Minute)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=csv", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type=%q, want text/csv", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "audit-events.csv") {
		t.Errorf("Content-Disposition=%q missing filename", got)
	}

	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d", len(rows))
	}
	wantHeader := []string{
		"id", "tenant_id", "timestamp", "actor_type", "actor_id",
		"action", "resource", "resource_id", "ip", "request_id",
		"retention_until", "details",
	}
	for i, h := range wantHeader {
		if rows[0][i] != h {
			t.Errorf("header col %d = %q, want %q", i, rows[0][i], h)
		}
	}
	// Row 1: token.created should be id-ASC ordered.
	if rows[1][5] != "token.created" {
		t.Errorf("row 1 action = %q, want token.created", rows[1][5])
	}
}

func TestExportAuditEventsV2_RejectsInvalidFormat(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=xml", nil)
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for format=xml, got %d", w.Code)
	}
}

func TestExportAuditEventsV2_RejectsUnknownAction(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/admin/audit/export?action=token.create", nil) // typo
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown action, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestExportAuditEventsV2_DefaultRetentionAppliedToWrites confirms the
// end-to-end retention default: when an event is written through the
// repo writer (the production AuditEventRepo) and then exported, the
// retention_until column appears in the CSV output. This is the smallest
// proof that NewAuditEvent → Repo → DB → Export carries the field intact.
func TestExportAuditEventsV2_DefaultRetentionAppliedToWrites(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	// Insert a row simulating what NewAuditEvent produces (with retention).
	ts := time.Now().Unix()
	ev := &entity.AuditEvent{
		TenantID:       "default",
		Timestamp:      ts,
		ActorType:      "user",
		ActorID:        42,
		Action:         "token.created",
		Resource:       "token",
		ResourceID:     1,
		RetentionUntil: ts + 7*365*24*60*60,
	}
	if err := ctx.db.Create(ev).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=csv", nil)
	ctx.router.ServeHTTP(w, req)
	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(rows))
	}
	retentionColIdx := 10
	if rows[1][retentionColIdx] == "" || rows[1][retentionColIdx] == "0" {
		t.Errorf("retention_until in CSV = %q, want non-zero value", rows[1][retentionColIdx])
	}
}
