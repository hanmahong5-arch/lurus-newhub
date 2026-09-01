package repo

// log_by_key_ownership_test.go — ownership guards for GetLogByKey.
//
// GET /api/log/token is TokenAuth-gated but the key it looks up is supplied
// by the caller, so the only thing standing between one customer and every
// other customer's spend history is the ownership predicate inside
// GetLogByKey. These tests pin each arm of that predicate: own key resolves,
// a same-tenant stranger's key does not, a cross-tenant key does not, a
// caller id of 0 (provisioned tokens) is denied outright, and a denial is
// byte-identical to an unknown key so the endpoint cannot be used to prove a
// key exists.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

// seedSameTenantStranger adds a second user + token (with one log row) inside
// tenant A, so "same tenant, different owner" can be told apart from
// "different tenant".
func seedSameTenantStranger(t *testing.T, tenantID string) *Token {
	t.Helper()
	stranger := seedUser(t, "scope-user-a2", "scope-a2@test.local", common.RoleCommonUser, common.UserStatusEnabled, tenantID)
	tok := &Token{UserId: stranger.Id, TenantId: tenantID, Key: common.GetRandomString(48), Name: "tok-a2", Status: common.TokenStatusEnabled}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed stranger token: %v", err)
	}
	row := &Log{UserId: stranger.Id, TenantId: tenantID, Username: stranger.Username, TokenId: tok.Id, TokenName: tok.Name,
		Type: LogTypeConsume, ModelName: "model-a", Quota: 300, Content: "stranger consume", CreatedAt: common.GetTimestamp()}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed stranger log: %v", err)
	}
	return tok
}

func TestGetLogByKey_OwnKeyReturnsRows(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, err := GetLogByKey("sk-"+f.tokenA.Key, f.userA.Id, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(own key): %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("GetLogByKey(own key) = %d rows, want 2", len(logs))
	}
	assertOnlyTenant(t, "GetLogByKey(own key)", logs, f.tenantA.Id)
}

func TestGetLogByKey_ForeignKeySameTenantDenied(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)
	stranger := seedSameTenantStranger(t, f.tenantA.Id)

	// Sanity: the stranger's own call does resolve, so an empty result below
	// means the guard fired and not that the fixture is empty.
	if own, err := GetLogByKey("sk-"+stranger.Key, stranger.UserId, f.tenantA.Id); err != nil || len(own) != 1 {
		t.Fatalf("stranger reading their OWN key must work: %d rows, err=%v", len(own), err)
	}

	logs, err := GetLogByKey("sk-"+stranger.Key, f.userA.Id, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(foreign key) err = %v, want the unknown-key shape (nil)", err)
	}
	if len(logs) != 0 {
		t.Fatalf("GetLogByKey(foreign key, same tenant) leaked %d rows of user %d to user %d", len(logs), stranger.UserId, f.userA.Id)
	}
}

func TestGetLogByKey_CrossTenantDenied(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	logs, err := GetLogByKey("sk-"+f.tokenB.Key, f.userA.Id, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(other tenant) err = %v, want the unknown-key shape (nil)", err)
	}
	if len(logs) != 0 {
		t.Fatalf("GetLogByKey(other tenant) leaked %d rows of tenant %q to tenant %q", len(logs), f.tenantB.Id, f.tenantA.Id)
	}

	// Same key, but the caller claims tenant B while owning nothing there:
	// the user id must not be enough on its own.
	logs, err = GetLogByKey("sk-"+f.tokenA.Key, f.userA.Id, f.tenantB.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(tenant mismatch) err = %v, want nil", err)
	}
	if len(logs) != 0 {
		t.Fatalf("GetLogByKey(tenant mismatch) returned %d rows, want 0", len(logs))
	}
}

