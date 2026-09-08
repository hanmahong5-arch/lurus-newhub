package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// Distribute's "channel == nil, err == nil" branch must tell apart two
// causes that both look like "no channel" to the selection call: the model
// was never configured for this group at all (a client error → 404), versus
// it was configured but every backing channel is currently disabled (a real
// outage → 503, unchanged from before).

// Test A: no ability row exists for (group, model) at all → 404 with
// model_not_found, not the old blanket 503.
func TestDistribute_ShouldSelect_ModelNeverConfigured_404(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistribute(r, `{"model":"this-model-does-not-exist-xyz"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a model with no configured ability; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model_not_found") {
		t.Errorf("body missing model_not_found code; body=%s", w.Body.String())
	}
	// L3-CONTRACT-TAXONOMY residual item 6: distributor.go's no-channel
	// rejection message was a bare Chinese sentence; it must render in
	// English on the wire like every other rejection this cycle standardised
	// on ("no available channel for model ... (distributor)"), not leak CJK.
	if !strings.Contains(w.Body.String(), "no available channel for model") {
		t.Errorf("body = %s, want the English no-available-channel message", w.Body.String())
	}
	if strings.ContainsAny(w.Body.String(), "分组渠道") {
		t.Errorf("body = %s, still leaks the retired Chinese no-channel message", w.Body.String())
	}
}

// Test B: an ability row exists for (group, model) but it's Enabled=false
// (the channel backing it is disabled) → selection still finds "no channel",
// but the existence probe must see the row and keep the response 503 — this
// is the genuine, possibly-transient outage case and must not regress to 404.
func TestDistribute_ShouldSelect_ChannelDisabledButAbilityExists_503(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Id: 30, Name: "disabled-svc", TenantId: "default", Key: "k",
		Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4o", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	// AddAbilities derives Enabled from channel.Status, so this leaves a
	// (group=default, model=gpt-4o) ability row with Enabled=false.
	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("add abilities: %v", err)
	}

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when the model is configured but its only channel is disabled; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no available channel for model") {
		t.Errorf("body = %s, want the English no-available-channel message", w.Body.String())
	}
	if strings.ContainsAny(w.Body.String(), "分组渠道") {
		t.Errorf("body = %s, still leaks the retired Chinese no-channel message", w.Body.String())
	}
}

// Test C: the probe's answer when it CANNOT answer. The caller starts at 503
// and only narrows to 404 on a true, so every unanswerable case must return
// false. Getting this direction backwards is silent and expensive: a DB blip
// during the probe would turn a real outage into a 404, telling every client
// to stop retrying a service that is merely unreachable. The baseline
// assertion below exists so that a probe which returns false for *everything*
// can't pass this test by accident.
func TestModelNeverConfigured_UnanswerableProbe_KeepsOutageReading(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{"model":"x"}`, "application/json")

	if !modelNeverConfigured(c, "default", "this-model-does-not-exist-xyz") {
		t.Fatal("baseline: a working DB with an empty ability table must answer true")
	}

	if modelNeverConfigured(c, "", "this-model-does-not-exist-xyz") {
		t.Error(`empty group: want false so the caller keeps 503, got true`)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}
	if modelNeverConfigured(c, "default", "this-model-does-not-exist-xyz") {
		t.Error("failed query: want false so the caller keeps 503, got true")
	}
}

// L3-CONTRACT-TAXONOMY: a token restricted to model_limits={"a"} requesting
// model "b" must 403 with the model_blocked code, and the wire-native type
// (permission_error) on both the OpenAI and the Anthropic envelope — not the
// retired new_api_error literal on either wire.
func mountDistributeModelLimit() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"a": true})
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat())
	v1.POST("/chat/completions", Distribute(), pass)
	v1.POST("/messages", Distribute(), pass)
	return r
}

func TestDistribute_ModelLimits_Blocked_OpenAIWire(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistributeModelLimit()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"permission_error"`) {
		t.Errorf("body = %s, want error.type permission_error", body)
	}
	if !strings.Contains(body, `"code":"model_blocked"`) {
		t.Errorf("body = %s, want error.code model_blocked", body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}

func TestDistribute_ModelLimits_Blocked_AnthropicWire(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistributeModelLimit()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"error":{`) {
		t.Errorf(`body = %s, want a Claude envelope ("type":"error","error":{...})`, body)
	}
	if !strings.Contains(body, `"type":"permission_error"`) {
		t.Errorf("body = %s, want the nested error.type permission_error", body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("body = %s, must not leak the retired new_api_error literal", body)
	}
}
