package handler

// cov_h5e_convergence_stats_test.go — business-acceptance coverage for the
// remaining DB-failure branches of internal_convergence_stats.go's
// InternalConvergenceStats that the happy-path/users-table-missing tests in
// cov_handler-deep-c_convergence_test.go don't reach: each of the 4 distinct
// COUNT queries (users_linked / tokens_total / tokens_linked /
// tokens_unlimited) has its OWN error branch, and a table-level failure only
// exercises the first ("count users failed"). To reach the later branches
// the earlier queries must succeed while a specific column referenced only
// by the later WHERE clause is missing — a genuine schema-drift failure
// mode, not an impossible application input.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

func h5eConvergenceRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/convergence-stats", InternalConvergenceStats)
	return r
}

// TestInternalConvergenceStats_UsersLinkedColumnMissing_500 drops
// lurus_account_id so users_total succeeds (no WHERE clause touches the
// dropped column) but the users_linked query — the only one filtering on
// it — fails with its own distinct error message.
func TestInternalConvergenceStats_UsersLinkedColumnMissing_500(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropColumn(&repo.User{}, "LurusAccountID"); err != nil {
		t.Fatalf("drop lurus_account_id column: %v", err)
	}

	r := h5eConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error"] != "count linked users failed" {
		t.Errorf("error = %v, want 'count linked users failed'", resp["error"])
	}
}

// TestInternalConvergenceStats_TokensTableMissing_500 covers the
// "count tokens failed" branch: users queries succeed, but the whole tokens
// table is gone.
func TestInternalConvergenceStats_TokensTableMissing_500(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens table: %v", err)
	}

	r := h5eConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error"] != "count tokens failed" {
		t.Errorf("error = %v, want 'count tokens failed'", resp["error"])
	}
}

// TestInternalConvergenceStats_TokensLinkedColumnMissing_500 drops
// identity_account_id so the plain tokens_total count still succeeds but
// the tokens_linked WHERE clause (the only reader of that column) fails.
func TestInternalConvergenceStats_TokensLinkedColumnMissing_500(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropColumn(&repo.Token{}, "IdentityAccountID"); err != nil {
		t.Fatalf("drop identity_account_id column: %v", err)
	}

	r := h5eConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error"] != "count linked tokens failed" {
		t.Errorf("error = %v, want 'count linked tokens failed'", resp["error"])
	}
}

// TestInternalConvergenceStats_TokensUnlimitedColumnMissing_500 drops
// unlimited_quota so everything up to tokens_linked succeeds but the final
// query — the only one filtering on unlimited_quota — fails.
func TestInternalConvergenceStats_TokensUnlimitedColumnMissing_500(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropColumn(&repo.Token{}, "UnlimitedQuota"); err != nil {
		t.Fatalf("drop unlimited_quota column: %v", err)
	}

	r := h5eConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error"] != "count unlimited tokens failed" {
		t.Errorf("error = %v, want 'count unlimited tokens failed'", resp["error"])
	}
}
