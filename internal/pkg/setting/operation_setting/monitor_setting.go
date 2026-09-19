package operation_setting

import (
	"os"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool    `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes float64 `json:"auto_test_channel_minutes"`
}

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled: false,
	AutoTestChannelMinutes: 10,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

// GetMonitorSetting returns the registered monitor configuration, with
// CHANNEL_TEST_FREQUENCY applied on top of it.
//
// It used to assign the registered struct's fields on EVERY call — from the
// automatic channel-test loop (handler/channel-test.go), which runs while the
// option-sync tick republishes the same struct — so a read path was writing
// hot-reloaded configuration with no lock at all. It now writes only when the
// environment asks for a value the struct does not already hold, and takes the
// configuration write lock to do it; in a deployment that sets the variable
// that is one write during the first call and none afterwards, and in a
// deployment that does not set it, none ever.
//
// Behaviour is unchanged and still pinned by fix_monitor_setting_test.go
// (0/negative disable, positive enables and sets the period, unparsable and
// unset leave the in-memory state alone) and by the three
// TestGetMonitorSetting_* cases in cov_boot-settings_operation_setting_test.go:
// the environment variable still wins over a console edit, which is the
// existing contract, not something this change introduces.
func GetMonitorSetting() *MonitorSetting {
	if enabled, minutes, setMinutes, ok := channelTestFrequencyOverride(); ok {
		applyMonitorOverride(enabled, minutes, setMinutes)
	}
	return &monitorSetting
}

// channelTestFrequencyOverride parses CHANNEL_TEST_FREQUENCY. ok is false when
// the variable is unset, empty or not a number, which means "do not touch the
// in-memory state". A value <= 0 disables the automatic test and deliberately
// leaves the period alone (setMinutes false) so no 0-minute ticker is built.
func channelTestFrequencyOverride() (enabled bool, minutes float64, setMinutes, ok bool) {
	raw := os.Getenv("CHANNEL_TEST_FREQUENCY")
	if raw == "" {
		return false, 0, false, false
	}
	frequency, err := strconv.Atoi(raw)
	if err != nil {
		return false, 0, false, false
	}
	if frequency > 0 {
		return true, float64(frequency), true, true
	}
	return false, 0, false, true
}

// applyMonitorOverride publishes the environment's answer only if it differs
// from what is already published. The read is under the configuration read
// lock and the write under the write lock, taken after the read lock is
// released — config.RLock is not reentrant.
func applyMonitorOverride(enabled bool, minutes float64, setMinutes bool) {
	config.RLock()
	current := monitorSetting
	config.RUnlock()

	if current.AutoTestChannelEnabled == enabled &&
		(!setMinutes || current.AutoTestChannelMinutes == minutes) {
		return
	}

	config.Lock()
	defer config.Unlock()

	monitorSetting.AutoTestChannelEnabled = enabled
	if setMinutes {
		monitorSetting.AutoTestChannelMinutes = minutes
	}
}
