package repo

import (
	"math"
	"sort"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"gorm.io/gorm"
)

// modelPerformanceMaxModels caps the number of models returned by
// GetModelPerformance. The model catalogue is naturally small (tens, not
// thousands), so 200 is a generous safety bound against pathological data.
const modelPerformanceMaxModels = 200

// ModelPerformanceStat is the per-model aggregate returned by
// GetModelPerformance. Latency fields are computed only over consume rows
// with total_latency_ms > 0 (rows recorded before the governance latency
// column existed carry 0 and would skew the distribution).
type ModelPerformanceStat struct {
	ModelName        string  `json:"model_name"`
	Requests         int64   `json:"requests"`
	Errors           int64   `json:"errors"`
	ErrorRate        float64 `json:"error_rate"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Quota            int64   `json:"quota"`
	LatencySamples   int64   `json:"latency_samples"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	P50LatencyMs     int     `json:"p50_latency_ms"`
	P95LatencyMs     int     `json:"p95_latency_ms"`
}

// GetModelPerformance aggregates the logs table per model over
// [startTime, endTime] (unix seconds, inclusive). Requests count consume +
// error rows; errors count only error rows; token/quota sums span both (error
// rows carry 0s, so they do not distort the sums).
//
// Percentiles use the nearest-rank definition (value at index ceil(q*n) of
// the ascending latency list) computed via ORDER BY + OFFSET, deliberately
// avoiding percentile_cont so the exact same SQL runs on PostgreSQL and on
// the hermetic SQLite test tier — the unit tests cover the production query
// shape, not a dialect fork.
func GetModelPerformance(startTime, endTime int64, tenantID, modelName string) ([]ModelPerformanceStat, error) {
	base := LOG_DB.Model(&entity.Log{}).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if tenantID != "" {
		base = base.Where("tenant_id = ?", tenantID)
	}
	if modelName != "" {
		base = base.Where("model_name = ?", modelName)
	}

	var results []ModelPerformanceStat
	err := base.
		Select(`model_name,
			COUNT(*) AS requests,
			SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS errors,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota,
			SUM(CASE WHEN type = ? AND total_latency_ms > 0 THEN 1 ELSE 0 END) AS latency_samples,
			COALESCE(AVG(CASE WHEN type = ? AND total_latency_ms > 0 THEN total_latency_ms END), 0) AS avg_latency_ms`,
			LogTypeError, LogTypeConsume, LogTypeConsume).
		Group("model_name").
		Order("requests DESC").
		Limit(modelPerformanceMaxModels).
		Find(&results).Error
	if err != nil {
		return nil, err
	}

	for i := range results {
		r := &results[i]
		r.TotalTokens = r.PromptTokens + r.CompletionTokens
		if r.Requests > 0 {
			r.ErrorRate = float64(r.Errors) / float64(r.Requests)
		}
		if r.LatencySamples > 0 {
			p50, err := latencyPercentileNearestRank(startTime, endTime, tenantID, r.ModelName, r.LatencySamples, 0.50)
			if err != nil {
				return nil, err
			}
			p95, err := latencyPercentileNearestRank(startTime, endTime, tenantID, r.ModelName, r.LatencySamples, 0.95)
			if err != nil {
				return nil, err
			}
			r.P50LatencyMs = p50
			r.P95LatencyMs = p95
		}
	}
	return results, nil
}

// latencyPercentileNearestRank returns the q-th percentile (nearest-rank) of
// total_latency_ms for one model's consume rows in the window. samples must
// be the count of rows matching the same predicate (latency > 0) so the
// OFFSET lands inside the result set.
//
// GetModelPerformance is its only caller today — a channel_type-keyed
// vendor rollup with the same percentile shape was drafted for this lane
// (cycle-7 plan §3/L4 spec item 1) and then dropped in favour of the
// latency-free GetRankings path (§8's "no latency subqueries" correction),
// so the helper stays model_name-only rather than carrying an unused
// group-column generalisation.
func latencyPercentileNearestRank(startTime, endTime int64, tenantID, modelName string, samples int64, q float64) (int, error) {
	rank := int64(math.Ceil(q * float64(samples)))
	if rank < 1 {
		rank = 1
	}
	tx := LOG_DB.Model(&entity.Log{}).
		Where("model_name = ? AND total_latency_ms > 0", modelName).
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if tenantID != "" {
		tx = tx.Where("tenant_id = ?", tenantID)
	}
	var vals []int
	err := tx.Order("total_latency_ms ASC").
		Offset(int(rank-1)).
		Limit(1).
		Pluck("total_latency_ms", &vals).Error
	if err != nil {
		return 0, err
	}
	if len(vals) == 0 {
		return 0, nil
	}
	return vals[0], nil
}

