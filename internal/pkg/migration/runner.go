// Package migration applies idempotent SQL migrations from an embedded
// fs.FS to a Postgres database. Ported from 2l-svc-platform's
// internal/pkg/migration — the in-process counterpart to the historical
// workflow of "operator psql -f migrations/NNN_*.sql", which left no
// record of which files had actually been applied to STAGE.
//
// newhub-specific baseline contract (read before adding migrations):
//
//   - migrations 001–004 are MySQL dialect (ON UPDATE CURRENT_TIMESTAMP,
//     ON DUPLICATE KEY UPDATE, COMMENT '...') and CANNOT execute on
//     PostgreSQL; 005–020 are PG-clean but were applied to STAGE by hand.
//     The Runner therefore NEVER executes 001–020: Run() seeds every
//     version <= BaselineThrough into public.schema_migrations via
//     INSERT ... ON CONFLICT DO NOTHING (bookkeeping only) and executes
//     only versions above it.
//
//   - On a fresh database the schema below the baseline comes from GORM
//     AutoMigrate, exactly as every AutoMigrate-only install (dev SQLite
//     tier, cold-start PG) has always worked. Known gap: pieces of
//     001–020 that AutoMigrate does not reproduce (006 seed rows, 004
//     composite UNIQUE, 008 column drops) will not exist on a fresh PG.
//     If one proves load-bearing, ship it as an idempotent PG-only
//     021_pg_baseline_gaps.sql through the root migration ledger.
//
//   - From 021 onward every migration MUST be PostgreSQL-only and
//     idempotent, and runs with the application's PG role — check table
//     ownership before shipping an ALTER (platform R6 lesson). Escape
//     hatch: MIGRATIONS_AUTO_RUN=false and apply by hand.
//
// The Runner is intentionally minimal: lex-sorted file order, one tx
// per file, a public.schema_migrations bookkeeping table, and a
// pg_advisory_lock so a future migrate subcommand or hand-run binary
// cannot race a booting pod (the boot leader-lease does not cover
// those).
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// AdvisoryLockID is the 64-bit key passed to pg_advisory_lock so that
// concurrent Runner.Run() invocations serialize instead of
// double-applying. The value is the ASCII bytes of "LurusHub" packed
// big-endian — distinct from platform's "LurusPla" key, so the two
// services never contend even if they ever share a database.
const AdvisoryLockID int64 = 0x4C75727573487562

// Runner applies SQL migrations from FS against DB.
type Runner struct {
	DB     *sql.DB
	FS     fs.FS
	Logger *slog.Logger

	// BaselineThrough names the highest version (filename minus .sql)
	// that is bookkeeping-only: Run() marks it and everything sorting
	// at or below it as applied WITHOUT executing the SQL. See the
	// package comment for why 001–020 must never execute. Empty means
	// no baseline (platform behavior: execute everything pending).
	BaselineThrough string
}

// Run discovers all *.sql files at the FS root, sorts them
// lexicographically, seeds versions <= BaselineThrough as applied
// (bookkeeping only), and executes any remaining version not yet
// recorded in public.schema_migrations. Concurrent invocations are
// serialized via pg_advisory_lock. Each migration runs in its own
// transaction.
func (r *Runner) Run(ctx context.Context) error {
	if r.DB == nil {
		return errors.New("migration: Runner.DB is nil")
	}
	if r.FS == nil {
		return errors.New("migration: Runner.FS is nil")
	}
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// One dedicated connection for the whole run: the advisory lock is
	// session-scoped, so lock and unlock through the pool can land on
	// different connections — the unlock silently no-ops and the lock
	// stays stranded until the owning connection is recycled.
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Lift the DSN statement_timeout for the whole critical section on this
	// connection; restored before it returns to the pool. reset is deferred
	// before lock so it runs after unlock but before conn.Close().
	reset, err := exemptFromStatementTimeout(ctx, conn)
	if err != nil {
		return err
	}
	defer reset()

	if err := r.lock(ctx, conn); err != nil {
		return err
	}
	defer r.unlock(ctx, conn, logger)

	if err := r.ensureTracker(ctx, conn); err != nil {
		return err
	}

	versions, err := DiscoverVersions(r.FS)
	if err != nil {
		return err
	}

	if r.BaselineThrough != "" {
		var baseline []string
		for _, v := range versions {
			if v <= r.BaselineThrough {
				baseline = append(baseline, v)
			}
		}
		if err := r.markApplied(ctx, logger, conn, baseline); err != nil {
			return fmt.Errorf("seed baseline through %s: %w", r.BaselineThrough, err)
		}
	}

	applied, err := r.loadApplied(ctx, conn)
	if err != nil {
		return err
	}

	pending := 0
	for _, v := range versions {
		if applied[v] {
			continue
		}
		if err := r.applyOne(ctx, logger, conn, v); err != nil {
			return fmt.Errorf("apply %s: %w", v, err)
		}
		pending++
	}

	if pending == 0 {
		logger.Info("migration: no pending migrations",
			"applied_count", len(applied),
			"discovered_count", len(versions))
	} else {
		logger.Info("migration: run complete",
			"applied_now", pending,
			"applied_total", len(applied)+pending,
			"discovered_count", len(versions))
	}
	return nil
}

