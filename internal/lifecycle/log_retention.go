package lifecycle

// log_retention.go — the leader-gated retention sweep for `logs` and
// `download_logs` (cycle-13 L6).
//
// Before this task the `logs` table had no retention path: the only delete
// was the operator-driven DELETE /api/log/ (handler/log.go), and the
// production manifest's comment claiming the table was bounded "by ageing
// through the same DeleteOldLog" described something that never ran.
// download_logs had nothing at all.
//
// DEFAULT IS OFF. LOG_RETENTION_DAYS and LOG_RETENTION_MONEY_DAYS both
// default to 0 = disabled, so deploying this task deletes nothing until an
// operator sets a window (the contractual values are an owner decision —
// O-retention in the cycle-13 plan). doc/runbook/log-retention.md carries
// the semantics an operator needs before setting one.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// logRetentionTaskName is the "task" label this job registers under in
// taskreg and stamps on metrics.LeaderTaskLastSuccess (NewLeaderTask does
// the stamping). GET /api/v2/admin/system/tasks lists it by this name.
const logRetentionTaskName = "log-retention"

// LogRetentionMinDays is the floor under LOG_RETENTION_DAYS. Diagnostics
// rows (error/system/manage) are what an incident review reads a week or
// three after the fact, and they are the only record of a rejected request
// — 30 days is the shortest window that still answers "what happened last
// month". A shorter setting is REFUSED (logged, nothing deleted) rather
// than clamped: silently widening an operator's window would be its own
// surprise.
const LogRetentionMinDays = 30

// LogRetentionMoneyMinDays is the floor under LOG_RETENTION_MONEY_DAYS.
// consume/topup/refund rows are what the invoice endpoints sum
// (v2_billing_invoices.go, billing_self.go), so deleting one rewrites a
// bill that has already been shown to a customer. 400 days keeps thirteen
// monthly statements reproducible — twelve months plus the month in
// progress.
const LogRetentionMoneyMinDays = 400

// logRetentionDefaults for the tuning knobs an operator rarely touches.
const (
	logRetentionDefaultBatch      = 500
	logRetentionDefaultMaxBatches = 200
	logRetentionDefaultInterval   = 24 * time.Hour
	// Download rows carry ip/user-agent/referer for anonymous callers and
	// feed nothing but rough download counts, so unlike `logs` this one
	// defaults to ON at 90 days. 0 disables it.
	downloadLogRetentionDefaultDays = 90
)

// moneyLogTypes are the rows a bill is derived from. They age out on
// LOG_RETENTION_MONEY_DAYS, never on LOG_RETENTION_DAYS.
//
// Refund rows are here with consume and topup deliberately: a refund is the
// counter-entry to a consume row, and aging one out without the other would
// leave a period whose log-derived total is higher than what was charged.
var moneyLogTypes = []int{
	entity.LogTypeTopup,
	entity.LogTypeConsume,
	entity.LogTypeRefund,
}

// diagnosticLogTypes are the rows that age out on LOG_RETENTION_DAYS.
//
// Together with moneyLogTypes this is a PARTITION of every log type entity
// declares — enforced by TestLogRetentionTypes_PartitionEveryDeclaredLogType,
// which parses entity/log.go. A type added there and classified in neither
// list would otherwise be retained forever with nobody noticing.
var diagnosticLogTypes = []int{
	entity.LogTypeUnknown,
	entity.LogTypeManage,
	entity.LogTypeSystem,
	entity.LogTypeError,
}

// LogRetentionConfig is one resolved reading of the retention environment.
// Resolved per pass, not once at boot, so an operator who sets a window on a
// running deployment does not have to wait for a restart.
type LogRetentionConfig struct {
	// DiagnosticDays is LOG_RETENTION_DAYS: the window for
	// diagnosticLogTypes. 0 = disabled.
	DiagnosticDays int
	// MoneyDays is LOG_RETENTION_MONEY_DAYS: the window for moneyLogTypes.
	// 0 = disabled.
	MoneyDays int
	// DownloadDays is DOWNLOAD_LOG_RETENTION_DAYS. 0 = disabled.
	DownloadDays int
	// Batch is the row count per DELETE (LOG_RETENTION_BATCH).
	Batch int
	// MaxBatches bounds one pass (LOG_RETENTION_MAX_BATCHES_PER_PASS); the
	// rest of a backlog drains on later passes.
	MaxBatches int
	// Interval is the tick period (LOG_RETENTION_INTERVAL_SECONDS).
	Interval time.Duration
}