// TestGetLogByKey_ZeroCallerIdDenied covers the provisioned-token hazard:
// provisioned tokens carry UserId = 0, so a plain `target.UserId == callerID`
// equality would make every provisioned token the "owner" of every other
// provisioned token's history.
func TestGetLogByKey_ZeroCallerIdDenied(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	provisioned := &Token{UserId: 0, TenantId: f.tenantA.Id, Key: common.GetRandomString(48), Name: "tok-provisioned", Status: common.TokenStatusEnabled}
	if err := DB.Create(provisioned).Error; err != nil {
		t.Fatalf("seed provisioned token: %v", err)
	}
	victim := &Token{UserId: 0, TenantId: f.tenantA.Id, Key: common.GetRandomString(48), Name: "tok-provisioned-2", Status: common.TokenStatusEnabled}
	if err := DB.Create(victim).Error; err != nil {
		t.Fatalf("seed second provisioned token: %v", err)
	}
	if err := LOG_DB.Create(&Log{UserId: 0, TenantId: f.tenantA.Id, TokenId: victim.Id, TokenName: victim.Name,
		Type: LogTypeConsume, ModelName: "model-a", Quota: 400, Content: "provisioned consume", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed provisioned log: %v", err)
	}

	// Cross-key: caller id 0 must not reach another provisioned token's rows.
	logs, err := GetLogByKey("sk-"+victim.Key, 0, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(caller id 0, other provisioned key) err = %v, want nil", err)
	}
	if len(logs) != 0 {
		t.Fatalf("caller id 0 read %d rows of another provisioned token — 0 == 0 is not an identity", len(logs))
	}

	// Its own key is denied too (documented trade-off: deny is the simpler
	// half, and no console surfaces this endpoint for provisioned tokens).
	logs, err = GetLogByKey("sk-"+provisioned.Key, 0, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(caller id 0, own key) err = %v, want nil", err)
	}
	if len(logs) != 0 {
		t.Fatalf("caller id 0 returned %d rows, want 0 (denied outright)", len(logs))
	}

	// An empty tenant fails closed the same way ForTenant("") does.
	logs, err = GetLogByKey("sk-"+f.tokenA.Key, f.userA.Id, "")
	if err != nil {
		t.Fatalf("GetLogByKey(empty tenant) err = %v, want nil", err)
	}
	if len(logs) != 0 {
		t.Fatalf("GetLogByKey(empty caller tenant) returned %d rows, want 0", len(logs))
	}
}

// TestGetLogByKey_SoftDeletedOwnTokenStillReadable pins the deliberate
// Unscoped lookup: revoking your own key must not erase its spend history.
func TestGetLogByKey_SoftDeletedOwnTokenStillReadable(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	if err := DB.Delete(&Token{}, f.tokenA.Id).Error; err != nil {
		t.Fatalf("soft-delete own token: %v", err)
	}
	logs, err := GetLogByKey("sk-"+f.tokenA.Key, f.userA.Id, f.tenantA.Id)
	if err != nil {
		t.Fatalf("GetLogByKey(soft-deleted own token): %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("GetLogByKey(soft-deleted own token) = %d rows, want 2 (own history stays readable)", len(logs))
	}
}

// TestGetLogByKey_DenialIsNotAnExistenceOracle asserts a foreign key and a
// key that exists nowhere are indistinguishable to the caller, on BOTH
// storage layouts (the LOG_SQL_DSN branch answers with an error instead of an
// empty page, so it needs its own comparison).
func TestGetLogByKey_DenialIsNotAnExistenceOracle(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	f := seedLogScopeFixture(t)

	foreignLogs, foreignErr := GetLogByKey("sk-"+f.tokenB.Key, f.userA.Id, f.tenantA.Id)
	unknownLogs, unknownErr := GetLogByKey("sk-no-such-key-anywhere", f.userA.Id, f.tenantA.Id)
	if foreignErr != nil || unknownErr != nil {
		t.Fatalf("join branch: foreignErr=%v unknownErr=%v, want both nil", foreignErr, unknownErr)
	}
	if len(foreignLogs) != len(unknownLogs) {
		t.Fatalf("join branch: foreign key returned %d rows vs %d for an unknown key — that difference is an existence oracle", len(foreignLogs), len(unknownLogs))
	}

	// Separate-log-DB layout: the env var alone selects the branch, the
	// handles stay on the hermetic SQLite fixture.
	t.Setenv("LOG_SQL_DSN", "postgres://placeholder/logs")
	_, foreignErr = GetLogByKey("sk-"+f.tokenB.Key, f.userA.Id, f.tenantA.Id)
	_, unknownErr = GetLogByKey("sk-no-such-key-anywhere", f.userA.Id, f.tenantA.Id)
	if !errors.Is(foreignErr, gorm.ErrRecordNotFound) {
		t.Fatalf("LOG_SQL_DSN branch: foreign key err = %v, want record-not-found (same as unknown)", foreignErr)
	}
	if !errors.Is(unknownErr, gorm.ErrRecordNotFound) {
		t.Fatalf("LOG_SQL_DSN branch: unknown key err = %v, want record-not-found", unknownErr)
	}
}
