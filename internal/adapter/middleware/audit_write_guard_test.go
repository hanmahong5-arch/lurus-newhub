package middleware

// audit_write_guard_test.go — oracle for the L2 audit-completeness
// fail-closed fallback. AuditWriteGuard is exercised against a real gin
// engine + real handlers (not a hand-built *entity.AuditEvent), driving the
// same governance.NewAuditEvent / governance.RecordAuditEvent path production
// handlers use, so a mutation that removes the marking call or the guard's
// method/status checks shows up here.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// recordingAuditWriter captures every event handed to it, safe for
// concurrent use (RecordAuditEvent dispatches on a gopool goroutine).
type recordingAuditWriter struct {
	mu     sync.Mutex
	events []*entity.AuditEvent
}

func (w *recordingAuditWriter) CreateAuditEvent(event *entity.AuditEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return nil
}

func (w *recordingAuditWriter) snapshot() []*entity.AuditEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*entity.AuditEvent, len(w.events))
	copy(out, w.events)
	return out
}

// waitForEvents polls (bounded) for RecordAuditEvent's async gopool.Go write
// to land — mirrors the pattern L1 uses for the same reason (audit.go's
// RecordAuditEvent runs the actual persist on a background goroutine). It
// then holds for a fixed settle window and re-snapshots: a test asserting
// "exactly N events" must not pass just because it stopped looking the
// instant the Nth arrived — a second, unwanted write (e.g. the fallback
// firing even though the handler already audited) needs time to show up
// too, or the assertion below it would pass on a false negative.
func waitForEvents(t *testing.T, w *recordingAuditWriter, min int) []*entity.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := w.snapshot(); len(got) >= min {
			time.Sleep(100 * time.Millisecond) // settle window for stragglers
			return w.snapshot()
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for >= %d audit event(s), got %d", min, len(w.snapshot()))
	return nil
}

// setupGuardRouter wires a fake admin route behind AuditWriteGuard.
// auditsItself controls whether the handler calls governance.RecordAuditEvent
// itself before responding; status is the response code the handler answers
// with (so tests can drive both the accepted and rejected write paths).
func setupGuardRouter(t *testing.T, auditsItself bool, status int) (*gin.Engine, *recordingAuditWriter) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	writer := &recordingAuditWriter{}
	governance.SetAuditWriter(writer)
	// Cleanup swaps in a fresh, discarded writer rather than nil: a
	// RecordAuditEvent call still in flight on a gopool goroutine when this
	// test returns must not dereference a writer this test has torn down.
	t.Cleanup(func() { governance.SetAuditWriter(&recordingAuditWriter{}) })

	engine := gin.New()
	admin := engine.Group("/fake/admin")
	admin.Use(func(c *gin.Context) { c.Set("id", 7); c.Next() })
	admin.Use(AuditWriteGuard())
	admin.POST("/widgets", func(c *gin.Context) {
		if auditsItself {
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
				governance.ActionTokenCreated, governance.ResourceToken, 1, ""))
		}
		c.JSON(status, gin.H{"success": status < 300})
	})
	admin.GET("/widgets", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return engine, writer
}

func TestAuditWriteGuard_FallbackRowWhenHandlerSilent(t *testing.T) {
	engine, writer := setupGuardRouter(t, false, http.StatusCreated)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want exactly 1", len(events))
	}
	ev := events[0]
	if ev.Action != governance.ActionAdminWriteUnaudited {
		t.Errorf("action = %q, want %q", ev.Action, governance.ActionAdminWriteUnaudited)
	}
	if ev.ActorID != 7 {
		t.Errorf("actor_id = %d, want 7", ev.ActorID)
	}
	if ev.ActorType != governance.ActorAdmin {
		t.Errorf("actor_type = %q, want %q", ev.ActorType, governance.ActorAdmin)
	}
	for _, s := range []string{`"route":"/fake/admin/widgets"`, `"method":"POST"`, `"status":201`} {
		if !strings.Contains(ev.Details, s) {
			t.Errorf("details = %q, want to contain %q", ev.Details, s)
		}
	}
}

func TestAuditWriteGuard_NoDuplicateWhenHandlerAudits(t *testing.T) {
	engine, writer := setupGuardRouter(t, true, http.StatusCreated)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want exactly 1 (the handler's own, no fallback duplicate)", len(events))
	}
	if events[0].Action != governance.ActionTokenCreated {
		t.Errorf("action = %q, want %q (the handler's own event, not the fallback)", events[0].Action, governance.ActionTokenCreated)
	}
}

func TestAuditWriteGuard_FiresOnRejectedWrite(t *testing.T) {
	engine, writer := setupGuardRouter(t, false, http.StatusForbidden)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want exactly 1", len(events))
	}
	if !strings.Contains(events[0].Details, `"status":403`) {
		t.Errorf("details = %q, want to contain status:403 — a rejected write must still be audited", events[0].Details)
	}
}

func TestAuditWriteGuard_ReadRoutesIgnored(t *testing.T) {
	engine, writer := setupGuardRouter(t, false, http.StatusOK)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// No async write was even scheduled, so a short, bounded settle is
	// enough — there is nothing in flight to wait for.
	time.Sleep(20 * time.Millisecond)
	if got := writer.snapshot(); len(got) != 0 {
		t.Fatalf("GET produced %d audit events, want 0 (guard must ignore read routes)", len(got))
	}
}

// TestAuditWriteGuard_MetricIncrementsOnFallback proves the counter side of
// the fallback path moves in lockstep with the audit row — the production
// writer TestDeclaredSeriesHaveAProductionWriter requires.
func TestAuditWriteGuard_MetricIncrementsOnFallback(t *testing.T) {
	engine, writer := setupGuardRouter(t, false, http.StatusCreated)
	before := testutil.ToFloat64(metrics.AdminWriteUnauditedTotal.WithLabelValues("/fake/admin/widgets"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)
	waitForEvents(t, writer, 1)

	after := testutil.ToFloat64(metrics.AdminWriteUnauditedTotal.WithLabelValues("/fake/admin/widgets"))
	if after != before+1 {
		t.Errorf("lurus_gateway_admin_write_unaudited_total{route=/fake/admin/widgets} = %v, want %v", after, before+1)
	}
}
