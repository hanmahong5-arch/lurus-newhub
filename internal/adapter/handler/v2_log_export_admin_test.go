package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupAdminLogExportRouter(t *testing.T) *adminGovCtx {
	t.Helper()
	ctx := setupAdminGovRouter(t)
	admin := ctx.router.Group("/api/v2/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	admin.GET("/logs/export", ExportAdminLogsV2)
	return ctx
}

func seedAdminExportLog(t *testing.T, db *gorm.DB, l *entity.Log) {
	t.Helper()
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed export log: %v", err)
	}
}

func TestExportAdminLogsV2_CSVHeaderRowsAndEscaping(t *testing.T) {
	ctx := setupAdminLogExportRouter(t)
	defer ctx.cleanup()

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	trickyToken := `tok "quoted", with comma`
	seedAdminExportLog(t, ctx.db, &entity.Log{
		UserId: 3, TenantId: "test-tenant", CreatedAt: base,
		Type: entity.LogTypeConsume, ModelName: "m-1",
		Username: "alice", TokenName: trickyToken,
		PromptTokens: 10, CompletionTokens: 2, Quota: 5,
		UseTime: 3, TotalLatencyMs: 120, IsStream: true,
		ChannelId: 7, Group: "default", Ip: "1.2.3.4",
		Content: "SECRET-PROMPT-CONTENT",
		Other:   `{"admin_info":"internal"}`,
	})
	seedAdminExportLog(t, ctx.db, &entity.Log{
		UserId: 4, TenantId: "test-tenant", CreatedAt: base + 60,
		Type: entity.LogTypeError, ModelName: "m-2",
		Username: "bob", TokenName: "plain",
	})
	// Different tenant — must be excluded by the tenant filter.
	seedAdminExportLog(t, ctx.db, &entity.Log{
		UserId: 5, TenantId: "other-tenant", CreatedAt: base + 120,
		Type: entity.LogTypeConsume, ModelName: "m-3",
	})

	w := doGET(ctx.router, "/api/v2/admin/logs/export?tenant_id=test-tenant")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type=%q, want text/csv", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "admin-logs-") {
		t.Errorf("Content-Disposition=%q missing admin-logs filename", got)
	}
	if got := w.Header().Get("X-Truncated"); got != "" {
		t.Errorf("X-Truncated=%q, want unset (2 rows < cap)", got)
	}
	if got := w.Header().Get("X-Total-Matched"); got != "2" {
		t.Errorf("X-Total-Matched=%q, want 2", got)
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
		"id", "created_at", "tenant_id", "user_id", "username", "log_type",
		"model_name", "upstream_model", "token_name", "prompt_tokens",
		"completion_tokens", "quota", "use_time", "total_latency_ms",
		"is_stream", "channel_id", "group", "ip",
	}
	if len(rows[0]) != len(wantHeader) {
		t.Fatalf("header has %d cols, want %d", len(rows[0]), len(wantHeader))
	}
	for i, h := range wantHeader {
		if rows[0][i] != h {
			t.Errorf("header col %d = %q, want %q", i, rows[0][i], h)
		}
	}

	// Rows come back id-ASC: row 1 = alice's consume log with exact values.
	r1 := rows[1]
	want1 := map[int]string{
		1:  "2026-07-01T12:00:00Z", // created_at RFC3339
		2:  "test-tenant",
		3:  "3",
		4:  "alice",
		5:  "2", // consume
		6:  "m-1",
		8:  trickyToken, // csv round-trips the quotes/comma intact
		9:  "10",
		10: "2",
		11: "5",
		12: "3",
		13: "120",
		14: "true",
		15: "7",
		16: "default",
		17: "1.2.3.4",
	}
	for col, want := range want1 {
		if r1[col] != want {
			t.Errorf("row1 col %d (%s) = %q, want %q", col, wantHeader[col], r1[col], want)
		}
	}
	if rows[2][4] != "bob" || rows[2][5] != "5" || rows[2][6] != "m-2" {
		t.Errorf("row2 = %v, want bob's error log for m-2", rows[2])
	}

	// Sensitive fields must never leak: no content/other columns, no values.
	body := w.Body.String()
	if strings.Contains(body, "SECRET-PROMPT-CONTENT") {
		t.Error("CSV leaked log content")
	}
	if strings.Contains(body, "admin_info") {
		t.Error("CSV leaked the other/internal JSON blob")
	}
	for _, forbidden := range []string{"content", "other", "key"} {
		for _, h := range rows[0] {
			if h == forbidden {
				t.Errorf("header contains forbidden column %q", forbidden)
			}
		}
	}
}

func TestExportAdminLogsV2_TruncationCapAndHeader(t *testing.T) {
	ctx := setupAdminLogExportRouter(t)
	defer ctx.cleanup()

	base := time.Now().Unix()
	for i := 0; i < 5; i++ {
		seedAdminExportLog(t, ctx.db, &entity.Log{
			UserId: 1, TenantId: "test-tenant", CreatedAt: base + int64(i),
			Type: entity.LogTypeConsume, ModelName: fmt.Sprintf("m-%d", i),
		})
	}

	w := doGET(ctx.router, "/api/v2/admin/logs/export?max_rows=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Truncated"); got != "true" {
		t.Errorf("X-Truncated=%q, want true (5 matched > cap 2)", got)
	}
	if got := w.Header().Get("X-Total-Matched"); got != "5" {
		t.Errorf("X-Total-Matched=%q, want 5", got)
	}

	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows (capped), got %d", len(rows))
	}
	// id-ASC → the two oldest-inserted rows.
	if rows[1][6] != "m-0" || rows[2][6] != "m-1" {
		t.Errorf("capped rows = %q,%q, want m-0,m-1", rows[1][6], rows[2][6])
	}
}

func TestExportAdminLogsV2_TimeRangeFilter(t *testing.T) {
	ctx := setupAdminLogExportRouter(t)
	defer ctx.cleanup()

	base := int64(1_760_000_000)
	for i, ts := range []int64{base, base + 100, base + 200} {
		seedAdminExportLog(t, ctx.db, &entity.Log{
			UserId: 1, TenantId: "test-tenant", CreatedAt: ts,
			Type: entity.LogTypeConsume, ModelName: fmt.Sprintf("t-%d", i),
		})
	}

	w := doGET(ctx.router, fmt.Sprintf(
		"/api/v2/admin/logs/export?start_time=%d&end_time=%d", base+50, base+150))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	reader := csv.NewReader(strings.NewReader(w.Body.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row in window, got %d", len(rows))
	}
	if rows[1][6] != "t-1" {
		t.Errorf("windowed row model = %q, want t-1", rows[1][6])
	}
}

func TestExportAdminLogsV2_RejectsNonCSVFormat(t *testing.T) {
	ctx := setupAdminLogExportRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/admin/logs/export?format=json")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for format=json, got %d", w.Code)
	}
}

// TestExportAdminLogsV2_UnauthenticatedRejected mounts the production
// RootJWTAuth middleware (same wiring as api-v2-router.go's adminRoute) and
// verifies an anonymous request never reaches the handler.
func TestExportAdminLogsV2_UnauthenticatedRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("log-export-auth-test-secret"))
	router.Use(sessions.Sessions("session", store))
	admin := router.Group("/api/v2/admin")
	admin.Use(middleware.RootJWTAuth())
	admin.GET("/logs/export", ExportAdminLogsV2)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/logs/export", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for anonymous request, got %d body=%s", w.Code, w.Body.String())
	}
}
