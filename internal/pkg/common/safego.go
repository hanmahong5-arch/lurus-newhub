package common

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync/atomic"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// panicLogSink is the sink for panic-recovery log lines emitted by the
// detached goroutines below. In production it is always nil, so logging goes
// to SysError — behavior is unchanged. Tests install a hook so they can wait
// for the detached goroutine's log call to complete before the test returns;
// otherwise the leaked SysError call runs concurrently with later tests that
// reset the global slog logger and read their private log buffers (data race).
var panicLogSink atomic.Pointer[func(string)]

func logGoroutinePanic(msg string) {
	// Count every recovered goroutine panic before the test log-sink short
	// circuit, so the metric reflects production reality regardless of test hooks.
	metrics.RecordPanic("goroutine")
	if h := panicLogSink.Load(); h != nil {
		(*h)(msg)
		return
	}
	SysError(msg)
}

// SafeGo starts a goroutine with panic recovery.
// If the function panics, it logs the error and recovers.
// Use this for fire-and-forget operations that don't need to be tracked.
func SafeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logGoroutinePanic(fmt.Sprintf("panic in SafeGo: %v\n%s", r, debug.Stack()))
			}
		}()
		f()
	}()
}

// SafeGoWithContext starts a goroutine with panic recovery and context support.
// The function receives a context that should be checked for cancellation.
func SafeGoWithContext(ctx context.Context, f func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logGoroutinePanic(fmt.Sprintf("panic in SafeGoWithContext: %v\n%s", r, debug.Stack()))
			}
		}()
		f(ctx)
	}()
}

// SafeGoNamed starts a named goroutine with panic recovery.
// The name is used in error logging for easier debugging.
func SafeGoNamed(name string, f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logGoroutinePanic(fmt.Sprintf("panic in SafeGo[%s]: %v\n%s", name, r, debug.Stack()))
			}
		}()
		f()
	}()
}

// MustGo starts a goroutine with panic recovery that retries on panic.
// Use with caution - infinite retries can cause issues.
// maxRetries of 0 means no limit.
func MustGo(f func(), maxRetries int) {
	go func() {
		retries := 0
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						retries++
						logGoroutinePanic(fmt.Sprintf("panic in MustGo (retry %d): %v\n%s", retries, r, debug.Stack()))
					}
				}()
				f()
			}()

			// If we reach here without panic, we're done
			if maxRetries > 0 && retries >= maxRetries {
				logGoroutinePanic(fmt.Sprintf("MustGo exceeded max retries (%d)", maxRetries))
				return
			}

			// If no panic occurred, exit the loop
			return
		}
	}()
}
