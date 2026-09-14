package lifecycle

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// TestSessionSweep_StartRegistersHeartbeat is the A-F11/B-F8 oracle
// (operator-approved ruling): session-sweep is a live NewLeaderTask
// heartbeat that GET /api/v2/admin/system/tasks previously omitted because
// nothing registered it in taskreg. Mirrors
// TestSecretRotation_StartRegistersHeartbeat — does not assert the gauge
// resets to 0, since that is NewLeaderTask's job (leader_election_test.go),
// not this one line's.
func TestSessionSweep_StartRegistersHeartbeat(t *testing.T) {
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartSessionSweepWithContext(ctx)

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == "session-sweep" {
			found = true
			if !task.LeaderOnly {
				t.Errorf("session-sweep task.LeaderOnly = false, want true")
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartSessionSweepWithContext", "session-sweep")
	}
}
