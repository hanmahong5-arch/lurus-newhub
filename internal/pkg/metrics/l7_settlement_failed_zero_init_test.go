package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestBillingSettlementFailedTotal_ZeroInitLabels locks billing.go's init()
// behavior for BillingSettlementFailedTotal: all three path label values
// (text/claude/audio) must already exist as child series (value 0) before
// any consume-quota settlement has ever failed.
//
// Deliberately uses Collect() rather than WithLabelValues() to read the
// vector, same pattern as TestR4dCostSpikeBreachTotal_ZeroInitLabels in
// r4d_cost_spike_zero_init_test.go: WithLabelValues() LAZILY creates the
// child series as a side effect, so a test built on it would pass even if
// init()'s pre-registration were deleted — it would just be re-creating the
// series it claims to be checking for. Collect() only enumerates series
// that already exist, so it actually distinguishes "init() pre-registered
// these" from "nothing has touched this vector yet".
//
// Why this matters operationally: this cycle's UAT probe
// (cycle11-first-customer-2026-09-19.md, L7) is literally "/metrics shows
// the three path series at 0" — an absent series before any failure would
// be indistinguishable from the metric not being wired into any call site
// at all.
func TestBillingSettlementFailedTotal_ZeroInitLabels(t *testing.T) {
	ch := make(chan prometheus.Metric, 16)
	BillingSettlementFailedTotal.Collect(ch)
	close(ch)

	seen := map[string]float64{}
	for m := range ch {
		pb := &dto.Metric{}
		if err := m.Write(pb); err != nil {
			t.Fatalf("Write: %v", err)
		}
		var path string
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "path" {
				path = lp.GetValue()
			}
		}
		seen[path] = pb.GetCounter().GetValue()
	}

	for _, path := range []string{"text", "claude", "audio"} {
		v, ok := seen[path]
		if !ok {
			t.Errorf("path=%s: no series found via Collect() — init() must pre-register all three path label values so /metrics never omits them entirely", path)
			continue
		}
		if v != 0 {
			t.Errorf("path=%s: initial counter value = %v, want 0", path, v)
		}
	}
}
