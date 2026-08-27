package repo

// r5d_user_delete_cascade_test.go — G5b: deleting a user must also revoke
// every relay token it owns. See DeleteUserById (user.go) for the fix and
// its rationale.

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func r5dSeedDeleteCascadeUser(t *testing.T) *User {
	t.Helper()
	u := &User{
		Username: "r5d-del-" + common.GetUUID(), DisplayName: "r5d", Email: common.GetUUID() + "@test.local",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		TenantId: "default", Group: "default", Quota: 1000,
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func r5dSeedDeleteCascadeToken(t *testing.T, userID int) *Token {
	t.Helper()
	// Key must be exactly 48 chars — Token.Key is `char(48)` (token.go:17).
	// A shorter value round-trips through PostgreSQL blank-padded to 48
	// chars, which changes cacheDeleteToken/cacheSetToken's HMAC input and
	// silently breaks cache-key equality; real tokens never hit this because
	// every production key comes from common.GenerateRandomKey(48) (see
	// AutoCreateDefaultToken, switch_redeem.go's provisionSwitchEndUserToken)
	// — mirror that width here instead of a short "sk-...-<uuid>" fixture.
	key, err := common.GenerateRandomKey(48)
	if err != nil {
		t.Fatalf("generate token key: %v", err)
	}
	tok := &Token{
		UserId:         userID,
		TenantId:       "default",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "r5d-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok
}

// TestUserDelete_TokensSurviveSoftDeleteAlone is a CHARACTERIZATION test: it
// pins today's actual behavior of the unchanged User.Delete() method (a bare
// soft delete of the users row, nothing else) in isolation, BEFORE
// DeleteUserById's cascade fix is layered on top of it. This is what makes
// the pre-fix defect real: a soft-deleted user's tokens keep validating.
//
// Observed by running this test against the unmodified code (go test -run
// TestUserDelete_TokensSurviveSoftDeleteAlone ./internal/adapter/repo/...,
// TEST_POSTGRES_DSN set): GetTokenByKey found the token row unchanged
// (Status still TokenStatusEnabled) after user.Delete(), and GetUserCache
// returned a non-nil error whose message was exactly "record not found" —
// GORM's soft-delete scope makes User.Delete()'s target invisible to
// GetUserById's plain DB.First(&user, "id = ?", id), so
// repo.GetUserCache (Redis disabled in this test tier, so it falls through
// to GetUserById) surfaces gorm.ErrRecordNotFound instead of a clean
// "user disabled" signal. middleware.TokenAuth (auth.go:472-475) turns that
// into a 500 for the caller, not the 403 a genuinely deleted user should
// get — but the relay-authorization outcome for the token itself is what
// this pins: nothing here revoked it.
func TestUserDelete_TokensSurviveSoftDeleteAlone(t *testing.T) {
	SetupTestDB(t)
	u := r5dSeedDeleteCascadeUser(t)
	tok := r5dSeedDeleteCascadeToken(t, u.Id)

	if err := u.Delete(); err != nil {
		t.Fatalf("User.Delete(): %v", err)
	}

	// The token row itself is completely untouched by a bare user soft
	// delete: still present, still Enabled.
	gotTok, err := GetTokenByKey(tok.Key, true)
	if err != nil {
		t.Fatalf("token must still be resolvable by key after a bare user soft-delete, got err: %v", err)
	}
	if gotTok.Status != common.TokenStatusEnabled {
		t.Fatalf("token status changed by a bare user soft-delete (it must not — User.Delete() never touches tokens), got status=%d", gotTok.Status)
	}

	// GetUserCache — the exact call middleware.TokenAuth makes to decide
	// whether the token's owner is still allowed to relay — surfaces the
	// raw "record not found" error rather than a clean disabled/forbidden
	// signal, because GetUserById's DB.First is scoped out by GORM's soft
	// delete on the now-deleted row.
	if _, err := GetUserCache(u.Id); err == nil {
		t.Fatal("expected GetUserCache to fail for a soft-deleted user (this is the broken signal the fix must route around at the token level, not paper over here)")
	} else if err.Error() != "record not found" {
		t.Fatalf("observed error text changed from the one this test documents (%q) — re-verify the characterization before trusting it: got %q", "record not found", err.Error())
	}
}

// TestDeleteUserById_RevokesAllTokens is the fixed-behavior test: after the
// cascade, every token row the user owned must be gone (soft-deleted), not
// merely orphaned.
func TestDeleteUserById_RevokesAllTokens(t *testing.T) {
	SetupTestDB(t)
	u := r5dSeedDeleteCascadeUser(t)
	tokA := r5dSeedDeleteCascadeToken(t, u.Id)
	tokB := r5dSeedDeleteCascadeToken(t, u.Id)
	// A token belonging to a DIFFERENT user must survive untouched — the
	// cascade is scoped to user_id, not global.
	other := r5dSeedDeleteCascadeUser(t)
	otherTok := r5dSeedDeleteCascadeToken(t, other.Id)

	if err := DeleteUserById(u.Id); err != nil {
		t.Fatalf("DeleteUserById: %v", err)
	}

	var liveCount int64
	if err := DB.Model(&Token{}).Where("user_id = ?", u.Id).Count(&liveCount).Error; err != nil {
		t.Fatalf("count live tokens: %v", err)
	}
	if liveCount != 0 {
		t.Fatalf("expected 0 live tokens for the deleted user, got %d", liveCount)
	}

	for _, tok := range []*Token{tokA, tokB} {
		if _, err := GetTokenByKey(tok.Key, true); err == nil {
			t.Errorf("token %d must no longer resolve by key after DeleteUserById, but it still does", tok.Id)
		}
	}

	// The other user's token must be untouched.
	if _, err := GetTokenByKey(otherTok.Key, true); err != nil {
		t.Errorf("an unrelated user's token must survive DeleteUserById, got err: %v", err)
	}

	// The user row itself must be soft-deleted (unscoped lookup still finds
	// it, scoped lookup does not).
	var scoped User
	if err := DB.Where("id = ?", u.Id).First(&scoped).Error; err == nil {
		t.Fatal("expected the deleted user to be invisible to a scoped lookup")
	}
	var unscoped User
	if err := DB.Unscoped().Where("id = ?", u.Id).First(&unscoped).Error; err != nil {
		t.Fatalf("expected the deleted user row to still exist (soft delete, not hard delete): %v", err)
	}
}

// TestDeleteUserById_InvalidatesRedisTokenCache is the non-hollow half of
// the proof: a bulk `DB.Where("user_id = ?").Delete(&Token{})` would satisfy
// TestDeleteUserById_RevokesAllTokens (the DB rows would still end up
// deleted) while silently skipping cacheDeleteToken per key — a token
// served from a warm Redis cache would keep validating from cache until its
// TTL even though the DB row is gone. This test pre-warms the token cache
// via cacheSetToken (mirroring what GetTokenByKey(key, false) does on a
// live cache hit) and asserts the cache entry is actually gone after
// DeleteUserById, polling briefly because Token.Delete()'s cache
// invalidation runs on a detached gopool goroutine (token.go:404-417).
func TestDeleteUserById_InvalidatesRedisTokenCache(t *testing.T) {
	SetupTestDB(t)
	repoWithMiniRedis(t)

	u := r5dSeedDeleteCascadeUser(t)
	tok := r5dSeedDeleteCascadeToken(t, u.Id)

	if err := cacheSetToken(*tok); err != nil {
		t.Fatalf("pre-warm token cache: %v", err)
	}
	if _, err := cacheGetTokenByKey(tok.Key); err != nil {
		t.Fatalf("sanity: token cache must be warm before delete, got err: %v", err)
	}

	if err := DeleteUserById(u.Id); err != nil {
		t.Fatalf("DeleteUserById: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		_, err := cacheGetTokenByKey(tok.Key)
		if err != nil {
			break // cache miss — invalidation landed
		}
		if time.Now().After(deadline) {
			t.Fatal("token cache entry still present 500ms after DeleteUserById — cacheDeleteToken was not run per key (a bulk-delete implementation would fail exactly this way)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
