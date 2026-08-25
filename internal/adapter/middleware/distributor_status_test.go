package middleware

import (
	"net/http"
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
