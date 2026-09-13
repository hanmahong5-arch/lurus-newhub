package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// sessionSweepInterval is the scan cadence for the user_sessions retention
// sweep (L7 repair round 3, finding routing-resilience-limits-13#11). Daily
// matches secretRotationInterval — the two retention windows this sweep
// enforces (repo.sessionSweepRevokedRetention/sessionSweepInactiveRetention)
// are both measured in whole days, so finer polling buys nothing.
const sessionSweepInterval = 24 * time.Hour

// StartSessionSweepWithContext launches the leader-gated user_sessions
// retention sweep: rows revoked more than 30 days ago, or never revoked but
// idle for more than 90 days, are hard-deleted (repo.SweepExpiredUserSessions).
// Wrapped in a LeaderTask so only the current leader sweeps across a
// multi-replica deployment — the delete is a plain conditional WHERE, safe
// to run more than once, but firing it on every replica would just be
// wasted DB work. The goroutine exits when ctx is cancelled.
func StartSessionSweepWithContext(ctx context.Context) {
	common.SysLog(fmt.Sprintf("user_sessions retention sweep started, interval=%s", sessionSweepInterval))

	task := NewLeaderTask("session-sweep", sessionSweepInterval, func(c context.Context) error {
		n, err := repo.SweepExpiredUserSessions(time.Now())
		if err != nil {
			common.SysError("user_sessions retention sweep failed: " + err.Error())
			return err
		}
		if n > 0 {
			common.SysLog(fmt.Sprintf("user_sessions retention sweep: removed %d row(s)", n))
		}
		return nil
	})

	common.SafeGoWithContext(ctx, func(c context.Context) {
		_ = task.Run(c)
	})
}
