package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// stubResolver overrides the package-level account resolver for the duration of
// a test and restores it on cleanup. Tests using it must NOT call t.Parallel()
// — the seam is a shared package var.
func stubResolver(t *testing.T, fn func(context.Context, string) (*common.IdentityMapping, error)) {
	t.Helper()
	orig := resolveAccountByZitadelSub
	resolveAccountByZitadelSub = fn
	t.Cleanup(func() { resolveAccountByZitadelSub = orig })
}

func ptrAccount(v int64) *int64 { return &v }

// TestProvision_Resolvable_LinksAndUnlimitsToken: a resolvable zitadel_sub links
// the new user to the platform account, stamps the token's IdentityAccountID,
// and forces UnlimitedQuota=true even though initial_quota>0 (Risk-2: the wallet
// becomes the sole spend gate so a zero local quota can't strand the user).
func TestProvision_Resolvable_LinksAndUnlimitsToken(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const accountID int64 = 4242
	const sub = "zsub-resolvable-1"
	stubResolver(t, func(_ context.Context, s string) (*common.IdentityMapping, error) {
		if s != sub {
			t.Errorf("resolver called with unexpected sub %q", s)
		}
		return &common.IdentityMapping{ID: accountID, ZitadelSub: s}, nil
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub":          sub,
		"email":                "resolvable@test.local",
		"initial_quota":        100, // non-zero ⇒ default UnlimitedQuota would be false
		"create_initial_token": true,
	}, authHeaders())

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body: %s", w.Code, w.Body.String())
	}
	assertSuccess(t, parseResponse(t, w))

	user, _, err := repo.GetUserByIDPSubject(sub, "default")
	if err != nil {
		t.Fatalf("provisioned user not found: %v", err)
	}
	if user.LurusAccountID == nil || *user.LurusAccountID != accountID {
		t.Fatalf("user not linked: LurusAccountID=%v want %d", user.LurusAccountID, accountID)
	}
	tokens, err := repo.GetAllUserTokens(user.Id, 0, 10)
	if err != nil || len(tokens) == 0 {
		t.Fatalf("no token created: err=%v len=%d", err, len(tokens))
	}
	if tokens[0].IdentityAccountID != accountID {
		t.Errorf("token.IdentityAccountID=%d want %d", tokens[0].IdentityAccountID, accountID)
	}
	if !tokens[0].UnlimitedQuota {
		t.Errorf("linked token must have UnlimitedQuota=true despite initial_quota>0 (Risk-2)")
	}
}

// TestProvision_Unresolvable_CreatesUnlinkedNoError: when the platform account
// does not resolve (nil result), provisioning must FAIL-OPEN — create an
// unlinked user/token and still return 201. With initial_quota>0 the unlinked
// token keeps UnlimitedQuota=false, proving that linking is what forces it.
func TestProvision_Unresolvable_CreatesUnlinkedNoError(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const sub = "zsub-unresolvable-1"
	stubResolver(t, func(_ context.Context, _ string) (*common.IdentityMapping, error) {
		return nil, nil // account not found / not yet created
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub":          sub,
		"email":                "unresolvable@test.local",
		"initial_quota":        100,
		"create_initial_token": true,
	}, authHeaders())

	if w.Code != http.StatusCreated {
		t.Fatalf("fail-open: expected 201 even when unresolvable, got %d, body: %s", w.Code, w.Body.String())
	}
	assertSuccess(t, parseResponse(t, w))

	user, _, err := repo.GetUserByIDPSubject(sub, "default")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.LurusAccountID != nil {
		t.Errorf("unresolvable user must be UNLINKED, got LurusAccountID=%d", *user.LurusAccountID)
	}
	tokens, _ := repo.GetAllUserTokens(user.Id, 0, 10)
	if len(tokens) == 0 {
		t.Fatal("expected a token")
	}
	if tokens[0].IdentityAccountID != 0 {
		t.Errorf("unlinked token must have IdentityAccountID=0, got %d", tokens[0].IdentityAccountID)
	}
	if tokens[0].UnlimitedQuota {
		t.Errorf("unlinked token with initial_quota>0 must keep UnlimitedQuota=false")
	}
}

