package repo

// ability_tenant_models_test.go — L2 oracle: GetGroupEnabledModelsForTenant
// must apply the same tenant scope abilityTenantScope already gives channel
// selection (channel_cache_tenant_test.go), so /v1/models stops listing
// models a caller's routing would 404/503 on.

import (
	"sort"
	"testing"
)

func TestGetGroupEnabledModelsForTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenantRelayChannel(t, 9601, "tenant-a", "default", "a-only")
	seedTenantRelayChannel(t, 9602, "default", "default", "shared-model")

	sortedCopy := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return out
	}

	t.Run("foreign tenant sees only the platform-shared model", func(t *testing.T) {
		got := sortedCopy(GetGroupEnabledModelsForTenant("default", "tenant-b"))
		want := []string{"shared-model"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("tenant-b: got %v, want %v", got, want)
		}
	})

	t.Run("owning tenant sees its own model plus the shared one", func(t *testing.T) {
		got := sortedCopy(GetGroupEnabledModelsForTenant("default", "tenant-a"))
		want := []string{"a-only", "shared-model"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("tenant-a: got %v, want %v", got, want)
		}
	})

	t.Run("empty tenantID reproduces the tenant-blind query byte-for-byte", func(t *testing.T) {
		got := sortedCopy(GetGroupEnabledModelsForTenant("default", ""))
		want := sortedCopy(GetGroupEnabledModels("default"))
		if len(got) != len(want) {
			t.Fatalf("unscoped: got %v, want (== GetGroupEnabledModels) %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("unscoped: got %v, want (== GetGroupEnabledModels) %v", got, want)
			}
		}
	})
}
