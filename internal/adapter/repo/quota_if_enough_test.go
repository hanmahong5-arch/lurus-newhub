package repo

// quota_if_enough_test.go — deterministic (hermetic SQLite) contract tests for
// the atomic pre-consume debit helpers DecreaseTokenQuotaIfEnough /
// DecreaseUserQuotaIfEnough. These prove the WHERE ... >= ? guard:
//
//   - sufficient / exact balance → ok=true, debited exactly, never negative;
//   - insufficient balance      → ok=false, row UNCHANGED (no overdraw);
//   - negative amount           → error.
//
// The companion "*_Unconditional_AllowsNegative_Characterization" tests pin the
// root cause the guard exists to prevent: the general (unconditional)
// DecreaseTokenQuota / DecreaseUserQuota happily drive the balance negative when
// asked to debit more than is present. That unconditional behavior is
// intentional (post-consume settlement/compensation relies on it) and stays;
// the IfEnough variants are the ONLY ones the pre-consume gate may call.
//
// SQLite serialises writers, so it cannot manufacture the concurrent
// interleaving that overdraws the gate — the real contention proof lives in
// quota_if_enough_concurrency_pg_test.go (self-skips without TEST_POSTGRES_DSN).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func seedLimitedToken(t *testing.T, userID int, remain int) *Token {
	t.Helper()
	tok := &Token{
		UserId:      userID,
		Key:         "sk-ifenough-" + common.GetRandomString(20),
		Status:      common.TokenStatusEnabled,
		Name:        "ifenough",
		CreatedTime: common.GetTimestamp(),
		ExpiredTime: -1,
		RemainQuota: remain,
		Group:       "default",
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok
}

func readTokenRemain(t *testing.T, id int) int {
	t.Helper()
	var remain int
	if err := DB.Model(&Token{}).Where("id = ?", id).Select("remain_quota").Scan(&remain).Error; err != nil {
		t.Fatalf("read token remain: %v", err)
	}
	return remain
}

func readUserQuota(t *testing.T, id int) int {
	t.Helper()
	var quota int
	if err := DB.Model(&User{}).Where("id = ?", id).Select("quota").Scan(&quota).Error; err != nil {
		t.Fatalf("read user quota: %v", err)
	}
	return quota
}

