package handler

// zita_bootstrap_seat_cap_test.go — L9 (cycle 12): the session-bridge signup
// path must honour tenants.max_users.
//
// The OIDC provisioning path enforces the cap (repo/user_mapping.go:271-277
// calls TenantCanAddUser and returns "tenant has reached maximum user limit").
// Neither bridge path did: an invite code let any number of platform accounts
// land in a tenant whose plan says otherwise, and a switch-issued entitlement
// token did the same through ProvisionV2. The check therefore lives in
// autoCreateBridgedUser — the single insert both callers go through — and
// returns a typed *seatLimitError they both map to 403 TENANT_SEAT_LIMIT.
//
// The count behind the check is rows in `users` (users.tenant_id), not
// user_identity_mappings: this path writes no mapping row, so a mapping-based
// count reads 0 for a bridge-provisioned tenant and the cap would sit there
// unable to fire — see repo.TenantUserSeatCount. The same number now feeds the
// two admin-facing displays (repo.GetTenantStats and GET
// /api/v2/admin/tenants/:id), which is pinned below so the number an admin
// reads can never drift from the ceiling that refuses the next login.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	zita "github.com/hanmahong5-arch/zita-sdk-go"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// enableSeatCapAudit makes the audit trail readable from this test's own DB:
// the tables the writer needs, plus a writer pinned to that DB for the
// duration of the test (pinAuditWriter, audit_writer_pin_test.go).
// governance.AsyncGo is already synchronous for this package (TestMain in
// async_seam_test.go), so a row is durable by the time the request returns.
func enableSeatCapAudit(t *testing.T, ctx *V2TestContext) {
	t.Helper()
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("migrate audit tables: %v", err)
	}
	pinAuditWriter(t, ctx.DB)
}

// seatCapAuditDetails returns the parsed `details` JSON of the single
// auth.failed/tenant audit row, failing the test when the count is not one.
func seatCapAuditDetails(t *testing.T, ctx *V2TestContext) map[string]interface{} {
	t.Helper()
	var events []entity.AuditEvent
	if err := ctx.DB.Where("action = ? AND resource = ?",
		governance.ActionAuthFailed, governance.ResourceTenant).Find(&events).Error; err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit rows with action=%s resource=%s = %d, want exactly 1 — the denial's only durable record",
			governance.ActionAuthFailed, governance.ResourceTenant, len(events))
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Details), &details); err != nil {
		t.Fatalf("parse audit details %q: %v", events[0].Details, err)
	}
	return details
}

// seatLimitDeniedCount reads the current value of the seat-limit outcome
// series. Process-global, so tests compare a delta.
func seatLimitDeniedCount(t *testing.T) float64 {
	t.Helper()
	return testutil.ToFloat64(metrics.TenantGateTotal.WithLabelValues(metrics.TenantGateOutcomeSeatLimitDenied))
}

// TestZitaBootstrap_SeatCapReached_Rejected is the headline lock: a tenant at
// max_users=1 with one user already in it refuses the next invited first
// login, provisions nothing, leaves the one-time invite code unspent, and
// leaves an audit row naming the reason and the tenant.
func TestZitaBootstrap_SeatCapReached_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}
	enableSeatCapAudit(t, ctx)

	tenant := seedSeatCapTenant(t, ctx, "seatcapfull", 1, 1)
	invite, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	const accountID = int64(700501)
	beforeDenied := seatLimitDeniedCount(t)
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

	// The one-time code must survive a refusal. ConsumeTenantInvite is atomic
	// and single-use, so the seat check runs BEFORE it (repo.PendingInviteTenantID
	// peeks at the invited tenant); a caller who hits the ceiling can retry the
	// same link once an admin frees a seat instead of needing a fresh code.
	var persistedInvite repo.TenantInvite
	if err := ctx.DB.Where("id = ?", invite.Id).First(&persistedInvite).Error; err != nil {
		t.Fatalf("readback invite: %v", err)
	}
	if persistedInvite.Status != repo.TenantInviteStatusPending {
		t.Errorf("invite status = %d, want still pending(%d) — a refused login must not burn the code",
			persistedInvite.Status, repo.TenantInviteStatusPending)
	}

	// The audit row is the only durable record an operator has of this denial.
	details := seatCapAuditDetails(t, ctx)
	if details["reason"] != "tenant_seat_limit" {
		t.Errorf("audit reason = %v, want tenant_seat_limit (details=%v)", details["reason"], details)
	}
	if details["tenant_id"] != tenant.Id {
		t.Errorf("audit tenant_id = %v, want %q (details=%v)", details["tenant_id"], tenant.Id, details)
	}
	if got, want := details["max_users"], float64(1); got != want {
		t.Errorf("audit max_users = %v, want %v (details=%v)", got, want, details)
	}

	if after := seatLimitDeniedCount(t); after != beforeDenied+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v — a denial an operator cannot count is invisible",
			metrics.TenantGateOutcomeSeatLimitDenied, after, beforeDenied+1)
	}
}

