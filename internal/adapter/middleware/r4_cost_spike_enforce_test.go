package middleware

// r4_cost_spike_enforce_test.go — D-A6 regression coverage: CostSpikeLimit
// must default to observe mode (log + count a breach, but never disable the
// account or block the request) and only switch to the legacy
// disable+429 behavior when common.CostSpikeEnforce is explicitly true.
// See internal/pkg/common/constants.go (CostSpikeEnforce doc comment) for
// the full rationale.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// r4SeedBreachWindow seeds a cost-spike window entry that exceeds
// common.CostSpikeHardLimitPer5Min for the given user via the shared
// miniredis instance withMiniRedis installed as common.RDB.
func r4SeedBreachWindow(t *testing.T, rdb *redis.Client, userID int, amount int64) {
	t.Helper()
	nowMs := time.Now().UnixMilli()
	if err := rdb.ZAdd(context.Background(), costSpikeKey(userID), redis.Z{
		Score:  float64(nowMs),
		Member: fmt.Sprintf("%d:%d", nowMs, amount),
	}).Err(); err != nil {
		t.Fatalf("seed zset: %v", err)
	}
}

// r4SetCostSpikeGlobals sets the three globals CostSpikeLimit reads and
// returns a restore func, so tests stay -count=1 safe.
func r4SetCostSpikeGlobals(enforce bool, limit int) func() {
	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	prevEnforce := common.CostSpikeEnforce
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = limit
	common.CostSpikeEnforce = enforce
	return func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
		common.CostSpikeEnforce = prevEnforce
	}
}

// TestR4CostSpikeLimit_ObserveModeAdmitsAndCounts: with CostSpikeEnforce=false
// (the default) a breach must NOT disable the account and must NOT block the
// request — only the counter/log fire. Reading the user row back from the DB
// (rather than only checking the HTTP status) is deliberate: a 200 alone
// would also pass if DisableUserById still ran but the request happened to
// continue anyway, which would hide the exact regression this test guards.
func TestR4CostSpikeLimit_ObserveModeAdmitsAndCounts(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	user := &repo.User{Username: "r4-spike-observe", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "r4-spike-observe@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	restore := r4SetCostSpikeGlobals(false, 50000)
	defer restore()

	before := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed"))

	r4SeedBreachWindow(t, rdb, user.Id, 60000) // > 50000 limit

	w := runCostSpike(user.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 in observe mode (request must proceed); body=%s", w.Code, w.Body.String())
	}

	var got repo.User
	if err := repo.DB.First(&got, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != common.UserStatusEnabled {
		t.Errorf("user status = %d, want still enabled (%d) in observe mode", got.Status, common.UserStatusEnabled)
	}

	if got := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed")) - before; got != 1 {
		t.Errorf("CostSpikeBreachTotal{action=observed} delta = %v, want 1", got)
	}
}

// TestR4CostSpikeLimit_EnforceModeDisablesAnd429: with CostSpikeEnforce=true
// the legacy behavior is unchanged end-to-end — disable + 429 — and the
// counter records action="enforced" instead of "observed".
func TestR4CostSpikeLimit_EnforceModeDisablesAnd429(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	user := &repo.User{Username: "r4-spike-enforce", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "r4-spike-enforce@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	restore := r4SetCostSpikeGlobals(true, 50000)
	defer restore()

	before := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("enforced"))

	r4SeedBreachWindow(t, rdb, user.Id, 60000) // > 50000 limit

	w := runCostSpike(user.Id)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 in enforce mode; body=%s", w.Code, w.Body.String())
	}

	var got repo.User
	if err := repo.DB.First(&got, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != common.UserStatusDisabled {
		t.Errorf("user status = %d, want disabled (%d) in enforce mode", got.Status, common.UserStatusDisabled)
	}

	if got := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("enforced")) - before; got != 1 {
		t.Errorf("CostSpikeBreachTotal{action=enforced} delta = %v, want 1", got)
	}
}

// TestR4CostSpikeLimit_UnderLimitNoCount: a window under the limit must not
// increment either counter label, in either mode — guards against a counter
// that fires unconditionally instead of only on breach.
func TestR4CostSpikeLimit_UnderLimitNoCount(t *testing.T) {
	for _, enforce := range []bool{false, true} {
		enforce := enforce
		t.Run(fmt.Sprintf("enforce=%t", enforce), func(t *testing.T) {
			_, rdb, cleanup := withMiniRedis(t)
			defer cleanup()

			restore := r4SetCostSpikeGlobals(enforce, 50000)
			defer restore()

			beforeObserved := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed"))
			beforeEnforced := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("enforced"))

			uid := 771020
			if enforce {
				uid = 771021
			}
			r4SeedBreachWindow(t, rdb, uid, 100) // well under the 50000 limit

			w := runCostSpike(uid)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 under limit", w.Code)
			}
			if got := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed")) - beforeObserved; got != 0 {
				t.Errorf("CostSpikeBreachTotal{action=observed} delta = %v, want 0 (no breach)", got)
			}
			if got := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("enforced")) - beforeEnforced; got != 0 {
				t.Errorf("CostSpikeBreachTotal{action=enforced} delta = %v, want 0 (no breach)", got)
			}
		})
	}
}

