package repo

// log_retention.go — age-based deletion for the two append-only tables that
// had no retention path at all before cycle-13 L6: `logs` and
// `download_logs`.
//
// Before this file the ONLY delete on `logs` was the operator-driven
// DELETE /api/log/ (handler/log.go -> DeleteOldLog), which is synchronous and
// unbounded in wall-clock time, and nothing at all deleted `download_logs`.
// The production manifest's own comment claimed the table was "bounded by
// ageing through the same DeleteOldLog" — it was not.
//
// The caller (internal/lifecycle/log_retention.go) owns the policy: which
// window applies to which rows, the floors under those windows, and whether
// retention is enabled at all. This file owns only the SQL shape.

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// logRetentionMaxBatches bounds how many batches one pass may run when the
// caller passes a non-positive maxBatches. A pass that never yields would
// hold a delete loop against the relay's own log writes for as long as the
// backlog takes; a bounded pass just resumes on the next tick.
const logRetentionMaxBatches = 200

// DeleteLogsBefore hard-deletes `logs` rows older than cutoff (a unix second
// matching logs.created_at) whose `type` is in types, in batches of batch
// rows, and returns how many rows it removed.
//
// types is REQUIRED and must be non-empty: the whole point of splitting
// retention by row type is that billing rows (consume/topup/refund) age out
// on a different, much longer window than diagnostics do, so a call that
// silently meant "every type" would be the bug this signature exists to
// prevent. An empty slice is an error, not "all".
//
// Batching uses the same `id IN (SELECT id ... ORDER BY id LIMIT ?)` shape as
// DeleteOldLog, for the same reason spelled out there: gorm's DeleteClauses
// render no LIMIT on a DELETE for either the postgres or glebarez/sqlite
// driver, so `.Limit(n).Delete(...)` is silently unbounded and one call
// removes every matching row in a single transaction.
//
// The pass stops after maxBatches batches even if more rows still match, so
// the remaining backlog drains over subsequent passes instead of in one long
// lock-holding loop. The returned count is what this pass deleted, not the
// size of the backlog — use CountLogsBefore for that.
func DeleteLogsBefore(ctx context.Context, cutoff int64, types []int, batch int, maxBatches int) (int64, error) {
	if len(types) == 0 {
		return 0, fmt.Errorf("repo: DeleteLogsBefore needs an explicit row type list")
	}
	if batch <= 0 {
		batch = 500
	}
	if maxBatches <= 0 {
		maxBatches = logRetentionMaxBatches
	}

	var total int64
	for i := 0; i < maxBatches; i++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := LOG_DB.Model(&Log{}).
			Select("id").
			Where("created_at < ?", cutoff).
			Where("type IN ?", types).
			Order("id").
			Limit(batch)
		result := LOG_DB.Where("id IN (?)", idQuery).Delete(&Log{})
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(batch) {
			break
		}
	}
	return total, nil
}

// CountLogsBefore reports how many `logs` rows of the given types are older
// than cutoff — the backlog a retention pass has still to work through.
// Feeds the pending-rows gauge, which is how an operator sees a retention
// task that is falling behind its own batch budget rather than one that has
// caught up.
func CountLogsBefore(ctx context.Context, cutoff int64, types []int) (int64, error) {
	if len(types) == 0 {
		return 0, fmt.Errorf("repo: CountLogsBefore needs an explicit row type list")
	}
	var n int64
	err := LOG_DB.WithContext(ctx).Model(&Log{}).
		Where("created_at < ?", cutoff).
		Where("type IN ?", types).
		Count(&n).Error
	return n, err
}

// DeleteDownloadLogsBefore hard-deletes `download_logs` rows downloaded
// before cutoff, in the same bounded batches as DeleteLogsBefore.
//
// download_logs carries per-download client data (ip_address, user_agent,
// referer) for anonymous callers — there is no user_id to erase it by, so
// age is the only lever a privacy request can pull on it. It lives on DB,
// not LOG_DB: releases and their download rows are application tables, and
// only `logs` is split out by LOG_SQL_DSN.
func DeleteDownloadLogsBefore(ctx context.Context, cutoff time.Time, batch int, maxBatches int) (int64, error) {
	if batch <= 0 {
		batch = 500
	}
	if maxBatches <= 0 {
		maxBatches = logRetentionMaxBatches
	}

	var total int64
	for i := 0; i < maxBatches; i++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := DB.Model(&entity.DownloadLog{}).
			Select("id").
			Where("downloaded_at < ?", cutoff).
			Order("id").
			Limit(batch)
		result := DB.Where("id IN (?)", idQuery).Delete(&entity.DownloadLog{})
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(batch) {
			break
		}
	}
	return total, nil
}

// CountDownloadLogsBefore reports the download_logs backlog older than
// cutoff. Counterpart of CountLogsBefore.
func CountDownloadLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := DB.WithContext(ctx).Model(&entity.DownloadLog{}).
		Where("downloaded_at < ?", cutoff).
		Count(&n).Error
	return n, err
}
