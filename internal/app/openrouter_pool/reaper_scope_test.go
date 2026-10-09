package openrouter_pool

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// The reaper must scan multi-key channels of every type; scanning only the
// OpenRouter ones would leave keys parked on any other channel type forever.
func TestReaper_SourceIsAllTypeMultiKeyList(t *testing.T) {
	got := reflect.ValueOf(listReaperChannelsFn).Pointer()
	want := reflect.ValueOf(repo.ListMultiKeyChannelsForReaper).Pointer()
	if got != want {
		t.Fatal("reaper channel source is not repo.ListMultiKeyChannelsForReaper")
	}
}

// ReapOnce walks whatever the source returns, a non-OpenRouter channel included.
func TestReapOnce_VisitsListedChannels(t *testing.T) {
	prev := listReaperChannelsFn
	t.Cleanup(func() { listReaperChannelsFn = prev })
	called := 0
	listReaperChannelsFn = func() ([]*repo.Channel, error) {
		called++
		ch := &repo.Channel{Id: 5, Type: 1, Status: common.ChannelStatusEnabled}
		ch.ChannelInfo.IsMultiKey = true
		return []*repo.Channel{ch}, nil
	}
	if err := ReapOnce(context.Background(), func() time.Time { return time.Unix(1, 0) }); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("source called %d times, want 1", called)
	}
}
