package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// Legacy console save path (POST/PUT /api/channel/), per plan doc §14
// amendment: validateChannelContent (channel.go:validateChannelContent) is
// reached from AddChannel/UpdateChannel via validateChannel, and now runs the
// same four config-document validators as the v2 handlers. Drives the real
// AddChannel/UpdateChannel handlers, mirroring registerV1ChannelRoutes in
// channel_v1_regression_test.go (same package).
// ============================================================================

func registerV1ChannelUpdateRoute(ctx *V2TestContext) {
	v1 := ctx.Router.Group("/api")
	v1.Use(func(c *gin.Context) {
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", ctx.AdminUser.Id)
		c.Next()
	})
	v1.PUT("/channel", UpdateChannel)
}

func registerV1ChannelTagEditRoute(ctx *V2TestContext) {
	v1 := ctx.Router.Group("/api")
	v1.Use(func(c *gin.Context) {
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", ctx.AdminUser.Id)
		c.Next()
	})
	v1.PUT("/channel/tag", EditTagChannels)
}

func TestAddChannel_MalformedParamOverride_RejectedNoRow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelRoutes(ctx)

	body := map[string]interface{}{
		"mode": "single",
		"channel": map[string]interface{}{
			"name":           "Legacy Bad Param Override",
			"key":            "sk-legacy-bad-param-override",
			"models":         "gpt-4",
			"param_override": "{",
		},
	}
	w := V2Request(ctx.Router, http.MethodPost, "/api/channel", body, nil)

	// Legacy shape: 200 + success:false, never a partial write.
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for a malformed param_override, body: %s", w.Body.String())
	}

	var count int64
	ctx.DB.Table("channels").Where("name = ?", "Legacy Bad Param Override").Count(&count)
	if count != 0 {
		t.Errorf("expected no row to be created for a rejected AddChannel, got %d", count)
	}
}

func TestAddChannel_ValidParamOverride_Saved(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelRoutes(ctx)

	body := map[string]interface{}{
		"mode": "single",
		"channel": map[string]interface{}{
			"name":           "Legacy Valid Param Override",
			"key":            "sk-legacy-valid-param-override",
			"models":         "gpt-4",
			"param_override": `{"operations":[{"path":"temperature","mode":"set","value":0.1}]}`,
		},
	}
	w := V2Request(ctx.Router, http.MethodPost, "/api/channel", body, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	if success, _ := resp["success"].(bool); !success {
		t.Fatalf("expected success=true for a valid param_override, body: %s", w.Body.String())
	}

	var count int64
	ctx.DB.Table("channels").Where("name = ?", "Legacy Valid Param Override").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row to be created, got %d", count)
	}
}

// TestUpdateChannel_UnknownOperationModeParamOverride_Rejected guards the
// legacy save path against a document that is syntactically valid JSON but
// semantically broken (an operation mode the override engine does not
// implement): validateChannelContent must dry-run it through the same engine
// the relay uses, not accept anything that merely parses as JSON.
func TestUpdateChannel_UnknownOperationModeParamOverride_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelUpdateRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Unknown Mode Channel")

	body := map[string]interface{}{
		"id":             channel.Id,
		"param_override": `{"operations":[{"path":"model","mode":"not_a_real_mode","value":"x"}]}`,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for an unknown operation mode, body: %s", w.Body.String())
	}

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ParamOverride != nil {
		t.Errorf("expected param_override to remain unset after a rejected update, got %q", *stored.ParamOverride)
	}
}

func TestUpdateChannel_MalformedParamOverride_RejectedRowUnchanged(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelUpdateRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Update Channel")

	body := map[string]interface{}{
		"id":             channel.Id,
		"param_override": "{",
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for a malformed param_override, body: %s", w.Body.String())
	}

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ParamOverride != nil {
		t.Errorf("expected param_override to remain unset after a rejected update, got %q", *stored.ParamOverride)
	}
}

