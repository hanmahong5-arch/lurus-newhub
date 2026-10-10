package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Uses Collect() (not WithLabelValues, which would create the series it is
// checking for) — same pattern as l7_settlement_failed_zero_init_test.go.
func TestNATSPublishFailedTotal_ZeroInitLabels(t *testing.T) {
	ch := make(chan prometheus.Metric, 16)
	NATSPublishFailedTotal.Collect(ch)
	close(ch)

	seen := map[string]float64{}
	for m := range ch {
		pb := &dto.Metric{}
		if err := m.Write(pb); err != nil {
			t.Fatalf("Write: %v", err)
		}
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "subject_group" {
				seen[lp.GetValue()] = pb.GetCounter().GetValue()
			}
		}
	}
	for _, g := range []string{"quota", "usage", "image", "other"} {
		v, ok := seen[g]
		if !ok {
			t.Errorf("subject_group=%s not pre-registered", g)
		} else if v != 0 {
			t.Errorf("subject_group=%s initial value %v, want 0", g, v)
		}
	}
}

// Plain counters are registered at declaration, so they are on /metrics at 0
// from boot; assert that rather than assume it.
func TestDataPipelineCounters_PresentAtZero(t *testing.T) {
	for name, c := range map[string]prometheus.Collector{
		"log_search_sync_failed":  LogSearchSyncFailedTotal,
		"quota_data_write_failed": QuotaDataWriteFailedTotal,
	} {
		ch := make(chan prometheus.Metric, 2)
		c.Collect(ch)
		close(ch)
		n := 0
		for range ch {
			n++
		}
		if n != 1 {
			t.Errorf("%s: %d series via Collect, want 1", name, n)
		}
	}
}
