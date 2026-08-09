package search

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// resetSyncGlobals isolates the package-level async worker pool / scheduled
// sync context/cancel across tests, restoring (and, if the test left a
// goroutine running, cancelling it) on cleanup.
// covSyncLogBuffer is a mutex-guarded sink for gin.DefaultWriter. The mutex is
// load-bearing, not defensive: reading it after the scheduled-sync goroutine has
// written its exit line is what establishes a happens-before edge with that
// goroutine, which is otherwise unjoinable.
type covSyncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *covSyncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *covSyncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// drainAsyncPool blocks until every task submitted so far has finished. The
// pool's queue is FIFO, so once WorkerCount sentinels have run, no worker can
// still be inside an earlier task. Without this the workers keep reading
// Client/RetryCount/IndexPrefix while the test's cleanup restores them.
func drainAsyncPool(workers int) {
	if asyncPool == nil {
		return
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		asyncPool.Go(wg.Done)
	}
	wg.Wait()
}

func resetSyncGlobals(t *testing.T) {
	t.Helper()
	prevPool, prevCtx, prevCancel := asyncPool, syncCtx, syncCancel
	asyncPool, syncCtx, syncCancel = nil, nil, nil

	// InitSyncWithContext starts ScheduledSyncWithContext as a bare goroutine
	// that reads the package globals (Client / Enabled / SyncEnabled / Debug)
	// and offers no join point, so restoring those globals underneath it is a
	// genuine data race. Its exit log line is the only observable signal, so
	// gin.DefaultWriter is routed through a mutex-guarded buffer for the
	// duration and the cleanup waits for that line before restoring.
	prevWriter := gin.DefaultWriter
	logBuf := &covSyncLogBuffer{}
	gin.DefaultWriter = logBuf

	t.Cleanup(func() {
		workers := WorkerCount
		if syncCancel != nil {
			syncCancel()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) &&
				!strings.Contains(logBuf.String(), "scheduled sync stopped") {
				time.Sleep(5 * time.Millisecond)
			}
			if !strings.Contains(logBuf.String(), "scheduled sync stopped") {
				t.Error("scheduled sync goroutine did not stop within 5s of cancellation")
			}
		}
		drainAsyncPool(workers)
		gin.DefaultWriter = prevWriter
		asyncPool, syncCtx, syncCancel = prevPool, prevCtx, prevCancel
	})
}

// waitForRequest polls fm until a request matching want arrives or timeout
// elapses; used to observe work submitted to the async worker pool without
// coupling the test to internal goroutine scheduling.
func waitForRequest(t *testing.T, fm *fakeMeili, timeout time.Duration, want func(recordedReq) bool) recordedReq {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, r := range fm.requests() {
			if want(r) {
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for expected async request; saw %+v", timeout, fm.requests())
	return recordedReq{}
}

func TestInitSyncWithContext(t *testing.T) {
	t.Run("disabled creates no worker pool", func(t *testing.T) {
		withDisabled(t)
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		if asyncPool != nil {
			t.Fatal("asyncPool was created while integration disabled")
		}
	})

	t.Run("search enabled but sync disabled creates no worker pool", func(t *testing.T) {
		_, _ = withFakeClient(t)
		SyncEnabled = false
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		if asyncPool != nil {
			t.Fatal("asyncPool was created while SyncEnabled=false")
		}
	})

	t.Run("interval<=0 creates a pool but does not schedule background sync", func(t *testing.T) {
		_, _ = withFakeClient(t)
		SyncEnabled = true
		SyncInterval = 0
		WorkerCount = 2
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		if asyncPool == nil {
			t.Fatal("expected the worker pool to be created")
		}
		if syncCancel != nil {
			t.Fatal("did not expect a scheduled-sync goroutine when SyncInterval<=0")
		}
	})

	t.Run("interval>0 creates a pool and a stoppable scheduled sync goroutine", func(t *testing.T) {
		_, _ = withFakeClient(t)
		SyncEnabled = true
		SyncInterval = 1
		WorkerCount = 2
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		if asyncPool == nil {
			t.Fatal("expected the worker pool to be created")
		}
		if syncCancel == nil {
			t.Fatal("expected a scheduled-sync cancel func when SyncInterval>0")
		}
		StopSync() // must not panic / hang
	})

	t.Run("InitSync wraps InitSyncWithContext with a background context", func(t *testing.T) {
		withDisabled(t)
		resetSyncGlobals(t)
		if err := InitSync(); err != nil {
			t.Fatalf("InitSync() error = %v, want nil", err)
		}
	})
}

func TestStopSync_NilCancelIsSafe(t *testing.T) {
	resetSyncGlobals(t)
	syncCancel = nil
	StopSync() // must not panic when nothing was ever started
}

func TestScheduledSyncWithContext(t *testing.T) {
	t.Run("disabled returns immediately without starting a ticker", func(t *testing.T) {
		withDisabled(t)
		done := make(chan struct{})
		go func() {
			ScheduledSyncWithContext(context.Background(), 100)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ScheduledSyncWithContext did not return promptly when disabled")
		}
	})

	t.Run("stops promptly when context is already cancelled, before any tick", func(t *testing.T) {
		_, _ = withFakeClient(t)
		SyncEnabled = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{})
		go func() {
			ScheduledSyncWithContext(ctx, 100) // long interval; must exit via ctx.Done()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ScheduledSyncWithContext did not stop when context was already cancelled")
		}
	})

	t.Run("fires on tick and stops when cancelled mid-flight", func(t *testing.T) {
		_, _ = withFakeClient(t)
		SyncEnabled = true
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			ScheduledSyncWithContext(ctx, 1) // 1s tick, exercises the ticker.C branch
			close(done)
		}()
		time.Sleep(1500 * time.Millisecond) // let at least one tick fire
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ScheduledSyncWithContext did not stop after cancel following a tick")
		}
	})
}

