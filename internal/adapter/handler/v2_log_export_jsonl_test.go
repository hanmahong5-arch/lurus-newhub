package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// jsonlRouter adds the routes the JSONL export needs on top of the CSV
// fixture: an auth stub whose role comes from X-Mock-Admin, and the admin route.
func jsonlRouter(t *testing.T) (*logExportCtx, *gin.Engine) {
	t.Helper()
	ctx := setupLogExportRouter(t)
	if err := ctx.db.AutoMigrate(&repo.LogBody{}); err != nil {
		t.Fatalf("migrate log_bodies: %v", err)
	}
	r := gin.New()
	auth := func(c *gin.Context) {
		tc := &middleware.TenantContext{TenantID: ctx.tenantID, UserID: ctx.userID, Username: "export-tester"}
		if c.GetHeader("X-Mock-Admin") == "1" {
			tc.Roles = []string{"admin"}
		}
		c.Set("tenant_context", tc)
		c.Set("id", ctx.userID)
		c.Set("tenant_id", ctx.tenantID)
		c.Next()
	}
	r.GET("/api/v2/:tenant_slug/logs/export", auth, ExportLogsV2)
	r.GET("/api/v2/admin/logs/export", ExportAdminLogsV2)
	return ctx, r
}

func seedJSONLLog(t *testing.T, ctx *logExportCtx, tenantID, rid string, keyIdx int64, at int64) int {
	t.Helper()
	l := &repo.Log{
		UserId: ctx.userID, TenantId: tenantID, Type: repo.LogTypeConsume,
		ModelName: "model-a", TokenName: "tok", PromptTokens: 1, CompletionTokens: 2,
		Quota: 3, ChargedCNY4: 12500, CreatedAt: at, ChannelKeyIdx: &keyIdx,
		Other: fmt.Sprintf(`{"request_id":%q}`, rid),
	}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	return l.Id
}

func seedJSONLBody(t *testing.T, ctx *logExportCtx, tenantID, rid, reqBody string) {
	t.Helper()
	b := &repo.LogBody{
		RequestId: rid, TenantId: tenantID, Model: "model-a", RequestBody: reqBody,
		ResponseText: "resp-" + rid, ResponseCaptured: true,
		CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 3600,
	}
	if err := ctx.db.Create(b).Error; err != nil {
		t.Fatalf("seed body: %v", err)
	}
}

