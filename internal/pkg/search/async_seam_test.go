package search

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// TestMain forces this package's fire-and-forget spawn seam inline for the
// whole test binary.
//
// Tests here swap the package globals Client / Enabled / SyncEnabled /
// RetryCount / RetryDelay / Debug / IndexPrefix and restore them from
// t.Cleanup (cov_helpers_test.go withFakeClient/withDisabled). The work the
// Sync*Async functions submit reads those same globals, and a pool submission
// has no join point, so the restore and the read run concurrently. Forcing
// AsyncGo inline means each submission has finished before the test that made
// it returns. Production is unaffected: AsyncGo's default value submits to
// asyncPool (sync.go).
func TestMain(m *testing.M) {
	AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}

// TestSyncLogsBatchAsync_HonoursSeam is the oracle for the seam: with AsyncGo
// forced inline (TestMain above), SyncLogsBatchAsync must have issued the
// index request by the time it returns — no polling, no sleep. A submission
// that goes straight to asyncPool.Go instead of through the seam leaves
// fm.requests() empty at that instant, which is what makes this test red.
func TestSyncLogsBatchAsync_HonoursSeam(t *testing.T) {
	_, fm := withFakeClient(t)
	SyncEnabled = true
	SyncBatchSize = 1000
	WorkerCount = 2
	SyncInterval = 0
	RetryCount = 1
	resetSyncGlobals(t)
	if err := InitSyncWithContext(context.Background()); err != nil {
		t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
	}

	SyncLogsBatchAsync([]*Log{sampleLog()})

	reqs := fm.requests()
	if len(reqs) != 1 {
		t.Fatalf("after SyncLogsBatchAsync returned, fm.requests() = %d (%+v), want exactly 1 — "+
			"the batch submission did not go through the AsyncGo seam", len(reqs), reqs)
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != "/indexes/logs/documents" {
		t.Errorf("request = %s %s, want POST /indexes/logs/documents", reqs[0].Method, reqs[0].Path)
	}
}

// TestSyncAsyncFunctions_HonourSeam covers the other three submissions the
// same way, so a later edit cannot quietly route one of them back around the
// seam. Enumerated by grepping `asyncPool.Go(` and `AsyncGo(` in this package's
// non-test files: SyncLogAsync, SyncLogsBatchAsync (above), SyncUserAsync,
// SyncChannelAsync. InitSyncWithContext's `go ScheduledSyncWithContext(...)`
// is deliberately not in that set — it is a lifecycle loop joined by context
// cancellation (StopSync), not a fire-and-forget task, and forcing it inline
// would block until its context is cancelled.
func TestSyncAsyncFunctions_HonourSeam(t *testing.T) {
	cases := []struct {
		name string
		call func()
		path string
	}{
		{"SyncLogAsync", func() { SyncLogAsync(sampleLog()) }, "/indexes/logs/documents"},
		{"SyncUserAsync", func() { SyncUserAsync(sampleUser()) }, "/indexes/users/documents"},
		{"SyncChannelAsync", func() { SyncChannelAsync(&Channel{Id: 1, Name: "c"}) }, "/indexes/channels/documents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, fm := withFakeClient(t)
			SyncEnabled = true
			WorkerCount = 2
			SyncInterval = 0
			RetryCount = 1
			resetSyncGlobals(t)
			if err := InitSyncWithContext(context.Background()); err != nil {
				t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
			}

			tc.call()

			reqs := fm.requests()
			if len(reqs) != 1 || reqs[0].Path != tc.path {
				t.Fatalf("after %s returned, fm.requests() = %+v, want exactly one %s — "+
					"the submission did not go through the AsyncGo seam", tc.name, reqs, tc.path)
			}
		})
	}
}