// TestZitaBootstrap_SeatCapReached_ConsoleShowsTheEnforcedNumber closes the
// console/gate gap: the seat number the admin API reports for the tenant must
// be the number the cap enforced against, on BOTH admin-facing surfaces. They
// used to come from the identity-mapping count, which is 0 for exactly the
// bridge-provisioned users the cap measures — so an admin saw "0 users" next
// to max_users while the next login got 403.
func TestZitaBootstrap_SeatCapReached_ConsoleShowsTheEnforcedNumber(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenant := seedSeatCapTenant(t, ctx, "seatcapshown", 3, 3)

	free, used, limit := repo.TenantHasFreeSeat(tenant.Id)
	if free {
		t.Fatalf("TenantHasFreeSeat(%q) = (true, %d, %d), want the tenant to be full", tenant.Id, used, limit)
	}

	stats, err := repo.GetTenantStats(tenant.Id)
	if err != nil {
		t.Fatalf("GetTenantStats: %v", err)
	}
	if stats.UserCount != used {
		t.Errorf("GetTenantStats(%q).UserCount = %d, want %d — the console must show the number the cap enforced",
			tenant.Id, stats.UserCount, used)
	}

	ctx.Router.GET("/api/v2/admin/tenants/:id", GetTenant)
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/tenants/"+tenant.Id, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET tenant status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := handlerDeployParseBody(t, w)
	data, _ := body["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("no data envelope: %s", w.Body.String())
	}
	if got, want := data["user_count"], float64(used); got != want {
		t.Errorf("GET /api/v2/admin/tenants/%s user_count = %v, want %v — same number as the gate",
			tenant.Id, got, want)
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
	// An admitted login must still spend the code (single-use), otherwise the
	// pre-consumption seat check above would have turned invites reusable.
	var persistedInvite repo.TenantInvite
	if err := ctx.DB.Where("id = ?", invite.Id).First(&persistedInvite).Error; err != nil {
		t.Fatalf("readback invite: %v", err)
	}
	if persistedInvite.Status != repo.TenantInviteStatusConsumed {
		t.Errorf("invite status = %d, want consumed(%d) — a successful login must spend the one-time code",
			persistedInvite.Status, repo.TenantInviteStatusConsumed)
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

// TestProvisionV2_SeatCapReached_Rejected is the second caller: a
// switch-issued entitlement token for a brand-new account hits the same
// ceiling, because the check lives in autoCreateBridgedUser rather than in
// ZitaBootstrap. ProvisionV2 always provisions into "default", so that is the
// tenant this test caps.
func TestProvisionV2_SeatCapReached_Rejected(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)
	enableSeatCapAudit(t, ctx)

	seedSeatCapTenant(t, ctx, "default", 1, 1)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("990501", map[string]string{"plan_code": "cc_pro", "quota": "120000"}))

	beforeDenied := seatLimitDeniedCount(t)
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — the \"default\" tenant is at max_users=1; body=%v", code, resp)
	}
	if resp["error_code"] != "TENANT_SEAT_LIMIT" {
		t.Errorf("error_code = %v, want TENANT_SEAT_LIMIT (not the generic PROVISION_FAILED 500); body=%v",
			resp["error_code"], resp)
	}
	if _, err := repo.GetUserByLurusAccountID(990501); err == nil {
		t.Error("a user was provisioned despite the 403")
	}
	if after := seatLimitDeniedCount(t); after != beforeDenied+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v", metrics.TenantGateOutcomeSeatLimitDenied, after, beforeDenied+1)
	}
	details := seatCapAuditDetails(t, ctx)
	if details["reason"] != "tenant_seat_limit" || details["tenant_id"] != "default" {
		t.Errorf("audit details = %v, want reason=tenant_seat_limit tenant_id=default", details)
	}
}

// TestTenantUserSeatCount_CountsUsersRowsNotMappings is the unit-level half of
// the lock: the seat count must see a bridge-provisioned user, which owns no
// user_identity_mappings row. GetTenantUserCount (mapping-based) is asserted
// alongside so the difference is recorded rather than assumed — it is still
// the number TenantCanAddUser uses on the OIDC provisioning path, which this
// lane deliberately left alone.
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

// TestPendingInviteTenantID_OnlyAnswersForARedeemableCode pins the peek that
// protects the invite code. It must answer for exactly the codes
// ConsumeTenantInvite would accept: anything else has to fall through to the
// ordinary "fall back to default, still log in" path rather than produce a
// seat refusal against a tenant the login was never going to reach.
func TestPendingInviteTenantID_OnlyAnswersForARedeemableCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	tenant := seedSeatCapTenant(t, ctx, "seatcappeek", 10, 0)
	pending, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if got, ok := repo.PendingInviteTenantID(pending.Code); !ok || got != tenant.Id {
		t.Errorf("PendingInviteTenantID(pending) = (%q, %v), want (%q, true)", got, ok, tenant.Id)
	}
	if _, ok := repo.PendingInviteTenantID(""); ok {
		t.Error("PendingInviteTenantID(\"\") answered; an absent code must not resolve to a tenant")
	}
	if _, ok := repo.PendingInviteTenantID("no-such-code"); ok {
		t.Error("PendingInviteTenantID(unknown) answered")
	}

	revoked, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := repo.RevokeTenantInvite(revoked.Id, tenant.Id); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}
	if _, ok := repo.PendingInviteTenantID(revoked.Code); ok {
		t.Error("PendingInviteTenantID(revoked) answered; a revoked code never redeems")
	}

	expired, err := repo.CreateTenantInvite(tenant.Id, ctx.RootUser.Id, time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := ctx.DB.Model(&repo.TenantInvite{}).Where("id = ?", expired.Id).
		Update("expired_time", common.GetTimestamp()-60).Error; err != nil {
		t.Fatalf("age the invite: %v", err)
	}
	if _, ok := repo.PendingInviteTenantID(expired.Code); ok {
		t.Error("PendingInviteTenantID(expired) answered; an expired code never redeems")
	}
}
