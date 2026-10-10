package handler

import (
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/planquota"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// scheduledTestChannels narrows the channel list of a test pass. A manual
// "test all" (manual = true) always tests everything; the scheduled pass
// honours the test modes (all / auto_ban_only / none), so plan channels are
// not probed — and their quota not burned — unless they are auto-disabled and
// need a recovery check.
func scheduledTestChannels(channels []*repo.Channel, manual bool) []*repo.Channel {
	if manual {
		return channels
	}
	return planquota.FilterScheduledTest(channels, operation_setting.GetMonitorSetting().AutoTestChannelMode, time.Now())
}