// LoadLogRetentionConfig reads the six environment variables. A value that
// does not parse falls back to the default, matching
// common.GetEnvOrDefault's behaviour everywhere else in the codebase; a
// value that parses but is below a floor is left as-is here and refused by
// RunLogRetentionPass, so the refusal is logged with the number the
// operator actually set.
func LoadLogRetentionConfig() LogRetentionConfig {
	interval := logRetentionDefaultInterval
	if raw := os.Getenv("LOG_RETENTION_INTERVAL_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}
	return LogRetentionConfig{
		DiagnosticDays: common.GetEnvOrDefault("LOG_RETENTION_DAYS", 0),
		MoneyDays:      common.GetEnvOrDefault("LOG_RETENTION_MONEY_DAYS", 0),
		DownloadDays:   common.GetEnvOrDefault("DOWNLOAD_LOG_RETENTION_DAYS", downloadLogRetentionDefaultDays),
		Batch:          common.GetEnvOrDefault("LOG_RETENTION_BATCH", logRetentionDefaultBatch),
		MaxBatches:     common.GetEnvOrDefault("LOG_RETENTION_MAX_BATCHES_PER_PASS", logRetentionDefaultMaxBatches),
		Interval:       interval,
	}
}

// LogRetentionInterval resolves the tick period for taskreg, which needs a
// func it can call at any time rather than a value fixed at boot.
func LogRetentionInterval() time.Duration { return LoadLogRetentionConfig().Interval }

// RunLogRetentionPass executes one retention pass anchored at now and
// returns the rows deleted per logical table ("logs" for both `logs` legs,
// "download_logs"). Returns an error when a leg failed, so the caller's
// LeaderTask does not stamp a success timestamp on a pass that did not do
// its job.
//
// Each leg is independent: a refused or disabled window skips that leg and
// leaves the others running.
func RunLogRetentionPass(ctx context.Context, cfg LogRetentionConfig, now time.Time) (map[string]int64, error) {
	deleted := map[string]int64{}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if days, ok := acceptedWindow("LOG_RETENTION_DAYS", cfg.DiagnosticDays, LogRetentionMinDays); ok {
		n, err := sweepLogs(ctx, now, days, diagnosticLogTypes, cfg)
		deleted["logs"] += n
		note(err)
	}
	if days, ok := acceptedWindow("LOG_RETENTION_MONEY_DAYS", cfg.MoneyDays, LogRetentionMoneyMinDays); ok {
		n, err := sweepLogs(ctx, now, days, moneyLogTypes, cfg)
		deleted["logs"] += n
		note(err)
	}
	if cfg.DownloadDays > 0 {
		cutoff := now.AddDate(0, 0, -cfg.DownloadDays)
		n, err := repo.DeleteDownloadLogsBefore(ctx, cutoff, cfg.Batch, cfg.MaxBatches)
		deleted["download_logs"] += n
		if err != nil {
			common.SysError(fmt.Sprintf("log retention: delete download_logs before %s: %v", cutoff.Format(time.RFC3339), err))
			note(err)
		} else {
			recordLogRetentionDeleted("download_logs", n)
			if pending, cErr := repo.CountDownloadLogsBefore(ctx, cutoff); cErr == nil {
				recordLogRetentionPending("download_logs", pending)
			}
		}
	}

	return deleted, firstErr
}

// acceptedWindow applies the floor policy: 0 (or negative) means the leg is
// disabled and says nothing; a positive value below the floor is REFUSED
// with a SysError naming both numbers, and deletes nothing.
func acceptedWindow(envName string, days int, floor int) (int, bool) {
	if days <= 0 {
		return 0, false
	}
	if days < floor {
		common.SysError(fmt.Sprintf(
			"log retention: %s=%d is below the %d-day floor; refusing to delete anything on this window",
			envName, days, floor))
		return 0, false
	}
	return days, true
}

// sweepLogs runs one leg: delete `logs` rows of these types older than
// now-days, then report the remaining backlog.
func sweepLogs(ctx context.Context, now time.Time, days int, types []int, cfg LogRetentionConfig) (int64, error) {
	cutoff := now.AddDate(0, 0, -days).Unix()
	n, err := repo.DeleteLogsBefore(ctx, cutoff, types, cfg.Batch, cfg.MaxBatches)
	if err != nil {
		common.SysError(fmt.Sprintf("log retention: delete logs types=%v before %d: %v", types, cutoff, err))
		return n, err
	}
	recordLogRetentionDeleted("logs", n)
	if pending, cErr := repo.CountLogsBefore(ctx, cutoff, types); cErr == nil {
		recordLogRetentionPending("logs", pending)
	}
	return n, nil
}

// recordLogRetentionDeleted / recordLogRetentionPending are the two
// observability hooks. They log today because internal/pkg/metrics is not
// this lane's to edit: the cycle-13 plan puts
// lurus_gateway_log_retention_deleted_total{table} and
// lurus_gateway_log_retention_pending_rows{table} in L10, to be reached
// through metrics.RecordLogRetention / metrics.SetLogRetentionPending once
// those exist. Swapping the body here is the whole wiring step.
func recordLogRetentionDeleted(table string, n int64) {
	if n > 0 {
		common.SysLog(fmt.Sprintf("log retention: deleted %d row(s) from %s", n, table))
	}
}

func recordLogRetentionPending(table string, n int64) {
	if n > 0 {
		common.SysLog(fmt.Sprintf("log retention: %s still has %d row(s) past the window (will drain on later passes)", table, n))
	}
}

// StartLogRetentionWithContext launches the leader-gated retention sweep.
// Leader-only for the same reason session-sweep is: the deletes are plain
// conditional WHEREs that are safe to run twice, but running them on all
// three replicas is wasted database work against the relay's own log
// writes. The goroutine exits when ctx is cancelled.
func StartLogRetentionWithContext(ctx context.Context) {
	cfg := LoadLogRetentionConfig()
	common.SysLog(fmt.Sprintf(
		"log retention task started, interval=%s, logs=%dd, money=%dd, download_logs=%dd (0 = disabled)",
		cfg.Interval, cfg.DiagnosticDays, cfg.MoneyDays, cfg.DownloadDays))

	taskreg.Register(logRetentionTaskName, LogRetentionInterval, true, nil)

	task := NewLeaderTask(logRetentionTaskName, cfg.Interval, func(c context.Context) error {
		_, err := RunLogRetentionPass(c, LoadLogRetentionConfig(), time.Now())
		return err
	})

	common.SafeGoWithContext(ctx, func(c context.Context) {
		_ = task.Run(c)
	})
}
