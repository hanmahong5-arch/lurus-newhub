package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// secretRotationInterval is the scan cadence for due token rotations. Daily is
// the finest meaningful granularity since rotation intervals are configured in
// whole days.
const secretRotationInterval = 24 * time.Hour

// StartSecretRotationWithContext launches the leader-gated automatic token
// rotation task. It is wrapped in a LeaderTask so that, across a multi-replica
// deployment, only the current leader scans and rotates — each due token is
// rotated exactly once. The goroutine exits when ctx is cancelled.
//
// Phase H1.4: rotates tokens with auto_rotate_days > 0 whose interval elapsed,
// records an audit event per rotation, and emails the owner.
func StartSecretRotationWithContext(ctx context.Context) {
	common.SysLog(fmt.Sprintf("secret rotation started, interval=%s", secretRotationInterval))

	// L3: NewLeaderTask already stamps metrics.LeaderTaskLastSuccess and
	// Set(0)s it at construction (leader_election.go); this only adds the
	// static registry entry the other five heartbeat jobs also register
	// for GET /api/v2/admin/system/tasks.
	taskreg.Register("secret-rotation", func() time.Duration { return secretRotationInterval }, true, nil)

	task := NewLeaderTask("secret-rotation", secretRotationInterval, func(c context.Context) error {
		n, err := app.RotateDueTokens(c, common.GetTimestamp(), common.SendEmail)
		if err != nil {
			common.SysError("secret rotation pass failed: " + err.Error())
			return err
		}
		if n > 0 {
			common.SysLog(fmt.Sprintf("secret rotation: rotated %d token(s)", n))
		}
		return nil
	})

	common.SafeGoWithContext(ctx, func(c context.Context) {
		_ = task.Run(c)
	})
}
