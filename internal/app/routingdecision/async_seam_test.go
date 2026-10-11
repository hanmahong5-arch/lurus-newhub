package routingdecision

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
)

// billing_log_row_test.go swaps common.RedisEnabled (and the repo DB handles)
// and restores them in t.Cleanup, around a real settleEval that reaches repo,
// app and governance — each of which may hand work to a detached goroutine.
// Forcing those seams inline means every spawn has finished before the test
// that caused it restores the globals (see repo.AsyncGo,
// internal/adapter/repo/async.go). repo's
// TestPackagesSwappingRedisGlobalsForceTheSeam requires this TestMain.
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	app.AsyncGo = func(f func()) { f() }
	governance.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
