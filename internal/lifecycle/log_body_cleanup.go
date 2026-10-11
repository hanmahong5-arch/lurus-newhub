package lifecycle

// log_body_cleanup.go - the leader-gated sweep of expired archived bodies
// (migration 052, table log_bodies).
//
// Unlike log_retention.go this one is ON by default and has no floor: every
// archived row carries its own expires_at, stamped at write time from
// LOG_BODY_RETENTION_DAYS, so the sweep only enforces a promise the row
// already made. Reads treat an expired row as absent, so a sweep that lags
// never serves a body past its deadline.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

const logBodyCleanupTaskName = "log-body-cleanup"

const (
	logBodyCleanupDefaultBatch      = 500
	logBodyCleanupDefaultMaxBatches = 200
	logBodyCleanupDefaultInterval   = time.Hour
)

// LogBodyCleanupConfig is one reading of the sweep's tuning environment.
type LogBodyCleanupConfig struct {
	Batch      int           // LOG_BODY_CLEANUP_BATCH
	MaxBatches int           // LOG_BODY_CLEANUP_MAX_BATCHES_PER_PASS
	Interval   time.Duration // LOG_BODY_CLEANUP_INTERVAL_SECONDS
}

// LoadLogBodyCleanupConfig reads the environment; unparsable or non-positive
// values fall back to the defaults.
func LoadLogBodyCleanupConfig() LogBodyCleanupConfig {
	interval := logBodyCleanupDefaultInterval
	if raw := os.Getenv("LOG_BODY_CLEANUP_INTERVAL_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}
	pos := func(name string, def int) int {
		if v := common.GetEnvOrDefault(name, def); v > 0 {
			return v
		}
		return def
	}
	return LogBodyCleanupConfig{
		Batch:      pos("LOG_BODY_CLEANUP_BATCH", logBodyCleanupDefaultBatch),
		MaxBatches: pos("LOG_BODY_CLEANUP_MAX_BATCHES_PER_PASS", logBodyCleanupDefaultMaxBatches),
		Interval:   interval,
	}
}

// LogBodyCleanupInterval resolves the tick period for taskreg.
func LogBodyCleanupInterval() time.Duration { return LoadLogBodyCleanupConfig().Interval }

// RunLogBodyCleanupPass deletes expired bodies anchored at now and returns the
// row count. An error is returned so the LeaderTask does not stamp success on
// a pass that failed.
func RunLogBodyCleanupPass(ctx context.Context, cfg LogBodyCleanupConfig, now time.Time) (int64, error) {
	n, err := repo.DeleteExpiredLogBodies(ctx, now.Unix(), cfg.Batch, cfg.MaxBatches)
	if err != nil {
		common.SysError(fmt.Sprintf("log body cleanup: delete expired bodies: %v", err))
		return n, err
	}
	if n > 0 {
		common.SysLog(fmt.Sprintf("log body cleanup: deleted %d expired body row(s)", n))
	}
	// Second promise: a tenant that withdrew consent keeps nothing. The
	// withdrawal itself purges synchronously; this catches what that missed.
	m, err := repo.DeleteUnconsentedLogBodies(ctx, cfg.Batch, cfg.MaxBatches)
	if err != nil {
		common.SysError(fmt.Sprintf("log body cleanup: delete unconsented bodies: %v", err))
		return n + m, err
	}
	if m > 0 {
		common.SysLog(fmt.Sprintf("log body cleanup: deleted %d body row(s) of tenants without consent", m))
	}
	return n + m, nil
}

// StartLogBodyCleanupWithContext launches the leader-gated sweep. Leader-only
// for the same reason log-retention is: the deletes are idempotent, but three
// replicas running them is wasted work against the relay's own writes.
func StartLogBodyCleanupWithContext(ctx context.Context) {
	cfg := LoadLogBodyCleanupConfig()
	common.SysLog(fmt.Sprintf("log body cleanup task started, interval=%s", cfg.Interval))

	taskreg.Register(logBodyCleanupTaskName, LogBodyCleanupInterval, true, nil)

	task := NewLeaderTask(logBodyCleanupTaskName, cfg.Interval, func(c context.Context) error {
		_, err := RunLogBodyCleanupPass(c, LoadLogBodyCleanupConfig(), time.Now())
		return err
	})

	common.SafeGoWithContext(ctx, func(c context.Context) {
		_ = task.Run(c)
	})
}
