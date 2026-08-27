package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestR4dCostSpikeBreachTotal_ZeroInitLabels locks the r4_cost_spike.go
// init() behavior: both "action" label values must already exist as child
// series (value 0) before CostSpikeLimit ever runs a single request.
// Deliberately uses Collect() rather than WithLabelValues()/
// GetMetricWithLabelValues() to read the vector — both of those LAZILY
// create the child series as a side effect, which would make this test pass
// even if init()'s pre-registration were deleted. Collect() only enumerates
// series that already exist, so it actually distinguishes "init()
// pre-registered these" from "nothing has touched this vector yet".
//
// Why this matters operationally: a CounterVec child series is otherwise
// absent from /metrics until its first Inc(). An absent
// "observed"/"enforced" series in a scrape would then be ambiguous between
// "no breach has happened yet" and "the middleware isn't wired into the
// router at all" — the same ambiguity a PromQL alert on this raw counter
// (before any rate()) cannot resolve on its own.
func TestR4dCostSpikeBreachTotal_ZeroInitLabels(t *testing.T) {
	ch := make(chan prometheus.Metric, 16)
	CostSpikeBreachTotal.Collect(ch)
	close(ch)

	seen := map[string]float64{}
	for m := range ch {
		pb := &dto.Metric{}
		if err := m.Write(pb); err != nil {
			t.Fatalf("Write: %v", err)
		}
		var action string
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "action" {
				action = lp.GetValue()
			}
		}
		seen[action] = pb.GetCounter().GetValue()
	}

	for _, action := range []string{"observed", "enforced"} {
		if _, ok := seen[action]; !ok {
			t.Errorf("action=%s: no series found via Collect() — init() must pre-register both label values so /metrics never omits them entirely", action)
		}
	}
}
