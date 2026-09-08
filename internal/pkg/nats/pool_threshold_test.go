package nats

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- minimal mocks (mirrors internal/app/quota_threshold_test.go patterns) ---

// mockPoolRedis implements poolRedisDeduper; controls SETNX behaviour per key.
type mockPoolRedis struct {
	mu       sync.Mutex
	setKeys  map[string]bool // key -> already set
	failErr  error           // if non-nil, SetNXBool returns this error
	disabled bool            // if true, always returns (true, nil) — simulates no-Redis mode
}

func newMockPoolRedis() *mockPoolRedis { return &mockPoolRedis{setKeys: map[string]bool{}} }

func (m *mockPoolRedis) SetNXBool(_ context.Context, key string, _ time.Duration) (bool, error) {
	if m.disabled {
		return true, nil
	}
	if m.failErr != nil {
		return false, m.failErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setKeys[key] {
		return false, nil
	}
	m.setKeys[key] = true
	return true, nil
}

// mockPoolPublisher records every published message.
type mockPoolPublisher struct {
	mu       sync.Mutex
	messages []poolPublished
	failErr  error
}

type poolPublished struct {
	subject string
	payload any
}

func (p *mockPoolPublisher) Publish(_ context.Context, subject string, payload any) error {
	if p.failErr != nil {
		return p.failErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, poolPublished{subject, payload})
	return nil
}

func (p *mockPoolPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

// mockPoolDB implements poolDB. lastFiredAt is the value LastAlertFiredAt
// returns (nil = never fired). markCalls records how often MarkAlertFired
// was invoked + the timestamp it was called with.
type mockPoolDB struct {
	mu          sync.Mutex
	lastFiredAt *time.Time
	readErr     error
	markErr     error
	markCalls   []time.Time
}

func (d *mockPoolDB) LastAlertFiredAt(_ context.Context, _ int64) (*time.Time, error) {
	if d.readErr != nil {
		return nil, d.readErr
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastFiredAt == nil {
		return nil, nil
	}
	t := *d.lastFiredAt
	return &t, nil
}

func (d *mockPoolDB) MarkAlertFired(_ context.Context, _ int64, now time.Time) error {
	if d.markErr != nil {
		return d.markErr
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.markCalls = append(d.markCalls, now)
	d.lastFiredAt = &now
	return nil
}

func (d *mockPoolDB) markCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.markCalls)
}

// --- Lane δ exit-criteria tests ---

// Test 1: publisher publishes when alert_fired_at is nil AND Redis dedup unset.
func TestPublishPoolThreshold_FiresWhenBothDedupOpen(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{} // lastFiredAt == nil
	now := time.Now().UTC()

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-A", 42, 100, 1000, 80,
		pub, rdb, db, now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.count() != 1 {
		t.Errorf("expected 1 publish, got %d", pub.count())
	}
	if pub.messages[0].subject != SubjectPoolThreshold {
		t.Errorf("subject = %q, want %q", pub.messages[0].subject, SubjectPoolThreshold)
	}
	payload, ok := pub.messages[0].payload.(PoolThresholdPayload)
	if !ok {
		t.Fatalf("payload type mismatch: %T", pub.messages[0].payload)
	}
	if payload.TenantID != "tenant-A" || payload.PoolID != 42 || payload.ThresholdPct != 80 {
		t.Errorf("payload mismatch: %+v", payload)
	}
}

// Test 2: publisher no-ops when alert_fired_at is recent (< 1h ago) — schema
// dedup blocks regardless of Redis state.
func TestPublishPoolThreshold_BlockedBySchemaDedup(t *testing.T) {
	rdb := newMockPoolRedis() // Redis is open
	pub := &mockPoolPublisher{}
	now := time.Now().UTC()
	recent := now.Add(-10 * time.Minute) // within poolSchemaDedupWindow (1h)
	db := &mockPoolDB{lastFiredAt: &recent}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-B", 7, 50, 1000, 80,
		pub, rdb, db, now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.count() != 0 {
		t.Errorf("expected 0 publishes (schema dedup), got %d", pub.count())
	}
	if db.markCount() != 0 {
		t.Errorf("expected 0 MarkAlertFired calls, got %d", db.markCount())
	}
}

// Test 2b: alert_fired_at older than 1h should NOT block (sanity for the
// boundary — confirms the dedup window is bounded, not perpetual).
func TestPublishPoolThreshold_AllowedWhenSchemaDedupExpired(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Hour)
	db := &mockPoolDB{lastFiredAt: &stale}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-B2", 7, 50, 1000, 80,
		pub, rdb, db, now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.count() != 1 {
		t.Errorf("expected 1 publish after stale schema dedup, got %d", pub.count())
	}
}

// Test 3: publisher no-ops when Redis SETNX returns acquired=false (already
// locked) — Redis dedup blocks even when schema dedup is open.
func TestPublishPoolThreshold_BlockedByRedisDedup(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{} // schema is open

	// Pre-load the key so SETNX returns false.
	rdb.setKeys[poolDedupKey("tenant-C", 99)] = true

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-C", 99, 25, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.count() != 0 {
		t.Errorf("expected 0 publishes (redis dedup), got %d", pub.count())
	}
	if db.markCount() != 0 {
		t.Errorf("expected 0 MarkAlertFired calls when redis blocks, got %d", db.markCount())
	}
}

