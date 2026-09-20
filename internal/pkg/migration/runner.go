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
//
// One exception to "one tx per file": a file whose FIRST line is
// NoTransactionDirective runs outside any transaction, one statement at a
// time, and may contain nothing but CREATE INDEX CONCURRENTLY IF NOT EXISTS
// (039_logs_tenant_created_index.sql is the first). Read that constant's
// doc comment before adding another one — outside a transaction a failure
// part way through is not undone.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
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

// NoTransactionDirective, when it is the FIRST line of a migration file,
// opts that file out of the one-transaction-per-file rule: the Runner
// executes its statements one at a time on the run's dedicated connection
// instead of inside a BeginTx. It exists for CREATE INDEX CONCURRENTLY,
// which PostgreSQL rejects with SQLSTATE 25001 ("cannot run inside a
// transaction block") — the reason logs had no (tenant_id, created_at)
// index before migration 039.
//
// One statement at a time, not one ExecContext for the whole body: a
// multi-statement simple-query message runs inside an IMPLICIT transaction
// block, which PostgreSQL rejects for the same reason an explicit BEGIN
// would. NoTransactionStatements does the splitting.
//
// FAILURE MODE a no-transaction file must document in its own header: a
// CREATE INDEX CONCURRENTLY that fails part way leaves an INVALID index
// behind, and because every allowed statement carries IF NOT EXISTS the
// next run SKIPS it and records the version — so the index stays INVALID
// and unused. Repair is operator-driven: DROP INDEX CONCURRENTLY <name>,
// then re-create it by hand (see doc/runbook/database.md).
const NoTransactionDirective = "-- lurus:no-transaction"

// RequiresTableDirective is the optional second directive of a
// no-transaction file: "-- lurus:requires-table public.logs" makes the
// Runner skip that file's statements (recording the version anyway) when
// the table is absent.
//
// It is the no-transaction counterpart of the to_regclass guard every
// transactional migration since 022 wraps its body in (see
// 023_add_rate_limit_columns.sql). That guard is a DO $$ block, which is a
// transaction context of its own and therefore cannot hold a CREATE INDEX
// CONCURRENTLY. Without this directive, 039 hard-fails the whole run on a
// database where AutoMigrate has not created "logs" — runner-first
// databases such as a partial DR restore, and the empty-database
// integration tests. Skip-and-record matches what the DO $$ guards already
// do (they RAISE WARNING and continue); the consequence — the index is
// missing on a runner-first database — belongs in the migration's header.
const RequiresTableDirective = "-- lurus:requires-table"

// noTransactionStatementRe is the allow-list for the statements a
// no-transaction file may contain. Narrow on purpose: running outside a
// transaction means a failure part way through leaves the database in a
// state no rollback undoes, which is acceptable for an additive,
// IF NOT EXISTS index build and for nothing else in this schema yet.
// Enforced both here (at apply time) and by the structural test over the
// embedded FS (no_transaction_files_structural_test.go), so a file that
// would be rejected at boot is rejected in CI first.
var noTransactionStatementRe = regexp.MustCompile(
	`(?is)^CREATE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+[^\s(;]+\s+ON\s+[^;]+$`)

// requiresTableRe bounds what RequiresTableDirective may name before it
// reaches to_regclass: a bare identifier or schema-qualified pair. to_regclass
// takes text and raises on malformed input rather than returning NULL, so the
// shape is checked before the query, not after.
var requiresTableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// lineCommentRe strips a "-- ..." trailing comment. Line comments only: the
// allowed statement shape carries no string literals, so there is no
// quoted "--" for this to mangle, and NoTransactionStatements re-checks
// every statement against noTransactionStatementRe afterwards anyway.
var lineCommentRe = regexp.MustCompile(`--[^\n]*`)

// HasNoTransactionDirective reports whether body's first line is exactly
// NoTransactionDirective. First line only: a directive buried in a comment
// block further down must not silently change how a file executes.
func HasNoTransactionDirective(body []byte) bool {
	first, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimSpace(first) == NoTransactionDirective
}

// RequiredTable returns the table named by RequiresTableDirective, or ""
// when the file carries no such directive. Only comment lines are scanned —
// the directive is header metadata, not something a caller can hide after
// the first statement.
func RequiredTable(body []byte) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "--") {
			break
		}
		if !strings.HasPrefix(trimmed, RequiresTableDirective) {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, RequiresTableDirective))
		name = strings.TrimPrefix(name, ":")
		name = strings.TrimSpace(name)
		if !requiresTableRe.MatchString(name) {
			return "", fmt.Errorf("migration: %s names %q, want [schema.]table", RequiresTableDirective, name)
		}
		return name, nil
	}
	return "", nil
}

// NoTransactionStatements splits a no-transaction migration body into the
// individual statements the Runner will execute, rejecting anything outside
// noTransactionStatementRe. An empty body is an error too: a file that opts
// out of transactions and then runs nothing is a mistake, not a no-op.
func NoTransactionStatements(body []byte) ([]string, error) {
	stripped := lineCommentRe.ReplaceAllString(string(body), "")
	var out []string
	for _, raw := range strings.Split(stripped, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		if !noTransactionStatementRe.MatchString(stmt) {
			return nil, fmt.Errorf(
				"migration: a %s file may only contain CREATE INDEX CONCURRENTLY IF NOT EXISTS statements, got %q",
				NoTransactionDirective, truncateForError(stmt))
		}
		out = append(out, stmt)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("migration: %s file contains no statement", NoTransactionDirective)
	}
	return out, nil
}

// truncateForError keeps a rejected statement readable in a boot log
// without pasting an entire file into it.
func truncateForError(stmt string) string {
	const max = 120
	stmt = strings.Join(strings.Fields(stmt), " ")
	if len(stmt) <= max {
		return stmt
	}
	return stmt[:max] + "…"
}

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

	if HasNoTransactionDirective(body) {
		return r.applyNoTransaction(ctx, logger, conn, version, body, started)
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

// applyNoTransaction is applyOne's arm for a file whose first line is
// NoTransactionDirective: no BeginTx, one ExecContext per statement (see
// that constant for why the split matters), then a separate statement to
// record the version.
//
// The version record is NOT rolled back with the statements if a later one
// fails, because there is nothing to roll back into — a partially applied
// no-transaction file is exactly the state NoTransactionDirective's doc
// comment tells the operator how to repair. Recording happens only after
// every statement succeeded, so a failed run leaves the version pending and
// the next boot retries it.
func (r *Runner) applyNoTransaction(ctx context.Context, logger *slog.Logger, conn *sql.Conn, version string, body []byte, started time.Time) error {
	stmts, err := NoTransactionStatements(body)
	if err != nil {
		return err
	}
	table, err := RequiredTable(body)
	if err != nil {
		return err
	}

	skipped := false
	if table != "" {
		var present bool
		if err := conn.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&present); err != nil {
			return fmt.Errorf("check required table %s: %w", table, err)
		}
		if !present {
			skipped = true
			logger.Warn("migration: required table absent, statements skipped",
				"version", version,
				"table", table)
		}
	}

	if !skipped {
		for _, stmt := range stmts {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("exec sql: %w", err)
			}
		}
	}

	if _, err := conn.ExecContext(ctx,
		`INSERT INTO public.schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record applied: %w", err)
	}

	logger.Info("migration: applied without transaction",
		"version", version,
		"statements", len(stmts),
		"skipped", skipped,
		"duration", time.Since(started).String())
	return nil
}
