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

func GetMonitorSetting() *MonitorSetting {
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err == nil {
			// 能解析出来的值一律生效：<= 0 表示显式关闭自动测试，
			// 否则会保留之前的状态导致无法关掉。
			monitorSetting.AutoTestChannelEnabled = frequency > 0
			if frequency > 0 {
				monitorSetting.AutoTestChannelMinutes = float64(frequency)
			}
		}
	}
	return &monitorSetting
}
