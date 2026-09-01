package app

// a1_provisioned_quota_test.go — the money half of A1.
//
// Provisioned keys (handler/provisioning.go) are tenant-scoped and carry
// UserId=0 by design: no user row exists for them, so every user-ledger leg
// (balance gate, pre-deduction, post-consume debit, daily quota, cost-spike
// window, low-balance notify) has no addressee. Their money is the token's own
// quota plus the tenant credit pool. These tests pin BOTH halves: the user legs
// must not run, and the token + pool legs must still run in full — a guard that
// accidentally short-circuits the whole settlement would be a free-relay bug.

import (
	"net/http"
	"sync/atomic"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// isolateBizTPMWindow clears the package-global in-memory business-TPM window
// after the test. PostConsumeQuota's TPM leg keys on the TOKEN ID, and every
// setupServiceTestDB gets a fresh sqlite whose ids restart at 1 — so two tests
// that both settle their first token write into the SAME window key. Without
// this, a test that asserts an exact TPM total (business_tpm_test.go) reads the
// sum of every earlier settlement in the binary.
func isolateBizTPMWindow(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		bizTPMMem.mu.Lock()
		defer bizTPMMem.mu.Unlock()
		bizTPMMem.entries = make(map[string][]bizTPMEntry)
	})
}

// seedZeroIdUserSentinel inserts a users row with the literal id 0. Production
// has no such row — GORM's `WHERE id = 0` simply matches nothing, which makes a
// stray user-ledger write against a provisioned key SILENT. This sentinel turns
// that silence into an observable: if any user-quota leg forgets its UserId>0
// guard, this row moves and the assertion fails. Raw SQL because GORM treats a
// zero-valued autoincrement primary key as "unset" and would let SQLite assign
// a fresh id instead.
func seedZeroIdUserSentinel(t *testing.T, db *gorm.DB, quota int) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO users (id, username, display_name, role, status, email, quota, "group", daily_used) `+
			`VALUES (0, 'zero-id-sentinel', 'Zero Id Sentinel', ?, ?, 'sentinel@test.local', ?, 'default', 0)`,
		common.RoleCommonUser, common.UserStatusEnabled, quota).Error; err != nil {
		t.Fatalf("seed id=0 sentinel user: %v", err)
	}
}

// zeroIdUserState reads back the sentinel row's quota and daily_used.
func zeroIdUserState(t *testing.T, db *gorm.DB) (quota int, dailyUsed int) {
	t.Helper()
	row := struct {
		Quota     int
		DailyUsed int
	}{}
	if err := db.Raw(`SELECT quota, daily_used FROM users WHERE id = 0`).Scan(&row).Error; err != nil {
		t.Fatalf("read id=0 sentinel user: %v", err)
	}
	return row.Quota, row.DailyUsed
}

// TestPreConsumeQuota_ProvisionedKey_SkipsUserLegKeepsTokenLeg is the pre-consume
// half of A1: repo.GetUserQuota(0) matches no row and answers 0 without an
// error, so before the fix the local-balance gate 402'd every provisioned relay.
func TestPreConsumeQuota_ProvisionedKey_SkipsUserLegKeepsTokenLeg(t *testing.T) {
	db := setupServiceTestDB(t)
	// UserId 0 == provisioned/tenant-scoped key.
	key, tokenId := seedTestToken(t, db, 0, 5_000, false)

	c := createTestGinContext()
	c.Set("token_quota", 5_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:         0,
		TokenId:        tokenId,
		TokenKey:       key,
		TokenUnlimited: false,
	}

	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr != nil {
		t.Fatalf("provisioned key must pre-consume without a user balance, got: %v", apiErr.Error())
	}
	if relayInfo.FinalPreConsumedQuota != 1_000 {
		t.Errorf("FinalPreConsumedQuota = %d, want 1000 (token freeze must still be taken)",
			relayInfo.FinalPreConsumedQuota)
	}
	// No user balance exists, so nothing may be reported as one.
	if relayInfo.UserQuota != 0 {
		t.Errorf("UserQuota = %d, want 0 for a tenant-scoped key", relayInfo.UserQuota)
	}
	// The token cap IS the local pre-deduction for these keys.
	if got := tokenRemain(t, db, tokenId); got != 4_000 {
		t.Errorf("token remain = %d, want 4000 (5000 - 1000 pre-consumed)", got)
	}
}

// TestPreConsumeQuota_ProvisionedKey_TokenCapStillEnforced: with the user leg
// gone, the per-key cap is the ONLY local money gate a provisioned key has. If
// this regresses, a tenant-scoped key relays for free.
func TestPreConsumeQuota_ProvisionedKey_TokenCapStillEnforced(t *testing.T) {
	db := setupServiceTestDB(t)
	key, tokenId := seedTestToken(t, db, 0, 500, false)

	c := createTestGinContext()
	c.Set("token_quota", 500)

	relayInfo := &relaycommon.RelayInfo{
		UserId:         0,
		TokenId:        tokenId,
		TokenKey:       key,
		TokenUnlimited: false,
	}

	apiErr := PreConsumeQuota(c, 1_000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected 402: token remain 500 cannot cover a 1000 pre-consume")
	}
	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402", apiErr.StatusCode)
	}
	if got := tokenRemain(t, db, tokenId); got != 500 {
		t.Errorf("token remain = %d, want 500 (rejected pre-consume must not debit)", got)
	}
}

// TestPostConsumeQuota_ProvisionedKey_NoUserLedgerMutation is the settlement
// half: the token leg and the tenant-pool leg must fire in full while every
// user-keyed leg stays untouched. The id=0 sentinel row is the oracle for the
// user legs (see seedZeroIdUserSentinel); the AsyncGo counter is the oracle for
// the two user-keyed async legs (cost-spike window, low-balance notify) that
// leave no DB trace with Redis and NATS off.
func TestPostConsumeQuota_ProvisionedKey_NoUserLedgerMutation(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	seedZeroIdUserSentinel(t, db, 12_345)

	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	// Count the fire-and-forget legs. TestMain already runs AsyncGo inline.
	var asyncCalls atomic.Int32
	prevAsync := AsyncGo
	AsyncGo = func(f func()) {
		asyncCalls.Add(1)
		prevAsync(f)
	}
	t.Cleanup(func() { AsyncGo = prevAsync })

	const tenantID = "t-prov-settle"
	key := common.GetRandomString(32)
	tok := repo.Token{
		UserId:         0, // provisioned
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "provisioned-settle",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    10_000,
		UnlimitedQuota: false,
		Group:          "default",
		TenantId:       tenantID,
	}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed provisioned token: %v", err)
	}

	pool, err := repo.CreateTenantCreditPool(tenantID, 1, 100_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, tenantID, 5_000, 1, "seed"); err != nil {
		t.Fatalf("topup pool: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:   0,
		TokenId:  tok.Id,
		TokenKey: key,
	}

	// delta 300 on top of a 200 pre-consume => actual cost 500 for the pool.
	// sendEmail=true so the low-balance notify leg would fire if unguarded.
	if err := PostConsumeQuota(relayInfo, 300, 200, true); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	// --- user legs must be untouched ---
	quota, dailyUsed := zeroIdUserState(t, db)
	if quota != 12_345 {
		t.Errorf("id=0 user quota = %d, want 12345 (no user-quota write may address a provisioned key)", quota)
	}
	if dailyUsed != 0 {
		t.Errorf("id=0 user daily_used = %d, want 0 (daily-quota leg must be skipped)", dailyUsed)
	}
	// Only the TPM leg (token/tenant-keyed) may dispatch: the cost-spike window
	// and the low-balance notify are both user-keyed.
	if got := asyncCalls.Load(); got != 1 {
		t.Errorf("AsyncGo dispatches = %d, want 1 (TPM only; cost-spike + notify are user-keyed)", got)
	}

	// --- token + pool legs must have fired ---
	if got := tokenRemain(t, db, tok.Id); got != 9_700 {
		t.Errorf("token remain = %d, want 9700 (10000 - 300 delta)", got)
	}
	gotPool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("readback pool: %v", err)
	}
	if gotPool.CurrentBalance != 4_500 {
		t.Errorf("pool balance = %d, want 4500 (5000 - 500 actual cost)", gotPool.CurrentBalance)
	}
}

// TestPostConsumeQuota_UserOwnedToken_StillDebitsUser is the anti-regression
// half: the UserId>0 guards must not have turned the normal settlement into a
// no-op. Same shape as the provisioned test, real user id.
func TestPostConsumeQuota_UserOwnedToken_StillDebitsUser(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)

	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 8_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
	}
	if err := PostConsumeQuota(relayInfo, 300, 200, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	if got := userQuota(t, db, userId); got != 9_700 {
		t.Errorf("user quota = %d, want 9700 (10000 - 300) — the UserId>0 guard must not skip real users", got)
	}
	if got := tokenRemain(t, db, tokenId); got != 7_700 {
		t.Errorf("token remain = %d, want 7700 (8000 - 300)", got)
	}
}
