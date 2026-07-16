package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordPanic(t *testing.T) {
	before := testutil.ToFloat64(PanicsRecovered.WithLabelValues("test_boundary"))
	RecordPanic("test_boundary")
	after := testutil.ToFloat64(PanicsRecovered.WithLabelValues("test_boundary"))
	if after-before != 1 {
		t.Errorf("RecordPanic should increment the counter by 1: before=%f after=%f", before, after)
	}
}

// RegisterDBStats(nil) must be a safe no-op and must never panic.
func TestRegisterDBStats_NilIsNoOp(t *testing.T) {
	RegisterDBStats("nil_pool", nil)
}
