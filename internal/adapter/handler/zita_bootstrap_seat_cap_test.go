package handler

// zita_bootstrap_seat_cap_test.go — L9 (cycle 12): the session-bridge signup
// path must honour tenants.max_users.
//
// The OIDC provisioning path enforces the cap (repo/user_mapping.go:271-277
// calls TenantCanAddUser and returns "tenant has reached maximum user limit").
// ZitaBootstrap's auto-create branch did not:
// an invite code let any number of platform accounts land in a tenant whose
// plan says otherwise, and the console's own tenant stats then report a user
// count above the limit it displays next to it.
//
// The count behind the check is rows in `users` (users.tenant_id), not
// user_identity_mappings: this path writes no mapping row, so a mapping-based
// count reads 0 for a bridge-provisioned tenant and the cap would sit there
// unable to fire — see repo.TenantUserSeatCount.

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// seedSeatCapTenant creates a tenant with an explicit max_users and fills it
// with `occupants` users, then returns the tenant.
func seedSeatCapTenant(t *testing.T, ctx *V2TestContext, id string, maxUsers, occupants int) *repo.Tenant {
	t.Helper()
	tenant := &repo.Tenant{
		Id:        id,
		Slug:      id,
		Name:      "Seat Cap " + id,
		IDPOrgID:  "org_" + id,
		Status:    repo.TenantStatusEnabled,
		MaxUsers:  maxUsers,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
	for i := 0; i < occupants; i++ {
		occupant := &repo.User{
			Username:    id + "-occupant-" + string(rune('a'+i)),
			DisplayName: "Seat Occupant",
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			TenantId:    tenant.Id,
			Group:       "default",
		}
		if err := ctx.DB.Create(occupant).Error; err != nil {
			t.Fatalf("seed occupant %d: %v", i, err)
		}
	}
	return tenant
}

// TestZitaBootstrap_SeatCapReached_Rejected is the headline lock: a tenant at
// max_users=1 with one user already in it refuses the next invited first
// login, and provisions nothing.
func TestZitaBootstrap_SeatCapReached_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	tenant := seedSeatCapTenant(t, ctx, "seatcapfull", 1, 1)
	invite, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	const accountID = int64(700501)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — tenant %q is at its max_users=1 ceiling; body=%s",
			w.Code, tenant.Id, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error_code"] != "TENANT_SEAT_LIMIT" {
		t.Errorf("error_code = %v, want TENANT_SEAT_LIMIT; body=%s", resp["error_code"], w.Body.String())
	}

	var persisted repo.User
	if err := ctx.DB.Unscoped().Where("lurus_account_id = ?", accountID).First(&persisted).Error; err == nil {
		t.Errorf("user %d was provisioned into %q despite the 403", persisted.Id, persisted.TenantId)
	}

	// Recorded, not endorsed: the invite is consumed before the seat check can
	// know which tenant to measure (see the ordering note in ZitaBootstrap —
	// ConsumeTenantInvite is what resolves a code to its tenant, atomically and
	// single-use). A caller who hits the ceiling burns the code and needs a new
	// one from the admin.
	var persistedInvite repo.TenantInvite
	if err := ctx.DB.Where("id = ?", invite.Id).First(&persistedInvite).Error; err != nil {
		t.Fatalf("readback invite: %v", err)
	}
	if persistedInvite.Status != repo.TenantInviteStatusConsumed {
		t.Errorf("invite status = %d, want consumed(%d) — pinning the burn so a future change to it is deliberate",
			persistedInvite.Status, repo.TenantInviteStatusConsumed)
	}
}

// TestZitaBootstrap_SeatAvailable_Admitted is the negative control: one seat
// below the ceiling still logs in and lands in the invited tenant. Without it
// the guard could be satisfied by rejecting every invited first login.
func TestZitaBootstrap_SeatAvailable_Admitted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	tenant := seedSeatCapTenant(t, ctx, "seatcapfree", 2, 1)
	invite, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	const accountID = int64(700502)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — tenant %q has a free seat; body=%s", w.Code, tenant.Id, w.Body.String())
	}
	var persisted repo.User
	if err := ctx.DB.Unscoped().Where("lurus_account_id = ?", accountID).First(&persisted).Error; err != nil {
		t.Fatalf("expected auto-created user: %v", err)
	}
	if persisted.TenantId != tenant.Id {
		t.Errorf("persisted TenantId = %q, want %q", persisted.TenantId, tenant.Id)
	}
}

// TestZitaBootstrap_TenantRowMissing_Admitted pins the fail-open direction:
// the "default" tenant has no row on some deployments, and a login must not
// depend on one existing.
func TestZitaBootstrap_TenantRowMissing_Admitted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const accountID = int64(700503)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrap(r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — no tenant row means no configured ceiling; body=%s", w.Code, w.Body.String())
	}
}

// TestTenantUserSeatCount_CountsUsersRowsNotMappings is the unit-level half of
// the lock: the seat count must see a bridge-provisioned user, which owns no
// user_identity_mappings row. GetTenantUserCount (mapping-based) is asserted
// alongside so the difference is recorded rather than assumed.
func TestTenantUserSeatCount_CountsUsersRowsNotMappings(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenant := seedSeatCapTenant(t, ctx, "seatcapcount", 10, 3)

	seats, err := repo.TenantUserSeatCount(tenant.Id)
	if err != nil {
		t.Fatalf("TenantUserSeatCount: %v", err)
	}
	if seats != 3 {
		t.Errorf("TenantUserSeatCount(%q) = %d, want 3", tenant.Id, seats)
	}

	mappings, err := repo.GetTenantUserCount(tenant.Id)
	if err != nil {
		t.Fatalf("GetTenantUserCount: %v", err)
	}
	if mappings != 0 {
		t.Errorf("GetTenantUserCount(%q) = %d, want 0 — these three users have no identity mapping, which is why the seat check cannot use it",
			tenant.Id, mappings)
	}
}
