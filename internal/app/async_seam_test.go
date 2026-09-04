package app

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestMain forces PostConsumeQuota's async side effects (see AsyncGo in
// quota.go) to run inline for the whole app test binary. Under the -race gate
// the production gopool goroutines read package globals such as
// common.RedisEnabled; running them synchronously guarantees those reads finish
// before the spawning test returns, so they cannot race a later test's
// setup/teardown that restores those globals. Production behaviour is unchanged
// (AsyncGo stays gopool.Go outside tests).
func TestMain(m *testing.M) {
	AsyncGo = func(f func()) { f() }
	// The same treatment for the repo layer. app's own seam covered only the
	// spawns in quota.go, but PostConsumeQuota reaches IncreaseTokenQuota in
	// internal/adapter/repo, which spawned through gopool directly — so the
	// cache writer outlived the test and raced a later test's miniredis
	// teardown. CI caught it as a DATA RACE plus a nil dereference inside the
	// pool, on a PR that touched neither package.
	repo.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
