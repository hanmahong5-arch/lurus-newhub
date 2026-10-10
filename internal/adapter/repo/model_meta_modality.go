package repo

import (
	"sync/atomic"

	"gorm.io/gorm"
)

// modalityOverrides is the administrator-set model modality (models.modality,
// migration 053), keyed by model name. It is kept in memory and refreshed with
// the channel cache because the route filter reads it once per candidate per
// request, which must never be a database round-trip.
var modalityOverrides atomic.Pointer[map[string]string]

// ModalityOverride returns the administrator override for a model, or "" when
// the model has none (or the cache has not loaded yet).
func ModalityOverride(model string) string {
	m := modalityOverrides.Load()
	if m == nil {
		return ""
	}
	return (*m)[model]
}

// SetModalityOverridesForTest replaces the in-memory override table.
func SetModalityOverridesForTest(m map[string]string) {
	modalityOverrides.Store(&m)
}

// loadModalityOverrides reads every non-empty override. It fails open: on any
// error (for example a database whose models table predates migration 053) it
// returns an empty map, because losing an override only falls back to
// inference and must never block a channel write or a cache rebuild.
func loadModalityOverrides(db *gorm.DB) map[string]string {
	out := map[string]string{}
	if db == nil {
		return out
	}
	var rows []struct {
		ModelName string
		Modality  string
	}
	if err := db.Table("models").
		Select("model_name, modality").
		Where("modality <> '' AND deleted_at IS NULL").
		Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.ModelName] = r.Modality
	}
	return out
}
