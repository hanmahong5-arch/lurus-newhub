package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// model_health.go — storage for per-(channel, model) probe results
// (migration 044, entity.ModelHealth). Deliberately dumb: the decision of
// when a model becomes auto-disabled or recovers lives in
// internal/app/modelprobe; this file only reads and writes rows.

// GetModelHealth returns the row for one (channel, model), or (nil, nil) when
// the pair has never been probed.
func GetModelHealth(channelID int, model string) (*entity.ModelHealth, error) {
	var row entity.ModelHealth
	err := DB.Where("channel_id = ? AND model = ?", channelID, model).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SaveModelHealth inserts or fully updates the row keyed by
// (channel_id, model). The caller owns every field's value.
func SaveModelHealth(row *entity.ModelHealth) error {
	if row == nil || row.ChannelId <= 0 || row.Model == "" {
		return errors.New("model health row needs channel_id and model")
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "channel_id"}, {Name: "model"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"last_probe_at", "ok", "latency_ms", "last_error",
			"consecutive_failures", "auto_disabled", "auto_disabled_at", "updated_at",
		}),
	}).Create(row).Error
}

// ListModelHealth returns every row for the given channels (all rows when
// channelIDs is empty), ordered by channel then model.
func ListModelHealth(channelIDs []int) ([]entity.ModelHealth, error) {
	var rows []entity.ModelHealth
	q := DB.Order("channel_id, model")
	if len(channelIDs) > 0 {
		q = q.Where("channel_id IN ?", channelIDs)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// LoadAutoDisabledModelPairs returns channel_id -> set of models the prober
// has taken out of routing. An empty map (never nil) when there are none.
func LoadAutoDisabledModelPairs() (map[int]map[string]bool, error) {
	var rows []entity.ModelHealth
	if err := DB.Select("channel_id", "model").Where("auto_disabled = ?", true).Find(&rows).Error; err != nil {
		return map[int]map[string]bool{}, err
	}
	out := make(map[int]map[string]bool, len(rows))
	for _, r := range rows {
		if out[r.ChannelId] == nil {
			out[r.ChannelId] = make(map[string]bool)
		}
		out[r.ChannelId][r.Model] = true
	}
	return out, nil
}

// DeleteModelHealthForChannel removes a deleted channel's rows so they do not
// linger in the pool overview.
func DeleteModelHealthForChannel(channelID int) error {
	return DB.Where("channel_id = ?", channelID).Delete(&entity.ModelHealth{}).Error
}
