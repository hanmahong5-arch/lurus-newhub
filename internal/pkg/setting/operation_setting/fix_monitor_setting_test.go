package operation_setting

// fix_monitor_setting_test.go — 回归测试：CHANNEL_TEST_FREQUENCY 只在解析结果
// > 0 时才生效，"0" 被静默忽略，于是已经开启过自动测渠道的部署无法用这个环境变量
// 关掉；而且该环境变量每次读取都会覆盖内存态，控制台里关掉也会被立刻改回来。

import (
	"testing"
)

// fixMonitorSettingSnapshot 保存并恢复包级配置。
func fixMonitorSettingSnapshot(t *testing.T) {
	t.Helper()
	prev := monitorSetting
	t.Cleanup(func() { monitorSetting = prev })
}

// TestFixGetMonitorSetting_ZeroDisables: 已开启的情况下设 "0" 必须关闭自动测试。
// 修复前 frequency > 0 的前置条件不成立，整段赋值被跳过，AutoTestChannelEnabled
// 仍然是 true。
func TestFixGetMonitorSetting_ZeroDisables(t *testing.T) {
	fixMonitorSettingSnapshot(t)

	monitorSetting.AutoTestChannelEnabled = true
	monitorSetting.AutoTestChannelMinutes = 30

	t.Setenv("CHANNEL_TEST_FREQUENCY", "0")
	got := GetMonitorSetting()
	if got.AutoTestChannelEnabled {
		t.Error("CHANNEL_TEST_FREQUENCY=0 must disable the automatic channel test")
	}
	// 关闭时不改动周期，避免出现 0 分钟的定时器
	if got.AutoTestChannelMinutes != 30 {
		t.Errorf("AutoTestChannelMinutes = %v, want the previous 30 to be kept", got.AutoTestChannelMinutes)
	}
}

// TestFixGetMonitorSetting_NegativeDisables: 负数同样表示关闭。
func TestFixGetMonitorSetting_NegativeDisables(t *testing.T) {
	fixMonitorSettingSnapshot(t)

	monitorSetting.AutoTestChannelEnabled = true
	t.Setenv("CHANNEL_TEST_FREQUENCY", "-5")
	if GetMonitorSetting().AutoTestChannelEnabled {
		t.Error("a negative CHANNEL_TEST_FREQUENCY must disable the automatic channel test")
	}
}

// TestFixGetMonitorSetting_PositiveStillEnables: 正常取值的行为不变。
func TestFixGetMonitorSetting_PositiveStillEnables(t *testing.T) {
	fixMonitorSettingSnapshot(t)

	monitorSetting.AutoTestChannelEnabled = false
	monitorSetting.AutoTestChannelMinutes = 10

	t.Setenv("CHANNEL_TEST_FREQUENCY", "45")
	got := GetMonitorSetting()
	if !got.AutoTestChannelEnabled || got.AutoTestChannelMinutes != 45 {
		t.Errorf("enabled=%v minutes=%v, want true/45", got.AutoTestChannelEnabled, got.AutoTestChannelMinutes)
	}
}

// TestFixGetMonitorSetting_UnparsableIsIgnored: 非数字保持原样（不是 0，不改状态）。
func TestFixGetMonitorSetting_UnparsableIsIgnored(t *testing.T) {
	fixMonitorSettingSnapshot(t)

	monitorSetting.AutoTestChannelEnabled = true
	monitorSetting.AutoTestChannelMinutes = 20

	t.Setenv("CHANNEL_TEST_FREQUENCY", "not-a-number")
	got := GetMonitorSetting()
	if !got.AutoTestChannelEnabled || got.AutoTestChannelMinutes != 20 {
		t.Errorf("enabled=%v minutes=%v, want the in-memory state to be untouched", got.AutoTestChannelEnabled, got.AutoTestChannelMinutes)
	}
}

// TestFixGetMonitorSetting_UnsetEnvKeepsState: 未设置环境变量时完全不干预。
func TestFixGetMonitorSetting_UnsetEnvKeepsState(t *testing.T) {
	fixMonitorSettingSnapshot(t)

	t.Setenv("CHANNEL_TEST_FREQUENCY", "")
	monitorSetting.AutoTestChannelEnabled = true
	monitorSetting.AutoTestChannelMinutes = 15

	got := GetMonitorSetting()
	if !got.AutoTestChannelEnabled || got.AutoTestChannelMinutes != 15 {
		t.Errorf("enabled=%v minutes=%v, want true/15", got.AutoTestChannelEnabled, got.AutoTestChannelMinutes)
	}
}
