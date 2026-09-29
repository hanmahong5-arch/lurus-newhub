package operation_setting

// model_probe_setting_test.go — GetModelProbeSetting must (1) return the
// documented defaults on a fresh registration, (2) return a COPY (mutating
// the result must not touch the package-level state) and (3) clamp
// nonsense values instead of handing a broken interval/threshold/cap to the
// prober (internal/app/modelprobe).

import "testing"

func snapshotModelProbeSetting(t *testing.T) {
	t.Helper()
	previous := modelProbeSetting
	t.Cleanup(func() { modelProbeSetting = previous })
}

func TestGetModelProbeSetting_Defaults(t *testing.T) {
	snapshotModelProbeSetting(t)
	modelProbeSetting = ModelProbeSetting{
		Enabled:          false,
		Groups:           "free",
		IntervalHours:    24,
		DisableThreshold: 3,
		MaxProbesPerRun:  40,
		SleepSeconds:     3,
	}

	got := GetModelProbeSetting()
	if got.Enabled {
		t.Error("Enabled default must be false")
	}
	if got.Groups != "free" {
		t.Errorf("Groups = %q, want \"free\"", got.Groups)
	}
	if got.IntervalHours != 24 || got.DisableThreshold != 3 || got.MaxProbesPerRun != 40 || got.SleepSeconds != 3 {
		t.Errorf("got = %+v, want the documented defaults", got)
	}
}

func TestGetModelProbeSetting_ReturnsACopy(t *testing.T) {
	snapshotModelProbeSetting(t)
	modelProbeSetting = ModelProbeSetting{Groups: "free", IntervalHours: 24, DisableThreshold: 3, MaxProbesPerRun: 40, SleepSeconds: 3}

	got := GetModelProbeSetting()
	got.Groups = "mutated"
	got.IntervalHours = 999

	again := GetModelProbeSetting()
	if again.Groups != "free" || again.IntervalHours != 24 {
		t.Errorf("mutating the returned value leaked into the package state: %+v", again)
	}
}

func TestGetModelProbeSetting_ClampsNonsenseValues(t *testing.T) {
	snapshotModelProbeSetting(t)
	modelProbeSetting = ModelProbeSetting{
		Enabled:          true,
		Groups:           "free,tier1",
		IntervalHours:    0.5, // < 1h
		DisableThreshold: 0,
		MaxProbesPerRun:  0,
		SleepSeconds:     -1,
	}

	got := GetModelProbeSetting()
	if got.IntervalHours != 24 {
		t.Errorf("IntervalHours = %v, want clamped to 24", got.IntervalHours)
	}
	if got.DisableThreshold != 3 {
		t.Errorf("DisableThreshold = %v, want clamped to 3", got.DisableThreshold)
	}
	if got.MaxProbesPerRun != 40 {
		t.Errorf("MaxProbesPerRun = %v, want clamped to 40", got.MaxProbesPerRun)
	}
	if got.SleepSeconds != 3 {
		t.Errorf("SleepSeconds = %v, want clamped to 3", got.SleepSeconds)
	}
	// Values that need no clamping pass through untouched.
	if !got.Enabled || got.Groups != "free,tier1" {
		t.Errorf("got = %+v, want Enabled/Groups passed through", got)
	}
}

func TestGetModelProbeSetting_ValidValuesPassThrough(t *testing.T) {
	snapshotModelProbeSetting(t)
	modelProbeSetting = ModelProbeSetting{
		Enabled:          true,
		Groups:           "free",
		IntervalHours:    1, // exactly the boundary, must not clamp
		DisableThreshold: 1,
		MaxProbesPerRun:  1,
		SleepSeconds:     0,
	}

	got := GetModelProbeSetting()
	if got.IntervalHours != 1 || got.DisableThreshold != 1 || got.MaxProbesPerRun != 1 || got.SleepSeconds != 0 {
		t.Errorf("boundary values were clamped when they should not have been: %+v", got)
	}
}