// TestAddChannel_MalformedParamOverride_ResponseCarriesCode locks finding-2:
// the legacy 200+success:false body must carry the same field-naming "code"
// v2 already exposes, not just a message string. Reverting AddChannel to the
// bare gin.H{"success":false,"message":err.Error()} shape (no
// legacyChannelConfigErrorResponse) turns this red.
func TestAddChannel_MalformedParamOverride_ResponseCarriesCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelRoutes(ctx)

	body := map[string]interface{}{
		"mode": "single",
		"channel": map[string]interface{}{
			"name":           "Legacy Code Add Channel",
			"key":            "sk-legacy-code-add",
			"models":         "gpt-4",
			"param_override": "{",
		},
	}
	w := V2Request(ctx.Router, http.MethodPost, "/api/channel", body, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:param_override_invalid" {
		t.Errorf("expected code=channel:param_override_invalid, got %v (full body: %v)", resp["code"], resp)
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "channel:param_override_invalid") {
		t.Errorf("expected message to be prefixed with the code, got %q", msg)
	}
}

// TestUpdateChannel_MalformedParamOverride_ResponseCarriesCode is the
// UpdateChannel counterpart of the above.
func TestUpdateChannel_MalformedParamOverride_ResponseCarriesCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelUpdateRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Code Update Channel")

	body := map[string]interface{}{
		"id":             channel.Id,
		"param_override": "{",
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel", body, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if code, _ := resp["code"].(string); code != "channel:param_override_invalid" {
		t.Errorf("expected code=channel:param_override_invalid, got %v (full body: %v)", resp["code"], resp)
	}
}

// ============================================================================
// EditTagChannels (legacy bulk-by-tag save path), finding-3: this handler's
// param_override/header_override dry-run had no oracle (nothing exercised
// EditTagChannels through the real router), and model_mapping was not
// validated at all here. Both are fixed against the real handler below.
// ============================================================================

// TestEditTagChannels_UnknownOperationModeParamOverride_Rejected is the
// oracle finding-3 asked for: it goes red if EditTagChannels' param_override
// check is ever downgraded from relaycommon.ValidateParamOverride (a real
// dry-run) to a bare json.Valid, because "not_a_real_mode" is syntactically
// valid JSON and would then pass.
func TestEditTagChannels_UnknownOperationModeParamOverride_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelTagEditRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Tag Edit Channel")
	tag := "legacy-tag-edit"
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", channel.Id).Update("tag", &tag).Error; err != nil {
		t.Fatalf("failed to seed channel tag: %v", err)
	}

	body := map[string]interface{}{
		"tag":            tag,
		"param_override": `{"operations":[{"path":"model","mode":"not_a_real_mode","value":"x"}]}`,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel/tag", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for an unknown operation mode, body: %s", w.Body.String())
	}

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ParamOverride != nil {
		t.Errorf("expected param_override to remain unset after a rejected tag edit, got %q", *stored.ParamOverride)
	}
}

// TestEditTagChannels_EmptyModelMappingKey_Rejected locks the finding-3 gap:
// EditTagChannels previously never validated model_mapping at all.
func TestEditTagChannels_EmptyModelMappingKey_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelTagEditRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Tag Edit Mapping Channel")
	tag := "legacy-tag-edit-mapping"
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", channel.Id).Update("tag", &tag).Error; err != nil {
		t.Fatalf("failed to seed channel tag: %v", err)
	}

	body := map[string]interface{}{
		"tag":           tag,
		"model_mapping": `{"":"x"}`,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel/tag", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for an empty model_mapping key, body: %s", w.Body.String())
	}

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ModelMapping != nil {
		t.Errorf("expected model_mapping to remain unset after a rejected tag edit, got %q", *stored.ModelMapping)
	}
}

// TestEditTagChannels_ValidDocuments_Saved is the accept-path counterpart:
// a syntactically and semantically valid model_mapping must save (guards
// against an over-broad structural-error classification).
func TestEditTagChannels_ValidDocuments_Saved(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelTagEditRoute(ctx)

	channel := SeedV2Channel(t, ctx, "Legacy Tag Edit Valid Channel")
	tag := "legacy-tag-edit-valid"
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", channel.Id).Update("tag", &tag).Error; err != nil {
		t.Fatalf("failed to seed channel tag: %v", err)
	}

	body := map[string]interface{}{
		"tag":           tag,
		"model_mapping": `{"gpt-4":"gpt-4o"}`,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/channel/tag", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	if success, _ := resp["success"].(bool); !success {
		t.Fatalf("expected success=true for a valid model_mapping, body: %s", w.Body.String())
	}

	var stored repo.Channel
	if err := ctx.DB.Table("channels").Where("id = ?", channel.Id).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload channel: %v", err)
	}
	if stored.ModelMapping == nil || *stored.ModelMapping == "" {
		t.Fatal("expected model_mapping to be stored")
	}
}
