package handler

// cov_handler-deep-c_convergence_test.go — business-acceptance coverage for
// internal_convergence_stats.go's InternalConvergenceStats (41.2% before
// this file): the internal admin endpoint that reports newhub/platform
// linkage counts (users_total/linked, tokens_total/linked/unlimited). This
// file exercises the full success path with real, distinguishable seeded
// rows (so counts must be exactly right, not merely "present") plus a
// genuine DB-failure path via a dropped table.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func handlerDeepCConvergenceRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/convergence-stats", InternalConvergenceStats)
	return r
}

func TestInternalConvergenceStats_CountsReflectSeededRowsExactly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r := handlerDeepCConvergenceRouter()

	// SetupV2TestRouter already seeded 3 users (root/admin/normal), none
	// linked to a platform account. Add one more, linked.
	linkedAccountID := int64(555)
	linkedUser := &repo.User{
		Username: "linked-user", DisplayName: "Linked", Role: 1, Status: 1,
		Email: "linked@test.local", TenantId: ctx.TenantID,
		LurusAccountID: &linkedAccountID,
	}
	if err := ctx.DB.Create(linkedUser).Error; err != nil {
		t.Fatalf("seed linked user: %v", err)
	}

	// Tokens: one linked+capped, one linked+unlimited, one unlinked+unlimited.
	tokens := []repo.Token{
		{UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Key: "k1", Name: "capped-linked",
			IdentityAccountID: 100, UnlimitedQuota: false},
		{UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Key: "k2", Name: "unlimited-linked",
			IdentityAccountID: 200, UnlimitedQuota: true},
		{UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Key: "k3", Name: "unlimited-unlinked",
			IdentityAccountID: 0, UnlimitedQuota: true},
	}
	for i := range tokens {
		if err := ctx.DB.Create(&tokens[i]).Error; err != nil {
			t.Fatalf("seed token %d: %v", i, err)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)

	// 3 pre-seeded (root/admin/normal, unlinked) + 1 linked = 4 total, 1 linked.
	if got := resp["users_total"]; got != float64(4) {
		t.Errorf("users_total = %v, want 4", got)
	}
	if got := resp["users_linked"]; got != float64(1) {
		t.Errorf("users_linked = %v, want 1", got)
	}
	if got := resp["tokens_total"]; got != float64(3) {
		t.Errorf("tokens_total = %v, want 3", got)
	}
	if got := resp["tokens_linked"]; got != float64(2) {
		t.Errorf("tokens_linked = %v, want 2 (identity_account_id > 0)", got)
	}
	if got := resp["tokens_unlimited"]; got != float64(2) {
		t.Errorf("tokens_unlimited = %v, want 2", got)
	}
}

func TestInternalConvergenceStats_NoRows_AllZero(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	// Wipe the 3 users SetupV2TestRouter seeds so the zero-row path is real,
	// not just "small counts happen to be low".
	if err := ctx.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Where("1 = 1").Delete(&repo.User{}).Error; err != nil {
		t.Fatalf("clear users: %v", err)
	}

	r := handlerDeepCConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	resp := handlerDeployParseBody(t, w)
	for _, field := range []string{"users_total", "users_linked", "tokens_total", "tokens_linked", "tokens_unlimited"} {
		if got := resp[field]; got != float64(0) {
			t.Errorf("%s = %v, want 0", field, got)
		}
	}
}

// TestInternalConvergenceStats_UsersTableMissing_500 covers a genuine DB
// failure: with the users table dropped, the FIRST count query must fail
// and short-circuit the handler with a 500 + the users-specific error
// message — never falling through to report zeroed-out token stats as if
// the query had succeeded.
func TestInternalConvergenceStats_UsersTableMissing_500(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.User{}); err != nil {
		t.Fatalf("drop users table: %v", err)
	}

	r := handlerDeepCConvergenceRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/convergence-stats", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error"] != "count users failed" {
		t.Errorf("error = %v, want 'count users failed'", resp["error"])
	}
}
