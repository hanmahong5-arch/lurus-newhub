package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// RecordRateLimited must bump exactly the (scope, type) pair it was called
// with — cross-label bleed would make the token/tenant panels lie.
func TestRecordRateLimited_LabelsAreIndependent(t *testing.T) {
	beforeTok := testutil.ToFloat64(RateLimitedTotal.WithLabelValues("token", "rpm"))
	beforeTen := testutil.ToFloat64(RateLimitedTotal.WithLabelValues("tenant", "rpm"))

	RecordRateLimited("token", "rpm")
	RecordRateLimited("token", "rpm")
	RecordRateLimited("tenant", "rpm")

	if got := testutil.ToFloat64(RateLimitedTotal.WithLabelValues("token", "rpm")) - beforeTok; got != 2 {
		t.Errorf("token/rpm delta = %v, want 2", got)
	}
	if got := testutil.ToFloat64(RateLimitedTotal.WithLabelValues("tenant", "rpm")) - beforeTen; got != 1 {
		t.Errorf("tenant/rpm delta = %v, want 1", got)
	}
}

// The exported series name must be exactly newhub_rate_limited_total — that is
// the contract dashboards and alert rules address. CollectAndCount filtered by
// the fully-qualified name returns 0 if the metric were registered under any
// other name.
func TestRateLimitedTotal_MetricName(t *testing.T) {
	RecordRateLimited("token", "tpm") // ensure at least one child exists
	if n := testutil.CollectAndCount(RateLimitedTotal, "newhub_rate_limited_total"); n < 1 {
		t.Fatalf("no series named newhub_rate_limited_total collected (got %d)", n)
	}
	if n := testutil.CollectAndCount(RateLimitedTotal, "newhub_some_other_name"); n != 0 {
		t.Fatalf("name filter sanity check failed: %d series under a wrong name", n)
	}
}
