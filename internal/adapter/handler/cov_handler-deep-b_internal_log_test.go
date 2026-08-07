package handler

// cov_handler-deep-b_internal_log_test.go — closes the remaining gaps in
// internal_log_api.go left by cover_uplift_internal_test.go (which already
// covers the happy paths, the malformed-:id 400 branch, and the
// nonexistent-user/token empty-result cases): the page/per_page clamp
// bounds and the three repo-query DB-error branches (dropped logs table —
// a genuine DB failure, not a fabricated input).
//
// Reuses SetupV2TestRouter / doInternalGet / idStr from
// cover_uplift_internal_test.go (same package).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// ─── InternalGetUserLogs: page/per_page clamp bounds ──────────────────────

func TestInternalGetUserLogs_PageClampBounds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	for i := 0; i < 3; i++ {
		if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4o", CreatedAt: int64(1700000000 + i)}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	cases := []struct {
		name        string
		query       string
		wantPage    float64
		wantPerPage float64
	}{
		{"page_zero_clamps_to_1", "?page=0", 1, 20},
		{"page_negative_clamps_to_1", "?page=-3", 1, 20},
		{"per_page_zero_clamps_to_20", "?per_page=0", 1, 20},
		{"per_page_over_100_clamps_to_20", "?per_page=999", 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doInternalGet(r, "/internal/log/user/"+idStr(ctx.NormalUser.Id)+tc.query)
			resp := ParseV2Response(t, w)
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
			}
			if resp["page"].(float64) != tc.wantPage {
				t.Errorf("page = %v, want %v", resp["page"], tc.wantPage)
			}
			if resp["per_page"].(float64) != tc.wantPerPage {
				t.Errorf("per_page = %v, want %v", resp["per_page"], tc.wantPerPage)
			}
		})
	}
}

func TestInternalGetUserLogs_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	w := doInternalGet(r, "/internal/log/user/"+idStr(ctx.NormalUser.Id))
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

// ─── InternalGetUserLogStat: DB-error branch ───────────────────────────────

func TestInternalGetUserLogStat_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	r := gin.New()
	r.GET("/internal/log/user/:id/stat", InternalGetUserLogStat)

	w := doInternalGet(r, "/internal/log/user/"+idStr(ctx.NormalUser.Id)+"/stat")
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

// ─── InternalGetTokenLogs: page/per_page clamp bounds + DB-error branch ───

func TestInternalGetTokenLogs_PageClampBounds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "internal-token-logs-target")
	for i := 0; i < 3; i++ {
		if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4o", CreatedAt: int64(1700000000 + i)}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	r := gin.New()
	r.GET("/internal/log/token/:token_id", InternalGetTokenLogs)

	cases := []struct {
		name        string
		query       string
		wantPage    float64
		wantPerPage float64
	}{
		{"page_zero_clamps_to_1", "?page=0", 1, 20},
		{"per_page_over_100_clamps_to_20", "?per_page=500", 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doInternalGet(r, "/internal/log/token/"+idStr(tok.Id)+tc.query)
			resp := ParseV2Response(t, w)
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
			}
			if resp["page"].(float64) != tc.wantPage {
				t.Errorf("page = %v, want %v", resp["page"], tc.wantPage)
			}
			if resp["per_page"].(float64) != tc.wantPerPage {
				t.Errorf("per_page = %v, want %v", resp["per_page"], tc.wantPerPage)
			}
		})
	}
}

func TestInternalGetTokenLogs_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "internal-token-logs-dberr")
	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	r := gin.New()
	r.GET("/internal/log/token/:token_id", InternalGetTokenLogs)

	w := doInternalGet(r, "/internal/log/token/"+idStr(tok.Id))
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}