// TestR4CostSpikeLimit_ObserveModeLogThrottlesPerUser: with the account left
// enabled in observe mode, every subsequent request in the same 5-minute
// window re-breaches. The structured "cost_spike_triggered" log line must be
// throttled to at most one per user per minute (same D-A5 noise class as
// warnZeroWalletAmount) while the metrics counter keeps incrementing every
// time. This drives CostSpikeLimit end-to-end through the costSpikeLogf seam
// (a counting stub substituted for common.SysLogf) so the assertion is on
// what the middleware actually emits, not just on the throttle helper in
// isolation.
func TestR4CostSpikeLimit_ObserveModeLogThrottlesPerUser(t *testing.T) {
	resetCostSpikeLogThrottle()
	t.Cleanup(resetCostSpikeLogThrottle)

	const uidA, uidB = 881001, 881002

	if !costSpikeShouldLog(uidA) {
		t.Fatal("first call for a fresh user must log")
	}
	if costSpikeShouldLog(uidA) {
		t.Error("second call within the same minute for the same user must NOT log")
	}
	if costSpikeShouldLog(uidA) {
		t.Error("third call within the same minute for the same user must NOT log")
	}
	// A different user is a distinct throttle bucket — must log independently.
	if !costSpikeShouldLog(uidB) {
		t.Error("a different user must log even while uidA is throttled")
	}
	resetCostSpikeLogThrottle()

	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	user := &repo.User{Username: "r4-spike-throttle", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "r4-spike-throttle@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	restore := r4SetCostSpikeGlobals(false, 50000)
	defer restore()

	var logCalls int
	prevLogf := costSpikeLogf
	costSpikeLogf = func(format string, args ...any) { logCalls++ }
	defer func() { costSpikeLogf = prevLogf }()

	before := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed"))

	r4SeedBreachWindow(t, rdb, user.Id, 60000)
	for i := 0; i < 3; i++ {
		if w := runCostSpike(user.Id); w.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200 in observe mode", i, w.Code)
		}
	}

	// The log line must fire once for the first breach and stay silent for
	// the two repeats within the same minute.
	if logCalls != 1 {
		t.Errorf("cost_spike_triggered log calls = %d, want 1 (throttled across 3 repeat breaches within a minute)", logCalls)
	}
	// The counter must reflect all 3 breaches even though the log was
	// throttled after the first — throttling the log must never throttle
	// the metric.
	if got := testutil.ToFloat64(metrics.CostSpikeBreachTotal.WithLabelValues("observed")) - before; got != 3 {
		t.Errorf("CostSpikeBreachTotal{action=observed} delta = %v, want 3 (unthrottled across 3 repeat breaches)", got)
	}
}

// TestR4CostSpikeEnforce_DefaultsFalse pins the D-A6 default itself.
//
// It does NOT call common.InitEnv() to re-derive common.CostSpikeEnforce
// from a t.Setenv'd value: internal/adapter/provider/claude/cov_adaptor_test.go
// already documents why unit tests must not invoke InitEnv() — it calls
// flag.Parse() a second time and can os.Exit/log.Fatal, side effects a
// package-level test has no business triggering. Instead this test asserts
// two equivalent things that together pin the same contract without that
// risk: (a) common.CostSpikeEnforce, as loaded by this test binary's single
// InitEnv()-free process start, is false (no other test in this package
// permanently mutates it — every caller in this file, in
// cost_spike_cover_test.go, and in gap_r3_cover_test.go restores it via
// defer); (b) the exact GetEnvOrDefaultBool("COST_SPIKE_ENFORCE", false) call
// init.go makes resolves to true once COST_SPIKE_ENFORCE=true is set — the
// identical mechanism InitEnv() would use, exercised directly.
func TestR4CostSpikeEnforce_DefaultsFalse(t *testing.T) {
	if common.CostSpikeEnforce != false {
		t.Errorf("common.CostSpikeEnforce = %v, want false as the process-start default", common.CostSpikeEnforce)
	}
	if got := common.GetEnvOrDefaultBool("COST_SPIKE_ENFORCE", false); got != false {
		t.Errorf("GetEnvOrDefaultBool(COST_SPIKE_ENFORCE, false) = %v, want false with no env set", got)
	}
	t.Setenv("COST_SPIKE_ENFORCE", "true")
	if got := common.GetEnvOrDefaultBool("COST_SPIKE_ENFORCE", false); got != true {
		t.Errorf("GetEnvOrDefaultBool(COST_SPIKE_ENFORCE, false) = %v, want true with COST_SPIKE_ENFORCE=true", got)
	}
}
