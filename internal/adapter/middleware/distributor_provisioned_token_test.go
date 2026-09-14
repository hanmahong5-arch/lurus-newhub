package middleware

// distributor_provisioned_token_test.go — REAL-CHAIN lock for L2: a relay
// token minted with ModelLimitsEnabled/ModelLimits set (the shape ProvisionV2
// now stamps from an entitlement's ent.models claim, handler/v2_provision.go)
// is enforced end to end through the ACTUAL TokenAuth() -> Distribute()
// chain, not by hand-setting the model-limit context keys the way
// distributor_status_test.go's mountDistributeModelLimit does. This lives in
// the middleware package (not handler) so the chain under test is the real
// production mount order (relay-router.go), independent of how a token row
// got its ModelLimits — this test seeds the row directly rather than calling
// ProvisionV2.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// mountProvisionedTokenAuthDistribute wires the real relay auth+selection
// chain: TokenAuth() resolves the DB row (including ModelLimitsEnabled/
// ModelLimits) into the per-request context keys, then Distribute() reads
// them back out — the same TokenAuth-then-Distribute order
// handler/router/relay-router.go:100-121 mounts for the httpRouter branch
// that serves /v1/chat/completions. The production chain additionally mounts
// PoolBalanceCheck/CostSpikeLimit/EntitlementCheck and the rate limiters
// between TokenAuth and Distribute; this harness omits them since they are
// not part of what this test locks (the model-limit gate itself).
func mountProvisionedTokenAuthDistribute() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat())
	v1.POST("/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func provisionedModelLimitProbe(t *testing.T, r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestDistributor_ProvisionedTokenBlockedModel_403: a token whose
// ModelLimits (as ProvisionV2 now mints it) does not include the requested
// model is rejected 403 model_blocked through the REAL TokenAuth+Distribute
// chain — the mutation this locks: removing the mint's ModelLimitsEnabled
// assignment (or Distribute's read of it) turns this into a pass-through.
func TestDistributor_ProvisionedTokenBlockedModel_403(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "switch-model-limit-user", DisplayName: "u", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "switch-model-limit@local", TenantId: "default",
		Quota: 1_000_000, Group: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled,
		Name: "switch-provision-cc_pro", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true, Group: "default",
		ModelLimitsEnabled: true, ModelLimits: "model-a,model-b",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := mountProvisionedTokenAuthDistribute()

	w := provisionedModelLimitProbe(t, r, key, `{"model":"model-c"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("blocked model: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"model_blocked"`) {
		t.Errorf("blocked model: body = %s, want error.code model_blocked", w.Body.String())
	}
}

// TestDistributor_ProvisionedTokenAllowedModel_NotBlocked is the regression
// half: a model that IS in the CSV must pass the model-limit gate (i.e. the
// restriction is exactly the CSV, not "any non-empty ModelLimits blocks
// everything"). It still 404s past that gate (no channel configured in this
// hermetic DB) — that 404, not a 403 model_blocked, is what proves the gate
// let it through.
func TestDistributor_ProvisionedTokenAllowedModel_NotBlocked(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "switch-model-limit-user-2", DisplayName: "u", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "switch-model-limit-2@local", TenantId: "default",
		Quota: 1_000_000, Group: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled,
		Name: "switch-provision-cc_pro", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true, Group: "default",
		ModelLimitsEnabled: true, ModelLimits: "model-a,model-b",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := mountProvisionedTokenAuthDistribute()

	w := provisionedModelLimitProbe(t, r, key, `{"model":"model-a"}`)
	if w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "model_blocked") {
		t.Fatalf("allowed model was blocked: status = %d, body=%s", w.Code, w.Body.String())
	}
}
