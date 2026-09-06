package app

// token_service_identity_link_test.go — L2: token creation must stamp the
// owner's platform-account link (IdentityAccountID) so PostConsumeQuota's
// wallet-debit gate (quota.go:983, relayInfo.IdentityAccountID > 0) actually
// fires for keys the customer creates through the ordinary console flow, not
// only for keys built by hand in a test. Before this fix BuildCleanToken never
// touched the field — every self-service token settled locally only and never
// reached the platform wallet, no matter how its owner's account was linked.
//
// The identity-link assertion alone is a PROXY oracle: a mutation could leave
// the field stamped but never read by the money path. TestBuildCleanToken_
// FeedsThePlatformWalletDebitGate closes that by pinning the assertion on the
// debit call itself (same convention as a2_legacy_debit_gate_test.go).

import (
	"context"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// seedLinkedUser creates a user optionally bound to a lurus-platform account.
// accountID == 0 means "no link", matching the pre-Layer-C / unlinked state.
func seedLinkedUser(t *testing.T, accountID int64) int {
	t.Helper()
	user := repo.User{
		Username: "linked-" + common.GetRandomString(6),
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	if accountID > 0 {
		user.LurusAccountID = &accountID
	}
	if err := repo.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return user.Id
}

func TestBuildCleanToken_StampsOwnerPlatformAccount(t *testing.T) {
	setupServiceTestDB(t)
	userId := seedLinkedUser(t, 77001)

	src := &repo.Token{RemainQuota: 5000, UnlimitedQuota: false}
	key, err := GenerateTokenKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	clean := BuildCleanToken(userId, "", src, key)
	if err := clean.Insert(); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var reloaded repo.Token
	if err := repo.DB.First(&reloaded, "id = ?", clean.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.IdentityAccountID != 77001 {
		t.Errorf("identity_account_id = %d, want 77001", reloaded.IdentityAccountID)
	}
	// The caller's own quota decision must survive byte-identical — this is
	// NOT the self-heal endpoint, which also flips unlimited_quota=true.
	if reloaded.RemainQuota != 5000 || reloaded.UnlimitedQuota != false {
		t.Errorf("caller's quota fields were altered: remain=%d unlimited=%v", reloaded.RemainQuota, reloaded.UnlimitedQuota)
	}
}

func TestBuildCleanToken_UnlinkedOwnerYieldsZero(t *testing.T) {
	setupServiceTestDB(t)
	userId := seedLinkedUser(t, 0)

	src := &repo.Token{RemainQuota: 100, UnlimitedQuota: true}
	key, err := GenerateTokenKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	clean := BuildCleanToken(userId, "", src, key)
	if clean.IdentityAccountID != 0 {
		t.Errorf("identity_account_id = %d, want 0 for an unlinked owner", clean.IdentityAccountID)
	}
	if clean.RemainQuota != 100 || !clean.UnlimitedQuota {
		t.Errorf("caller's quota fields were altered: remain=%d unlimited=%v", clean.RemainQuota, clean.UnlimitedQuota)
	}
}

// hookWalletDebit stubs the money-moving call and records the account id of
// every call, so the assertion is "the gate fired for THIS account" rather
// than a bare call count that a mutation routing the debit to the wrong
// account could still satisfy.
func hookWalletDebit(t *testing.T) *[]int64 {
	t.Helper()
	var accounts []int64
	prevDebit := debitWalletGRPC
	debitWalletGRPC = func(_ context.Context, accountID int64, _ float64, _, _, _, _ string) (*common.DebitWalletResult, error) {
		accounts = append(accounts, accountID)
		return &common.DebitWalletResult{}, nil
	}
	t.Cleanup(func() { debitWalletGRPC = prevDebit })
	return &accounts
}

// TestBuildCleanToken_FeedsThePlatformWalletDebitGate is the money-level
// companion: a token minted through the real BuildCleanToken path for a
// linked owner must make PostConsumeQuota's wallet-debit gate fire for that
// owner's account — not just carry the field.
func TestBuildCleanToken_FeedsThePlatformWalletDebitGate(t *testing.T) {
	db := setupServiceTestDB(t)
	isolateBizTPMWindow(t)
	prevRedis := common.RedisEnabled
	prevURL := common.IdentityServiceURL
	common.RedisEnabled = false
	common.IdentityServiceURL = ""
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.IdentityServiceURL = prevURL
	})

	userId := seedLinkedUser(t, 77001)

	src := &repo.Token{RemainQuota: 100_000, UnlimitedQuota: false}
	key, err := GenerateTokenKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	clean := BuildCleanToken(userId, "", src, key)
	if err := clean.Insert(); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var reloaded repo.Token
	if err := db.First(&reloaded, "id = ?", clean.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}

	accounts := hookWalletDebit(t)
	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           reloaded.Id,
		TokenKey:          reloaded.Key,
		IdentityAccountID: reloaded.IdentityAccountID,
		PlatformPreAuthID: 0, // no pre-auth => legacy branch, same as a2
		PlatformGoverned:  false,
	}
	if err := PostConsumeQuota(relayInfo, 700, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	if got := *accounts; len(got) != 1 || got[0] != 77001 {
		t.Errorf("wallet debit accounts = %v, want exactly one debit for account 77001", got)
	}
}

// TestAutoCreateDefaultToken_PoolDebit_ChargesOwnerTenantOnly is the two-pool
// companion for the AutoCreateDefaultToken tenant fix: settling through a
// token minted for an "acme" owner must draw down acme's credit pool and
// leave the "default" bootstrap pool untouched. Before the fix every
// auto-created token was hardcoded into "default", so this settlement would
// have drained the WRONG tenant's pool.
func TestAutoCreateDefaultToken_PoolDebit_ChargesOwnerTenantOnly(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)

	acmeUser := seedTestUser(t, db, 100_000)
	if err := db.Model(&repo.User{}).Where("id = ?", acmeUser).Update("tenant_id", "acme").Error; err != nil {
		t.Fatalf("set owner tenant: %v", err)
	}

	if _, err := repo.CreateTenantCreditPool("acme", 1, 1000, repo.PoolResetMonthly, 80); err != nil {
		t.Fatalf("create acme pool: %v", err)
	}
	acmePool, err := repo.GetTenantCreditPool("acme")
	if err != nil {
		t.Fatalf("get acme pool: %v", err)
	}
	if _, err := repo.TopupPool(acmePool.ID, "acme", 1000, 1, "seed"); err != nil {
		t.Fatalf("topup acme pool: %v", err)
	}

	if _, err := repo.CreateTenantCreditPool("default", 1, 1000, repo.PoolResetMonthly, 80); err != nil {
		t.Fatalf("create default pool: %v", err)
	}
	defaultPool, err := repo.GetTenantCreditPool("default")
	if err != nil {
		t.Fatalf("get default pool: %v", err)
	}
	if _, err := repo.TopupPool(defaultPool.ID, "default", 1000, 1, "seed"); err != nil {
		t.Fatalf("topup default pool: %v", err)
	}

	tok, err := repo.AutoCreateDefaultToken(acmeUser)
	if err != nil {
		t.Fatalf("AutoCreateDefaultToken: %v", err)
	}
	if tok.TenantId != "acme" {
		t.Fatalf("auto-created token tenant_id = %q, want %q", tok.TenantId, "acme")
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:   acmeUser,
		TokenId:  tok.Id,
		TokenKey: tok.Key,
	}
	if err := PostConsumeQuota(relayInfo, 100, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	acmeAfter, err := repo.GetTenantCreditPool("acme")
	if err != nil {
		t.Fatalf("reload acme pool: %v", err)
	}
	if acmeAfter.CurrentBalance != 900 {
		t.Errorf("acme pool balance = %d, want 900 (1000 - 100)", acmeAfter.CurrentBalance)
	}

	defaultAfter, err := repo.GetTenantCreditPool("default")
	if err != nil {
		t.Fatalf("reload default pool: %v", err)
	}
	if defaultAfter.CurrentBalance != 1000 {
		t.Errorf("default pool balance = %d, want untouched 1000 — settlement charged the wrong tenant", defaultAfter.CurrentBalance)
	}
}
