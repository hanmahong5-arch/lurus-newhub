package app

// channel_select_tenant_test.go — TI-1: a session-affinity binding pinned to
// a channel that has since become tenant-foreign to the caller must be
// treated as stale, exactly like a disabled channel or a channel that lost
// the model. Affinity is only allowed to bias the choice among channels the
// caller could already reach; it must never be a back door across a tenant
// boundary.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestLookupAffinityChannel_ForeignTenantIsStale(t *testing.T) {
	// setupAffinitySelection (channel_select_affinity_test.go) seeds channels
	// 9301/9302, both TenantId "default", group "default", model "gpt-4o".
	setupAffinitySelection(t)
	// setupAffinitySelection does not call this itself (its own tests happen
	// to run after another test in the package already has); GetSatisfiedChannelByID's
	// DB-path query below needs commonGroupCol quoted, so set it explicitly
	// rather than depend on test run order.
	repo.InitCol()

	// Re-own channel 9302 to a specific, non-shared tenant so it is no longer
	// platform-shared.
	if err := repo.DB.Model(&repo.Channel{}).Where("id = ?", 9302).Update("tenant_id", "tenant-a").Error; err != nil {
		t.Fatalf("reassign channel tenant: %v", err)
	}

	t.Run("foreign_tenant_binding_dropped", func(t *testing.T) {
		c := affinitySelectCtx(t, "foreign-tenant-key")
		affinityStore(c, "foreign-tenant-key", affinityRecord{ChannelID: 9302, Group: "default"})

		retry := 0
		param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry, TenantID: "tenant-b"}

		got, group := lookupAffinityChannel(param, "foreign-tenant-key")
		if got != nil {
			t.Fatalf("foreign-tenant pinned channel #%d must be treated as stale, not returned", got.Id)
		}
		if group != "" {
			t.Errorf("group = %q, want empty on a stale lookup", group)
		}
	})

	t.Run("owning_tenant_binding_still_hits", func(t *testing.T) {
		c := affinitySelectCtx(t, "owning-tenant-key")
		affinityStore(c, "owning-tenant-key", affinityRecord{ChannelID: 9302, Group: "default"})

		retry := 0
		param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry, TenantID: "tenant-a"}

		got, group := lookupAffinityChannel(param, "owning-tenant-key")
		if got == nil || got.Id != 9302 {
			t.Fatalf("owning tenant's pinned channel must still hit, got %+v", got)
		}
		if group != "default" {
			t.Errorf("group = %q, want default", group)
		}
	})

	t.Run("unresolved_caller_tenant_keeps_legacy_behaviour", func(t *testing.T) {
		// param.TenantID == "" (unset) must not apply the new check at all —
		// this is the pre-existing behaviour every other affinity test in
		// channel_select_affinity_test.go relies on.
		c := affinitySelectCtx(t, "no-tenant-key")
		affinityStore(c, "no-tenant-key", affinityRecord{ChannelID: 9302, Group: "default"})

		retry := 0
		param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

		got, group := lookupAffinityChannel(param, "no-tenant-key")
		if got == nil || got.Id != 9302 {
			t.Fatalf("unresolved-tenant caller must keep tenant-blind affinity behaviour, got %+v", got)
		}
		if group != "default" {
			t.Errorf("group = %q, want default", group)
		}
	})
}
