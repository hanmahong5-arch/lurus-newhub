package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// --- Unit tests for the shared content validator (validateChannelContent), the
// single source the v1 write path and the v2 create path now both funnel through.

func TestValidateChannelContent_ModelNameLengthGatedByIsAdd(t *testing.T) {
	ch := &repo.Channel{Key: "sk-x", Models: strings.Repeat("m", 256)}
	// On add, an over-long model name is rejected.
	if err := validateChannelContent(ch, true); err == nil {
		t.Error("isAdd: over-long model name must be rejected")
	}
	// On update (isAdd=false), the model-length check is skipped — matching the
	// v1 validateChannel semantics (length is only checked on add).
	if err := validateChannelContent(ch, false); err != nil {
		t.Errorf("update: model-length must not be enforced, got %v", err)
	}
}

func TestValidateChannelContent_VertexAIRegion(t *testing.T) {
	base := func(other string) *repo.Channel {
		return &repo.Channel{Key: "sk-x", Models: "gemini-pro", Type: constant.ChannelTypeVertexAi, Other: other}
	}
	if err := validateChannelContent(base(""), true); err == nil {
		t.Error("VertexAI with empty region must be rejected")
	}
	if err := validateChannelContent(base("not-json"), true); err == nil {
		t.Error("VertexAI with malformed region JSON must be rejected")
	}
	if err := validateChannelContent(base(`{"region2":"us-east1"}`), true); err == nil {
		t.Error("VertexAI region without a default key must be rejected")
	}
	if err := validateChannelContent(base(`{"default":"us-central1"}`), true); err != nil {
		t.Errorf("valid VertexAI region should pass, got %v", err)
	}
	// VertexAI rules are not gated by isAdd — they apply on update too, matching v1.
	if err := validateChannelContent(base(""), false); err == nil {
		t.Error("VertexAI region rules must apply regardless of isAdd")
	}
}

func TestValidateChannelContent_NonVertexChannelPasses(t *testing.T) {
	ch := &repo.Channel{Key: "sk-x", Models: "gpt-4", Type: 1}
	if err := validateChannelContent(ch, true); err != nil {
		t.Errorf("a normal channel should pass content validation, got %v", err)
	}
}

// --- Integration tests: prove CreateChannelV2 now funnels through the shared
// validator. Previously the v2 create path only checked settings and would
// accept an over-long model name or a VertexAI channel with no valid region.

func TestCreateChannelV2_ModelNameTooLong(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "long-model",
		"key":    "sk-test-key",
		"models": strings.Repeat("m", 256),
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "模型名称过长") {
		t.Errorf("expected over-long model rejection, got %q", msg)
	}
}

func TestCreateChannelV2_VertexAIRequiresRegion(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "vertex",
		"key":    "sk-test-key",
		"models": "gemini-pro",
		"type":   constant.ChannelTypeVertexAi,
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "部署地区") {
		t.Errorf("expected VertexAI region rejection, got %q", msg)
	}
}

func TestCreateChannelV2_VertexAIValidRegionSucceeds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "vertex-ok",
		"key":    "sk-test-key",
		"models": "gemini-pro",
		"type":   constant.ChannelTypeVertexAi,
		"other":  `{"default":"us-central1"}`,
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusCreated)
}
