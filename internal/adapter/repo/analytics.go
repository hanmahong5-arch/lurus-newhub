package repo

import (
	"math"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

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
func latencyPercentileNearestRank(startTime, endTime int64, tenantID, modelName string, samples int64, q float64) (int, error) {
	rank := int64(math.Ceil(q * float64(samples)))
	if rank < 1 {
		rank = 1
	}
	tx := LOG_DB.Model(&entity.Log{}).
		Where("type = ? AND model_name = ? AND total_latency_ms > 0", LogTypeConsume, modelName).
		Where("created_at >= ? AND created_at <= ?", startTime, endTime)
	if tenantID != "" {
		tx = tx.Where("tenant_id = ?", tenantID)
	}
	var vals []int
	err := tx.Order("total_latency_ms ASC").
		Offset(int(rank - 1)).
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

// CountAdminExportLogs returns how many log rows match the admin export
// filters. The export handler runs this before streaming so it can stamp the
// X-Truncated header ahead of the response body (headers are committed on
// the first CSV write).
func CountAdminExportLogs(tenantID string, logType int, modelName string, startTime, endTime int64) (int64, error) {
	tx := adminExportFilter(tenantID, logType, modelName, startTime, endTime)
	var total int64
	err := tx.Count(&total).Error
	return total, err
}

// ExportAdminLogsBatch fetches one id-ascending page of log rows for the
// admin CSV export. afterID is an exclusive cursor (pass the last row's id
// of the previous page; 0 starts from the beginning) — id-cursor pagination
// stays stable while new logs are appended, unlike OFFSET.
func ExportAdminLogsBatch(afterID int, tenantID string, logType int, modelName string, startTime, endTime int64, limit int) ([]*entity.Log, error) {
	tx := adminExportFilter(tenantID, logType, modelName, startTime, endTime)
	var logs []*entity.Log
	err := tx.Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

func adminExportFilter(tenantID string, logType int, modelName string, startTime, endTime int64) *gorm.DB {
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
	return tx
}