func TestDecreaseTokenQuotaIfEnough_Contract(t *testing.T) {
	defer setupSQLiteDB(t)()

	u := seedUser(t, "tif-user", "tif@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedLimitedToken(t, u.Id, 100)

	// sufficient → ok, debited exactly.
	ok, err := DecreaseTokenQuotaIfEnough(tok.Id, tok.Key, 40)
	if err != nil || !ok {
		t.Fatalf("sufficient debit: ok=%v err=%v, want ok=true nil", ok, err)
	}
	if got := readTokenRemain(t, tok.Id); got != 60 {
		t.Fatalf("remain = %d, want 60 (100-40)", got)
	}

	// exact-enough (60 remain, want 60) → ok, drains to 0.
	ok, err = DecreaseTokenQuotaIfEnough(tok.Id, tok.Key, 60)
	if err != nil || !ok {
		t.Fatalf("exact debit: ok=%v err=%v, want ok=true nil", ok, err)
	}
	if got := readTokenRemain(t, tok.Id); got != 0 {
		t.Fatalf("remain = %d, want 0", got)
	}

	// insufficient (0 remain, want 1) → ok=false, UNCHANGED, never negative.
	ok, err = DecreaseTokenQuotaIfEnough(tok.Id, tok.Key, 1)
	if err != nil {
		t.Fatalf("insufficient debit err: %v, want nil", err)
	}
	if ok {
		t.Fatal("insufficient debit: ok=true, want ok=false (guard must refuse)")
	}
	if got := readTokenRemain(t, tok.Id); got != 0 {
		t.Fatalf("remain = %d, want 0 (unchanged, not negative)", got)
	}

	// negative amount → error.
	if _, err := DecreaseTokenQuotaIfEnough(tok.Id, tok.Key, -1); err == nil {
		t.Fatal("negative amount: want error")
	}
}

// TestDecreaseTokenQuota_Unconditional_AllowsNegative_Characterization pins the
// root cause: the general unconditional debit drives remain_quota negative when
// asked to over-debit. This is why the pre-consume gate must use the IfEnough
// guard instead. Passes before and after the fix (unconditional stays
// unconditional by design).
func TestDecreaseTokenQuota_Unconditional_AllowsNegative_Characterization(t *testing.T) {
	defer setupSQLiteDB(t)()

	u := seedUser(t, "tunc-user", "tunc@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedLimitedToken(t, u.Id, 100)

	if err := DecreaseTokenQuota(tok.Id, tok.Key, 150); err != nil {
		t.Fatalf("DecreaseTokenQuota: %v", err)
	}
	if got := readTokenRemain(t, tok.Id); got != -50 {
		t.Fatalf("unconditional remain = %d, want -50 (documents the overdraw the IfEnough guard prevents)", got)
	}
}

func TestDecreaseUserQuotaIfEnough_Contract(t *testing.T) {
	defer setupSQLiteDB(t)()

	u := seedUser(t, "uif-user", "uif@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// seedUser sets Quota=1_000_000; normalise to a small known value.
	if err := DB.Model(&User{}).Where("id = ?", u.Id).Update("quota", 50).Error; err != nil {
		t.Fatalf("normalise quota: %v", err)
	}

	// sufficient → ok, debited exactly.
	ok, err := DecreaseUserQuotaIfEnough(u.Id, 20)
	if err != nil || !ok {
		t.Fatalf("sufficient debit: ok=%v err=%v, want ok=true nil", ok, err)
	}
	if got := readUserQuota(t, u.Id); got != 30 {
		t.Fatalf("quota = %d, want 30 (50-20)", got)
	}

	// exact-enough (30, want 30) → ok, drains to 0.
	ok, err = DecreaseUserQuotaIfEnough(u.Id, 30)
	if err != nil || !ok {
		t.Fatalf("exact debit: ok=%v err=%v, want ok=true nil", ok, err)
	}
	if got := readUserQuota(t, u.Id); got != 0 {
		t.Fatalf("quota = %d, want 0", got)
	}

	// insufficient (0, want 1) → ok=false, UNCHANGED, never negative.
	ok, err = DecreaseUserQuotaIfEnough(u.Id, 1)
	if err != nil {
		t.Fatalf("insufficient debit err: %v, want nil", err)
	}
	if ok {
		t.Fatal("insufficient debit: ok=true, want ok=false (guard must refuse)")
	}
	if got := readUserQuota(t, u.Id); got != 0 {
		t.Fatalf("quota = %d, want 0 (unchanged, not negative)", got)
	}

	// negative amount → error.
	if _, err := DecreaseUserQuotaIfEnough(u.Id, -1); err == nil {
		t.Fatal("negative amount: want error")
	}
}

// TestDecreaseUserQuota_Unconditional_AllowsNegative_Characterization — the user
// twin of the token characterization above.
func TestDecreaseUserQuota_Unconditional_AllowsNegative_Characterization(t *testing.T) {
	defer setupSQLiteDB(t)()

	u := seedUser(t, "uunc-user", "uunc@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := DB.Model(&User{}).Where("id = ?", u.Id).Update("quota", 100).Error; err != nil {
		t.Fatalf("normalise quota: %v", err)
	}

	if err := DecreaseUserQuota(u.Id, 150); err != nil {
		t.Fatalf("DecreaseUserQuota: %v", err)
	}
	if got := readUserQuota(t, u.Id); got != -50 {
		t.Fatalf("unconditional quota = %d, want -50 (documents the overdraw the IfEnough guard prevents)", got)
	}
}
