package handler

// zita_bootstrap_invite_test.go — business-acceptance coverage for the
// tenant-invite consumption path wired into ZitaBootstrap's auto-create
// branch (N2, ledger recon 2026-09-01). Reuses handlerDeepCZitaRouter
// (cov_handler-deep-c_zita_bootstrap_test.go) so the invite param travels
// through the SAME route/middleware chain the other ZitaBootstrap tests use.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

func handlerDeepCDoZitaBootstrapWithInvite(r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, invite string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/zita-bootstrap?invite="+invite, nil)
	r.ServeHTTP(w, req)
	return w
}

// TestZitaBootstrap_ValidInvite_NewUserLandsInInvitedTenant_ConsumesCode is
// the happy path: a brand-new bridge login carrying ?invite=<valid code>
// must land in the INVITED tenant (not "default"), and the code must be
// consumed (single-use) as a result.
func TestZitaBootstrap_ValidInvite_NewUserLandsInInvitedTenant_ConsumesCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	invitedTenant := &repo.Tenant{
		Id: "invited-tenant", Name: "Invited Co", Slug: "invited-co",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_invited",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(invitedTenant).Error; err != nil {
		t.Fatalf("seed invited tenant: %v", err)
	}
	invite, err := repo.CreateTenantInvite(invitedTenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	const accountID = int64(700001)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	if data["tenant_slug"] != "invited-co" {
		t.Errorf("tenant_slug = %v, want invited-co (invite must route the new user into its tenant)", data["tenant_slug"])
	}

	var persisted repo.User
	if err := ctx.DB.Unscoped().Where("lurus_account_id = ?", accountID).First(&persisted).Error; err != nil {
		t.Fatalf("expected auto-created user: %v", err)
	}
	if persisted.TenantId != invitedTenant.Id {
		t.Errorf("persisted TenantId = %q, want %q", persisted.TenantId, invitedTenant.Id)
	}

	var persistedInvite repo.TenantInvite
	if err := ctx.DB.Where("id = ?", invite.Id).First(&persistedInvite).Error; err != nil {
		t.Fatalf("readback invite: %v", err)
	}
	if persistedInvite.Status != repo.TenantInviteStatusConsumed {
		t.Errorf("invite status = %d, want consumed(%d) — code must be single-use", persistedInvite.Status, repo.TenantInviteStatusConsumed)
	}
}

// TestZitaBootstrap_ReplayedInvite_SecondNewUserFallsBackToDefault: the
// invite from the happy-path test above is single-use — a DIFFERENT
// brand-new account presenting the SAME already-consumed code must fall
// back to "default", not land in the (already claimed) invited tenant.
func TestZitaBootstrap_ReplayedInvite_SecondNewUserFallsBackToDefault(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	invitedTenant := &repo.Tenant{
		Id: "invited-tenant-2", Name: "Invited Co 2", Slug: "invited-co-2",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_invited_2",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(invitedTenant).Error; err != nil {
		t.Fatalf("seed invited tenant: %v", err)
	}
	invite, err := repo.CreateTenantInvite(invitedTenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// First consumer wins the code.
	if _, err := repo.ConsumeTenantInvite(invite.Code, 1); err != nil {
		t.Fatalf("pre-consume by first winner: %v", err)
	}

	const accountID = int64(700002)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (replay must never fail the login), body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if data["tenant_slug"] != "default" {
		t.Errorf("tenant_slug = %v, want default (replayed code must not re-grant the invited tenant)", data["tenant_slug"])
	}

	var persisted repo.User
	if err := ctx.DB.Unscoped().Where("lurus_account_id = ?", accountID).First(&persisted).Error; err != nil {
		t.Fatalf("expected auto-created user: %v", err)
	}
	if persisted.TenantId != "default" {
		t.Errorf("persisted TenantId = %q, want default", persisted.TenantId)
	}
}

// TestZitaBootstrap_ExpiredInvite_NewUserFallsBackToDefault pins the
// fail-safe explicitly: an expired code must never block a login and must
// never grant the invited tenant, HTTP 200 throughout.
func TestZitaBootstrap_ExpiredInvite_NewUserFallsBackToDefault(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	invitedTenant := &repo.Tenant{
		Id: "invited-tenant-3", Name: "Invited Co 3", Slug: "invited-co-3",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_invited_3",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(invitedTenant).Error; err != nil {
		t.Fatalf("seed invited tenant: %v", err)
	}
	invite, err := repo.CreateTenantInvite(invitedTenant.Id, ctx.RootUser.Id, time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := ctx.DB.Model(&repo.TenantInvite{}).Where("id = ?", invite.Id).
		Update("expired_time", 1).Error; err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	const accountID = int64(700003)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if data["tenant_slug"] != "default" {
		t.Errorf("tenant_slug = %v, want default (expired code must fall back)", data["tenant_slug"])
	}
}

// TestZitaBootstrap_GarbageInvite_NewUserFallsBackToDefault covers an
// unknown/garbage code (never issued) — same fail-safe, HTTP 200.
func TestZitaBootstrap_GarbageInvite_NewUserFallsBackToDefault(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	const accountID = int64(700004)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, "not-a-real-code-at-all")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if data["tenant_slug"] != "default" {
		t.Errorf("tenant_slug = %v, want default (garbage code must fall back, never 500)", data["tenant_slug"])
	}
}

// TestZitaBootstrap_ExistingUser_InviteCookiePresent_TenantUnchanged pins
// "existing users' tenant is NEVER changed by an invite": a user already
// bridged to one tenant, presenting a VALID invite for a DIFFERENT tenant on
// a repeat login, must keep their original tenant untouched — the invite
// param is only consulted on the auto-create (first-login) branch.
func TestZitaBootstrap_ExistingUser_InviteCookiePresent_TenantUnchanged(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.TenantInvite{}); err != nil {
		t.Fatalf("migrate TenantInvite: %v", err)
	}

	otherTenant := &repo.Tenant{
		Id: "other-tenant-invite-test", Name: "Other Co", Slug: "other-co",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_other_invite",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(otherTenant).Error; err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}
	invite, err := repo.CreateTenantInvite(otherTenant.Id, ctx.RootUser.Id, 0)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	const accountID = int64(700005)
	existing := &repo.User{
		Username: "zita-existing-invite-test", DisplayName: "Existing", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "existing-invite@test.local", TenantId: ctx.TenantID,
		Group: "default", LurusAccountID: func() *int64 { v := accountID; return &v }(),
	}
	if err := ctx.DB.Create(existing).Error; err != nil {
		t.Fatalf("seed existing bridged user: %v", err)
	}

	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrapWithInvite(r, invite.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var persisted repo.User
	if err := ctx.DB.Where("id = ?", existing.Id).First(&persisted).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if persisted.TenantId != ctx.TenantID {
		t.Errorf("existing user TenantId = %q, want unchanged %q (invite must not re-tenant a repeat login)", persisted.TenantId, ctx.TenantID)
	}

	// The invite must still be untouched/pending — a repeat login must never
	// spend someone else's invite code either.
	var persistedInvite repo.TenantInvite
	if err := ctx.DB.Where("id = ?", invite.Id).First(&persistedInvite).Error; err != nil {
		t.Fatalf("readback invite: %v", err)
	}
	if persistedInvite.Status != repo.TenantInviteStatusPending {
		t.Errorf("invite status = %d, want still pending(%d) — existing-user login must not consume it", persistedInvite.Status, repo.TenantInviteStatusPending)
	}
}
