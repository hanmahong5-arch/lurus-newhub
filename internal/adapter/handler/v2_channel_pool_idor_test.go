package handler

// Cross-tenant regression tests for the account-pool ops endpoints. Every one
// is platform-staff only, but staff of tenant A must still not read, probe,
// restore or mutate a channel owned by tenant B, nor append keys to it.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func seedForeignPool(t *testing.T, ctx *V2TestContext) *repo.Channel {
	t.Helper()
	ch := poolSeedChannel(t, ctx, []string{"sk-victim-a", "sk-victim-b"}, func(c *repo.Channel) {
		c.TenantId = "other-tenant-xyz"
	})
	return ch
}

func TestPoolOps_CrossTenantStaffForbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	env := newPoolOpsEnv(t)
	victim := seedForeignPool(t, ctx)
	before := victim.Key

	cases := []struct {
		name, method, path string
		body               interface{}
	}{
		{"health", http.MethodGet, fmt.Sprintf("%s/%d/health", poolBase, victim.Id), nil},
		{"test", http.MethodPost, fmt.Sprintf("%s/%d/keys/0/test", poolBase, victim.Id), nil},
		{"restore", http.MethodPost, fmt.Sprintf("%s/%d/keys/0/restore", poolBase, victim.Id), map[string]string{"proof": "x"}},
		{"settings", http.MethodPut, fmt.Sprintf("%s/%d/keys/0/settings", poolBase, victim.Id), map[string]int{"weight": 0}},
		{"import_append", http.MethodPost, poolBase + "/import", map[string]interface{}{
			"channel_id": victim.Id, "keys": "sk-attacker", "dry_run": false, "probe": true,
		}},
	}
	for _, tc := range cases {
		code, m := poolAdmin(ctx, tc.method, tc.path, tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s: cross-tenant staff = %d, want 403 (%v)", tc.name, code, m)
		}
		if raw := fmt.Sprint(m); strings.Contains(raw, "sk-victim") {
			t.Errorf("%s: response leaked victim key material", tc.name)
		}
	}
	if len(env.probes) != 0 {
		t.Errorf("no upstream probe may fire for a foreign channel, got %v", env.probes)
	}
	var after repo.Channel
	if err := ctx.DB.First(&after, victim.Id).Error; err != nil {
		t.Fatal(err)
	}
	if after.Key != before {
		t.Error("victim channel keys were modified by a cross-tenant request")
	}
	if after.ChannelInfo.KeyWeight(0) != victim.ChannelInfo.KeyWeight(0) {
		t.Error("victim key weight was modified by a cross-tenant request")
	}
}
