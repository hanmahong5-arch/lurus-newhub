package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// ============================================================================
// v2 channel save-time document validation (param_override, header_override,
// model_mapping, setting). See internal/adapter/provider/common/
// channel_config_validate.go. Drives the real CreateChannelV2/UpdateChannelV2
// handlers via the router harness used by TestUpdateChannelV2_PartialUpdate,
// not hand-built structs.
// ============================================================================

func TestUpdateChannelV2_ParamOverride_MalformedJSON_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Param Override Channel")

	body := map[string]interface{}{"param_override": "{"}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:param_override_invalid" {
		t.Errorf("expected code=channel:param_override_invalid, got %v (full body: %v)", resp["code"], resp)
	}

	// Row must be unchanged: param_override was nil before, still nil after.
	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ParamOverride != nil {
		t.Errorf("expected param_override to remain unset after a rejected update, got %q", *stored.ParamOverride)
	}
}

func TestUpdateChannelV2_ParamOverride_ValidOperationsDocument_Stored(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Param Override Channel Valid")

	body := map[string]interface{}{
		"param_override": `{"operations":[{"path":"temperature","mode":"set","value":0.1}]}`,
	}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ParamOverride == nil || *stored.ParamOverride == "" {
		t.Fatal("expected param_override to be stored")
	}
}

func TestUpdateChannelV2_HeaderOverride_NotAnObject_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Header Override Channel")

	body := map[string]interface{}{"header_override": "[1]"}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:header_override_invalid" {
		t.Errorf("expected code=channel:header_override_invalid, got %v", resp["code"])
	}
}

func TestUpdateChannelV2_ModelMapping_EmptyKey_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Model Mapping Channel")

	body := map[string]interface{}{"model_mapping": `{"":"x"}`}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:model_mapping_invalid" {
		t.Errorf("expected code=channel:model_mapping_invalid, got %v", resp["code"])
	}
}

func TestUpdateChannelV2_Setting_TypeMismatch_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Setting Channel")

	body := map[string]interface{}{"setting": `{"proxy":1}`}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:setting_invalid" {
		t.Errorf("expected code=channel:setting_invalid, got %v", resp["code"])
	}
}

func TestCreateChannelV2_ParamOverride_MalformedJSON_RejectedNoRow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":           "Bad Param Override Channel",
		"key":            "sk-bad-param-override",
		"models":         "gpt-4",
		"param_override": "{",
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)

	var count int64
	ctx.DB.Table("channels").Where("name = ?", "Bad Param Override Channel").Count(&count)
	if count != 0 {
		t.Errorf("expected no row to be created for a rejected create, got %d", count)
	}
}
