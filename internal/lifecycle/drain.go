package lifecycle

// drain.go — cycle 13 L11 (graceful shutdown). Before this, a shutdown that
// ran past its budget returned an error from httpServer.Shutdown, which
// main.go's errgroup propagated up to a FatalLog + os.Exit(1) — the busiest
// pod on the cluster (the one still finishing in-flight relay streams when
// the budget expired) crash-looped instead of exiting cleanly, there was no
// readiness flip to pull it out of the Service's endpoint list first, and
// nothing recorded how many requests got cut. Drainer answers the three: it
// is the single source of truth GetHealthDetailed (readiness) and GetStatus
// (startup + liveness) consult to fail their probes the instant a shutdown
// signal is observed, and it turns a budget timeout into an accounted,
// non-fatal outcome.

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	dto "github.com/prometheus/client_model/go"
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
// pattern common.IsLeader/SetLeader uses for HA state) — health.go and
// misc.go's GetStatus import this package and call the free functions below
// rather than threading a *Drainer through gin.
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

// ResetForTest restores d to the not-draining state. It is meant for tests:
// draining has no way back for the remaining life of the process it belongs
// to, so a production caller would be a bug. It exists so tests that mark the process-wide
// Default() draining (health_draining_test.go, in package handler) can undo
// that afterward instead of leaking a permanently-drained flag into every
// later test in the same binary, mirroring the *ForTest reset convention
// used elsewhere in this codebase (e.g. totp.ResetStateForTest).
func (d *Drainer) ResetForTest() {
	d.draining.Store(false)
}

// Shutdown marks d draining, then attempts a graceful HTTP shutdown within
// ctx's deadline. Three outcomes, none of which returns a non-nil error —
// the caller (main.go) must not FatalLog/os.Exit(1) on a shutdown that ran
// out of budget, which is exactly what GRACEFUL_SHUTDOWN_TIMEOUT used to
// cause:
//
//   - srv.Shutdown returns nil: every connection went idle and closed on its
//     own inside the budget. cut is 0 and inflight is not consulted —
//     nothing was cut.
//   - ctx expires first: srv.Shutdown returns context.DeadlineExceeded. That
//     is the expected result of a budget, not a process failure. cut is
//     whatever inflightRequests reports at that instant, logged for the
//     runbook (doc/runbook/graceful-drain.md) and, once a counter named
//     lurus_gateway_shutdown_cut_requests_total exists in internal/pkg/metrics
//     (not declared there at the time of writing — grep the package before
//     assuming otherwise), recorded as a counter too; until then this SysLog
//     line is the record.
//   - srv.Shutdown returns some other error: net/http hands back the first
//     listener-close failure, and it does so only after every connection has
//     gone idle (net/http.Server.Shutdown returns lnerr from inside the
//     closeIdleConns loop). So nothing was cut here either — cut is 0 and the
//     error gets its own log line instead of being dressed up as a budget
//     timeout with a meaningless count, which is what the first cut of this
//     file did.
//
// inflight may be nil, in which case the count comes from the in-flight
// request gauge; see inflightRequests. srv must not be nil.
func (d *Drainer) Shutdown(ctx context.Context, srv *http.Server, inflight func() int) (int, error) {
	d.MarkDraining()

	shutdownErr := srv.Shutdown(ctx)
	switch {
	case shutdownErr == nil:
		common.SysLog("graceful shutdown: complete within budget, cut=0 requests")
		return 0, nil
	case errors.Is(shutdownErr, context.DeadlineExceeded):
		cut := inflightRequests(inflight)
		common.SysLogf("graceful shutdown: budget exceeded, cut=%d in-flight request(s)", cut)
		return cut, nil
	default:
		common.SysLogf("graceful shutdown: shutdown error (not a budget timeout), cut=0 requests: %v", shutdownErr)
		return 0, nil
	}
}

// inflightRequests reports how many requests were still being served at the
// moment the shutdown budget expired.
//
// The caller may supply its own probe (a test does; main.go may too, e.g.
// from an http.Server.ConnState hook). With inflight nil the count comes
// from metrics.ActiveConnections, which despite its name is a request gauge:
// internal/pkg/metrics/middleware.go:50-51 Incs it on entry and Decs it on
// exit around c.Next(), and router/main.go:26 installs that middleware on the
// whole engine. Reading it in-process through the client library's own
// Write(&dto.Metric{}) — the mechanism testutil.ToFloat64 uses — avoids
// scraping and parsing /metrics from inside the process that is shutting
// down. A negative or unreadable value is reported as 0 rather than a
// nonsense count.
func inflightRequests(inflight func() int) int {
	if inflight != nil {
		return inflight()
	}
	var m dto.Metric
	if err := metrics.ActiveConnections.Write(&m); err != nil {
		return 0
	}
	value := m.GetGauge().GetValue()
	if value <= 0 {
		return 0
	}
	return int(value)
}
