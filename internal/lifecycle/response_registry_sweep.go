package lifecycle

// response_registry_sweep.go — leader-gated retention sweep for
// response_registry (cycle-8 L7, tasks-plugins-12). Mirrors
// session_sweep.go / secret_rotation.go's NewLeaderTask pattern.

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// responseRegistrySweepTaskName is the "task" label this job stamps on
// metrics.LeaderTaskLastSuccess (via NewLeaderTask) and registers under in
// taskreg — must match the string GET /api/v2/admin/system/tasks expects.
const responseRegistrySweepTaskName = "response-registry-sweep"

// responseRegistrySweepInterval matches the batch delete pass cadence: rows
// expire on the scale of RESPONSE_REGISTRY_TTL_DAYS (default 30 whole
// days), so hourly polling is a wide enough margin without leaving expired
// rows queryable for long.
const responseRegistrySweepInterval = time.Hour

// StartResponseRegistrySweepWithContext launches the leader-gated
// response_registry retention sweep: rows whose expires_at has passed are
// hard-deleted in batches (repo.SweepExpiredResponseRegistry). Wrapped in a
// LeaderTask so only the current leader sweeps across a multi-replica
// deployment. The goroutine exits when ctx is cancelled.
func StartResponseRegistrySweepWithContext(ctx context.Context) {
	common.SysLog(fmt.Sprintf("response_registry retention sweep started, interval=%s", responseRegistrySweepInterval))

	// L3: NewLeaderTask already stamps metrics.LeaderTaskLastSuccess and
	// Set(0)s it at construction (leader_election.go); this only adds the
	// static registry entry the other heartbeat jobs also register for
	// GET /api/v2/admin/system/tasks.
	taskreg.Register(responseRegistrySweepTaskName, func() time.Duration { return responseRegistrySweepInterval }, true, nil)

	task := NewLeaderTask(responseRegistrySweepTaskName, responseRegistrySweepInterval, runResponseRegistrySweep)

	common.SafeGoWithContext(ctx, func(c context.Context) {
		_ = task.Run(c)
	})
}

// runResponseRegistrySweep executes one sweep pass. Extracted from
// StartResponseRegistrySweepWithContext so tests can drive a single pass
// directly (mirrors runAuditCleanup's role in audit_cleanup.go) without
// waiting on the ticker. Does NOT stamp metrics.LeaderTaskLastSuccess
// itself — LeaderTask.Run does that on a nil return, same as every other
// NewLeaderTask-wrapped job in this package.
func runResponseRegistrySweep(c context.Context) error {
	n, err := repo.SweepExpiredResponseRegistry(time.Now())
	if err != nil {
		common.SysError("response_registry retention sweep failed: " + err.Error())
		return err
	}
	if n > 0 {
		common.SysLog(fmt.Sprintf("response_registry retention sweep: removed %d row(s)", n))
	}
	return nil
}
