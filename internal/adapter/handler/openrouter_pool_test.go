package handler

// openrouter_pool_test.go — cycle 13 L4 (V1DOORS/SECURITY-14) oracle:
// GetOpenRouterApiPoolStatus (GET /api/openrouter-sync/api-pool) is
// AdminAuth-gated (role >= admin, not necessarily root — W raises this to
// RootAuth as a hand-off), so before the tenant scope added in this lane any
// tenant admin could see every OTHER tenant's OpenRouter channel names and
// masked key prefixes through the unscoped list function (now
// repo.ListOpenRouterMultiKeyChannelsForReaper), which carries no tenant
// predicate.
//
// The plan named a real-chain oracle (through SetApiRouter). That is not
// reachable from package handler: internal/adapter/handler/router imports
// handler, so a handler-package test importing router would be an import
// cycle, and router/*_test.go belongs to the serial wiring lane W. The
// handler is therefore driven directly through v1Ctx
// (v1_cross_tenant_idor_test.go), which sets exactly the keys the auth
// middleware sets (role / tenant_id / id). The real-chain half for this
// route is W's RootAuth hand-off plus router/idor_completeness_test.go.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"gorm.io/gorm"
)

// seedOpenRouterChannel persists a multi-key OpenRouter channel for tenantID
// — mirrors internal/app/openrouter_pool/pgsetup_test.go's
// seedMultiKeyChannel (a different package, not importable here without a
// cycle), trimmed to the fields GetOpenRouterApiPoolStatus reads.
func seedOpenRouterChannel(t *testing.T, db *gorm.DB, tenantID, name string) *repo.Channel {
	t.Helper()
	keys := []string{
		"sk-or-v1-" + common.GetRandomString(24),
		"sk-or-v1-" + common.GetRandomString(24),
	}
	ch := &repo.Channel{
		Name:     name,
		TenantId: tenantID,
		Type:     constant.ChannelTypeOpenRouter,
		Key:      strings.Join(keys, "\n"),
		Status:   common.ChannelStatusEnabled,
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: len(keys),
		},
		CreatedTime: common.GetTimestamp(),
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("seed openrouter channel %s: %v", name, err)
	}
	return ch
}

// TestV1OpenRouterApiPool_TenantScoped is the L4 plan's named oracle: two
// tenants each get one multi-key OpenRouter channel; a role-10 (tenant
// admin) caller sees only their own tenant's channel, root sees both.
func TestV1OpenRouterApiPool_TenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenantB := &repo.Tenant{
		Id:        "openrouter-tenant-b",
		Slug:      "openrouter-tenant-b",
		Name:      "OpenRouter Tenant B",
		Status:    repo.TenantStatusEnabled,
		IDPOrgID:  "org_openrouter_b",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(tenantB).Error; err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}

	chA := seedOpenRouterChannel(t, ctx.DB, ctx.TenantID, "openrouter-tenant-a-channel")
	chB := seedOpenRouterChannel(t, ctx.DB, tenantB.Id, "openrouter-tenant-b-channel")

	// role-10 in tenant A — must see only tenant A's channel.
	cAdmin, wAdmin := v1Ctx(http.MethodGet, "/api/openrouter-sync/api-pool", nil,
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetOpenRouterApiPoolStatus(cAdmin)
	if wAdmin.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200; body=%s", wAdmin.Code, wAdmin.Body.String())
	}
	adminBody := v1Body(t, wAdmin)
	adminData, ok := adminBody["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, body=%s", wAdmin.Body.String())
	}
	if len(adminData) != 1 {
		t.Fatalf("role-10 in tenant A: expected 1 channel, got %d: %v", len(adminData), adminData)
	}
	firstAdmin, _ := adminData[0].(map[string]interface{})
	if name, _ := firstAdmin["channel_name"].(string); name != chA.Name {
		t.Errorf("role-10 saw channel_name %q, want %q (tenant A's own channel)", name, chA.Name)
	}

	// root — must see both tenants' channels.
	cRoot, wRoot := v1Ctx(http.MethodGet, "/api/openrouter-sync/api-pool", nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetOpenRouterApiPoolStatus(cRoot)
	if wRoot.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200; body=%s", wRoot.Code, wRoot.Body.String())
	}
	rootBody := v1Body(t, wRoot)
	rootData, ok := rootBody["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, body=%s", wRoot.Body.String())
	}
	if len(rootData) != 2 {
		t.Fatalf("root: expected 2 channels (both tenants), got %d: %v", len(rootData), rootData)
	}
	seenNames := map[string]bool{}
	for _, item := range rootData {
		m, _ := item.(map[string]interface{})
		if name, _ := m["channel_name"].(string); name != "" {
			seenNames[name] = true
		}
	}
	if !seenNames[chA.Name] || !seenNames[chB.Name] {
		t.Errorf("root did not see both channels: got %v, want %q and %q", seenNames, chA.Name, chB.Name)
	}

	// Sanity: each key entry is a masked prefix (maskKeyPrefix,
	// openrouter_pool.go: first 12 chars + "***"), never the raw key.
	rawKeys := strings.Split(chA.Key, "\n")
	keysField, ok := firstAdmin["keys"].([]interface{})
	if !ok || len(keysField) != len(rawKeys) {
		t.Fatalf("expected %d key entries, got %v", len(rawKeys), firstAdmin["keys"])
	}
	for i, raw := range rawKeys {
		entry, _ := keysField[i].(map[string]interface{})
		prefix, _ := entry["key_prefix"].(string)
		if prefix == raw {
			t.Errorf("key entry %d exposed the full raw key instead of a masked prefix", i)
		}
		if !strings.HasPrefix(prefix, raw[:12]) {
			t.Errorf("key entry %d prefix %q does not start with raw key's first 12 chars %q", i, prefix, raw[:12])
		}
	}
}

