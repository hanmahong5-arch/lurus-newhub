package middleware

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// Tests in this package swap common.RDB / common.RedisEnabled and restore them
// in t.Cleanup, and they call into repo, which spawns fire-and-forget cache
// writers. A detached goroutine outlives the test that spawned it, so the
// restore and the cache write run concurrently — a data race, and a nil
// dereference once the swapped-out client has been closed.
//
// Forcing the seam inline means every spawn has finished before the spawning
// test returns. See repo.AsyncGo (internal/adapter/repo/async.go) for the full
// story; production is unaffected, since AsyncGo's value there is gopool.Go.
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	code := m.Run()
	// rate_limit_message_lock_test.go's scanner-honesty floor: only checked
	// after every test has had a chance to record a site, only when the
	// package otherwise passed, and only on a full, unfiltered run — a
	// deliberate `-test.run` selecting a subset of tests (e.g. mutation
	// verification driving one Test function at a time) legitimately
	// exercises fewer sites and must not be reported as a regression, and a
	// `-test.list` invocation (go test -list, used by the security-suite
	// selector guard in CI) never executes a test at all.
	if code == 0 && !hasTestRunFilter() && !hasTestListFlag() {
		if ok, seen, sites := requireRateLimitMessageSitesFloorMet(); !ok {
			fmt.Fprintf(os.Stderr, "rate_limit_message_lock_test.go: only %d distinct reject sites exercised (want >= %d): %v\n", seen, rlMessageSitesFloor, sites)
			code = 1
		}
	}
	os.Exit(code)
}

// hasTestRunFilter reports whether the test binary was invoked with
// -test.run (directly or via `go test -run`), which narrows which Test
// functions execute.
func hasTestRunFilter() bool {
	for _, a := range os.Args[1:] {
		if a == "-test.run" || strings.HasPrefix(a, "-test.run=") {
			return true
		}
	}
	return false
}

// hasTestListFlag reports whether the binary was invoked with -test.list
// (`go test -list`), which prints matching test names and runs nothing — so
// zero recorded sites is the expected outcome, not a regression.
func hasTestListFlag() bool {
	for _, a := range os.Args[1:] {
		if a == "-test.list" || strings.HasPrefix(a, "-test.list=") {
			return true
		}
	}
	return false
}
