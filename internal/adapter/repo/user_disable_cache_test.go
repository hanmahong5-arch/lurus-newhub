package repo

// user_disable_cache_test.go — DisableUserById is the corrective action the
// cost-spike middleware and the platform erasure seam use to lock an account
// (admin edits go through AdminUpdateUser, which rewrites the whole hash).
// If it leaves a stale "enabled" entry in the user cache, authHelper's
// per-request re-validation (middleware/auth.go) keeps reading the old
// status and the disable is cosmetic until the cache's own TTL expires.
// Exercised against a real miniredis instance (not a hand-built cache
// struct) so the assertion is about the actual cache-invalidation call, not
// a stand-in for it.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestDisableUserById_InvalidatesUserCache(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	repoWithMiniRedis(t)

	user := seedUser(t, "cache-disable-user", "cache-disable@local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	// Warm the cache the same way production does after a DB read/write —
	// updateUserCache mirrors GetUserCache's own async refresh-on-miss path.
	if err := updateUserCache(*user); err != nil {
		t.Fatalf("updateUserCache (warm): %v", err)
	}
	warm, err := GetUserCache(user.Id)
	if err != nil {
		t.Fatalf("GetUserCache (warm): %v", err)
	}
	if warm.Status != common.UserStatusEnabled {
		t.Fatalf("warm cache status = %d, want Enabled before disabling", warm.Status)
	}

	if err := DisableUserById(user.Id); err != nil {
		t.Fatalf("DisableUserById: %v", err)
	}

	// No time advance, no TTL expiry — the very next read must observe the
	// disable, which only happens if DisableUserById invalidated the cache
	// entry (forcing a DB re-read) rather than leaving the warm Enabled
	// entry in place.
	got, err := GetUserCache(user.Id)
	if err != nil {
		t.Fatalf("GetUserCache (post-disable): %v", err)
	}
	if got.Status != common.UserStatusDisabled {
		t.Errorf("cached status = %d, want %d (Disabled) immediately after DisableUserById", got.Status, common.UserStatusDisabled)
	}
}
