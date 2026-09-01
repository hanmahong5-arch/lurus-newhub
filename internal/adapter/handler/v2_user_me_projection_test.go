package handler

// v2_user_me_projection_test.go — GET /api/v2/:tenant_slug/user/me contract.
//
// Two defects met here on 2026-09-01:
//
//  1. The production router had this path wired to the v1 `GetSelf`, whose
//     projection has neither `remaining_quota` nor `token_count`. The v2
//     dashboard reads exactly those two (pages/v2/Dashboard/index.jsx:200,219),
//     so the "remaining quota" card fell through to its unlimited-plan branch —
//     telling a metered customer they were on an unlimited plan — and the
//     "active keys" card was pinned at 0, which kept the "you have no keys yet"
//     banner up while the keys page listed keys. The test router had always
//     pointed at GetSelfV2, so the whole suite was green against a handler
//     production never called.
//
//  2. GetSelfV2 itself computed `remaining_quota` as quota - used_quota, which
//     subtracts the spend a second time: `quota` IS the spendable balance (the
//     settlement path decrements it and increments used_quota in the same
//     write), so quota + used_quota is the funded-amount invariant, not the
//     difference. Swapping the route without fixing this would have shipped a
//     fresh money-display bug in place of the old one.
//
// Mutation oracles: restore `user.Quota - user.UsedQuota` and the balance
// assertion goes red; swap CountUserTokensByTenant back to CountUserTokens and
// the cross-tenant assertion goes red; drop any legacy key and its assertion
// goes red.

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestGetSelfV2_RemainingQuotaIsTheBalanceNotBalanceMinusSpend(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// A user who has actually spent: quota (balance) and used_quota (lifetime
	// spend) are BOTH non-zero and different, so the two formulas disagree.
	const balance, lifetimeSpend = 700_000, 300_000
	if err := ctx.DB.Model(&repo.User{}).Where("id = ?", ctx.NormalUser.Id).
		Updates(map[string]interface{}{"quota": balance, "used_quota": lifetimeSpend}).Error; err != nil {
		t.Fatalf("seed spent user: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/user/me", nil, nil)
	data := AssertV2Success(t, w)["data"].(map[string]interface{})

	if got := data["quota"].(float64); got != balance {
		t.Fatalf("quota = %v, want %d", got, balance)
	}
	if got := data["used_quota"].(float64); got != lifetimeSpend {
		t.Fatalf("used_quota = %v, want %d", got, lifetimeSpend)
	}
	remaining, ok := data["remaining_quota"].(float64)
	if !ok {
		t.Fatal("no remaining_quota field — the v2 dashboard's balance card reads exactly this key " +
			"and renders an unlimited-plan placeholder when it is absent")
	}
	if remaining != balance {
		t.Errorf("remaining_quota = %v, want %d (the balance itself). %d would be balance-minus-spend, "+
			"which subtracts the lifetime spend a second time and under-reports what the customer can still use",
			remaining, balance, balance-lifetimeSpend)
	}
}

func TestGetSelfV2_TokenCountIsTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Token(t, ctx, ctx.NormalUser.Id, "in-tenant-1")
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "in-tenant-2")

	// Same user, different tenant — must NOT be counted. A hub user can hold
	// keys in more than one tenant; counting all of them both over-states the
	// dashboard card and leaks the cardinality of another tenant's keys.
	foreign := &repo.Token{
		UserId:         ctx.NormalUser.Id,
		TenantId:       "some-other-tenant",
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "foreign-tenant-key",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := ctx.DB.Create(foreign).Error; err != nil {
		t.Fatalf("seed foreign-tenant token: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/user/me", nil, nil)
	data := AssertV2Success(t, w)["data"].(map[string]interface{})

	if got := data["token_count"].(float64); got != 2 {
		t.Errorf("token_count = %v, want 2 — 3 means the count crossed the tenant boundary", got)
	}
}

// TestGetSelfV2_IsSupersetOfV1Projection guards the route swap. Two legacy-shell
// consumers (components/topup/index.jsx, hooks/dashboard/useDashboardData.js)
// push this whole payload into the shared user store, so anything the v1
// GetSelf used to provide must still be here or the legacy top-up page loses
// its sidebar config and permission gating.
func TestGetSelfV2_IsSupersetOfV1Projection(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/user/me", nil, nil)
	data := AssertV2Success(t, w)["data"].(map[string]interface{})

	// Exactly the key set v1 GetSelf returns (handler/user.go).
	for _, k := range []string{
		"id", "username", "display_name", "role", "status", "email", "group",
		"quota", "used_quota", "request_count", "setting", "sidebar_modules", "permissions",
	} {
		if _, ok := data[k]; !ok {
			t.Errorf("missing v1-parity key %q — the legacy shell reads this off the shared user store", k)
		}
	}
	// …plus the v2-only keys the v2 console needs.
	for _, k := range []string{"remaining_quota", "token_count", "tenant_id", "daily_quota"} {
		if _, ok := data[k]; !ok {
			t.Errorf("missing v2 key %q", k)
		}
	}
}
