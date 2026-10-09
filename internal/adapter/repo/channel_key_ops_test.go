package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func weightedChannel(id int, weights map[int]int, status map[int]int) *Channel {
	ch := &Channel{Id: id, Key: "k0\nk1\nk2"}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 3
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeWeighted
	ch.ChannelInfo.MultiKeyWeight = weights
	ch.ChannelInfo.MultiKeyStatusList = status
	return ch
}

func TestWeightedKeyDistribution(t *testing.T) {
	ch := weightedChannel(9101, map[int]int{0: 60, 1: 30, 2: 10}, nil)
	ResetWeightedCursor(ch.Id)
	counts := map[int]int{}
	const n = 1000
	for i := 0; i < n; i++ {
		_, idx, err := ch.GetNextEnabledKey()
		if err != nil {
			t.Fatal(err)
		}
		counts[idx]++
	}
	// Smooth WRR is deterministic: over a multiple of the weight total the
	// split is exact.
	if counts[0] != 600 || counts[1] != 300 || counts[2] != 100 {
		t.Fatalf("distribution = %v, want 600/300/100", counts)
	}
}

func TestWeightedKeyZeroNeverPickedAndDefault50(t *testing.T) {
	// idx 1 explicit 0 (never); idx 0 default 50; idx 2 explicit 50.
	ch := weightedChannel(9102, map[int]int{1: 0, 2: 50}, nil)
	ResetWeightedCursor(ch.Id)
	for i := 0; i < 200; i++ {
		_, idx, err := ch.GetNextEnabledKey()
		if err != nil {
			t.Fatal(err)
		}
		if idx == 1 {
			t.Fatal("weight-0 key was selected")
		}
	}
}

func TestWeightedAllZeroNoKey(t *testing.T) {
	ch := weightedChannel(9103, map[int]int{0: 0, 1: 0, 2: 0}, nil)
	_, _, err := ch.GetNextEnabledKey()
	if err == nil || err.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
		t.Fatalf("want no available key, got %v", err)
	}
}

func TestWeightedSkipsDisabledKeys(t *testing.T) {
	ch := weightedChannel(9104, map[int]int{0: 50, 1: 50, 2: 50}, map[int]int{1: common.ChannelStatusAutoDisabled})
	ResetWeightedCursor(ch.Id)
	for i := 0; i < 50; i++ {
		_, idx, err := ch.GetNextEnabledKey()
		if err != nil || idx == 1 {
			t.Fatalf("idx=%d err=%v", idx, err)
		}
	}
}

func TestPollingAndRandomUnchangedByWeights(t *testing.T) {
	ch := weightedChannel(9105, map[int]int{0: 0}, nil)
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		_, idx, _ := ch.GetNextEnabledKey()
		seen[idx] = true
	}
	if !seen[0] {
		t.Fatal("random mode must ignore weights (key 0 never drawn)")
	}
}

func TestKeyIfRoutable(t *testing.T) {
	ch := weightedChannel(9106, map[int]int{2: 0}, map[int]int{1: common.ChannelStatusAutoDisabled})
	if k, ok := ch.KeyIfRoutable(0); !ok || k != "k0" {
		t.Fatal("key 0 should be routable")
	}
	if _, ok := ch.KeyIfRoutable(1); ok {
		t.Fatal("disabled key must not be routable")
	}
	if _, ok := ch.KeyIfRoutable(2); ok {
		t.Fatal("weight-0 key must not be routable in weighted mode")
	}
	if _, ok := ch.KeyIfRoutable(7); ok {
		t.Fatal("out of range")
	}
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	if _, ok := ch.KeyIfRoutable(2); !ok {
		t.Fatal("weight is ignored outside weighted mode")
	}
}

func TestWeightedMixedWithZeroExactSplit(t *testing.T) {
	// The zero-weight key sits first so a weight<=0 guard that lets it join
	// the rotation would hand it the first pick.
	ch := weightedChannel(9107, map[int]int{0: 0, 1: 75, 2: 25}, nil)
	ResetWeightedCursor(ch.Id)
	counts := map[int]int{}
	for i := 0; i < 400; i++ {
		_, idx, err := ch.GetNextEnabledKey()
		if err != nil {
			t.Fatal(err)
		}
		counts[idx]++
	}
	if counts[0] != 0 || counts[1] != 300 || counts[2] != 100 {
		t.Fatalf("distribution = %v, want 0/300/100", counts)
	}
}
