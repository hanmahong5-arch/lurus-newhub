package handler

// cov_h5d_token_log_edges_test.go — business-acceptance coverage for the
// remaining uncovered branches in v2_token.go, v2_log.go, v2_log_cluster.go
// and v2_log_export_admin.go: genuine DB-write/read failures on the token
// mutation paths (list/delete/batch-delete/rotate — a dropped table or a
// blocked write, never a fabricated nil input), the log-listing pagination
// clamp bounds (page<1 / page_size out of [1,100]) on both the self-scoped
// and tenant-admin log endpoints, the queryInt64 malformed-value fallback,
// and the admin CSV export's zero-match path (statistics must not choke on
// an empty result set).
//
// Reuses SetupV2TestRouter / V2RequestAsUser / SeedV2Token /
// handlerDeepBSetupAdminExportRouter from the package's existing
// *_test.go scaffolding per "先读既有测试...复用脚手架".

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// ─── ListTokensV2: dropped-table read failure ──────────────────────────────

// TestH5DListTokensV2_GetTokensDBError forces a genuine DB read failure (the
// tokens table dropped from under a live connection — schema drift / a
// mid-deploy race) and confirms the handler surfaces 500 instead of a
// silently-empty or garbled 200.
func TestH5DListTokensV2_GetTokensDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "h5d-list-target")

	if err := ctx.DB.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens table: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to retrieve tokens" {
		t.Errorf("message = %v, want 'Failed to retrieve tokens'", resp["message"])
	}
}

// ─── DeleteTokenV2 / DeleteTokensV2: blocked DELETE ────────────────────────

// TestH5DDeleteTokenV2_DeleteDBError installs a BEFORE UPDATE trigger that
// rejects every write to the tokens table (repo.Token soft-deletes via
// gorm.DeletedAt, so a "delete" is actually an UPDATE ... SET deleted_at —
// this models a read-replica-only / FK-constraint failure at the storage
// layer: the ownership-check SELECT still succeeds, the soft-delete UPDATE
// does not) and confirms the handler surfaces 500 without silently
// reporting success.
func TestH5DDeleteTokenV2_DeleteDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "h5d-delete-fail-target")

	if err := ctx.DB.Exec(`CREATE TRIGGER h5d_block_token_delete
		BEFORE UPDATE ON tokens
		BEGIN SELECT RAISE(ABORT, 'h5d simulated delete failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to delete token" {
		t.Errorf("message = %v, want 'Failed to delete token'", resp["message"])
	}

	ctx.DB.Exec(`DROP TRIGGER h5d_block_token_delete`)
	var count int64
	ctx.DB.Model(&repo.Token{}).Where("id = ?", token.Id).Count(&count)
	if count != 1 {
		t.Errorf("token was deleted despite the blocked-DELETE 500: count=%d", count)
	}
}

// TestH5DDeleteTokensV2_BatchDeleteDBError exercises the same blocked-DELETE
// failure mode through the batch-delete path (repo.BatchDeleteTokens runs a
// transaction — the trigger fires inside it and must roll the whole batch
// back cleanly).
func TestH5DDeleteTokensV2_BatchDeleteDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	t1 := SeedV2Token(t, ctx, ctx.NormalUser.Id, "h5d-batch-fail-1")
	t2 := SeedV2Token(t, ctx, ctx.NormalUser.Id, "h5d-batch-fail-2")

	if err := ctx.DB.Exec(`CREATE TRIGGER h5d_block_token_batch_delete
		BEFORE UPDATE ON tokens
		BEGIN SELECT RAISE(ABORT, 'h5d simulated batch delete failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	body := map[string]interface{}{"ids": []int{t1.Id, t2.Id}}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens/batch-delete", body, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to delete tokens" {
		t.Errorf("message = %v, want 'Failed to delete tokens'", resp["message"])
	}

	ctx.DB.Exec(`DROP TRIGGER h5d_block_token_batch_delete`)
	var count int64
	ctx.DB.Model(&repo.Token{}).Where("id IN ?", []int{t1.Id, t2.Id}).Count(&count)
	if count != 2 {
		t.Errorf("batch delete partially applied despite the transactional 500: count=%d, want 2 survivors", count)
	}
}

// ─── RotateTokenV2: blocked UPDATE + idempotent old-key invalidation ──────

