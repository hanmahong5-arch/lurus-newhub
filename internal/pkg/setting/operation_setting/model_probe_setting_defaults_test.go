package operation_setting

import "testing"

// shippedModelProbeDefaults is captured at package initialisation, before any
// test overwrites modelProbeSetting, so the assertion below sees the values
// that actually ship.
var shippedModelProbeDefaults = modelProbeSetting

// The prober must ship OFF (it spends provider quota) and scoped to the
// "free" group only; turning it on or widening it is an explicit option
// change, never a code default.
func TestModelProbeSetting_ShippedDefaults(t *testing.T) {
	want := ModelProbeSetting{
		Enabled:          false,
		Groups:           "free",
		IntervalHours:    24,
		DisableThreshold: 3,
		MaxProbesPerRun:  40,
		SleepSeconds:     3,
	}
	if shippedModelProbeDefaults != want {
		t.Fatalf("shipped defaults = %+v, want %+v", shippedModelProbeDefaults, want)
	}
}
