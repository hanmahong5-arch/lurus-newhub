package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// Re-enabling one key (or all keys) of a multi-key channel is an operator
// action that lifts the channel-level 429 cooldown.
func TestManageMultiKeys_EnableKey_LiftsCooldown(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1"})
	coolChannel(t, ch.Id)

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "disable_key", "key_index": 1,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if handler_channel_mkBody(t, w)["success"] != true {
		t.Fatalf("disable_key failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) == 0 {
		t.Fatal("disabling a key must not lift the cooldown")
	}

	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_key", "key_index": 1,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if handler_channel_mkBody(t, w)["success"] != true {
		t.Fatalf("enable_key failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
		t.Fatal("enable_key left the channel cooling")
	}
}

func TestManageMultiKeys_EnableAllKeys_LiftsCooldown(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1"})
	coolChannel(t, ch.Id)

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_all_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if handler_channel_mkBody(t, w)["success"] != true {
		t.Fatalf("enable_all_keys failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
		t.Fatal("enable_all_keys left the channel cooling")
	}
}

// A successful v2 connectivity test proves the upstream is serving again.
func TestV2ChannelTest_Success_LiftsCooldown(t *testing.T) {
	allowLoopbackEgress(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()
	prev := channelTestHTTPClient
	channelTestHTTPClient = upstream.Client()
	defer func() { channelTestHTTPClient = prev }()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := seedChannelWithBase(t, ctx, "cool-then-test", upstream.URL)
	coolChannel(t, ch.Id)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if ParseV2Response(t, w)["success"] != true {
		t.Fatalf("test did not succeed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
		t.Fatal("successful channel test left the channel cooling")
	}
}

// A successful legacy (v1) probe lifts the cooldown too. The harness DB is
// fresh, so the first channel it creates gets id 1; the test asserts that.
func TestV1ChannelTest_Success_LiftsCooldown(t *testing.T) {
	ctx := setupProbeAttributionDB(t)
	const predictedID = 1
	coolChannel(t, predictedID)
	row := runProbeAsActor(t, ctx, ctx.actor, probeActorTenant)
	if row.ChannelId != predictedID {
		t.Fatalf("precondition: probe channel id = %d, want %d", row.ChannelId, predictedID)
	}
	if app.ChannelCoolingUntil(predictedID, 0) != 0 {
		t.Fatal("successful v1 probe left the channel cooling")
	}
}

// refreshAfterTagEnable must not run unscoped for a non-root caller without a
// tenant: GetChannelsByTagAndTenant("") drops the tenant filter and would lift
// every tenant's cooldown under the tag.
func TestRefreshAfterTagEnable_EmptyTenantNonRoot_DoesNothing(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "foreign-tagged", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("tag", "shared-tag")
	coolChannel(t, ch.Id)

	c, _ := r2chanNewCtx(http.MethodPost, "/", nil)
	c.Set("role", common.RoleAdminUser)
	c.Set("tenant_id", "")
	refreshAfterTagEnable(c, "shared-tag")
	if app.ChannelCoolingUntil(ch.Id, 0) == 0 {
		t.Fatal("tenant-less non-root caller lifted another tenant's cooldown")
	}

	// Control: with the owning tenant it does lift.
	c2, _ := r2chanNewCtx(http.MethodPost, "/", nil)
	c2.Set("role", common.RoleAdminUser)
	c2.Set("tenant_id", ctx.TenantID)
	app.MarkChannelCooldown(ch.Id, 0, time.Now().Add(time.Hour).Unix())
	refreshAfterTagEnable(c2, "shared-tag")
	if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
		t.Fatal("tenant admin enable must lift its own channels' cooldown")
	}
}