// RankingUsageTotal is the latency-free per-group aggregate shared by
// GetModelUsageTotals and getVendorUsageTotals. GetRankings never surfaces
// latency percentiles (cycle-7 plan §8/L4 "no latency subqueries"), so this
// carries only the fields a leaderboard rank/share/growth computation needs.
type RankingUsageTotal struct {
	Name             string `json:"name"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Quota            int64  `json:"quota"`
}

// GetModelUsageTotals is GetModelPerformance's latency-free sibling: one
// GROUP BY model_name, no per-group percentile subquery. GetRankings runs
// this twice (current window + immediately preceding window) so the
// two-window comparison stays at two indexed aggregate queries against
// logs, not hundreds of unindexed OFFSET scans.
func GetModelUsageTotals(startTime, endTime int64, tenantID string) ([]RankingUsageTotal, error) {
	base := LOG_DB.Model(&entity.Log{}).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if tenantID != "" {
		base = base.Where("tenant_id = ?", tenantID)
	}
	var rows []RankingUsageTotal
	err := base.
		Select(`model_name AS name,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Group("model_name").
		Find(&rows).Error
	return rows, err
}

// vendorUsageRow is the raw channel_type-keyed scan target for
// getVendorUsageTotals — channel_type is an int column, so it cannot be
// aliased straight into RankingUsageTotal.Name (string); the vendor name is
// resolved via constant.GetChannelTypeName after the query returns.
type vendorUsageRow struct {
	ChannelType      int
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}

