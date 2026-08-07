package handler

// cov_h5e_zita_autocreate_test.go — business-acceptance coverage for the
// remaining gaps in zita_bootstrap.go: autoCreateBridgedUser's two Insert
// failure sub-branches (concurrent-race recovery vs. genuine failure) and
// resolveTenantSlug's "tenant row exists but Slug is empty" fallback.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestAutoCreateBridgedUser_ConcurrentRaceLost_ReturnsWinnerRow covers the
// "lost the race" recovery branch: another in-flight bootstrap already
// inserted a row with this exact lurus_account_id (violates the
// uniqueIndex on the column), so the fresh Insert fails, and
// autoCreateBridgedUser must probe by account_id and return the winner
// instead of propagating the constraint error.
func TestAutoCreateBridgedUser_ConcurrentRaceLost_ReturnsWinnerRow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const accountID = int64(919191)
	winner := &repo.User{
		Username: "already-bridged", DisplayName: "Already Bridged", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "already@test.local", TenantId: ctx.TenantID,
		Group: "default", LurusAccountID: func() *int64 { v := accountID; return &v }(),
	}
	if err := ctx.DB.Create(winner).Error; err != nil {
		t.Fatalf("seed race winner: %v", err)
	}

	got, err := autoCreateBridgedUser(accountID)
	if err != nil {
		t.Fatalf("autoCreateBridgedUser: want nil error (race recovery), got %v", err)
	}
	if got == nil || got.Id != winner.Id {
		t.Fatalf("got user id = %v, want the pre-existing winner id %d", got, winner.Id)
	}

	// Must NOT have provisioned a second row for the same account_id.
	var count int64
	if err := ctx.DB.Unscoped().Model(&repo.User{}).Where("lurus_account_id = ?", accountID).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("rows with lurus_account_id=%d = %d, want exactly 1 (no duplicate provisioning)", accountID, count)
	}
}

// TestAutoCreateBridgedUser_GenuineInsertFailure_PropagatesError covers the
// final "not a race, a real failure" branch: the Insert fails for a reason
// UNRELATED to lurus_account_id (a username collision on the derived
// "lurus_<account_id>" name, scoped to the same "default" tenant
// auto-created users always land in), so the account_id probe finds
// nothing and the original error must propagate.
func TestAutoCreateBridgedUser_GenuineInsertFailure_PropagatesError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const accountID = int64(919192)
	collidingUsername := "lurus_919192" // matches fmt.Sprintf("lurus_%d", accountID)
	collision := &repo.User{
		Username: collidingUsername, DisplayName: "Colliding User", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "collide@test.local", TenantId: "default",
		Group: "default", // no LurusAccountID — unrelated to the race-probe key
	}
	if err := ctx.DB.Create(collision).Error; err != nil {
		t.Fatalf("seed username-colliding user: %v", err)
	}

	got, err := autoCreateBridgedUser(accountID)
	if err == nil {
		t.Fatalf("autoCreateBridgedUser: want a genuine error (username collision, no race winner), got user=%+v", got)
	}
	if got != nil {
		t.Errorf("got user = %+v, want nil on genuine failure", got)
	}

	// No row must have been provisioned for this account_id.
	var count int64
	if err := ctx.DB.Unscoped().Model(&repo.User{}).Where("lurus_account_id = ?", accountID).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("rows with lurus_account_id=%d = %d, want 0 (insert must not have partially succeeded)", accountID, count)
	}
}

// TestResolveTenantSlug_TenantRowExistsButSlugEmpty_FallsBackToDefault
// covers the branch where the tenant lookup itself succeeds but the row's
// Slug column is empty — a defensive fallback distinct from the
// lookup-error path (already covered) and the literal-"default" fast path
// (already covered).
func TestResolveTenantSlug_TenantRowExistsButSlugEmpty_FallsBackToDefault(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenant := &repo.Tenant{
		Id: "h5e-empty-slug-tenant", IDPOrgID: "h5e-empty-slug-org",
		Slug: "", Name: "Empty Slug Tenant", Status: 1,
	}
	if err := ctx.DB.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant with empty slug: %v", err)
	}

	got := resolveTenantSlug(tenant.Id)
	if got != "default" {
		t.Errorf("resolveTenantSlug(%q) = %q, want \"default\" (empty-slug fallback)", tenant.Id, got)
	}
}
