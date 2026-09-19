package governance

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// TestMain forces this package's fire-and-forget spawn seam inline for the
// whole test binary.
//
// RecordAuditEvent hands the event to the configured AuditWriter on a spawned
// goroutine. Tests here install writers and then reset auditWriterRef when
// they return; callers in other packages install writers bound to a per-test
// database handle they close on cleanup. Neither has a join point while the
// spawn is detached. Forcing AsyncGo inline gives one: the write is done
// before the call that caused it returns. Production is unaffected — AsyncGo's
// default value is gopool.Go (audit.go).
func TestMain(m *testing.M) {
	AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}

// countingAuditWriter counts CreateAuditEvent calls. The counter is atomic
// because the production path may run it on a spawned goroutine; under the
// inline seam installed by TestMain it runs on the caller's.
type countingAuditWriter struct{ n atomic.Int64 }

func (w *countingAuditWriter) CreateAuditEvent(*entity.AuditEvent) error {
	w.n.Add(1)
	return nil
}

// TestRecordAuditEvent_HonoursSeam is the oracle for the seam: with AsyncGo
// forced inline (TestMain above), the writer must have been called by the time
// RecordAuditEvent returns — no channel, no sleep, no polling. A dispatch that
// calls gopool.Go directly instead of going through the seam leaves the count
// at 0 at that instant, which is what makes this test red.
func TestRecordAuditEvent_HonoursSeam(t *testing.T) {
	prev := auditWriterRef.Load()
	t.Cleanup(func() { auditWriterRef.Store(prev) })

	w := &countingAuditWriter{}
	SetAuditWriter(w)

	RecordAuditEvent(&entity.AuditEvent{Action: ActionTokenCreated, Resource: ResourceToken})

	if got := w.n.Load(); got != 1 {
		t.Fatalf("CreateAuditEvent calls at the instant RecordAuditEvent returned = %d, want 1 — "+
			"the dispatch did not go through the AsyncGo seam", got)
	}
}

// TestRecordAuditEvent_NilWriterIsNoOp pins SetAuditWriter's contract that a
// nil writer means "no writer", which the doc comment claimed and the code did
// not honour.
//
// auditWriterRef is an atomic.Pointer[AuditWriter], so SetAuditWriter(nil)
// stores a NON-NIL pointer to a NIL INTERFACE: the `wp == nil` guard in
// RecordAuditEvent passes it straight through to writer.CreateAuditEvent and
// the call panics. internal/adapter/repo's admin_permission_grant_test.go
// detaches its db-bound writer exactly that way
// (`t.Cleanup(func() { governance.SetAuditWriter(nil) })`), and any audit the
// next test triggered hit the panic — invisible until cycle 12, because a
// gopool dispatch recovers panics, so the write just disappeared. Under
// `go test -shuffle=on` with the seam inline it became a hard test panic,
// which is how it surfaced.
//
// The package's existing TestRecordAuditEvent_NilWriter (audit_test.go) does
// NOT cover this: it stores a nil POINTER (auditWriterRef.Store(nil)), which
// the first guard already handled. The two tests look like duplicates and
// exercise opposite sides of the bug.
//
// Deleting the `writer == nil` early return in RecordAuditEvent's guard makes
// this test panic instead of pass.
func TestRecordAuditEvent_NilWriterIsNoOp(t *testing.T) {
	prev := auditWriterRef.Load()
	t.Cleanup(func() { auditWriterRef.Store(prev) })

	SetAuditWriter(nil)

	// Must not panic, and must leave the caller's context marking intact.
	RecordAuditEvent(&entity.AuditEvent{Action: ActionTokenCreated, Resource: ResourceToken})

	// And a writer installed afterwards must still be used — "no writer" is a
	// state, not a latch.
	w := &countingAuditWriter{}
	SetAuditWriter(w)
	RecordAuditEvent(&entity.AuditEvent{Action: ActionTokenCreated, Resource: ResourceToken})
	if got := w.n.Load(); got != 1 {
		t.Fatalf("CreateAuditEvent calls after re-installing a writer = %d, want 1", got)
	}
}
