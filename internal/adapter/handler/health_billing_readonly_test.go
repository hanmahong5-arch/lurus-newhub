package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// tuneBillingBreakerForTest points the process-wide billing breaker at
// millisecond windows for one test and restores the production defaults after,
// so "open and past its cooldown" is reachable without a 15s sleep.
func tuneBillingBreakerForTest(t *testing.T, timeout, halfOpenTimeout time.Duration) {
	t.Helper()
	common.ConfigureBillingBreaker(1, timeout, halfOpenTimeout)
	t.Cleanup(func() {
		common.ConfigureBillingBreaker(common.BillingBreakerDefaultThreshold,
			common.BillingBreakerDefaultTimeout, common.BillingBreakerDefaultHalfOpenTimeout)
	})
}

// TestGetHealthDetailed_DoesNotConsumeTheBillingProbe is the oracle for cycle
// 14 L1-B. GET /api/health is the readinessProbe target (every 5s, see
// deploy/k8s/r6-stage/deployment.yaml:338-344). It used to ask the billing
// breaker for PERMISSION (common.BillingBreakerAllow), which on an open
// breaker past its cooldown flips it to half-open and hands the caller the one
// probe slot — a slot the health check then never returned, because it reports
// neither success nor failure. Half-open refuses everyone else, so the first
// probe after any platform blip wedged every real billing call until the pod
// restarted.
//
// The test puts the breaker exactly in that state, runs the health check, and
// then checks that a REAL billing caller still gets the probe.
func TestGetHealthDetailed_DoesNotConsumeTheBillingProbe(t *testing.T) {
	healthSnapshotAll(t)
	common.SetBillingUnifiedEnabled(true)
	common.RedisEnabled = false
	tuneBillingBreakerForTest(t, 5*time.Millisecond, time.Hour)

	common.BillingBreakerFailure() // threshold 1 → open
	if !common.BillingBreakerIsOpen() {
		t.Fatal("setup: breaker must be open")
	}
	time.Sleep(10 * time.Millisecond) // open AND past its cooldown: the probe is due

	// Five probes, i.e. 25 seconds of kubelet readiness traffic.
	for i := 0; i < 5; i++ {
		w, _ := callHealth(t)
		if w.Code != http.StatusOK {
			t.Fatalf("probe %d: status = %d, want 200 (billing alone must not fail readiness); body=%s",
				i, w.Code, w.Body.String())
		}
		_, checks := healthFullBody(t, w)
		if checks["billing"] != "circuit_open" {
			t.Errorf("probe %d: checks.billing = %q, want circuit_open — the breaker is open and the "+
				"health body must say so", i, checks["billing"])
		}
		if got := common.BillingBreakerState(); got != common.BillingBreakerStateOpen {
			t.Fatalf("probe %d moved the breaker to %q: the health check consumed the half-open probe slot",
				i, got)
		}
	}

	// The slot is still there for the caller it belongs to.
	if err := common.BillingBreakerAllow(); err != nil {
		t.Fatalf("a real billing call was refused (%v) — the readiness probe had taken its slot", err)
	}
	if got := common.BillingBreakerState(); got != common.BillingBreakerStateHalfOpen {
		t.Errorf("state = %q, want half_open once the real caller took the probe", got)
	}
}

// TestGetHealthDetailed_ReportsHalfOpenDistinctly keeps the health body honest
// about the third state. Reporting half-open as "ok" is what let the wedge run
// unnoticed: BillingBreakerIsOpen() is false while half-open, so the degrade
// path stayed shut too and nothing anywhere said "billing calls are being
// refused right now".
func TestGetHealthDetailed_ReportsHalfOpenDistinctly(t *testing.T) {
	healthSnapshotAll(t)
	common.SetBillingUnifiedEnabled(true)
	common.RedisEnabled = false
	tuneBillingBreakerForTest(t, 5*time.Millisecond, time.Hour)

	common.BillingBreakerFailure()
	time.Sleep(10 * time.Millisecond)
	if err := common.BillingBreakerAllow(); err != nil { // a real caller takes the probe
		t.Fatalf("setup: probe must be admitted: %v", err)
	}

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	status, checks := healthFullBody(t, w)
	if checks["billing"] != "circuit_half_open" {
		t.Errorf("checks.billing = %q, want circuit_half_open", checks["billing"])
	}
	if status != "degraded" {
		t.Errorf("status = %q, want degraded — while the probe is out, every other billing call is refused",
			status)
	}
}
