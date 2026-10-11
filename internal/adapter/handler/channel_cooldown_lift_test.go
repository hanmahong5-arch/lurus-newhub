package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func coolChannel(t *testing.T, id int) {
	t.Helper()
	app.ClearChannelCooldowns()
	t.Cleanup(app.ClearChannelCooldowns)
	app.MarkChannelCooldown(id, 0, time.Now().Add(time.Hour).Unix())
	if app.ChannelCoolingUntil(id, 0) == 0 {
		t.Fatal("setup: cooldown not recorded")
	}
}

// Bulk enable by tag is an operator re-enabling channels: it lifts their
// cooldown like the single-channel enable does.
func TestEnableTagChannels_LiftsCooldown(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "cooling-tagged", constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("tag", "cd-tag")
	coolChannel(t, ch.Id)

	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelTag{Tag: "cd-tag"})
	EnableTagChannels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("enable tag failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
		t.Fatal("tag enable left the channel cooling")
	}
}

// Saving an already-enabled channel (weight, model list, name) is not a
// re-enable and must not lift a live 429 cooldown.
func TestUpdateChannel_SaveWithoutStatusChangeKeepsCooldown(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := SeedV2Channel(t, ctx, "edit-keeps-cooldown")
	coolChannel(t, ch.Id)

	body := updateChannelBody(ch, "")
	body["status"] = common.ChannelStatusEnabled
	c, w := v1Ctx(http.MethodPut, "/api/channel/", body, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("legacy update failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(ch.Id, 0) == 0 {
		t.Fatal("legacy save of an enabled channel lifted its cooldown")
	}

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", ch.Id)
	w2 := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, map[string]interface{}{"name": "renamed"}, []string{"admin"})
	AssertV2Status(t, w2, http.StatusOK)
	if app.ChannelCoolingUntil(ch.Id, 0) == 0 {
		t.Fatal("v2 save of an enabled channel lifted its cooldown")
	}
}

// Flipping a disabled channel to enabled through either update path lifts it.
func TestUpdateChannel_EnableTransitionLiftsCooldown(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	legacy := SeedV2Channel(t, ctx, "legacy-reenable")
	v2 := SeedV2Channel(t, ctx, "v2-reenable")
	for _, id := range []int{legacy.Id, v2.Id} {
		ctx.DB.Model(&repo.Channel{}).Where("id = ?", id).Update("status", common.ChannelStatusManuallyDisabled)
	}
	coolChannel(t, legacy.Id)
	app.MarkChannelCooldown(v2.Id, 0, time.Now().Add(time.Hour).Unix())

	body := updateChannelBody(legacy, "")
	body["status"] = common.ChannelStatusEnabled
	c, w := v1Ctx(http.MethodPut, "/api/channel/", body, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("legacy update failed: %s", w.Body.String())
	}
	if app.ChannelCoolingUntil(legacy.Id, 0) != 0 {
		t.Fatal("legacy re-enable left the channel cooling")
	}

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", v2.Id)
	w2 := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, map[string]interface{}{"status": common.ChannelStatusEnabled}, []string{"admin"})
	AssertV2Status(t, w2, http.StatusOK)
	if app.ChannelCoolingUntil(v2.Id, 0) != 0 {
		t.Fatal("v2 re-enable left the channel cooling")
	}
}
