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

// TestAuditWriteGuard_InternalGroupAttributesSystemActor proves the guard
// tells the internal-admin mount point apart from adminRoute by the presence
// of "internal_api_key_id" (set by middleware.InternalApiAuth) and attributes
// the fallback row to ActorSystem using that key's id, not "id"/ActorAdmin.
func TestAuditWriteGuard_InternalGroupAttributesSystemActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &recordingAuditWriter{}
	governance.SetAuditWriter(writer)
	t.Cleanup(func() { governance.SetAuditWriter(&recordingAuditWriter{}) })

	engine := gin.New()
	internalAdmin := engine.Group("/fake/internal/admin")
	internalAdmin.Use(func(c *gin.Context) { c.Set("internal_api_key_id", 5); c.Next() })
	internalAdmin.Use(AuditWriteGuard())
	internalAdmin.POST("/widgets", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/internal/admin/widgets", nil)
	engine.ServeHTTP(w, req)

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.ActorType != governance.ActorSystem {
		t.Errorf("actor_type = %q, want %q (internal_api_key_id present)", ev.ActorType, governance.ActorSystem)
	}
	if ev.ActorID != 5 {
		t.Errorf("actor_id = %d, want 5 (from internal_api_key_id)", ev.ActorID)
	}
}

// TestAuditWriteGuard_JWTRootRecordsAdminSub covers RootJWTAuth's Bearer-JWT
// branch (admin_jwt_auth.go), which never populates the "id" context key —
// so c.GetInt("id") is 0 and the fallback must fall back further, to
// admin_sub, rather than silently attributing the row to actor id 0 with no
// other identifying detail.
func TestAuditWriteGuard_JWTRootRecordsAdminSub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &recordingAuditWriter{}
	governance.SetAuditWriter(writer)
	t.Cleanup(func() { governance.SetAuditWriter(&recordingAuditWriter{}) })

	engine := gin.New()
	admin := engine.Group("/fake/admin")
	admin.Use(func(c *gin.Context) { c.Set("admin_sub", "sub-x"); c.Next() }) // no "id" set
	admin.Use(AuditWriteGuard())
	admin.POST("/widgets", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/widgets", nil)
	engine.ServeHTTP(w, req)

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.ActorType != governance.ActorAdmin {
		t.Errorf("actor_type = %q, want %q", ev.ActorType, governance.ActorAdmin)
	}
	if ev.ActorID != 0 {
		t.Errorf("actor_id = %d, want 0 (\"id\" was never set on this context)", ev.ActorID)
	}
	if !strings.Contains(ev.Details, `"admin_sub":"sub-x"`) {
		t.Errorf("details = %q, want to contain admin_sub:sub-x", ev.Details)
	}
}

// TestAuditWriteGuard_FallbackRecordsRouteParams proves the fallback details
// carry the matched route's path parameters (not query/body) — without
// them, a fallback row for a parameterised route like DELETE
// /fake/admin/widgets/:id cannot identify which widget the request touched.
func TestAuditWriteGuard_FallbackRecordsRouteParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &recordingAuditWriter{}
	governance.SetAuditWriter(writer)
	t.Cleanup(func() { governance.SetAuditWriter(&recordingAuditWriter{}) })

	engine := gin.New()
	admin := engine.Group("/fake/admin")
	admin.Use(func(c *gin.Context) { c.Set("id", 7); c.Next() })
	admin.Use(AuditWriteGuard())
	admin.DELETE("/widgets/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/fake/admin/widgets/42?extra=should-not-appear", nil)
	engine.ServeHTTP(w, req)

	events := waitForEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	details := events[0].Details
	if !strings.Contains(details, `"params":{"id":"42"}`) {
		t.Errorf("details = %q, want to contain params:{id:42}", details)
	}
	if strings.Contains(details, "should-not-appear") {
		t.Errorf("details = %q, must not leak the query string", details)
	}
}

// TestAuditWriteGuard_ForgetsPendingConstructWithoutRecord locks the
// governance.ForgetPending sweep: a handler that builds an event via
// governance.NewAuditEvent(c, …) and drops it without ever calling
// RecordAuditEvent (the decoupled construct/record shape at
// internal_privacy_erase.go:153-156) must not leave that entry in
// governance's pendingAuditContexts once the request finishes — the guard's
// own fallback still fires (no AuditedContextKey was set), but the dropped
// event's own pending slot must be swept.
func TestAuditWriteGuard_ForgetsPendingConstructWithoutRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &recordingAuditWriter{}
	governance.SetAuditWriter(writer)
	t.Cleanup(func() { governance.SetAuditWriter(&recordingAuditWriter{}) })

	before := governance.PendingAuditContextCount()

	engine := gin.New()
	admin := engine.Group("/fake/admin")
	admin.Use(func(c *gin.Context) { c.Set("id", 7); c.Next() })
	admin.Use(AuditWriteGuard())
	admin.POST("/orphan", func(c *gin.Context) {
		_ = governance.NewAuditEvent(c, governance.ActorAdmin, 7,
			governance.ActionTokenCreated, governance.ResourceToken, 1, "")
		c.JSON(http.StatusCreated, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fake/admin/orphan", nil)
	engine.ServeHTTP(w, req)

	// The fallback row still gets written (the dropped event never marked
	// AuditedContextKey).
	waitForEvents(t, writer, 1)

	after := governance.PendingAuditContextCount()
	if after > before {
		t.Errorf("PendingAuditContextCount grew from %d to %d — the dropped NewAuditEvent(c,…) call was never swept by ForgetPending", before, after)
	}
}
