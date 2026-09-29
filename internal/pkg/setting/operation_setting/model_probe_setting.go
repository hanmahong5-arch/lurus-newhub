package operation_setting

import "github.com/LurusTech/lurus-hub/internal/pkg/setting/config"

// ModelProbeSetting controls the active per-model health prober
// (internal/app/modelprobe). The channel-level test (channel-test.go) only
// ever exercises a channel's first/TestModel model, so a pooled channel that
// carries many models (an OpenRouter free pool carries ~20) has the rest
// tested by nothing. This setting turns on a separate pass that probes every
// (channel, model) pair on its own schedule.
type ModelProbeSetting struct {
	Enabled bool `json:"enabled"`
	// Groups is a CSV of channel groups eligible for probing (SelectDue
	// intersects it against each channel's own Group CSV).
	Groups string `json:"groups"`
	// IntervalHours is how long a (channel, model) pair may go unprobed
	// before it is due again.
	IntervalHours float64 `json:"interval_hours"`
	// DisableThreshold is the number of consecutive non-rate-limit failures
	// before a pair is auto-disabled.
	DisableThreshold int `json:"disable_threshold"`
	// MaxProbesPerRun caps how many pairs one RunOnce pass probes.
	MaxProbesPerRun int `json:"max_probes_per_run"`
	// SleepSeconds is the pause between two probes within one run.
	SleepSeconds float64 `json:"sleep_seconds"`
}

// 默认配置
var modelProbeSetting = ModelProbeSetting{
	Enabled:          false,
	Groups:           "free",
	IntervalHours:    24,
	DisableThreshold: 3,
	MaxProbesPerRun:  40,
	SleepSeconds:     3,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("model_probe_setting", &modelProbeSetting)
}

// GetModelProbeSetting returns a COPY of the registered setting with
// nonsense values clamped to their defaults, so a caller never has to
// re-derive "what does 0 mean here" itself. Read under the configuration
// read lock (config.RLock) so it is ordered against the option-sync tick's
// writes to the registered struct — see
// internal/pkg/setting/config/config.go's "No nesting" note: this function
// must not call back into anything that takes the lock itself, and does not.
func GetModelProbeSetting() ModelProbeSetting {
	config.RLock()
	s := modelProbeSetting
	config.RUnlock()

	if s.IntervalHours < 1 {
		s.IntervalHours = 24
	}
	if s.DisableThreshold < 1 {
		s.DisableThreshold = 3
	}
	if s.MaxProbesPerRun < 1 {
		s.MaxProbesPerRun = 40
	}
	if s.SleepSeconds < 0 {
		s.SleepSeconds = 3
	}
	return s
}
