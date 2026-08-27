package common

import (
	"os"
	"path/filepath"
	"testing"
)

// r4_cost_spike_env_test.go — zero-regression lock for the env wiring at
// init.go:106 (`CostSpikeEnforce = GetEnvOrDefaultBool("COST_SPIKE_ENFORCE",
// false)`). Without this, that line has no coverage of its own: existing
// tests only assert on GetEnvOrDefaultBool directly (e.g. the middleware
// package's TestR4CostSpikeEnforce_DefaultsFalse), never on InitEnv()
// actually reading COST_SPIKE_ENFORCE and assigning the result to
// common.CostSpikeEnforce. The D-A6 default-false rationale in
// constants.go leans on "one env var flips this back on, fully reversible"
// — that reversibility claim needs its own test, or a silent deletion of
// init.go:106 would leave the flag permanently false regardless of env.
//
// covSnapshotInitState/covRestoreInitState (cov_common_init_test.go) do not
// track CostSpikeEnforce, so this test snapshots/restores it separately
// alongside the shared fixture.
func TestInitEnv_CostSpikeEnforce_WiredFromEnv(t *testing.T) {
	snap := covSnapshotInitState()
	prevEnforce := CostSpikeEnforce
	t.Cleanup(func() {
		covRestoreInitState(snap)
		CostSpikeEnforce = prevEnforce
	})
	covClearInitEnvVars(t)

	if orig, had := os.LookupEnv("COST_SPIKE_ENFORCE"); had {
		t.Cleanup(func() { _ = os.Setenv("COST_SPIKE_ENFORCE", orig) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv("COST_SPIKE_ENFORCE") })
	}
	_ = os.Unsetenv("COST_SPIKE_ENFORCE")

	tmpLogDir := filepath.Join(t.TempDir(), "logs-cost-spike-enforce")
	*LogDir = tmpLogDir

	InitEnv()
	if CostSpikeEnforce != false {
		t.Errorf("CostSpikeEnforce after InitEnv() with COST_SPIKE_ENFORCE unset = %v, want false", CostSpikeEnforce)
	}

	if err := os.Setenv("COST_SPIKE_ENFORCE", "true"); err != nil {
		t.Fatalf("setenv COST_SPIKE_ENFORCE: %v", err)
	}
	InitEnv()
	if CostSpikeEnforce != true {
		t.Errorf("CostSpikeEnforce after InitEnv() with COST_SPIKE_ENFORCE=true = %v, want true", CostSpikeEnforce)
	}
}