func TestScheduledSync_DeprecatedWrapper(t *testing.T) {
	// Only exercise the disabled path directly: enabled would run forever
	// against context.Background() with no way to stop it from the caller.
	withDisabled(t)
	done := make(chan struct{})
	go func() {
		ScheduledSync(100)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ScheduledSync did not return promptly when disabled")
	}
}

func TestSyncLogAsync(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		withDisabled(t)
		resetSyncGlobals(t)
		SyncLogAsync(sampleLog()) // must not panic despite nil Client
	})

	t.Run("sync disabled is a no-op even though search is enabled", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = false
		resetSyncGlobals(t)
		SyncLogAsync(sampleLog())
		time.Sleep(50 * time.Millisecond)
		if len(fm.requests()) != 0 {
			t.Fatalf("expected no HTTP calls with SyncEnabled=false, got %+v", fm.requests())
		}
	})

	t.Run("pool not initialized falls back to synchronous indexing", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		resetSyncGlobals(t) // asyncPool stays nil
		SyncLogAsync(sampleLog())
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/indexes/logs/documents" {
			t.Fatalf("expected exactly one synchronous index call, got %+v", reqs)
		}
	})

	t.Run("pool initialized submits work asynchronously", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		WorkerCount = 2
		SyncInterval = 0
		RetryCount = 1 // RetryWithBackoff with RetryCount<=0 never calls fn at all (see FINDING in cov_config_test.go)
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		SyncLogAsync(sampleLog())
		waitForRequest(t, fm, 2*time.Second, func(r recordedReq) bool {
			return r.Method == http.MethodPost && r.Path == "/indexes/logs/documents"
		})
	})

	t.Run("pool initialized retries a transient upstream failure via RetryWithBackoff", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		WorkerCount = 1
		SyncInterval = 0
		RetryCount = 3
		RetryDelay = 1
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}

		var calls int32
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/indexes/logs/documents" {
				return false
			}
			if atomic.AddInt32(&calls, 1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false // 2nd attempt falls through to the default 202 success
		})

		SyncLogAsync(sampleLog())
		deadline := time.Now().Add(2 * time.Second)
		for atomic.LoadInt32(&calls) < 2 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := atomic.LoadInt32(&calls); got < 2 {
			t.Fatalf("saw %d attempts, want at least 2 (initial failure + retry success)", got)
		}
	})
}

func TestSyncLogsBatchAsync(t *testing.T) {
	t.Run("empty batch is a no-op", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		resetSyncGlobals(t)
		SyncLogsBatchAsync(nil)
		if len(fm.requests()) != 0 {
			t.Fatalf("expected no requests for an empty batch, got %+v", fm.requests())
		}
	})

	t.Run("pool not initialized falls back to synchronous batch indexing", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		SyncBatchSize = 1000
		resetSyncGlobals(t)
		SyncLogsBatchAsync([]*Log{sampleLog(), sampleLog()})
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/indexes/logs/documents" {
			t.Fatalf("expected one synchronous batch call, got %+v", reqs)
		}
	})

	t.Run("pool initialized submits the batch asynchronously", func(t *testing.T) {
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
		waitForRequest(t, fm, 2*time.Second, func(r recordedReq) bool {
			return r.Path == "/indexes/logs/documents"
		})
	})
}