// TestProvision_ResolverError_FailOpenUnlinked: a resolver ERROR (platform
// unreachable) must also fail-open — never a 500, never a blocked login.
func TestProvision_ResolverError_FailOpenUnlinked(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const sub = "zsub-resolver-error-1"
	stubResolver(t, func(_ context.Context, _ string) (*common.IdentityMapping, error) {
		return nil, errors.New("platform unreachable")
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub":          sub,
		"email":                "resolver-error@test.local",
		"create_initial_token": true,
	}, authHeaders())

	if w.Code != http.StatusCreated {
		t.Fatalf("fail-open on resolver error: expected 201, got %d, body: %s", w.Code, w.Body.String())
	}
	assertSuccess(t, parseResponse(t, w))

	user, _, err := repo.GetUserByIDPSubject(sub, "default")
	if err != nil {
		t.Fatalf("user not created on resolver error: %v", err)
	}
	if user.LurusAccountID != nil {
		t.Errorf("resolver-error must produce UNLINKED user, got %d", *user.LurusAccountID)
	}
	// Token-path fail-open: the initial token is still created, just unlinked.
	tokens, _ := repo.GetAllUserTokens(user.Id, 0, 10)
	if len(tokens) == 0 {
		t.Fatal("expected initial token even on resolver error")
	}
	if tokens[0].IdentityAccountID != 0 {
		t.Errorf("resolver-error token must be unlinked (IdentityAccountID=0), got %d", tokens[0].IdentityAccountID)
	}
}

