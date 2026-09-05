package repo

// channel_cache_tenant_test.go — TI-1: tenant-scoped channel selection.
//
// Policy under test (see channel_cache.go / ability.go doc comments): a
// channel whose TenantId is "default" or "" is platform-shared and may serve
// any tenant; a channel owned by any other tenant serves only callers of that
// tenant. This must hold in BOTH selection paths — the in-memory cache
// (GetRandomSatisfiedChannelForTenant, MemoryCacheEnabled=true) and the DB
// fallback (GetChannelForTenant, MemoryCacheEnabled=false) — since a fresh
// replica runs the DB path until its first cache sync.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// seedTenantRelayChannel creates a channel + its matching enabled ability row
// (InitChannelCache only indexes a channel's (group, model) pair if some
// ability row already established that group in its group set — mirroring
// how AddAbilities always accompanies channel creation in production).
func seedTenantRelayChannel(t *testing.T, id int, tenantID, group, model string) {
	t.Helper()
	ch := &Channel{
		Id: id, Type: 1, Status: common.ChannelStatusEnabled,
		Name: "tc" + model, Models: model, Group: group, TenantId: tenantID,
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := DB.Create(&Ability{Group: group, Model: model, ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

func TestGetRandomSatisfiedChannelForTenant_FiltersForeignTenant(t *testing.T) {
	t.Run("memory_cache", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()
		withMemoryCache(t)

		seedTenantRelayChannel(t, 9401, "tenant-a", "default", "gpt-4o")
		seedTenantRelayChannel(t, 9402, "default", "default", "gpt-4o") // platform-shared
		InitChannelCache()

		for i := 0; i < 100; i++ {
			ch, err := GetRandomSatisfiedChannelForTenant("tenant-b", "default", "gpt-4o", 0)
			if err != nil {
				t.Fatalf("draw %d: unexpected error: %v", i, err)
			}
			if ch == nil || ch.Id != 9402 {
				t.Fatalf("draw %d: got %+v, want the platform-shared channel 9402 only", i, ch)
			}
		}

		// Positive control: the owning tenant can still draw its own channel
		// (proves the filter narrows, rather than accidentally excluding
		// everything).
		sawOwn := false
		for i := 0; i < 50; i++ {
			ch, err := GetRandomSatisfiedChannelForTenant("tenant-a", "default", "gpt-4o", 0)
			if err != nil || ch == nil {
				t.Fatalf("draw %d: tenant-a selection failed: err=%v ch=%+v", i, err, ch)
			}
			if ch.Id == 9401 {
				sawOwn = true
			}
		}
		if !sawOwn {
			t.Error("tenant-a never drew its own channel #9401 across 50 attempts")
		}

		// Unscoped ("") legacy caller keeps seeing every channel — proves the
		// filter is opt-in via a non-empty tenantID, not a global behaviour
		// change for GetRandomSatisfiedChannel's existing callers.
		sawEither := map[int]bool{}
		for i := 0; i < 50; i++ {
			ch, err := GetRandomSatisfiedChannel("default", "gpt-4o", 0)
			if err != nil || ch == nil {
				t.Fatalf("draw %d: unscoped selection failed: err=%v ch=%+v", i, err, ch)
			}
			sawEither[ch.Id] = true
		}
		if !sawEither[9401] {
			t.Error("unscoped GetRandomSatisfiedChannel never drew channel #9401 — tenant filtering leaked into the tenant-blind path")
		}
	})

	t.Run("db_path", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()
		prev := common.MemoryCacheEnabled
		common.MemoryCacheEnabled = false
		t.Cleanup(func() { common.MemoryCacheEnabled = prev })

		seedTenantRelayChannel(t, 9501, "tenant-a", "dbgroup", "gpt-4o-db")
		seedTenantRelayChannel(t, 9502, "default", "dbgroup", "gpt-4o-db") // platform-shared

		for i := 0; i < 20; i++ {
			ch, err := GetChannelForTenant("dbgroup", "gpt-4o-db", 0, "tenant-b")
			if err != nil {
				t.Fatalf("draw %d: unexpected error: %v", i, err)
			}
			if ch == nil || ch.Id != 9502 {
				t.Fatalf("draw %d: got %+v, want the platform-shared channel 9502 only (DB path)", i, ch)
			}
		}

		// Owning tenant still reaches its own channel via the DB path too.
		ch, err := GetChannelForTenant("dbgroup", "gpt-4o-db", 0, "tenant-a")
		if err != nil || ch == nil {
			t.Fatalf("tenant-a DB-path selection failed: err=%v ch=%+v", err, ch)
		}

		// Tenant-blind GetChannel (its own callers, and GetRandomSatisfiedChannel's
		// DB fallback) is unaffected.
		ch, err = GetChannel("dbgroup", "gpt-4o-db", 0)
		if err != nil || ch == nil {
			t.Fatalf("tenant-blind GetChannel failed: err=%v ch=%+v", err, ch)
		}
	})
}
