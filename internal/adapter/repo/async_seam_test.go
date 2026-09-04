package repo

import (
	"os"
	"testing"
)

// TestMain runs this package's fire-and-forget side effects inline for the
// whole test binary, for the reason spelled out on AsyncGo in async.go: those
// goroutines read globals that production sets once at startup and tests swap
// per-test, and a detached goroutine outlives the test that spawned it.
//
// Running them synchronously means every spawn has finished before the
// spawning test returns, so a later test's setup or t.Cleanup cannot race it.
// It also makes the tests honest: a cache write that used to land whenever the
// scheduler felt like it is now observable at the point it was requested.
func TestMain(m *testing.M) {
	AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
