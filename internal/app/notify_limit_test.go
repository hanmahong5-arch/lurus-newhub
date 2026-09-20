package app

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestStartCleanupTaskWithContext_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		startCleanupTaskWithContext(ctx)
		close(done)
	}()

	// Let it initialize
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("startCleanupTaskWithContext did not respond to context cancellation")
	}
}

func TestStartCleanupTaskWithContext_ImmediateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		startCleanupTaskWithContext(ctx)
		close(done)
	}()

	select {
	case <-done:
		// OK - should exit immediately
	case <-time.After(500 * time.Millisecond):
		t.Fatal("should exit immediately when context is already cancelled")
	}
}

// TestInitNotifyLimitCleanup is the oracle for cycle 13 L11's notify-limit
// fix: InitNotifyLimitCleanup must actually start the cleanup goroutine
// (observable via cleanupCancel becoming non-nil — Once.Do blocks until its
// closure returns, and that closure sets cleanupCancel before launching the
// goroutine, so there is no window where this is nil right after Init
// returns) and StopNotifyLimitCleanup must be able to cancel it (observable
// via cleanupCtx.Err() flipping non-nil). resetNotifyLimitCleanupForTest
// gives this a fresh cleanupOnce regardless of what earlier tests in this
// binary already did to the shared package state — see its doc comment.
func TestInitNotifyLimitCleanup(t *testing.T) {
	resetNotifyLimitCleanupForTest()
	t.Cleanup(resetNotifyLimitCleanupForTest)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	InitNotifyLimitCleanup(ctx)

	if cleanupCancel == nil {
		t.Fatal("InitNotifyLimitCleanup did not start the cleanup goroutine: cleanupCancel is nil")
	}
	if cleanupCtx == nil || cleanupCtx.Err() != nil {
		t.Fatal("cleanup context must be live (not already done) immediately after InitNotifyLimitCleanup")
	}

	StopNotifyLimitCleanup()
	if cleanupCtx.Err() == nil {
		t.Fatal("StopNotifyLimitCleanup must cancel the cleanup goroutine's context")
	}
}

// TestCheckMemoryLimit_LazyStartGoesThroughInitNotifyLimitCleanup is the
// direct oracle for the fix: before cycle 13 L11, checkMemoryLimit's lazy
// start called the deprecated startCleanupTask through the SAME cleanupOnce
// InitNotifyLimitCleanup uses, so whichever of the two callers ran first
// silently claimed the Once and the other's semantics never applied. Under
// the old code this test's first assertion fails: checkMemoryLimit's lazy
// path never touched cleanupCancel at all.
func TestCheckMemoryLimit_LazyStartGoesThroughInitNotifyLimitCleanup(t *testing.T) {
	resetNotifyLimitCleanupForTest()
	t.Cleanup(resetNotifyLimitCleanupForTest)

	if cleanupCancel != nil {
		t.Fatal("test setup: cleanupCancel should be nil before the first checkMemoryLimit call")
	}

	if _, err := checkMemoryLimit(1, "lazy-start-probe"); err != nil {
		t.Fatalf("checkMemoryLimit: %v", err)
	}

	if cleanupCancel == nil {
		t.Fatal("checkMemoryLimit's lazy start must go through InitNotifyLimitCleanup and capture a cancel func")
	}
	if cleanupCtx == nil || cleanupCtx.Err() != nil {
		t.Fatal("checkMemoryLimit's lazy-started cleanup context must not already be done")
	}

	// Prove this is the SAME cancellable Once path InitNotifyLimitCleanup
	// uses, not a second, uncancellable goroutine: StopNotifyLimitCleanup
	// must be able to reach it.
	StopNotifyLimitCleanup()
	if cleanupCtx.Err() == nil {
		t.Fatal("StopNotifyLimitCleanup must cancel the context captured by checkMemoryLimit's lazy start")
	}
}

// TestInitNotifyLimitCleanup_LoseTheRaceToCheckMemoryLimit is the same
// oracle with the two entry points hit in the opposite order — the plan's
// "两种顺序下两测试都有意义" requirement: whichever of
// InitNotifyLimitCleanup/checkMemoryLimit's lazy start wins cleanupOnce, the
// other must still observe a live, cancellable cleanup context afterward
// (not silently no-op against a goroutine only the winner could see).
func TestInitNotifyLimitCleanup_LoseTheRaceToCheckMemoryLimit(t *testing.T) {
	resetNotifyLimitCleanupForTest()
	t.Cleanup(resetNotifyLimitCleanupForTest)

	// checkMemoryLimit's lazy path wins the race this time.
	if _, err := checkMemoryLimit(2, "lazy-start-wins-race"); err != nil {
		t.Fatalf("checkMemoryLimit: %v", err)
	}
	if cleanupCancel == nil {
		t.Fatal("checkMemoryLimit should have won cleanupOnce and started the goroutine")
	}

	// InitNotifyLimitCleanup now loses the race (Once already consumed) —
	// it must still leave a live, stoppable cleanup behind.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	InitNotifyLimitCleanup(ctx)

	if cleanupCtx == nil || cleanupCtx.Err() != nil {
		t.Fatal("losing the race must not leave the cleanup context in a dead/nil state")
	}
	StopNotifyLimitCleanup()
	if cleanupCtx.Err() == nil {
		t.Fatal("StopNotifyLimitCleanup must still be able to cancel the goroutine checkMemoryLimit started")
	}
}

