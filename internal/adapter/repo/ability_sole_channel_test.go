package repo

// ability_sole_channel_test.go — L3 oracle for SoleEnabledModelsForChannel
// (ability_sole_channel.go): four cells, each seeded with
// seedTenantRelayChannel (channel_cache_tenant_test.go) so this drives the
// real abilities/channels tables through the same tenant-scope helper
// route-time selection uses (abilityTenantScope), not a hand-built
// substitute.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestSoleEnabledModelsForChannel(t *testing.T) {
	t.Run("no sibling at all: sole", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()

		seedTenantRelayChannel(t, 9701, "tenant-a", "default", "rt-alpha")

		got, err := SoleEnabledModelsForChannel(9701, "tenant-a")
		if err != nil {
			t.Fatalf("SoleEnabledModelsForChannel: %v", err)
		}
		if len(got) != 1 || got[0].Group != "default" || got[0].Model != "rt-alpha" {
			t.Fatalf("got %+v, want [{default rt-alpha}] (only channel serving this pair)", got)
		}
	})

	t.Run("enabled sibling same tenant: not sole", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()

		seedTenantRelayChannel(t, 9702, "tenant-b", "default", "rt-beta")
		seedTenantRelayChannel(t, 9703, "tenant-b", "default", "rt-beta") // enabled sibling, same tenant

		got, err := SoleEnabledModelsForChannel(9702, "tenant-b")
		if err != nil {
			t.Fatalf("SoleEnabledModelsForChannel: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want [] (an enabled sibling in the same tenant covers this pair)", got)
		}
	})

	t.Run("disabled sibling same tenant counts as absent: sole", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()

		seedTenantRelayChannel(t, 9704, "tenant-c", "default", "rt-gamma")
		ch := &Channel{
			Id: 9705, Type: 1, Status: common.ChannelStatusAutoDisabled,
			Name: "tc-disabled-sibling", Models: "rt-gamma", Group: "default", TenantId: "tenant-c",
		}
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed disabled sibling channel: %v", err)
		}
		// Ability row itself is not enabled — mirrors AddAbilities, which
		// sets Enabled = channel.Status == ChannelStatusEnabled.
		if err := DB.Create(&Ability{Group: "default", Model: "rt-gamma", ChannelId: 9705, Enabled: false}).Error; err != nil {
			t.Fatalf("seed disabled sibling ability: %v", err)
		}

		got, err := SoleEnabledModelsForChannel(9704, "tenant-c")
		if err != nil {
			t.Fatalf("SoleEnabledModelsForChannel: %v", err)
		}
		if len(got) != 1 || got[0].Model != "rt-gamma" {
			t.Fatalf("got %+v, want [{default rt-gamma}] (a disabled sibling does not rescue sole status)", got)
		}
	})

	t.Run("own ability rows disabled: nothing is sole", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()

		ch := &Channel{
			Id: 9712, Type: 1, Status: common.ChannelStatusAutoDisabled,
			Name: "tc-self-disabled", Models: "rt-omega", Group: "default", TenantId: "tenant-f",
		}
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed self-disabled channel: %v", err)
		}
		if err := DB.Create(&Ability{Group: "default", Model: "rt-omega", ChannelId: 9712, Enabled: false}).Error; err != nil {
			t.Fatalf("seed disabled own ability: %v", err)
		}

		got, err := SoleEnabledModelsForChannel(9712, "tenant-f")
		if err != nil {
			t.Fatalf("SoleEnabledModelsForChannel: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want none (a channel whose own rows are disabled serves nothing)", got)
		}
	})

	t.Run("enabled sibling in a different tenant does not rescue: sole", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()

		seedTenantRelayChannel(t, 9706, "tenant-d", "default", "rt-delta")
		seedTenantRelayChannel(t, 9707, "tenant-e", "default", "rt-delta") // enabled, but a different tenant

		got, err := SoleEnabledModelsForChannel(9706, "tenant-d")
		if err != nil {
			t.Fatalf("SoleEnabledModelsForChannel: %v", err)
		}
		if len(got) != 1 || got[0].Model != "rt-delta" {
			t.Fatalf("got %+v, want [{default rt-delta}] (an enabled channel in a different tenant is invisible to tenant-d's routing, so it cannot rescue sole status)", got)
		}
	})
}