func TestSyncUserAsync(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		withDisabled(t)
		resetSyncGlobals(t)
		SyncUserAsync(sampleUser())
	})

	t.Run("pool not initialized falls back to synchronous indexing", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		resetSyncGlobals(t)
		SyncUserAsync(sampleUser())
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/indexes/users/documents" {
			t.Fatalf("expected one synchronous index call, got %+v", reqs)
		}
	})

	t.Run("pool initialized submits work asynchronously", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		WorkerCount = 2
		SyncInterval = 0
		RetryCount = 1
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		SyncUserAsync(sampleUser())
		waitForRequest(t, fm, 2*time.Second, func(r recordedReq) bool {
			return r.Path == "/indexes/users/documents"
		})
	})
}

func TestSyncChannelAsync(t *testing.T) {
	t.Run("disabled is a no-op", func(t *testing.T) {
		withDisabled(t)
		resetSyncGlobals(t)
		SyncChannelAsync(&Channel{Id: 1, Name: "c"})
	})

	t.Run("pool not initialized falls back to synchronous indexing", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		resetSyncGlobals(t)
		SyncChannelAsync(&Channel{Id: 1, Name: "c"})
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/indexes/channels/documents" {
			t.Fatalf("expected one synchronous index call, got %+v", reqs)
		}
	})

	t.Run("pool initialized submits work asynchronously", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncEnabled = true
		WorkerCount = 2
		SyncInterval = 0
		RetryCount = 1
		resetSyncGlobals(t)
		if err := InitSyncWithContext(context.Background()); err != nil {
			t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
		}
		SyncChannelAsync(&Channel{Id: 1, Name: "c"})
		waitForRequest(t, fm, 2*time.Second, func(r recordedReq) bool {
			return r.Path == "/indexes/channels/documents"
		})
	})
}

func TestSyncStubFunctions(t *testing.T) {
	stubs := map[string]func() error{
		"SyncRecentLogs": func() error { return SyncRecentLogs(3600) },
		"SyncAllLogs":    SyncAllLogs,
		"SyncAllUsers":   SyncAllUsers,
		"SyncAllChannels": SyncAllChannels,
	}
	for name, fn := range stubs {
		t.Run(name+" disabled returns an explicit error", func(t *testing.T) {
			withDisabled(t)
			if err := fn(); err == nil {
				t.Fatalf("%s() error = nil, want error when disabled", name)
			}
		})
		t.Run(name+" enabled returns nil (delegated to the model package)", func(t *testing.T) {
			_, _ = withFakeClient(t)
			if err := fn(); err != nil {
				t.Fatalf("%s() error = %v, want nil when enabled", name, err)
			}
		})
	}
}

func TestRebuildAllIndexes(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		if err := RebuildAllIndexes(); err == nil {
			t.Fatal("RebuildAllIndexes() error = nil, want error when disabled")
		}
	})

	t.Run("enabled rebuilds and syncs all three indexes", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := RebuildAllIndexes(); err != nil {
			t.Fatalf("RebuildAllIndexes() error = %v, want nil", err)
		}
		wantDeletes := map[string]bool{"/indexes/logs": false, "/indexes/users": false, "/indexes/channels": false}
		for _, r := range fm.requests() {
			if r.Method == http.MethodDelete {
				if _, ok := wantDeletes[r.Path]; ok {
					wantDeletes[r.Path] = true
				}
			}
		}
		for path, seen := range wantDeletes {
			if !seen {
				t.Fatalf("RebuildAllIndexes() never issued DELETE %s", path)
			}
		}
	})

	t.Run("logs rebuild failure aborts before users/channels", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/logs/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := RebuildAllIndexes()
		if err == nil {
			t.Fatal("RebuildAllIndexes() error = nil, want error when logs rebuild fails")
		}
		if !strings.Contains(err.Error(), "failed to rebuild logs index") {
			t.Fatalf("RebuildAllIndexes() error = %q, want it to name the logs index", err.Error())
		}
		for _, r := range fm.requests() {
			if r.Path == "/indexes/users" && r.Method == http.MethodDelete {
				t.Fatal("users index rebuild started after logs rebuild failed; expected short-circuit")
			}
		}
	})

	t.Run("users rebuild failure is wrapped and named", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/users/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := RebuildAllIndexes()
		if err == nil {
			t.Fatal("RebuildAllIndexes() error = nil, want error when users rebuild fails")
		}
		if !strings.Contains(err.Error(), "failed to rebuild users index") {
			t.Fatalf("RebuildAllIndexes() error = %q, want it to name the users index", err.Error())
		}
	})

	t.Run("channels rebuild failure is wrapped and named", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/channels/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := RebuildAllIndexes()
		if err == nil {
			t.Fatal("RebuildAllIndexes() error = nil, want error when channels rebuild fails")
		}
		if !strings.Contains(err.Error(), "failed to rebuild channels index") {
			t.Fatalf("RebuildAllIndexes() error = %q, want it to name the channels index", err.Error())
		}
	})
}
