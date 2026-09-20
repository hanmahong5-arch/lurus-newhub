package lifecycle

// drain.go — cycle 13 L11 (graceful shutdown). Before this, a shutdown that
// ran past its budget returned an error from httpServer.Shutdown, which
// main.go's errgroup propagated up to a FatalLog + os.Exit(1) — the busiest
// pod on the cluster (the one still finishing in-flight relay streams when
// the budget expired) crash-looped instead of exiting cleanly, there was no
// readiness flip to pull it out of the Service's endpoint list first, and
// nothing recorded how many requests got cut. Drainer fixes all three: it is
// the single source of truth GetHealthDetailed (and, once W wires it,
// GetStatus) consult to fail readiness/liveness the instant a shutdown
// signal is observed, and it turns a budget timeout into an accounted,
// non-fatal outcome.

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// Drainer tracks whether this process has begun draining for shutdown and
// performs the graceful HTTP drain once asked to. The zero value (via
// NewDrainer) starts not-draining.
type Drainer struct {
	draining atomic.Bool
}

// NewDrainer returns a fresh, not-draining Drainer. Most callers want
// Default() instead — this exists mainly so tests (and Shutdown's own
// oracle) can exercise an isolated instance instead of mutating the
// process-wide singleton.
func NewDrainer() *Drainer {
	return &Drainer{}
}

// defaultDrainer is the process-wide instance. Handlers in this codebase are
// plain gin.HandlerFunc values with no dependency injection (the same
// pattern common.IsLeader/SetLeader uses for HA state) — health.go, and
// misc.go's GetStatus once W wires it, import this package and call the
// free functions below rather than threading a *Drainer through gin.
var defaultDrainer = NewDrainer()

// Default returns the process-wide Drainer singleton. main.go's shutdown
// goroutine calls Shutdown on it; every HTTP handler answering a k8s probe
// calls IsDraining (directly, or via the package-level wrapper below).
func Default() *Drainer {
	return defaultDrainer
}

// IsDraining reports whether the process-wide default Drainer has begun
// draining.
func IsDraining() bool {
	return defaultDrainer.IsDraining()
}

// MarkDraining flips the process-wide default Drainer to draining.
func MarkDraining() {
	defaultDrainer.MarkDraining()
}

// MarkDraining flips d to draining. Idempotent and safe for concurrent use;
// the "draining started" line logs at most once per Drainer even if called
// more than once (e.g. Shutdown calling it again after an explicit caller
// already did).
func (d *Drainer) MarkDraining() {
	if !d.draining.Swap(true) {
		common.SysLog("graceful shutdown: draining started, readiness now reports 503")
	}
}

// IsDraining reports whether d has begun draining.
func (d *Drainer) IsDraining() bool {
	return d.draining.Load()
}

// ResetForTest restores d to the not-draining state. Production code never
// calls this — draining has no way back for the remaining life of the
// process it belongs to. It exists so tests that mark the process-wide
// Default() draining (health_draining_test.go, in package handler) can undo
// that afterward instead of leaking a permanently-drained flag into every
// later test in the same binary, mirroring the *ForTest reset convention
// used elsewhere in this codebase (e.g. totp.ResetStateForTest).
func (d *Drainer) ResetForTest() {
	d.draining.Store(false)
}

// Shutdown marks d draining, then attempts a graceful HTTP shutdown within
// ctx's deadline. Two outcomes:
//
//   - ctx does not expire first: srv.Shutdown returns nil once every
//     connection has gone idle and closed on its own. cut is 0 and inflight
//     is never even consulted — nothing was cut.
//   - ctx expires first: srv.Shutdown returns ctx's error (normally
//     context.DeadlineExceeded). That is the expected result of a budget,
//     not a process failure, so Shutdown always returns a nil error here —
//     the caller (main.go) must not FatalLog/os.Exit(1) on it, which is
//     exactly what running out of GRACEFUL_SHUTDOWN_TIMEOUT used to do. cut
//     is whatever inflight() reports at that instant, logged for the runbook
//     and (once L10's metrics.go declares lurus_gateway_shutdown_cut_requests_total)
//     recorded as a counter — until then this SysLog line is the only record,
//     same placeholder-then-wire pattern as L1's wallet-leg counter.
//
// inflight may be nil (treated as always-0); srv must not be nil.
func (d *Drainer) Shutdown(ctx context.Context, srv *http.Server, inflight func() int) (int, error) {
	d.MarkDraining()

	shutdownErr := srv.Shutdown(ctx)
	if shutdownErr == nil {
		common.SysLog("graceful shutdown: complete within budget, cut=0")
		return 0, nil
	}

	cut := 0
	if inflight != nil {
		cut = inflight()
	}
	common.SysLogf("graceful shutdown: budget exceeded, cut=%d in-flight request(s): %v", cut, shutdownErr)
	return cut, nil
}
