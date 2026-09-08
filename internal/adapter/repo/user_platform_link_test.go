package repo

// user_platform_link_test.go — LinkUserPlatformAccount is the single
// implementation behind every "bind a newhub user to a lurus-platform
// account" call site (OIDCCallback, the JWT auth middleware, the
// /internal provisioning self-heal path). Before this fix only the
// provisioning path performed the link and nothing on the SSO login path
// persisted it — an SSO-only user's account link lived in the request
// context/session only, never in users.lurus_account_id, so a token minted
// for that user (which reads the user row, not the session) never picked it
// up.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestLinkUserPlatformAccount_LinksUserAndBackfillsTokens(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "link-user-1", "link1@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	// An existing token with no platform link — must be backfilled.
	tok := &Token{
		UserId:            user.Id,
		TenantId:          "default",
		Key:               common.GetRandomString(48),
		Status:            common.TokenStatusEnabled,
		Name:              "pre-link",
		RemainQuota:       500,
		UnlimitedQuota:    false,
		IdentityAccountID: 0,
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if err := LinkUserPlatformAccount(user.Id, 4242, true); err != nil {
		t.Fatalf("LinkUserPlatformAccount: %v", err)
	}

	var reloadedUser User
	if err := DB.First(&reloadedUser, "id = ?", user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloadedUser.LurusAccountID == nil || *reloadedUser.LurusAccountID != 4242 {
		t.Fatalf("user.lurus_account_id = %v, want 4242", reloadedUser.LurusAccountID)
	}

	var reloadedTok Token
	if err := DB.First(&reloadedTok, "id = ?", tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloadedTok.IdentityAccountID != 4242 {
		t.Errorf("token.identity_account_id = %d, want 4242", reloadedTok.IdentityAccountID)
	}
	if !reloadedTok.UnlimitedQuota {
		t.Errorf("token.unlimited_quota = false, want true after link backfill")
	}
}

// TestLinkUserPlatformAccount_NoBackfill_LinksUserOnlyLeavesTokensUntouched is
// the money-safety case for the SSO callback / JWT middleware call sites:
// backfillTokens=false must link the user row but must NOT touch any
// existing token — an admin-capped token (RemainQuota=500,
// UnlimitedQuota=false) keeps its cap instead of silently becoming
// unlimited on a login request.
func TestLinkUserPlatformAccount_NoBackfill_LinksUserOnlyLeavesTokensUntouched(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "link-user-nobf", "link-nobf@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	tok := &Token{
		UserId:            user.Id,
		TenantId:          "default",
		Key:               common.GetRandomString(48),
		Status:            common.TokenStatusEnabled,
		Name:              "admin-capped",
		RemainQuota:       500,
		UnlimitedQuota:    false,
		IdentityAccountID: 0,
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if err := LinkUserPlatformAccount(user.Id, 8001, false); err != nil {
		t.Fatalf("LinkUserPlatformAccount: %v", err)
	}

	var reloadedUser User
	if err := DB.First(&reloadedUser, "id = ?", user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloadedUser.LurusAccountID == nil || *reloadedUser.LurusAccountID != 8001 {
		t.Fatalf("user.lurus_account_id = %v, want 8001", reloadedUser.LurusAccountID)
	}

	var reloadedTok Token
	if err := DB.First(&reloadedTok, "id = ?", tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloadedTok.IdentityAccountID != 0 {
		t.Errorf("token.identity_account_id = %d, want 0 (backfillTokens=false must not touch tokens)", reloadedTok.IdentityAccountID)
	}
	if reloadedTok.UnlimitedQuota {
		t.Errorf("token.unlimited_quota = true, want false (backfillTokens=false must leave the admin-set cap intact)")
	}
	if reloadedTok.RemainQuota != 500 {
		t.Errorf("token.remain_quota = %d, want 500 (untouched)", reloadedTok.RemainQuota)
	}
}

func TestLinkUserPlatformAccount_IdempotentNoOp(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "link-user-2", "link2@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	if err := LinkUserPlatformAccount(user.Id, 5001, true); err != nil {
		t.Fatalf("first link: %v", err)
	}
	// Second call for the SAME user/account must be a harmless no-op — the
	// `lurus_account_id IS NULL` guard makes the UPDATE affect zero rows,
	// and that must not surface as an error.
	if err := LinkUserPlatformAccount(user.Id, 5001, true); err != nil {
		t.Fatalf("second (idempotent) link: %v", err)
	}

	var reloaded User
	if err := DB.First(&reloaded, "id = ?", user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.LurusAccountID == nil || *reloaded.LurusAccountID != 5001 {
		t.Fatalf("user.lurus_account_id = %v, want 5001", reloaded.LurusAccountID)
	}
}

// TestLinkUserPlatformAccount_NeverRebindsOnCollision is the money-safety
// case: accountID is already bound to user A. Linking it to user B must be a
// silent skip — B stays unlinked, A's binding is untouched. Overwriting
// would let two local users share one wallet, so B's relay spend would be
// charged against A's platform account.
func TestLinkUserPlatformAccount_NeverRebindsOnCollision(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	userA := seedUser(t, "link-user-a", "linka@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	userB := seedUser(t, "link-user-b", "linkb@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	if err := LinkUserPlatformAccount(userA.Id, 6001, true); err != nil {
		t.Fatalf("link A: %v", err)
	}
	if err := LinkUserPlatformAccount(userB.Id, 6001, true); err != nil {
		t.Fatalf("link B (collision) must not error: %v", err)
	}

	var reloadedA, reloadedB User
	if err := DB.First(&reloadedA, "id = ?", userA.Id).Error; err != nil {
		t.Fatalf("reload A: %v", err)
	}
	if err := DB.First(&reloadedB, "id = ?", userB.Id).Error; err != nil {
		t.Fatalf("reload B: %v", err)
	}
	if reloadedA.LurusAccountID == nil || *reloadedA.LurusAccountID != 6001 {
		t.Fatalf("user A's original binding was disturbed: %v", reloadedA.LurusAccountID)
	}
	if reloadedB.LurusAccountID != nil {
		t.Fatalf("user B got bound to an account already owned by A: %v", *reloadedB.LurusAccountID)
	}
}

func TestIdentityAccountIDForUser(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	linked := seedUser(t, "idacct-linked", "idacct1@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := LinkUserPlatformAccount(linked.Id, 7001, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	unlinked := seedUser(t, "idacct-unlinked", "idacct2@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	if got := IdentityAccountIDForUser(linked.Id); got != 7001 {
		t.Errorf("IdentityAccountIDForUser(linked) = %d, want 7001", got)
	}
	if got := IdentityAccountIDForUser(unlinked.Id); got != 0 {
		t.Errorf("IdentityAccountIDForUser(unlinked) = %d, want 0", got)
	}
	if got := IdentityAccountIDForUser(-1); got != 0 {
		t.Errorf("IdentityAccountIDForUser(nonexistent) = %d, want 0", got)
	}
}

// TestLinkUserPlatformAccount_UserAlreadyLinkedElsewhere_SkipsEntirely: a
// user already bound to account A must not have account B stamped onto their
// tokens — even with backfillTokens=true. Before this guard the users UPDATE
// was a no-op (IS NULL clause) but the token UPDATE still ran, leaving the
// user row on wallet A and the tokens on wallet B.
func TestLinkUserPlatformAccount_UserAlreadyLinkedElsewhere_SkipsEntirely(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "link-user-relink", "relink@example.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := LinkUserPlatformAccount(user.Id, 111, false); err != nil {
		t.Fatalf("first link: %v", err)
	}
	tok := &Token{
		UserId:            user.Id,
		TenantId:          "default",
		Key:               common.GetRandomString(48),
		Status:            common.TokenStatusEnabled,
		Name:              "capped",
		RemainQuota:       500,
		UnlimitedQuota:    false,
		IdentityAccountID: 0,
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if err := LinkUserPlatformAccount(user.Id, 222, true); err != nil {
		t.Fatalf("second link must be a silent no-op, got %v", err)
	}

	var reloadedUser User
	if err := DB.First(&reloadedUser, "id = ?", user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloadedUser.LurusAccountID == nil || *reloadedUser.LurusAccountID != 111 {
		t.Fatalf("user.lurus_account_id = %v, want 111 (unchanged)", reloadedUser.LurusAccountID)
	}
	var reloadedTok Token
	if err := DB.First(&reloadedTok, "id = ?", tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloadedTok.IdentityAccountID != 0 || reloadedTok.UnlimitedQuota {
		t.Errorf("token = (identity_account_id=%d, unlimited=%v), want (0, false): a re-link to a different account must touch nothing",
			reloadedTok.IdentityAccountID, reloadedTok.UnlimitedQuota)
	}
}
