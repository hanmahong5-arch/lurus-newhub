package entity

// ModelHealth is the result of actively probing ONE model on ONE channel
// (migration 044). The channel-level test (channels.test_time /
// response_time) only ever exercises a channel's first or TestModel model, so
// the other models of a multi-model channel — an OpenRouter free pool carries
// ~20 — were never tested at all. One row per (channel_id, model).
//
// AutoDisabled is the routing lever: while it is true the channel is not a
// candidate for that model (repo.LoadAutoDisabledModelPairs is consulted when
// the in-memory routing cache is rebuilt, and the ability rows are kept in
// step for the DB-fallback path). It is set and cleared only by the model
// prober (internal/app/modelprobe), never by an admin form, so a model that
// recovers comes back on its own.
type ModelHealth struct {
	Id                  int    `json:"id" gorm:"primaryKey"`
	ChannelId           int    `json:"channel_id" gorm:"type:bigint;not null;uniqueIndex:idx_model_health_channel_model"`
	Model               string `json:"model" gorm:"type:varchar(255);not null;uniqueIndex:idx_model_health_channel_model"`
	LastProbeAt         int64  `json:"last_probe_at" gorm:"type:bigint;not null;default:0"`
	Ok                  bool   `json:"ok" gorm:"not null;default:false"`
	LatencyMs           int    `json:"latency_ms" gorm:"type:bigint;not null;default:0"`
	LastError           string `json:"last_error" gorm:"type:text"`
	ConsecutiveFailures int    `json:"consecutive_failures" gorm:"type:bigint;not null;default:0"`
	AutoDisabled        bool   `json:"auto_disabled" gorm:"not null;default:false"`
	AutoDisabledAt      int64  `json:"auto_disabled_at" gorm:"type:bigint;not null;default:0"`
	UpdatedAt           int64  `json:"updated_at" gorm:"type:bigint;not null;default:0"`
}

// TableName pins the table name; GORM would otherwise pluralise it to
// "model_healths".
func (ModelHealth) TableName() string { return "model_health" }
