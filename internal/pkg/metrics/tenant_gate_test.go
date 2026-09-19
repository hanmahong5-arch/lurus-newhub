package metrics

// tenant_gate_test.go — L9 (cycle 12): the tenant-gate counter's WIRE contract.
//
// Every other assertion on this counter (middleware/tenant_gate_test.go,
// handler/oidc_callback_org_binding_test.go,
// handler/zita_bootstrap_seat_cap_test.go) goes through the Go symbol
// TenantGateTotal, so a wrong Namespace/Subsystem/Name would ship silently
// while every test stayed green. The exported name is what the .env.example
// note, the runbook for flipping TENANT_MISSING_MODE and the plan's UAT probe
// all address, so it is pinned here the way the sibling counters are
// (ratelimit_test.go's TestRateLimitedTotal_MetricName,
// r4d_cost_spike_zero_init_test.go's zero-init check).

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// tenantGateWireName is the series an operator greps for in /metrics. It is
// spelled out literally rather than composed from namespace+subsystem: a test
// that rebuilds the name from the same constants the code uses would follow a
// rename instead of catching it.
const tenantGateWireName = "lurus_gateway_tenant_gate_total"

// TestTenantGateTotal_MetricName pins the exported series name.
func TestTenantGateTotal_MetricName(t *testing.T) {
	if n := testutil.CollectAndCount(TenantGateTotal, tenantGateWireName); n < 1 {
		t.Fatalf("no series named %s collected (got %d) — dashboards, the runbook and the UAT probe all address this exact name",
			tenantGateWireName, n)
	}
	// Name-filter sanity: CollectAndCount returns 0 for a name nothing carries,
	// so the assertion above cannot be passing for a trivial reason.
	if n := testutil.CollectAndCount(TenantGateTotal, "lurus_gateway_tenant_gate_total_wrong"); n != 0 {
		t.Fatalf("name filter sanity check failed: %d series under a wrong name", n)
	}
}

// TestTenantGateTotal_ZeroInitLabels locks init()'s pre-registration: every
// outcome must already exist as a child series at value 0 before anything
// increments it, because a CounterVec child is absent from /metrics until its
// first Inc() and an absent series is ambiguous between "this has not happened
// yet" and "this code is not wired up at all" — the exact question an operator
// must answer about missing_observed before flipping TENANT_MISSING_MODE to
// enforce, and about org_claim_absent before believing the organization guard
// can ever fire.
//
// Collect() is used deliberately instead of WithLabelValues()/
// GetMetricWithLabelValues(), which would lazily CREATE the child as a side
// effect and make this test pass with init()'s loop deleted.
func TestTenantGateTotal_ZeroInitLabels(t *testing.T) {
	ch := make(chan prometheus.Metric, 64)
	TenantGateTotal.Collect(ch)
	close(ch)

	seen := map[string]bool{}
	for m := range ch {
		pb := &dto.Metric{}
		if err := m.Write(pb); err != nil {
			t.Fatalf("Write: %v", err)
		}
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "outcome" {
				seen[lp.GetValue()] = true
			}
		}
	}

	for _, outcome := range []string{
		TenantGateOutcomeDisabledDenied,
		TenantGateOutcomeMissingObserved,
		TenantGateOutcomeMissingDenied,
		TenantGateOutcomeOrgClaimAbsent,
		TenantGateOutcomeOrgTenantUnbound,
		TenantGateOutcomeOrgMatched,
		TenantGateOutcomeOrgMismatchDenied,
		TenantGateOutcomeSeatLimitDenied,
	} {
		if !seen[outcome] {
			t.Errorf("outcome=%s: no series found via Collect() — init() must pre-register every label value so /metrics never omits one entirely",
				outcome)
		}
	}
}

// TestRecordTenantGate_LabelsAreIndependent: a recorded outcome must bump
// exactly its own series. Cross-label bleed would make the panel that decides
// the TENANT_MISSING_MODE flip lie about which decision was taken.
func TestRecordTenantGate_LabelsAreIndependent(t *testing.T) {
	beforeObserved := testutil.ToFloat64(TenantGateTotal.WithLabelValues(TenantGateOutcomeMissingObserved))
	beforeDenied := testutil.ToFloat64(TenantGateTotal.WithLabelValues(TenantGateOutcomeMissingDenied))

	RecordTenantGate(TenantGateOutcomeMissingObserved)
	RecordTenantGate(TenantGateOutcomeMissingObserved)
	RecordTenantGate(TenantGateOutcomeMissingDenied)

	if got := testutil.ToFloat64(TenantGateTotal.WithLabelValues(TenantGateOutcomeMissingObserved)) - beforeObserved; got != 2 {
		t.Errorf("missing_observed delta = %v, want 2", got)
	}
	if got := testutil.ToFloat64(TenantGateTotal.WithLabelValues(TenantGateOutcomeMissingDenied)) - beforeDenied; got != 1 {
		t.Errorf("missing_denied delta = %v, want 1", got)
	}
}