// TestH5DRotateTokenV2_RotateKeyDBError installs a BEFORE UPDATE trigger
// blocking writes to the tokens table (ownership-check read still succeeds,
// the key-swap write does not) and confirms 500 without silently returning
// a "new" key that was never actually persisted.
func TestH5DRotateTokenV2_RotateKeyDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "h5d-rotate-fail-target")
	originalKey := token.Key

	if err := ctx.DB.Exec(`CREATE TRIGGER h5d_block_token_rotate_update
		BEFORE UPDATE ON tokens
		BEGIN SELECT RAISE(ABORT, 'h5d simulated rotate failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id) + "/rotate"
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, path, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to rotate token key" {
		t.Errorf("message = %v, want 'Failed to rotate token key'", resp["message"])
	}

	ctx.DB.Exec(`DROP TRIGGER h5d_block_token_rotate_update`)
	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	// The old key must still be the one on record — a 500 that silently
	// invalidated the caller's live key while returning no usable new one
	// would lock the caller out entirely.
	if reloaded.Key != originalKey {
		t.Errorf("key changed despite the blocked-UPDATE 500: got %q, want unchanged %q", reloaded.Key, originalKey)
	}
}

// ─── GetLogsV2 / GetAllLogsV2: pagination clamp bounds ─────────────────────

// TestH5DGetLogsV2_PaginationBoundsClamped covers the page<1 and
// page_size-out-of-[1,100] clamp branches that TestGetLogsV2_Pagination
// (in-bounds only) never exercises.
func TestH5DGetLogsV2_PaginationBoundsClamped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)

	cases := []struct {
		name         string
		query        string
		wantPage     float64
		wantPageSize float64
	}{
		{"page_zero_clamps_to_1", "page=0&page_size=20", 1, 20},
		{"page_negative_clamps_to_1", "page=-7&page_size=20", 1, 20},
		{"page_size_zero_clamps_to_20", "page=1&page_size=0", 1, 20},
		{"page_size_over_100_clamps_to_20", "page=1&page_size=1000", 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs?"+tc.query, nil, nil)
			resp := AssertV2Success(t, w)
			data := resp["data"].(map[string]interface{})
			if data["page"].(float64) != tc.wantPage {
				t.Errorf("page = %v, want %v", data["page"], tc.wantPage)
			}
			if data["page_size"].(float64) != tc.wantPageSize {
				t.Errorf("page_size = %v, want %v", data["page_size"], tc.wantPageSize)
			}
		})
	}
}

// TestH5DGetAllLogsV2_PaginationBoundsClamped is the tenant-admin sibling —
// GetAllLogsV2 parses/clamps page and page_size independently of GetLogsV2,
// so the branch coverage is separate even though the logic reads identically.
func TestH5DGetAllLogsV2_PaginationBoundsClamped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	SeedV2Log(t, ctx, ctx.NormalUser.Id, repo.LogTypeConsume)

	cases := []struct {
		name         string
		query        string
		wantPage     float64
		wantPageSize float64
	}{
		{"page_negative_clamps_to_1", "page=-1&page_size=20", 1, 20},
		{"page_size_negative_clamps_to_20", "page=1&page_size=-5", 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all?"+tc.query, nil, nil)
			resp := AssertV2Success(t, w)
			data := resp["data"].(map[string]interface{})
			if data["page"].(float64) != tc.wantPage {
				t.Errorf("page = %v, want %v", data["page"], tc.wantPage)
			}
			if data["page_size"].(float64) != tc.wantPageSize {
				t.Errorf("page_size = %v, want %v", data["page_size"], tc.wantPageSize)
			}
		})
	}
}

// ─── queryInt64: malformed value falls back to the default ────────────────

// TestH5DQueryInt64_MalformedValueFallsBack proves the unexported query
// helper used by GetLogClusterV2's from/to params rejects a non-numeric
// value by returning the caller-supplied default rather than propagating a
// parse error or panicking (e.g. ?from=not-a-number must behave exactly like
// the param being absent).
func TestH5DQueryInt64_MalformedValueFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x?from=not-a-number&to=", nil)

	if got := queryInt64(c, "from", -1); got != -1 {
		t.Errorf("queryInt64(from=not-a-number, default=-1) = %d, want -1 (fallback)", got)
	}
	if got := queryInt64(c, "to", 42); got != 42 {
		t.Errorf("queryInt64(to=<empty>, default=42) = %d, want 42 (missing param fallback)", got)
	}
	if got := queryInt64(c, "absent", 99); got != 99 {
		t.Errorf("queryInt64(absent, default=99) = %d, want 99", got)
	}
	if got := queryInt64(c, "from", -1); got != -1 {
		// exercised twice on purpose: confirms the malformed branch is stable,
		// not a one-shot side-effect of context state.
		t.Errorf("second read of queryInt64(from=not-a-number) = %d, want -1", got)
	}
}

// ─── ExportAdminLogsV2: zero-match export must not choke ──────────────────

// TestH5DExportAdminLogsV2_ZeroMatches proves the admin CSV export handles
// an empty result set gracefully: header-only CSV body, X-Total-Matched: 0,
// and no X-Truncated flag — the "统计/聚类在零数据时不崩" edge case for the
// compliance-export surface specifically (siblings already cover the
// count-query-error and single-row paths, not the legitimate zero-rows case).
func TestH5DExportAdminLogsV2_ZeroMatches(t *testing.T) {
	adminCtx := handlerDeepBSetupAdminExportRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/logs/export", nil)
	w := httptest.NewRecorder()
	adminCtx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Total-Matched"); got != "0" {
		t.Errorf("X-Total-Matched = %q, want \"0\"", got)
	}
	if got := w.Header().Get("X-Truncated"); got != "" {
		t.Errorf("X-Truncated = %q, want empty on a zero-row export", got)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatal("body is empty — the CSV header row must still be written for a zero-match export")
	}
	// Exactly one line (the header) — no trailing data rows, no crash mid-stream.
	lineCount := 0
	for _, r := range body {
		if r == '\n' {
			lineCount++
		}
	}
	if lineCount != 1 {
		t.Errorf("body has %d newline-terminated lines, want 1 (header only): %q", lineCount, body)
	}
}
