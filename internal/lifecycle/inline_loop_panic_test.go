package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// recoveredPanics sums the recovered-panic counter across the two sources a
// background-loop panic can be booked under: "goroutine" (the one-shot
// SafeGoWithContext boundary, where the loop dies) and "background_tick" (the
// per-tick boundary, where it survives). Counting both keeps the test about
// behaviour — how many ticks panicked — rather than which label won.
func recoveredPanics() float64 {
	return testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("goroutine")) +
		testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("background_tick"))
}

// waitPanics blocks until at least want panics were recovered since base, or
// the deadline passes. Returns the number seen.
func waitPanics(base float64, want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if int(recoveredPanics()-base) >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return int(recoveredPanics() - base)
}

// With repo.DB nil every pass panics. The loops used to be wrapped once by
// SafeGoWithContext: the startup pass panicked, was recovered, and the
// goroutine ended — the task never ticked again. A later tick must still run.
func TestInlineLoops_PanicOnStartupPassDoesNotKillTheLoop(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		start func(ctx context.Context)
	}{
		{"audit-cleanup", "AUDIT_CLEANUP_INTERVAL_SECONDS", StartAuditCleanupWithContext},
		{"privacy-erasure", "PRIVACY_ERASURE_INTERVAL_SECONDS", StartPrivacyErasureWithContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "1")
			prevDB := repo.DB
			repo.DB = nil
			prevLeader := common.IsLeader()
			common.SetLeader(true)
			t.Cleanup(func() {
				common.SetLeader(prevLeader)
				repo.DB = prevDB
			})

			base := recoveredPanics()
			ctx, cancel := context.WithCancel(context.Background())
			tc.start(ctx)
			// Startup pass = panic #1; the 1s ticker supplies #2 only if the
			// loop survived #1.
			got := waitPanics(base, 2, 4*time.Second)
			cancel()
			time.Sleep(50 * time.Millisecond)
			if got < 2 {
				t.Fatalf("%s: %d panicking pass(es) recovered, want >=2 — loop died after the first", tc.name, got)
			}
		})
	}
}