// MarkApplied seeds versions as already-applied without running their
// SQL, taking the advisory lock itself. Exposed for operator tooling;
// Run() seeds its own baseline internally.
func (r *Runner) MarkApplied(ctx context.Context, versions []string) error {
	if r.DB == nil {
		return errors.New("migration: Runner.DB is nil")
	}
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	reset, err := exemptFromStatementTimeout(ctx, conn)
	if err != nil {
		return err
	}
	defer reset()
	if err := r.lock(ctx, conn); err != nil {
		return err
	}
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	defer r.unlock(ctx, conn, logger)
	if err := r.ensureTracker(ctx, conn); err != nil {
		return err
	}
	return r.markApplied(ctx, logger, conn, versions)
}

// markApplied is the lock-already-held core of MarkApplied.
func (r *Runner) markApplied(ctx context.Context, logger *slog.Logger, conn *sql.Conn, versions []string) error {
	for _, v := range versions {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO public.schema_migrations (version) VALUES ($1)
             ON CONFLICT (version) DO NOTHING`, v); err != nil {
			return fmt.Errorf("seed %s: %w", v, err)
		}
	}
	logger.Info("migration: marked versions as applied",
		"count", len(versions))
	return nil
}

// DiscoverVersions returns lex-sorted version strings (filename minus
// .sql extension) for every flat *.sql at the FS root. Subdirectories
// are skipped — rollback SQL is operator-driven, not auto-applied.
// Exported so tests and tools can validate ordering without
// instantiating a Runner.
func DiscoverVersions(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		versions = append(versions, strings.TrimSuffix(name, ".sql"))
	}
	sort.Strings(versions)
	return versions, nil
}

// exemptFromStatementTimeout lifts the DSN-injected statement_timeout and
// lock_timeout (P1-1) on a single dedicated connection for the whole boot
// migration critical section, returning a reset that restores the DSN
// defaults before the connection returns to the pool.
//
// Boot migrations are not request-path queries and must not be subject to the
// request-path cap: (1) a replica waits on the advisory lock for as long as a
// sibling replica's migration takes, which routinely exceeds the cap during a
// rolling boot and made Postgres cancel the wait -> pod FATAL (STAGE
// crash-loop 2026-07-15); (2) a legitimate heavy migration (int->BIGINT table
// rewrite) can exceed it; (3) even the bookkeeping DDL/queries can exceed a
// tight cap on a slow, contended node. Session-level SET (not SET LOCAL)
// covers every statement on the connection — lock wait, tracker DDL, applied
// scan, and each migration tx — and reset() (RESET, run WithoutCancel) returns
// the connection to the pool with the DSN caps intact so no other borrower is
// affected.
func exemptFromStatementTimeout(ctx context.Context, conn *sql.Conn) (reset func(), err error) {
	for _, q := range []string{`SET statement_timeout = 0`, `SET lock_timeout = 0`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	return func() {
		for _, q := range []string{`RESET statement_timeout`, `RESET lock_timeout`} {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), q)
		}
	}, nil
}

// lock takes the runner's session-scoped advisory lock on conn. The wait is
// unbounded because the connection has already been exempted from the DSN
// statement_timeout (see exemptFromStatementTimeout) — a holder that crashes
// releases the lock when its session ends, so there is no deadlock risk.
func (r *Runner) lock(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, AdvisoryLockID); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	return nil
}

func (r *Runner) unlock(ctx context.Context, conn *sql.Conn, logger *slog.Logger) {
	// WithoutCancel: unlock must proceed even when the caller's ctx is
	// already cancelled (e.g. shutdown mid-migration).
	if _, err := conn.ExecContext(context.WithoutCancel(ctx),
		`SELECT pg_advisory_unlock($1)`, AdvisoryLockID); err != nil {
		logger.Warn("migration: advisory unlock failed", "err", err)
	}
}

func (r *Runner) ensureTracker(ctx context.Context, conn *sql.Conn) error {
	const ddl = `CREATE TABLE IF NOT EXISTS public.schema_migrations (
        version    VARCHAR(255) PRIMARY KEY,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    )`
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	return nil
}

func (r *Runner) loadApplied(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version FROM public.schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations.version: %w", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter schema_migrations: %w", err)
	}
	return out, nil
}

func (r *Runner) applyOne(ctx context.Context, logger *slog.Logger, conn *sql.Conn, version string) error {
	started := time.Now()
	body, err := fs.ReadFile(r.FS, version+".sql")
	if err != nil {
		return fmt.Errorf("read %s.sql: %w", version, err)
	}

	// Runs on the run's dedicated connection, already exempted from the DSN
	// statement_timeout for the whole critical section (see
	// exemptFromStatementTimeout) — so a heavy migration (int->BIGINT table
	// rewrite) is not cancelled mid-rollout. The tx inherits the connection's
	// session setting.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("exec sql: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO public.schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record applied: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	logger.Info("migration: applied",
		"version", version,
		"duration", time.Since(started).String())
	return nil
}