func getJSONL(r *gin.Engine, path string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if admin {
		req.Header.Set("X-Mock-Admin", "1")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeLines(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(w.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line is not JSON: %v (%s)", err, sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func TestV2LogExportJSONL_CursorPagesCoverEveryRowOnce(t *testing.T) {
	ctx, r := jsonlRouter(t)
	for i := 0; i < 5; i++ {
		idx := int64(-1)
		if i == 2 {
			idx = 7
		}
		// Descending created_at vs ascending id: paging must follow id.
		seedJSONLLog(t, ctx, ctx.tenantID, fmt.Sprintf("req-%d", i), idx, int64(1_700_000_100-i))
	}
	base := "/api/v2/" + ctx.tenantSlug + "/logs/export?format=jsonl&limit=2"
	var all []map[string]any
	cursor := ""
	for page := 0; page < 5; page++ {
		path := base
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := getJSONL(r, path, false)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
			t.Fatalf("content-type %q", ct)
		}
		rows := decodeLines(t, w)
		all = append(all, rows...)
		if len(rows) < 2 {
			break
		}
		cursor = fmt.Sprintf("%d", int(rows[len(rows)-1]["id"].(float64)))
	}
	if len(all) != 5 {
		t.Fatalf("collected %d rows across pages, want 5 (no skip, no repeat)", len(all))
	}
	prev := 0.0
	for i, row := range all {
		id := row["id"].(float64)
		if id <= prev {
			t.Fatalf("row %d id %v not ascending after %v", i, id, prev)
		}
		prev = id
		if row["request_id"] != fmt.Sprintf("req-%d", i) {
			t.Errorf("row %d request_id = %v", i, row["request_id"])
		}
		if row["charged_cny"] != "1.2500" {
			t.Errorf("charged_cny = %v", row["charged_cny"])
		}
		if _, has := row["body"]; has {
			t.Errorf("body present without include_body")
		}
		if i == 2 {
			if row["channel_key_idx"] != float64(7) {
				t.Errorf("channel_key_idx = %v, want 7", row["channel_key_idx"])
			}
		} else if row["channel_key_idx"] != nil {
			t.Errorf("row %d channel_key_idx = %v, want null", i, row["channel_key_idx"])
		}
	}
}

func TestV2LogExportJSONL_IncludeBody(t *testing.T) {
	ctx, r := jsonlRouter(t)
	seedJSONLLog(t, ctx, ctx.tenantID, "has-body", -1, 1_700_000_001)
	seedJSONLLog(t, ctx, ctx.tenantID, "no-body", -1, 1_700_000_002)
	seedJSONLLog(t, ctx, ctx.tenantID, "foreign", -1, 1_700_000_003)
	seedJSONLBody(t, ctx, ctx.tenantID, "has-body", `{"p":"mine"}`)
	// Same request id archived under another tenant must never attach.
	seedJSONLBody(t, ctx, ctx.otherTenant.Id, "foreign", `{"p":"theirs"}`)
	path := "/api/v2/" + ctx.tenantSlug + "/logs/export?format=jsonl&include_body=true"

	if w := getJSONL(r, path, false); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin include_body = %d, want 403", w.Code)
	}
	w := getJSONL(r, path, true)
	if w.Code != 200 {
		t.Fatalf("admin include_body = %d: %s", w.Code, w.Body.String())
	}
	got := map[string]map[string]any{}
	for _, row := range decodeLines(t, w) {
		got[row["request_id"].(string)] = row
	}
	body, _ := got["has-body"]["body"].(map[string]any)
	if body == nil || body["request_body"] != `{"p":"mine"}` || body["response_text"] != "resp-has-body" {
		t.Fatalf("has-body row body = %v", got["has-body"]["body"])
	}
	if _, has := got["no-body"]["body"]; has {
		t.Errorf("row without archive carries a body")
	}
	if _, has := got["foreign"]["body"]; has {
		t.Errorf("another tenant's body leaked into this tenant's export")
	}
	// Without the flag an admin still gets no body.
	w = getJSONL(r, "/api/v2/"+ctx.tenantSlug+"/logs/export?format=jsonl", true)
	for _, row := range decodeLines(t, w) {
		if _, has := row["body"]; has {
			t.Fatalf("body attached without include_body")
		}
	}
}

func TestV2LogExportJSONL_BadParams(t *testing.T) {
	ctx, r := jsonlRouter(t)
	base := "/api/v2/" + ctx.tenantSlug + "/logs/export"
	for name, q := range map[string]string{
		"unknown format":         "?format=xml",
		"include_body with csv":  "?include_body=true",
		"negative cursor":        "?format=jsonl&cursor=-3",
		"non-numeric cursor":     "?format=jsonl&cursor=abc",
		"include_body csv exact": "?format=csv&include_body=true",
	} {
		if w := getJSONL(r, base+q, true); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}
}

func TestV2LogExportJSONL_CSVUnchangedByDefault(t *testing.T) {
	ctx, r := jsonlRouter(t)
	seedJSONLLog(t, ctx, ctx.tenantID, "r", -1, 1_700_000_001)
	w := getJSONL(r, "/api/v2/"+ctx.tenantSlug+"/logs/export", false)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("default format is not csv: %q", w.Header().Get("Content-Type"))
	}
}

func TestV2LogExportJSONL_AdminSpansTenantsAndAttachesPerTenantBody(t *testing.T) {
	ctx, r := jsonlRouter(t)
	seedJSONLLog(t, ctx, ctx.tenantID, "a", -1, 1_700_000_001)
	seedJSONLLog(t, ctx, ctx.otherTenant.Id, "b", 4, 1_700_000_002)
	seedJSONLBody(t, ctx, ctx.otherTenant.Id, "b", "other-body")

	w := getJSONL(r, "/api/v2/admin/logs/export?format=jsonl&include_body=true", false)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Total-Matched") != "2" {
		t.Errorf("X-Total-Matched = %q", w.Header().Get("X-Total-Matched"))
	}
	rows := decodeLines(t, w)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if _, has := rows[0]["body"]; has {
		t.Errorf("row a has no archive but carries a body")
	}
	if b, _ := rows[1]["body"].(map[string]any); b == nil || b["request_body"] != "other-body" {
		t.Errorf("row b body = %v", rows[1]["body"])
	}
	if rows[1]["channel_key_idx"] != float64(4) || rows[1]["tenant_id"] != ctx.otherTenant.Id {
		t.Errorf("row b = %v", rows[1])
	}
	if _, has := rows[1]["content"]; has {
		t.Errorf("admin export must not expose content")
	}
	// Cursor resumes after row a.
	first := int(rows[0]["id"].(float64))
	w = getJSONL(r, fmt.Sprintf("/api/v2/admin/logs/export?format=jsonl&cursor=%d", first), false)
	if got := decodeLines(t, w); len(got) != 1 || got[0]["request_id"] != "b" {
		t.Errorf("cursor page = %v", got)
	}
}