func TestCheckNotificationLimit_Memory(t *testing.T) {
	// Test memory-based limit checking
	// This doesn't require Redis

	// Set limit for test (default is 0 in test environment)
	oldLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 10
	defer func() { constant.NotifyLimitCount = oldLimit }()

	// First call should succeed
	allowed, err := checkMemoryLimit(12345, "test")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("first notification should be allowed")
	}
}

func TestCheckNotificationLimit_MemoryMultipleCalls(t *testing.T) {
	userId := 99999 // Use unique ID to avoid interference
	notifyType := "test-multi"

	// Make several calls
	for i := 0; i < 5; i++ {
		_, err := checkMemoryLimit(userId, notifyType)
		if err != nil {
			t.Errorf("call %d unexpected error: %v", i, err)
		}
	}
}

func TestGetDuration(t *testing.T) {
	duration := getDuration()
	// Duration can be 0 if NotificationLimitDurationMinute is not set
	// In production, it defaults to 10 minutes
	if duration < 0 {
		t.Error("duration should not be negative")
	}
}

func TestCheckNotificationLimit_MemoryAtLimit_ReturnsFalse(t *testing.T) {
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 3
	constant.NotificationLimitDurationMinute = 60 // set duration so entries don't expire immediately
	defer func() {
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	}()

	userId := 77701
	notifyType := "test-at-limit"

	// Call exactly limit times; all should succeed
	for i := 0; i < 3; i++ {
		allowed, err := checkMemoryLimit(userId, notifyType)
		if err != nil {
			t.Fatalf("call %d unexpected error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d should be allowed (within limit)", i)
		}
	}

	// One more call should exceed the limit
	allowed, err := checkMemoryLimit(userId, notifyType)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected false after exceeding limit")
	}
}

func TestCheckNotificationLimit_MemoryDifferentTypesAreIndependent(t *testing.T) {
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 2
	constant.NotificationLimitDurationMinute = 60
	defer func() {
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	}()

	userId := 77702

	// Exhaust limit for type A
	for i := 0; i < 3; i++ {
		checkMemoryLimit(userId, "typeA")
	}

	// Type B should still be allowed
	allowed, err := checkMemoryLimit(userId, "typeB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected typeB to be allowed independently of typeA")
	}
}

func TestCheckNotificationLimit_MemoryDifferentUsersAreIndependent(t *testing.T) {
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 2
	constant.NotificationLimitDurationMinute = 60
	defer func() {
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	}()

	notifyType := "test-users-independent"

	// Exhaust limit for user 1
	for i := 0; i < 3; i++ {
		checkMemoryLimit(77703, notifyType)
	}

	// User 2 should still be allowed
	allowed, err := checkMemoryLimit(77704, notifyType)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected different user to be allowed independently")
	}
}

func TestCheckNotificationLimit_MemoryZeroLimitBlocksAll(t *testing.T) {
	oldLimit := constant.NotifyLimitCount
	oldDuration := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 0
	constant.NotificationLimitDurationMinute = 60
	defer func() {
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	}()

	// Even the first call should be blocked since count(1) > limit(0)
	allowed, err := checkMemoryLimit(77705, "test-zero-limit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected false when limit is 0")
	}
}

func TestCheckNotificationLimit_MemoryFirstCallAllowed(t *testing.T) {
	oldLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 100
	defer func() { constant.NotifyLimitCount = oldLimit }()

	// Use a unique user/type combo so no interference
	allowed, err := checkMemoryLimit(77706, "test-first-call")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("first call should be allowed when limit is high")
	}
}

func TestCheckNotificationLimit_DispatchesBasedOnRedisEnabled(t *testing.T) {
	// When Redis is disabled, CheckNotificationLimit should use memory path
	origRedis := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = origRedis }()

	oldLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 50
	defer func() { constant.NotifyLimitCount = oldLimit }()

	allowed, err := CheckNotificationLimit(context.Background(), 77707, "test-dispatch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed when Redis disabled and limit is high")
	}
}

func TestGetDuration_CustomMinutes(t *testing.T) {
	orig := constant.NotificationLimitDurationMinute
	constant.NotificationLimitDurationMinute = 30
	defer func() { constant.NotificationLimitDurationMinute = orig }()

	d := getDuration()
	if d != 30*time.Minute {
		t.Errorf("expected 30m, got %v", d)
	}
}
