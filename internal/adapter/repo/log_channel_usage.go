package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
)

// ChannelUsageRow is one aggregate bucket of logs for the account usage report.
// KeyIdx is only meaningful for the per-key variant.
type ChannelUsageRow struct {
	ChannelId        int
	KeyIdx           int64
	Requests         int64
	Errors           int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
	// CostCNY4 is the record-time price (logs.priced_cny4) plus, for rows
	// written before migration 042 (priced = 0), their quota at the current
	// rate: the same rule the tenant statement uses.
	CostCNY4 int64
}

// AggregateChannelUsage sums consume and error log rows of the given channels
// since the unix timestamp, per channel (perKey=false) or per channel x key
// index (perKey=true). Requests counts every attempt (consume + error rows);
// Errors only the error rows.
//
// Access path: `channel_id IN (...) AND created_at >= ?` uses the existing
// single-column logs.channel_id index, then filters created_at on the heap.
func AggregateChannelUsage(channelIDs []int, since int64, perKey bool) ([]ChannelUsageRow, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	var raw []struct {
		ChannelId        int   `gorm:"column:channel_id"`
		KeyIdx           int64 `gorm:"column:key_idx"`
		Requests         int64 `gorm:"column:requests"`
		Errors           int64 `gorm:"column:errors"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
		Quota            int64 `gorm:"column:quota"`
		Priced           int64 `gorm:"column:priced"`
		UnpricedQuota    int64 `gorm:"column:unpriced_quota"`
	}
	sel := "channel_id, " +
		"COUNT(*) AS requests, " +
		"COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS errors, " +
		"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
		"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
		"COALESCE(SUM(quota), 0) AS quota, " +
		"COALESCE(SUM(priced_cny4), 0) AS priced, " +
		"COALESCE(SUM(CASE WHEN priced_cny4 = 0 THEN quota ELSE 0 END), 0) AS unpriced_quota"
	group := "channel_id"
	if perKey {
		sel = "channel_key_idx AS key_idx, " + sel
		group = "channel_id, channel_key_idx"
	}
	if err := LOG_DB.Model(&Log{}).
		Where("channel_id IN ? AND created_at >= ? AND type IN ?",
			channelIDs, since, []int{LogTypeConsume, LogTypeError}).
		Select(sel, LogTypeError).
		Group(group).
		Scan(&raw).Error; err != nil {
		return nil, err
	}
	out := make([]ChannelUsageRow, 0, len(raw))
	for _, r := range raw {
		key := NoChannelKeyIdx
		if perKey {
			key = r.KeyIdx
		}
		out = append(out, ChannelUsageRow{
			ChannelId:        r.ChannelId,
			KeyIdx:           key,
			Requests:         r.Requests,
			Errors:           r.Errors,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			Quota:            r.Quota,
			CostCNY4:         r.Priced + currency.CNYToUnits4(currency.QuotaToCNY(int(r.UnpricedQuota))),
		})
	}
	return out, nil
}