// TestProvision_Reprovision_SelfHealsLink: re-provisioning an existing unlinked
// user (created before its platform account existed) backfills the link onto
// the user AND its zero-account tokens, then returns the idempotent 200.
func TestProvision_Reprovision_SelfHealsLink(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const sub = "zsub-selfheal-1"
	const accountID int64 = 7777

	u := &repo.User{
		Username: "selfheal_user", TenantId: "default", Email: "selfheal@test.local",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	now := time.Now()
	if err := repo.DB.Create(&repo.UserIdentityMapping{
		LurusUserID: u.Id, IDPSubject: sub, TenantID: "default",
		Email: u.Email, PreferredUsername: u.Username, IsActive: true,
		LastSyncAt: &now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	if err := repo.DB.Create(&repo.Token{
		UserId: u.Id, TenantId: "default", Name: "seed",
		Key:    "selfheal-key-0000000000000000000000000000000",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, IdentityAccountID: 0,
		RemainQuota: 500, UnlimitedQuota: false, // capped, not-yet-linked token
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	stubResolver(t, func(_ context.Context, s string) (*common.IdentityMapping, error) {
		return &common.IdentityMapping{ID: accountID, ZitadelSub: s}, nil
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub": sub,
		"email":       "selfheal@test.local",
	}, authHeaders())

	if w.Code != http.StatusOK {
		t.Fatalf("re-provision should be idempotent 200, got %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	assertSuccess(t, resp)
	if data, _ := resp["data"].(map[string]interface{}); data == nil || data["is_existing"] != true {
		t.Errorf("expected is_existing=true, got %v", resp["data"])
	}

	healed, err := repo.GetUserByLurusAccountID(accountID)
	if err != nil {
		t.Fatalf("user not linked after self-heal: %v", err)
	}
	if healed.Id != u.Id {
		t.Errorf("self-heal linked wrong user: got %d want %d", healed.Id, u.Id)
	}
	tokens, _ := repo.GetAllUserTokens(u.Id, 0, 10)
	if len(tokens) == 0 {
		t.Fatal("expected the seeded token to survive self-heal")
	}
	if tokens[0].IdentityAccountID != accountID {
		t.Errorf("token identity_account_id not backfilled: got %d want %d", tokens[0].IdentityAccountID, accountID)
	}
	if !tokens[0].UnlimitedQuota {
		t.Errorf("self-healed linked token must have UnlimitedQuota=true (else wallet-debited AND token-capped at RemainQuota); got false")
	}
}

// TestProvision_WithTenantPlugin reproduces production: the GORM TenantPlugin is
// registered (repo/main.go does this at boot) and the internal API key context
// carries NO tenant. The provision handler opens its own transaction; if that tx
// is not tenant-scoped, the plugin's beforeCreate rejects every INSERT
// ("tenant_id is required for create operations") and the endpoint 500s instead
// of provisioning. This is the same class of bug as the InternalCreateUser tenant
// regression — caught here because the other provision tests don't register the
// plugin. Covers both the no-token and create-initial-token paths so all three
// tx.Create calls (user/mapping/token) are exercised under the plugin.
func TestProvision_WithTenantPlugin(t *testing.T) {
	cases := []struct {
		name        string
		createToken bool
	}{
		{name: "provision without initial token", createToken: false},
		{name: "provision with initial token", createToken: true},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, cleanup := SetupIntegrationRouter(t)
			t.Cleanup(cleanup)
			if err := repo.DB.Use(&repo.TenantPlugin{}); err != nil {
				t.Fatalf("failed to register tenant plugin: %v", err)
			}
			stubResolver(t, func(_ context.Context, _ string) (*common.IdentityMapping, error) {
				return nil, nil // unlinked path keeps the test free of platform deps
			})

			sub := "zsub-tenantplugin-" + strconv.Itoa(i)
			w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
				"zitadel_sub":          sub,
				"email":                "tenantplugin@test.local",
				"create_initial_token": tc.createToken,
			}, authHeaders())

			if w.Code != http.StatusCreated {
				t.Fatalf("expected 201 with tenant plugin active, got %d, body: %s", w.Code, w.Body.String())
			}
			assertSuccess(t, parseResponse(t, w))

			// The user, mapping, and (optional) token must actually exist in the
			// default tenant — a 201 with no row would be a hollow pass.
			user, mapping, err := repo.GetUserByIDPSubject(sub, "default")
			if err != nil || user == nil {
				t.Fatalf("provisioned user/mapping not found under tenant plugin: %v", err)
			}
			if user.TenantId != "default" {
				t.Errorf("expected tenant_id 'default', got %q", user.TenantId)
			}
			if mapping == nil || mapping.IDPSubject != sub {
				t.Errorf("identity mapping not written for sub %q", sub)
			}
			if tc.createToken {
				tokens, _ := repo.GetAllUserTokens(user.Id, 0, 10)
				if len(tokens) == 0 {
					t.Error("expected initial token row under tenant plugin")
				}
			}
		})
	}
}

// TestProvision_CreateRace_ReturnsWinner: a concurrent double-provision (the
// unique index on users.lurus_account_id fires) resolves into a clean idempotent
// 200 returning the already-committed winner — not a 500, no duplicate user.
// The race is exercised deterministically by pre-seeding the winner (linked to
// the account, but with no mapping for `sub` so the idempotency check misses and
// the create path runs into the unique violation).
func TestProvision_CreateRace_ReturnsWinner(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const sub = "zsub-race-1"
	const accountID int64 = 9999

	winner := &repo.User{
		Username: "race_winner", TenantId: "default", Email: "winner@test.local",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		LurusAccountID: ptrAccount(accountID),
	}
	if err := repo.DB.Create(winner).Error; err != nil {
		t.Fatalf("seed winner: %v", err)
	}

	stubResolver(t, func(_ context.Context, s string) (*common.IdentityMapping, error) {
		return &common.IdentityMapping{ID: accountID, ZitadelSub: s}, nil
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub": sub,
		"email":       "loser@test.local",
	}, authHeaders())

	if w.Code != http.StatusOK {
		t.Fatalf("race loser should get idempotent 200 winner, got %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	assertSuccess(t, resp)
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatal("no data in race response")
	}
	if data["is_existing"] != true {
		t.Errorf("expected is_existing=true on race, got %v", data["is_existing"])
	}
	if gotID, _ := data["user_id"].(float64); int(gotID) != winner.Id {
		t.Errorf("race must return winner user_id=%d, got %v", winner.Id, data["user_id"])
	}

	var count int64
	repo.DB.Model(&repo.User{}).Where("lurus_account_id = ?", accountID).Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 user linked to account %d, got %d", accountID, count)
	}
}

// TestProvision_CreateRace_CrossTenant_NoLeak: when the SAME platform account is
// already linked to a user in tenant "corp-a", provisioning the same sub under a
// DIFFERENT tenant "corp-b" hits the GLOBAL unique index on users.lurus_account_id.
// The race winner must NOT be corp-a's user — returning it would leak corp-a's
// identity to corp-b and misroute corp-b's spend to corp-a's wallet. The tenant
// guard makes provisionRaceWinner find no winner, so the create error surfaces
// honestly instead of a false idempotent 200.
func TestProvision_CreateRace_CrossTenant_NoLeak(t *testing.T) {
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)

	const sub = "zsub-xtenant-1"
	const accountID int64 = 12321

	winnerA := &repo.User{
		Username: "corp_a_winner", TenantId: "corp-a", Email: "a@test.local",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		LurusAccountID: ptrAccount(accountID),
	}
	if err := repo.DB.Create(winnerA).Error; err != nil {
		t.Fatalf("seed corp-a winner: %v", err)
	}

	stubResolver(t, func(_ context.Context, s string) (*common.IdentityMapping, error) {
		return &common.IdentityMapping{ID: accountID, ZitadelSub: s}, nil
	})

	w := internalRequest(router, "POST", "/internal/user/provision", map[string]interface{}{
		"zitadel_sub": sub,
		"email":       "b@test.local",
		"tenant_id":   "corp-b",
	}, authHeaders())

	// Critical property: must NOT be a false idempotent 200 carrying corp-a's user.
	if w.Code == http.StatusOK {
		t.Fatalf("cross-tenant account collision must NOT return 200 (cross-tenant leak); body: %s", w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp["data"] != nil {
		t.Errorf("error response must carry no user identity across tenants, got data=%v", resp["data"])
	}
	// corp-a stays the sole holder of the account; no corp-b user got linked.
	var linked int64
	repo.DB.Model(&repo.User{}).Where("lurus_account_id = ?", accountID).Count(&linked)
	if linked != 1 {
		t.Errorf("expected exactly 1 (corp-a) user linked to account %d, got %d", accountID, linked)
	}
}