// TestV1OpenRouterApiPool_ModelProbeReasons: the pool view names why a probed
// model is not fully healthy. A region-blocked model reads "region_blocked"
// (and not available), a rate-limited one reads "rate_limited" but stays
// available, a healthy one is not listed, and another channel's rows never
// leak into this channel's snapshot.
func TestV1OpenRouterApiPool_ModelProbeReasons(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&entity.ModelHealth{}); err != nil {
		t.Fatalf("migrate model_health: %v", err)
	}
	ch := seedOpenRouterChannel(t, ctx.DB, ctx.TenantID, "openrouter-reasons-channel")
	other := seedOpenRouterChannel(t, ctx.DB, ctx.TenantID, "openrouter-other-channel")

	rows := []entity.ModelHealth{
		{ChannelId: ch.Id, Model: "model-a:free", Ok: true, LastProbeAt: 100},
		{ChannelId: ch.Id, Model: "model-b:free", Ok: false, AutoDisabled: true, LastProbeAt: 200,
			LastError: `{"error":{"message":"This model is not available in your region.","code":403,"metadata":{"failed_routing_step":"Gate Endpoints with Geo Restrictions"}}}`},
		{ChannelId: ch.Id, Model: "model-c:free", Ok: false, LastProbeAt: 300,
			LastError: "model-c:free is temporarily rate-limited upstream"},
		{ChannelId: other.Id, Model: "model-d:free", Ok: false, AutoDisabled: true, LastProbeAt: 400,
			LastError: "This model is not available in your region."},
	}
	for i := range rows {
		if err := ctx.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed health: %v", err)
		}
	}

	c, w := v1Ctx(http.MethodGet, "/api/openrouter-sync/api-pool", nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetOpenRouterApiPoolStatus(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	data, _ := v1Body(t, w)["data"].([]interface{})

	byName := map[string]map[string]interface{}{}
	for _, item := range data {
		m, _ := item.(map[string]interface{})
		name, _ := m["channel_name"].(string)
		byName[name] = m
	}
	snap := byName[ch.Name]
	if snap == nil {
		t.Fatalf("channel %q missing from %v", ch.Name, byName)
	}
	probed, _ := snap["model_probe"].([]interface{})
	if len(probed) != 2 {
		t.Fatalf("want 2 unhealthy models (healthy one omitted), got %d: %v", len(probed), probed)
	}
	got := map[string]map[string]interface{}{}
	for _, p := range probed {
		m, _ := p.(map[string]interface{})
		model, _ := m["model"].(string)
		got[model] = m
	}
	if b := got["model-b:free"]; b == nil || b["reason"] != "region_blocked" || b["available"] != false {
		t.Errorf("model-b: want reason=region_blocked available=false, got %v", b)
	}
	if cc := got["model-c:free"]; cc == nil || cc["reason"] != "rate_limited" || cc["available"] != true {
		t.Errorf("model-c: want reason=rate_limited available=true, got %v", cc)
	}
	if _, leaked := got["model-d:free"]; leaked {
		t.Error("another channel's model leaked into this snapshot")
	}
}
