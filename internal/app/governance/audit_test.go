package governance

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// mockAuditWriter records events for testing. The mutex makes concurrent
// CreateAuditEvent calls (RecordAuditEvent dispatches them on pool goroutines)
// safe, so the -race detector measures the production atomic.Pointer rather
// than the mock's own unsynchronized slice append.
type mockAuditWriter struct {
	mu     sync.Mutex
	events []*entity.AuditEvent
	err    error
}

func (m *mockAuditWriter) CreateAuditEvent(event *entity.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, event)
	return nil
}

func TestSetAuditWriter_StoresWriter(t *testing.T) {
	// Reset state after test.
	defer auditWriterRef.Store(nil)

	w := &mockAuditWriter{}
	SetAuditWriter(w)

	wp := auditWriterRef.Load()
	if wp == nil {
		t.Fatal("expected writer to be stored")
	}
	if *wp != AuditWriter(w) {
		t.Error("stored writer does not match")
	}
}

func TestRecordAuditEvent_NilWriter(t *testing.T) {
	// Ensure no writer is set.
	auditWriterRef.Store(nil)

	// Should not panic.
	RecordAuditEvent(&entity.AuditEvent{Action: "test"})
}

func TestRecordAuditEvent_NilEvent(t *testing.T) {
	defer auditWriterRef.Store(nil)
	w := &mockAuditWriter{}
	SetAuditWriter(w)

	// Should not panic with nil event.
	RecordAuditEvent(nil)
}

func TestNewAuditEvent_FieldPopulation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/test", nil)
	c.Set("tenant_id", "tenant-abc")
	c.Set(common.RequestIdKey, "req-123")

	event := NewAuditEvent(c, ActorUser, 42, ActionTokenCreated, ResourceToken, 100, `{"name":"test"}`)

	if event.TenantID != "tenant-abc" {
		t.Errorf("expected tenant_id=tenant-abc, got %q", event.TenantID)
	}
	if event.ActorType != ActorUser {
		t.Errorf("expected actor_type=user, got %q", event.ActorType)
	}
	if event.ActorID != 42 {
		t.Errorf("expected actor_id=42, got %d", event.ActorID)
	}
	if event.Action != ActionTokenCreated {
		t.Errorf("expected action=token.created, got %q", event.Action)
	}
	if event.Resource != ResourceToken {
		t.Errorf("expected resource=token, got %q", event.Resource)
	}
	if event.ResourceID != 100 {
		t.Errorf("expected resource_id=100, got %d", event.ResourceID)
	}
	if event.Details != `{"name":"test"}` {
		t.Errorf("expected details json, got %q", event.Details)
	}
	if event.RequestID != "req-123" {
		t.Errorf("expected request_id=req-123, got %q", event.RequestID)
	}
	if event.Timestamp <= 0 {
		t.Error("expected timestamp > 0")
	}
}

func TestNewAuditEvent_DefaultTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	// No tenant_id set.

	event := NewAuditEvent(c, ActorSystem, 0, ActionChannelDisabled, ResourceChannel, 1, "")

	if event.TenantID != "default" {
		t.Errorf("expected default tenant, got %q", event.TenantID)
	}
}

func TestNewAuditEvent_DefaultRetention(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/test", nil)

	event := NewAuditEvent(c, ActorUser, 1, ActionTokenCreated, ResourceToken, 1, "")

	// retention_until should equal timestamp + 7y. We don't pin the exact
	// timestamp because NewAuditEvent reads the clock; assert the delta.
	delta := event.RetentionUntil - event.Timestamp
	if delta != DefaultAuditRetentionSeconds {
		t.Errorf("expected retention_until - timestamp = %d (7y), got %d",
			DefaultAuditRetentionSeconds, delta)
	}
	if event.RetentionUntil == 0 {
		t.Error("RetentionUntil should not be zero — 0 means 'no expiry' which would skip cleanup")
	}
}