// Test 4: on successful publish, MarkAlertFired must be called exactly once
// with the same timestamp passed in.
func TestPublishPoolThreshold_WritesAlertFiredAtOnSuccess(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{}
	now := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-D", 1, 10, 1000, 80,
		pub, rdb, db, now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db.markCount() != 1 {
		t.Fatalf("expected MarkAlertFired called once, got %d", db.markCount())
	}
	if !db.markCalls[0].Equal(now) {
		t.Errorf("MarkAlertFired called with %v, want %v", db.markCalls[0], now)
	}
}

// Test 5: publish failure leaves alert_fired_at MARKED — fail-closed order
// (2026-05-19 audit reorder, Step 3 before Step 4). The dedup state is
// already on disk, so the alert won't re-fire inside the dedup window
// even though the wire publish failed. Caller still sees the error.
func TestPublishPoolThreshold_MarksEvenWhenPublishFails(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{failErr: errors.New("simulated NATS down")}
	db := &mockPoolDB{}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-E", 5, 10, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("expected error from publish failure, got nil")
	}
	if db.markCount() != 1 {
		t.Errorf("expected MarkAlertFired called once before publish, got %d", db.markCount())
	}
	if pub.count() != 0 {
		t.Errorf("publish failed — expected 0 successful messages, got %d", pub.count())
	}
}

// Test 5b: when the schema mark itself fails, the publish must NOT happen.
// This is the other half of the fail-closed contract — if we cannot
// durably record "alert fired", we must not put it on the wire (otherwise
// schema dedup loses sync and the alert can storm).
func TestPublishPoolThreshold_DoesNotPublishWhenMarkFails(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{markErr: errors.New("simulated postgres write failure")}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-E2", 9, 10, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("expected error from mark failure, got nil")
	}
	if pub.count() != 0 {
		t.Errorf("mark failed — expected 0 publishes, got %d", pub.count())
	}
}

// Test 6: Redis transient failure (e.g. connection reset) should NOT block
// publication — schema dedup is the durable safety net per design comment
// in publishPoolThreshold. Verifies the "Redis might be down" rationale.
func TestPublishPoolThreshold_RedisFailureFallsThroughToSchema(t *testing.T) {
	rdb := newMockPoolRedis()
	rdb.failErr = errors.New("simulated redis dial timeout")
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-F", 11, 10, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.count() != 1 {
		t.Errorf("expected publication to proceed despite redis error, got %d publishes", pub.count())
	}
}

// Test 7: poolDedupKey format must be stable — consumers (Lane α / α↔δ
// coordination) rely on it. If you change this format you've broken the
// contract with any pod that already holds keys with the old format.
func TestPoolDedupKey_Format(t *testing.T) {
	got := poolDedupKey("acme", 42)
	want := "pool:threshold:acme:42"
	if got != want {
		t.Errorf("poolDedupKey = %q, want %q", got, want)
	}
}

// Test 8: SubjectPoolThreshold must match the ADR-mandated wire name.
func TestSubjectPoolThreshold_WireName(t *testing.T) {
	if SubjectPoolThreshold != "llm.pool.threshold" {
		t.Errorf("SubjectPoolThreshold = %q, want %q (per ADR 2026-05-18 §9 Q5)",
			SubjectPoolThreshold, "llm.pool.threshold")
	}
}
