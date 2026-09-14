package lifecycle

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// TestSecretRotation_StartRegistersHeartbeat is the A-F1 oracle for
// secret-rotation: taskreg.Register's call site inside
// StartSecretRotationWithContext is otherwise deletable with every test in
// this package staying green (nothing else asserts on the registry for this
// task name). Unlike the sibling Test<Job>_StartRegistersHeartbeat tests,
// this does NOT assert the gauge resets to 0 — NewLeaderTask (called a few
// lines below the Register site) already does that at construction and is
// covered by leader_election_test.go; asserting it again here would just be
// testing NewLeaderTask a second time, not this job's own wiring.
func TestSecretRotation_StartRegistersHeartbeat(t *testing.T) {
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartSecretRotationWithContext(ctx)

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == "secret-rotation" {
			found = true
			if !task.LeaderOnly {
				t.Errorf("secret-rotation task.LeaderOnly = false, want true")
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartSecretRotationWithContext", "secret-rotation")
	}
}
