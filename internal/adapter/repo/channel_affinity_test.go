package repo

// channel_affinity_test.go — GetSatisfiedChannelByID is the re-validation gate
// for session affinity. Its entire job is to be as restrictive as the normal
// selection path, so the tests below assert what it must REFUSE at least as
// hard as what it returns: a binding must never be a back door to a channel the
// caller could not otherwise reach.
//
// Both storage modes are covered, because the memory-cache flag flips the
// implementation completely (in-process index vs. SQL).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestGetSatisfiedChannelByID_MemoryCache_PG(t *testing.T) {
	SetupTestDB(t)
	withMemoryCache(t)

	seedCacheChannel(t, 8101, "default", "gpt-4o", common.ChannelStatusEnabled)
	seedCacheChannel(t, 8102, "vip", "gpt-4o", common.ChannelStatusEnabled)
	seedCacheChannel(t, 8103, "default", "claude-3", common.ChannelStatusManuallyDisabled)
	InitChannelCache()

	t.Run("eligible_channel_is_returned", func(t *testing.T) {
		got, err := GetSatisfiedChannelByID("default", "gpt-4o", 8101)
		if err != nil || got == nil || got.Id != 8101 {
			t.Fatalf("expected channel 8101, got %+v err=%v", got, err)
		}
	})

	t.Run("wrong_group_refused", func(t *testing.T) {
		// 8102 exists and serves gpt-4o, but only for group vip.
		got, _ := GetSatisfiedChannelByID("default", "gpt-4o", 8102)
		if got != nil {
			t.Errorf("affinity must not cross group boundaries, got channel #%d", got.Id)
		}
	})

	t.Run("model_not_served_refused", func(t *testing.T) {
		got, _ := GetSatisfiedChannelByID("default", "some-other-model", 8101)
		if got != nil {
			t.Errorf("affinity must not route a model the channel does not serve, got #%d", got.Id)
		}
	})

	t.Run("disabled_channel_refused", func(t *testing.T) {
		got, _ := GetSatisfiedChannelByID("default", "claude-3", 8103)
		if got != nil {
			t.Errorf("disabled channel must not be resurrected by a binding, got #%d", got.Id)
		}
	})

	t.Run("unknown_channel_is_a_miss_not_an_error", func(t *testing.T) {
		got, err := GetSatisfiedChannelByID("default", "gpt-4o", 999999)
		if got != nil || err != nil {
			t.Errorf("stale binding must fail open, got %+v err=%v", got, err)
		}
	})

	t.Run("degenerate_input_rejected", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			group, model string
			id           int
		}{
			{"zero_id", "default", "gpt-4o", 0},
			{"negative_id", "default", "gpt-4o", -1},
			{"empty_group", "", "gpt-4o", 8101},
			{"empty_model", "default", "", 8101},
		} {
			if got, err := GetSatisfiedChannelByID(tc.group, tc.model, tc.id); got != nil || err != nil {
				t.Errorf("%s: expected (nil,nil), got %+v err=%v", tc.name, got, err)
			}
		}
	})
}

func TestGetSatisfiedChannelByID_DBPath_PG(t *testing.T) {
	SetupTestDB(t)
	// Memory cache deliberately left OFF so the SQL branch runs.
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })

	seedCacheChannel(t, 8201, "default", "gpt-4o", common.ChannelStatusEnabled)
	seedCacheChannel(t, 8202, "vip", "gpt-4o", common.ChannelStatusEnabled)

	t.Run("eligible_channel_is_returned", func(t *testing.T) {
		got, err := GetSatisfiedChannelByID("default", "gpt-4o", 8201)
		if err != nil || got == nil || got.Id != 8201 {
			t.Fatalf("expected channel 8201, got %+v err=%v", got, err)
		}
	})

	t.Run("wrong_group_refused", func(t *testing.T) {
		if got, _ := GetSatisfiedChannelByID("default", "gpt-4o", 8202); got != nil {
			t.Errorf("affinity must not cross group boundaries, got channel #%d", got.Id)
		}
	})

	t.Run("channel_disabled_after_binding_refused", func(t *testing.T) {
		// Simulate the race the gate exists for: the ability row still says the
		// channel serves the model, but the channel itself was just disabled.
		if err := DB.Model(&Channel{}).Where("id = ?", 8201).
			Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
			t.Fatalf("disable channel: %v", err)
		}
		t.Cleanup(func() {
			_ = DB.Model(&Channel{}).Where("id = ?", 8201).
				Update("status", common.ChannelStatusEnabled).Error
		})

		if got, _ := GetSatisfiedChannelByID("default", "gpt-4o", 8201); got != nil {
			t.Errorf("a channel disabled after pinning must be refused, got #%d", got.Id)
		}
	})

	t.Run("unknown_channel_is_a_miss_not_an_error", func(t *testing.T) {
		got, err := GetSatisfiedChannelByID("default", "gpt-4o", 999999)
		if got != nil || err != nil {
			t.Errorf("stale binding must fail open, got %+v err=%v", got, err)
		}
	})
}
