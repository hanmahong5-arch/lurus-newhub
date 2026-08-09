package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
)

// PendingVersions reports which discovered migrations have DDL still waiting to
// run, plus how many versions the bookkeeping table already records.
//
// It is deliberately read-only and lock-free: no advisory lock, no CREATE TABLE,
// no writes of any kind. That is what makes it usable in exactly the situations
// the Runner does not cover — MIGRATIONS_AUTO_RUN=false, a replica set with no
// master-capable node, or an operator asking "did the rollout actually migrate?"
// from a pod that never ran the Runner. Taking the advisory lock here would make
// the observability path contend with the very boot it is meant to observe.
//
// Versions at or below baselineThrough are bookkeeping-only (see the package
// comment) and never execute, so they are not reported as pending even when they
// are absent from schema_migrations — there is no DDL behind them to wait for.
//
// A missing public.schema_migrations table is not an error: it means the Runner
// has never run against this database, so every above-baseline version is
// pending and the applied count is zero.
func PendingVersions(ctx context.Context, db *sql.DB, fsys fs.FS, baselineThrough string) (pending []string, applied int, err error) {
	if db == nil {
		return nil, 0, errors.New("migration: PendingVersions: db is nil")
	}
	if fsys == nil {
		return nil, 0, errors.New("migration: PendingVersions: fs is nil")
	}

	versions, err := DiscoverVersions(fsys)
	if err != nil {
		return nil, 0, err
	}

	var trackerExists sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT to_regclass('public.schema_migrations')::text`).Scan(&trackerExists); err != nil {
		return nil, 0, fmt.Errorf("probe schema_migrations: %w", err)
	}

	recorded := map[string]bool{}
	if trackerExists.Valid {
		rows, err := db.QueryContext(ctx, `SELECT version FROM public.schema_migrations`)
		if err != nil {
			return nil, 0, fmt.Errorf("query schema_migrations: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return nil, 0, fmt.Errorf("scan schema_migrations.version: %w", err)
			}
			recorded[v] = true
		}
		if err := rows.Err(); err != nil {
			return nil, 0, fmt.Errorf("iter schema_migrations: %w", err)
		}
	}

	for _, v := range versions {
		if baselineThrough != "" && v <= baselineThrough {
			continue
		}
		if !recorded[v] {
			pending = append(pending, v)
		}
	}
	return pending, len(recorded), nil
}
