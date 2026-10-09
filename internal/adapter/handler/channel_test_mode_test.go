package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestScheduledTestChannels_ManualTestsEverythingScheduledHonoursMode(t *testing.T) {
	plan := &repo.Channel{Id: 1, Status: common.ChannelStatusEnabled}
	plan.SetSetting(dto.ChannelSettings{PlanKind: "kimi_coding"})
	payg := &repo.Channel{Id: 2, Status: common.ChannelStatusEnabled}
	all := []*repo.Channel{plan, payg}

	if got := scheduledTestChannels(all, true); len(got) != 2 {
		t.Fatalf("manual test-all must cover every channel, got %d", len(got))
	}
	got := scheduledTestChannels(all, false)
	if len(got) != 1 || got[0].Id != 2 {
		t.Fatalf("scheduled pass must skip the healthy plan channel (default global mode stays all), got %v", got)
	}
}