// TestNewDetachedAuditEvent_FieldPopulation covers the gin-less constructor
// used by background paths (E4 quota threshold hook, lifecycle jobs).
// Asserts the explicit tenant_id is honoured and IP/RequestID are zero —
// callers without a request can't fake those without lying about origin.
func TestNewDetachedAuditEvent_FieldPopulation(t *testing.T) {
	event := NewDetachedAuditEvent(
		"tenant-xyz",
		ActorSystem,
		99,
		ActionBillingQuotaThreshold,
		ResourceUser,
		99,
		`{"threshold_pct":80}`,
	)
	if event.TenantID != "tenant-xyz" {
		t.Errorf("tenant_id = %q, want %q", event.TenantID, "tenant-xyz")
	}
	if event.ActorType != ActorSystem {
		t.Errorf("actor_type = %q, want %q", event.ActorType, ActorSystem)
	}
	if event.Action != ActionBillingQuotaThreshold {
		t.Errorf("action = %q, want %q", event.Action, ActionBillingQuotaThreshold)
	}
	if event.IP != "" {
		t.Errorf("ip = %q, want empty (no request context)", event.IP)
	}
	if event.RequestID != "" {
		t.Errorf("request_id = %q, want empty (no request context)", event.RequestID)
	}
	if event.RetentionUntil-event.Timestamp != DefaultAuditRetentionSeconds {
		t.Errorf("retention delta = %d, want %d", event.RetentionUntil-event.Timestamp, DefaultAuditRetentionSeconds)
	}
}

// TestNewDetachedAuditEvent_BlankTenantDefaults mirrors NewAuditEvent's blank
// tenant defaulting. Background paths shouldn't be punished with empty tenant
// columns just because they had no context to read from.
func TestNewDetachedAuditEvent_BlankTenantDefaults(t *testing.T) {
	event := NewDetachedAuditEvent(
		"",
		ActorSystem,
		1,
		ActionSystemStartup,
		ResourceSystem,
		0,
		"",
	)
	if event.TenantID != "default" {
		t.Errorf("tenant_id = %q, want %q", event.TenantID, "default")
	}
}

func TestSetAuditWriter_AtomicSafety(t *testing.T) {
	defer auditWriterRef.Store(nil)

	// Concurrent read/write should not race.
	var done atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for done.Load() == 0 {
			w := &mockAuditWriter{}
			SetAuditWriter(w)
		}
	}()
	go func() {
		defer wg.Done()
		for done.Load() == 0 {
			RecordAuditEvent(&entity.AuditEvent{Action: "test"})
		}
	}()

	// Let goroutines run briefly.
	for i := 0; i < 1000; i++ {
		RecordAuditEvent(&entity.AuditEvent{Action: "test"})
	}
	done.Store(1)
	// Join both goroutines before the deferred auditWriterRef.Store(nil) runs,
	// so the reset can't race with a still-running SetAuditWriter/RecordAuditEvent.
	wg.Wait()
}

// TestNewAuditEvent_MarksContext is the L2 audit-completeness oracle: after
// governance.NewAuditEvent(c, …) is handed to governance.RecordAuditEvent,
// the request's gin.Context must carry AuditedContextKey=true so
// middleware.AuditWriteGuard can tell "this write audited itself" from "this
// write forgot to". Per the operator amendment (cycle7 plan §8, L2): the flag
// is set inside RecordAuditEvent — the persisting call — not inside
// NewAuditEvent's construction, so a caller that builds an event and then
// decides not to record it (the decoupled construct/record shape at
// internal_privacy_erase.go:153-156) never marks the request as covered.
func TestNewAuditEvent_MarksContext(t *testing.T) {
	defer auditWriterRef.Store(nil)
	SetAuditWriter(&mockAuditWriter{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/test", nil)

	if c.GetBool(AuditedContextKey) {
		t.Fatal("AuditedContextKey should be unset before any audit call")
	}

	event := NewAuditEvent(c, ActorAdmin, 1, ActionTokenCreated, ResourceToken, 1, "")
	if c.GetBool(AuditedContextKey) {
		t.Fatal("construction alone (NewAuditEvent) must not mark the context — only RecordAuditEvent may")
	}

	RecordAuditEvent(event)
	if !c.GetBool(AuditedContextKey) {
		t.Fatal("RecordAuditEvent must set AuditedContextKey=true on the context the event was built from")
	}
}