// getVendorUsageTotals is GetModelUsageTotals' channel_type-keyed sibling.
// Unexported: GetRankings is its only caller today (GetModelUsageTotals is
// the one named in the plan as a public seam).
func getVendorUsageTotals(startTime, endTime int64, tenantID string) ([]RankingUsageTotal, error) {
	base := LOG_DB.Model(&entity.Log{}).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Where("channel_type > 0")
	if tenantID != "" {
		base = base.Where("tenant_id = ?", tenantID)
	}
	var raw []vendorUsageRow
	err := base.
		Select(`channel_type,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Group("channel_type").
		Find(&raw).Error
	if err != nil {
		return nil, err
	}
	rows := make([]RankingUsageTotal, len(raw))
	for i, r := range raw {
		rows[i] = RankingUsageTotal{
			Name:             constant.GetChannelTypeName(r.ChannelType),
			Requests:         r.Requests,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			Quota:            r.Quota,
		}
	}
	return rows, nil
}

// rankingsGroupExpr is the by="group" aggregation key: `group` is a SQL
// reserved word, so the identifier is double-quoted (Postgres rejects an
// unquoted `GROUP BY group`; SQLite's more permissive parser accepts it
// either way, which is why this must be verified against the real Postgres
// dialector — see log_rankings_test.go's
// TestRankings_ByGroup_QuotesTheReservedIdentifier, not the hermetic
// SQLite tier). Rows with an empty group fold into one explicit
// "(ungrouped)" bucket rather than appearing as a blank-named row.
const rankingsGroupExpr = `COALESCE(NULLIF("group", ''), '(ungrouped)')`

// getGroupUsageTotals is GetModelUsageTotals' group-column-keyed sibling.
// Unexported: GetRankings is its only caller today, mirroring
// getVendorUsageTotals.
func getGroupUsageTotals(startTime, endTime int64, tenantID string) ([]RankingUsageTotal, error) {
	base := LOG_DB.Model(&entity.Log{}).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if tenantID != "" {
		base = base.Where("tenant_id = ?", tenantID)
	}
	var rows []RankingUsageTotal
	err := base.
		Select(rankingsGroupExpr + ` AS name,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Group(rankingsGroupExpr).
		Find(&rows).Error
	return rows, err
}

// RankingRow is one leaderboard entry returned by GetRankings: the current
// window's usage plus its rank/trend/share against the immediately
// preceding window of equal length.
type RankingRow struct {
	Name              string   `json:"name"`
	Rank              int      `json:"rank"`
	RankDelta         int      `json:"rank_delta"`
	IsNew             bool     `json:"is_new"`
	Requests          int64    `json:"requests"`
	TotalTokens       int64    `json:"total_tokens"`
	Quota             int64    `json:"quota"`
	TokenSharePct     float64  `json:"token_share_pct"`
	QuotaSharePct     float64  `json:"quota_share_pct"`
	RequestsGrowthPct *float64 `json:"requests_growth_pct"`
}

// rankingsMaxRows caps GetRankings' limit parameter — the buyer leaderboard
// tab compares top vendors/models, not a full catalogue dump.
const rankingsMaxRows = 20

// GetRankings runs the model or vendor usage-totals aggregate (latency-free
// — see GetModelUsageTotals) over [startTime, endTime] and the immediately
// preceding window of equal length, then returns the top `limit` groups by
// current-window tokens with rank/rank_delta/share/growth attached.
//
// by == "vendor" groups by channel_type; by == "group" groups by the logs
// table's `group` column (see rankingsGroupExpr); anything else groups by
// model_name. rank_delta is (previous rank - current rank): positive means
// the group moved up the leaderboard. A group absent from the previous
// window gets rank_delta=0, is_new=true, and a nil requests_growth_pct (no
// baseline to divide by). token_share_pct/quota_share_pct are shares of the
// CURRENT window's full total across every group in that window — true
// market share, not a share of just the returned top `limit` rows. The two
// returned totals (totalTokens, totalQuota) are that same full-window sum,
// for a caller that wants to show it — the returned rows are capped at
// `limit` and must not be re-summed to recover it.
func GetRankings(startTime, endTime int64, tenantID, by string, limit int) (rows []RankingRow, totalTokens, totalQuota int64, err error) {
	if limit <= 0 || limit > rankingsMaxRows {
		limit = rankingsMaxRows
	}
	length := endTime - startTime
	prevEnd := startTime - 1
	prevStart := prevEnd - length

	fetch := GetModelUsageTotals
	switch by {
	case "vendor":
		fetch = getVendorUsageTotals
	case "group":
		fetch = getGroupUsageTotals
	}

	current, err := fetch(startTime, endTime, tenantID)
	if err != nil {
		return nil, 0, 0, err
	}
	previous, err := fetch(prevStart, prevEnd, tenantID)
	if err != nil {
		return nil, 0, 0, err
	}

	sortRankingTotalsByTokens(previous)
	prevRank := make(map[string]int, len(previous))
	prevRequests := make(map[string]int64, len(previous))
	for i, row := range previous {
		prevRank[row.Name] = i + 1
		prevRequests[row.Name] = row.Requests
	}

	sortRankingTotalsByTokens(current)

	for _, row := range current {
		totalTokens += row.PromptTokens + row.CompletionTokens
		totalQuota += row.Quota
	}

	n := len(current)
	if n > limit {
		n = limit
	}
	out := make([]RankingRow, n)
	for i := 0; i < n; i++ {
		row := current[i]
		tokens := row.PromptTokens + row.CompletionTokens
		r := RankingRow{
			Name:        row.Name,
			Rank:        i + 1,
			Requests:    row.Requests,
			TotalTokens: tokens,
			Quota:       row.Quota,
		}
		if totalTokens > 0 {
			r.TokenSharePct = float64(tokens) / float64(totalTokens) * 100
		}
		if totalQuota > 0 {
			r.QuotaSharePct = float64(row.Quota) / float64(totalQuota) * 100
		}
		if pr, ok := prevRank[row.Name]; ok {
			r.RankDelta = pr - r.Rank
			if pReq := prevRequests[row.Name]; pReq > 0 {
				growth := (float64(row.Requests) - float64(pReq)) / float64(pReq) * 100
				r.RequestsGrowthPct = &growth
			}
		} else {
			r.IsNew = true
		}
		out[i] = r
	}
	return out, totalTokens, totalQuota, nil
}

// sortRankingTotalsByTokens orders by total tokens desc (GetRankings' rank
// basis), breaking ties on name for a deterministic order across otherwise
// tied groups (both in tests and in a live tie).
func sortRankingTotalsByTokens(rows []RankingUsageTotal) {
	sort.Slice(rows, func(i, j int) bool {
		ti := rows[i].PromptTokens + rows[i].CompletionTokens
		tj := rows[j].PromptTokens + rows[j].CompletionTokens
		if ti != tj {
			return ti > tj
		}
		return rows[i].Name < rows[j].Name
	})
}

// CountAdminExportLogs returns how many log rows match the admin export
// filters. The export handler runs this before streaming so it can stamp the
// X-Truncated header ahead of the response body (headers are committed on
// the first CSV write).
func CountAdminExportLogs(tenantID string, logType int, modelName string, startTime, endTime int64, upstreamRequestID string) (int64, error) {
	tx := adminExportFilter(tenantID, logType, modelName, startTime, endTime, upstreamRequestID)
	var total int64
	err := tx.Count(&total).Error
	return total, err
}

// ExportAdminLogsBatch fetches one id-ascending page of log rows for the
// admin CSV export. afterID is an exclusive cursor (pass the last row's id
// of the previous page; 0 starts from the beginning) — id-cursor pagination
// stays stable while new logs are appended, unlike OFFSET.
func ExportAdminLogsBatch(afterID int, tenantID string, logType int, modelName string, startTime, endTime int64, upstreamRequestID string, limit int) ([]*entity.Log, error) {
	tx := adminExportFilter(tenantID, logType, modelName, startTime, endTime, upstreamRequestID)
	var logs []*entity.Log
	err := tx.Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func adminExportFilter(tenantID string, logType int, modelName string, startTime, endTime int64, upstreamRequestID string) *gorm.DB {
	tx := LOG_DB.Model(&entity.Log{})
	if tenantID != "" {
		tx = tx.Where("tenant_id = ?", tenantID)
	}
	if logType > 0 {
		tx = tx.Where("type = ?", logType)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if startTime > 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}
	// The vendor's own request/trace id (TierInternal) — this export route is
	// root-only, so it may bind it; the equivalent self-service log list never
	// gains this filter.
	if upstreamRequestID != "" {
		tx = tx.Where(jsonOtherTextExpr("upstream_request_id")+" = ?", upstreamRequestID)
	}
	return tx
}
