package repo

import (
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// log_billable.go — which consume rows a bill is allowed to count, and the
// per-period aggregates built on that predicate. Split out of log.go by the
// cycle-13 wiring pass as a pure move (every function here is byte-identical
// to the one that stood in log.go); internal/pkg/gates' source-size ratchet
// holds log.go at its measured line count, so this cycle's additions to that
// file are paid for by a move rather than by raising the ceiling.
//
// Keeping them together is not only about size: the two marker substrings
// BillableConsumePredicate excludes are owned HERE and nowhere else, so a
// caller that wants "what the customer actually pays for" has one function
// to reach for instead of a hand-rolled WHERE clause that drifts.

// BillableConsumePredicate scopes a query to log rows that were both a real
// charge attempt (type=consume) and actually settled: the Other column
// carries neither the settlement-failed nor the manual-probe marker. Use as
// db.Scopes(BillableConsumePredicate) — it already applies the type filter,
// so callers must not also add a "type = ?" clause. A row with no Other
// payload (empty string or NULL, the common case) or one that mentions
// neither marker passes.
func BillableConsumePredicate(db *gorm.DB) *gorm.DB {
	return db.Where("type = ?", LogTypeConsume).
		Where("(other IS NULL OR other = '' OR (other NOT LIKE ? AND other NOT LIKE ?))",
			"%"+billableConsumeExcludeSettlementFailed+"%",
			"%"+billableConsumeExcludeChannelProbe+"%")
}

// GetUserLogStatByPeriod returns consume-usage stats filtered by time period
// and grouped by model. created_at is a unix-epoch bigint (see
// GetUserLogStatInternal below); the caller passes a time.Time, so this
// function is responsible for the .Unix() conversion — binding time.Time
// directly into the Where clause makes PostgreSQL reject the query with
// 22P02 (invalid_text_representation). Rows are scoped through
// BillableConsumePredicate rather than a bare type=consume filter (cycle-13
// L2): topup/manage rows are written with an EMPTY model_name (RecordLog /
// RecordLogWithTenant, log.go:199-220/223-, build Log{} without setting
// Quota at all, so those rows are Quota==0, not "huge" — their actual
// problem is that grouping by model_name would add a spurious empty-key
// group to the result), LogTypeError rows carry a real ModelName but were
// never billed, so including them would inflate that model's count with
// requests that cost nothing, and a settlement-failed or manual-probe
// consume row would otherwise inflate a customer's self-service usage total
// with quota nobody actually paid.
func GetUserLogStatByPeriod(userID int, since time.Time) ([]LogStatEntry, error) {
	var results []LogStatEntry
	err := LOG_DB.Model(&Log{}).
		Select("model_name as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").
		Where("user_id = ? AND created_at >= ?", userID, since.Unix()).
		Scopes(BillableConsumePredicate).
		Group("model_name").
		Order("total_quota DESC").
		Find(&results).Error
	return results, err
}

// GetUserLogStatInternal returns aggregated usage stats (by model or day).
func GetUserLogStatInternal(userID int, groupBy string) ([]LogStatEntry, error) {
	var results []LogStatEntry
	var selectExpr, groupExpr string
	switch groupBy {
	case "day":
		// created_at is a unix-epoch bigint: PG has no DATE(bigint), so it
		// needs the TO_TIMESTAMP conversion. The SQLite arm exists only for
		// the hermetic unit-test tier (same convention as v2_log_cluster.go).
		var dayExpr string
		if common.UsingPostgreSQL {
			dayExpr = "TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM-DD')"
		} else {
			dayExpr = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch'))"
		}
		selectExpr = dayExpr + " as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota"
		groupExpr = dayExpr
	default:
		selectExpr = "model_name as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota"
		groupExpr = "model_name"
	}
	err := LOG_DB.Model(&Log{}).
		Select(selectExpr).
		Where("user_id = ?", userID).
		Group(groupExpr).
		Order("total_quota DESC").
		Find(&results).Error
	return results, err
}
