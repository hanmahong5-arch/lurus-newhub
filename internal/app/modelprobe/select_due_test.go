package modelprobe

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

func setting(groups string) operation_setting.ModelProbeSetting {
	return operation_setting.ModelProbeSetting{
		Enabled:          true,
		Groups:           groups,
		IntervalHours:    24,
		DisableThreshold: 3,
		MaxProbesPerRun:  40,
		SleepSeconds:     0,
	}
}

func TestSelectDue_GroupsFilter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1"},
		{Id: 2, Status: common.ChannelStatusEnabled, Group: "paid", Models: "m2"},
		{Id: 3, Status: common.ChannelStatusEnabled, Group: "paid,free", Models: "m3"},
	}
	due := SelectDue(channels, nil, setting("free"), now)
	if len(due) != 2 {
		t.Fatalf("got %d due pairs, want 2 (channel 1 and 3): %+v", len(due), due)
	}
	for _, d := range due {
		if d.Channel.Id == 2 {
			t.Fatalf("channel 2 (group=paid) must not be selected when setting.Groups=free")
		}
	}
}

func TestSelectDue_DisabledChannelSkipped(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1"},
		{Id: 2, Status: 2 /* disabled */, Group: "free", Models: "m2"},
	}
	due := SelectDue(channels, nil, setting("free"), now)
	if len(due) != 1 || due[0].Channel.Id != 1 {
		t.Fatalf("got %+v, want only channel 1", due)
	}
}

func TestSelectDue_IntervalGating(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1,m2"},
	}
	health := []entity.ModelHealth{
		// probed recently: not due (interval 24h = 86400s)
		{ChannelId: 1, Model: "m1", LastProbeAt: now.Unix() - 100},
		// probed long ago: due
		{ChannelId: 1, Model: "m2", LastProbeAt: now.Unix() - 90000},
	}
	due := SelectDue(channels, health, setting("free"), now)
	if len(due) != 1 || due[0].Model != "m2" {
		t.Fatalf("got %+v, want only m2 (over interval)", due)
	}
}

func TestSelectDue_NeverProbedIsDue(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1"},
	}
	due := SelectDue(channels, nil, setting("free"), now)
	if len(due) != 1 || due[0].Model != "m1" {
		t.Fatalf("got %+v, want the never-probed pair selected", due)
	}
}

func TestSelectDue_OrderingOldestAndNeverProbedFirst(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "old,never,newer"},
	}
	health := []entity.ModelHealth{
		{ChannelId: 1, Model: "old", LastProbeAt: now.Unix() - 200000},
		{ChannelId: 1, Model: "newer", LastProbeAt: now.Unix() - 100000},
		// "never" has no row at all
	}
	due := SelectDue(channels, health, setting("free"), now)
	if len(due) != 3 {
		t.Fatalf("got %d pairs, want 3", len(due))
	}
	if due[0].Model != "never" {
		t.Errorf("first = %s, want never-probed pair first", due[0].Model)
	}
	if due[1].Model != "old" || due[2].Model != "newer" {
		t.Errorf("order = [%s %s], want [old newer] after the never-probed pair", due[1].Model, due[2].Model)
	}
}

func TestSelectDue_CappedAtMaxProbesPerRun(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := setting("free")
	s.MaxProbesPerRun = 2
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1,m2,m3,m4"},
	}
	due := SelectDue(channels, nil, s, now)
	if len(due) != 2 {
		t.Fatalf("got %d due pairs, want capped at 2", len(due))
	}
}

func TestSelectDue_DedupesModelsWithinChannel(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	channels := []*repo.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "free", Models: "m1, m1 ,m1,,  "},
	}
	due := SelectDue(channels, nil, setting("free"), now)
	if len(due) != 1 || due[0].Model != "m1" {
		t.Fatalf("got %+v, want a single deduped/trimmed m1 pair", due)
	}
}
