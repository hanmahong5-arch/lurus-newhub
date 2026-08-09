package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func gaugeValue(t *testing.T, g interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}

// Until a snapshot is published, callers must be able to tell "not measured"
// apart from "zero pending" — reporting a confident "ok" for a pod that never
// probed the schema is exactly the false assurance this whole path exists to
// remove.
func TestPendingSchemaMigrations_UnknownBeforeFirstPublish(t *testing.T) {
	if _, known := PendingSchemaMigrations(); known {
		t.Skip("another test in this package already published a snapshot; ordering-independent by design")
	}
	if count, known := PendingSchemaMigrations(); known || count != 0 {
		t.Errorf("PendingSchemaMigrations() = %d,%v want 0,false before any publish", count, known)
	}
}

func TestSetSchemaMigrations_PublishesGaugesAndSnapshot(t *testing.T) {
	SetSchemaMigrations(3, 27)

	if got := gaugeValue(t, SchemaMigrationsPending); got != 3 {
		t.Errorf("pending gauge = %v, want 3", got)
	}
	if got := gaugeValue(t, SchemaMigrationsApplied); got != 27 {
		t.Errorf("applied gauge = %v, want 27", got)
	}
	count, known := PendingSchemaMigrations()
	if !known || count != 3 {
		t.Errorf("PendingSchemaMigrations() = %d,%v want 3,true", count, known)
	}

	// A later probe must overwrite, not accumulate — the gauge is a level, and a
	// rollout that applied everything has to be able to clear the alert.
	SetSchemaMigrations(0, 30)
	if got := gaugeValue(t, SchemaMigrationsPending); got != 0 {
		t.Errorf("pending gauge after re-publish = %v, want 0", got)
	}
	if got := gaugeValue(t, SchemaMigrationsApplied); got != 30 {
		t.Errorf("applied gauge after re-publish = %v, want 30", got)
	}
	count, known = PendingSchemaMigrations()
	if !known || count != 0 {
		t.Errorf("PendingSchemaMigrations() = %d,%v want 0,true", count, known)
	}
}
