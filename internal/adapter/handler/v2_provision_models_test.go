package handler

// v2_provision_models_test.go — ProvisionV2's entitlement models gate
// (ent.models -> Token.ModelLimits, enforced downstream by
// middleware.Distribute's existing model_blocked check) and the plan-change
// sibling-token revocation. The revocation oracle drives the real TokenAuth
// middleware chain against a Redis-backed cache so the claim "a plan change
// revokes the previous key even with a warm cache" is proven through the
// live authentication path, not by asserting on the DB row alone.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// TestProvisionV2_ModelsClaim_SetsModelLimits: a JSON-array ent.models claim
// mints a token with ModelLimitsEnabled=true and ModelLimits set to the
// trimmed, deduplicated CSV — the exact claim, not merely "non-empty".
func TestProvisionV2_ModelsClaim_SetsModelLimits(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991001", map[string]string{
			"plan_code": "cc_pro",
			"quota":     "1000",
			"models":    `["model-a ", " model-a", "model-b"]`,
		}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(991001)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if !row.ModelLimitsEnabled {
		t.Error("expected model_limits_enabled=true for a non-empty ent.models claim")
	}
	if row.ModelLimits != "model-a,model-b" {
		t.Errorf("model_limits=%q want %q (trimmed, deduplicated, first-seen order)", row.ModelLimits, "model-a,model-b")
	}
}

// TestProvisionV2_ModelsClaim_RefreshInPlaceApplies: the expired-but-Enabled
// refresh branch must apply the NEW call's models claim, not freeze the one
// captured at the original mint.
func TestProvisionV2_ModelsClaim_RefreshInPlaceApplies(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	prevMaxMB := constant.MaxRequestBodyMB
	if constant.MaxRequestBodyMB <= 0 {
		constant.MaxRequestBodyMB = 64
	}
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMaxMB })

	// B-F6 oracle: this branch used to write expired_time/model_limits* via a
	// bare GORM map Updates(), which does not touch the token's Redis cache
	// entry — a warm cache would keep enforcing the OLD model list until the
	// cache entry aged out on its own. Run against a real Redis-backed cache
	// (miniredis) so the fix (existing.Update(), which refreshes the cache)
	// is proven through the live TokenAuth+Distribute path, not just the DB row.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	prevRDB, prevRedisEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = rdb, true
	t.Cleanup(func() { common.RDB, common.RedisEnabled = prevRDB, prevRedisEnabled })

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991002", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": `["model-a"]`}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991002)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	data, _ := resp["data"].(map[string]any)
	tokenStr, _ := data["token"].(string)
	if tokenStr == "" {
		t.Fatalf("missing token in response: %v", resp)
	}

	authRouter := gin.New()
	authRouter.Use(gin.Recovery())
	relay := authRouter.Group("/v1")
	relay.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	relay.POST("/chat/completions", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	probeModel := func(model string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		w := httptest.NewRecorder()
		authRouter.ServeHTTP(w, req)
		return w.Code
	}

	// Warm the cache on the ORIGINAL model list before aging/refreshing.
	if c := probeModel("model-b"); c != http.StatusForbidden {
		t.Fatalf("warm-up: model-b must be blocked under the original [model-a] list, got %d", c)
	}

	// Age the token past its own expiry while leaving Status Enabled — the
	// routine re-provision shape TestProvisionV2_ExpiredEnabledTokenRefreshedNotReminted pins.
	if err := repo.DB.Model(&repo.Token{}).
		Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").
		Update("expired_time", time.Now().Add(-1*time.Hour).Unix()).Error; err != nil {
		t.Fatalf("simulate expiry: %v", err)
	}

	tok2 := provSignRS256(t, key, "test-kid",
		provClaims("991002", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": `["model-b","model-c"]`}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	if code != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d body=%v", code, resp)
	}
	if data, _ := resp["data"].(map[string]any); data["replayed"] != true {
		t.Errorf("expected replayed=true on refresh, got %v", resp["data"])
	}

	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.ModelLimits != "model-b,model-c" {
		t.Errorf("model_limits=%q want %q — refresh must apply the NEW claim's model list", row.ModelLimits, "model-b,model-c")
	}

	// The oracle: the SAME token key, through the SAME warm Redis cache
	// entry, must now enforce the REFRESHED list — model-b allowed (not
	// 403 model_blocked), model-a (only in the pre-refresh list) blocked.
	// A stale cache (the pre-fix bare-map write) would get this backwards.
	if c := probeModel("model-b"); c == http.StatusForbidden {
		t.Fatal("model-b must be allowed after refresh — the warm Redis cache must reflect the NEW model list, not the stale one")
	}
	if c := probeModel("model-a"); c != http.StatusForbidden {
		t.Fatalf("model-a must be blocked after refresh (dropped from the list) — the warm Redis cache must not keep serving the OLD model list, got %d", c)
	}
}

// TestProvisionV2_ModelsClaim_ReplayReconciles: a plain replay (still
// Enabled, still unexpired) whose entitlement models claim changed between
// calls must still reconcile the token's ModelLimits — not just the refresh
// branch.
func TestProvisionV2_ModelsClaim_ReplayReconciles(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991003", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": `["model-a"]`}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991003)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}

	tok2 := provSignRS256(t, key, "test-kid",
		provClaims("991003", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": `["model-x","model-y"]`}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	if code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["replayed"] != true {
		t.Errorf("expected replayed=true, got %v", data["replayed"])
	}

	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.ModelLimits != "model-x,model-y" {
		t.Errorf("model_limits=%q want %q — a plain replay must still reconcile a changed models claim", row.ModelLimits, "model-x,model-y")
	}

	var n int64
	repo.DB.Model(&repo.Token{}).Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").Count(&n)
	if n != 1 {
		t.Errorf("reconcile must not mint a duplicate row: count=%d want 1", n)
	}
}

// TestProvisionV2_NoModelsClaim_Unrestricted is the regression guard: no
// ent.models key at all keeps today's unrestricted behaviour.
func TestProvisionV2_NoModelsClaim_Unrestricted(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991004", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991004)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.ModelLimitsEnabled {
		t.Errorf("expected model_limits_enabled=false with no ent.models claim, got true limits=%q", row.ModelLimits)
	}
	if row.ModelLimits != "" {
		t.Errorf("model_limits=%q want empty", row.ModelLimits)
	}
}

// TestProvisionV2_MalformedModelsClaim_FailsClosed: a non-JSON-array
// ent.models value must degrade to MORE restriction (a CSV-split allowlist),
// never to unrestricted.
func TestProvisionV2_MalformedModelsClaim_FailsClosed(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991005", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": "model-x, model-y"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991005)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if !row.ModelLimitsEnabled {
		t.Fatal("malformed ent.models claim must fail CLOSED (restricted), not unrestricted")
	}
	if row.ModelLimits != "model-x,model-y" {
		t.Errorf("model_limits=%q want %q (CSV fallback, trimmed)", row.ModelLimits, "model-x,model-y")
	}
}

// TestProvisionV2_PlanChange_DisablesSiblingToken: minting a token for a
// SECOND plan_code (same platform account) must disable the FIRST plan's
// token, with a token.status_changed audit row naming the revoked token and
// reason:plan_changed.
func TestProvisionV2_PlanChange_DisablesSiblingToken(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}); err != nil {
		t.Fatalf("automigrate audit_events: %v", err)
	}
	pinAuditWriter(t, ctx.DB)

	tokA := provSignRS256(t, key, "test-kid",
		provClaims("991006", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokA})
	if code != http.StatusOK {
		t.Fatalf("plan A: expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991006)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var rowA repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&rowA).Error; err != nil {
		t.Fatalf("token A not found: %v", err)
	}

	tokB := provSignRS256(t, key, "test-kid",
		provClaims("991006", map[string]string{"plan_code": "cc_basic", "quota": "500"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokB})
	if code != http.StatusOK {
		t.Fatalf("plan B: expected 200, got %d body=%v", code, resp)
	}

	var rowAAfter repo.Token
	if err := repo.DB.First(&rowAAfter, rowA.Id).Error; err != nil {
		t.Fatalf("token A refetch: %v", err)
	}
	if rowAAfter.Status != common.TokenStatusDisabled {
		t.Errorf("token A status=%d want Disabled(%d) after a plan change", rowAAfter.Status, common.TokenStatusDisabled)
	}

	ev := pollAuditRow(t, governance.ActionTokenStatusChanged, 2*time.Second)
	if ev == nil {
		t.Fatal("no token.status_changed audit event recorded for the sibling disable")
	}
	if ev.ResourceID != rowA.Id {
		t.Errorf("audit resource_id=%d want %d (token A)", ev.ResourceID, rowA.Id)
	}
	if !strings.Contains(ev.Details, `"reason":"plan_changed"`) {
		t.Errorf("audit details=%s want reason=plan_changed", ev.Details)
	}
	if !strings.Contains(ev.Details, `"to":"cc_basic"`) {
		t.Errorf("audit details=%s want to=cc_basic", ev.Details)
	}
}

// TestProvisionV2_PlanChange_SiblingKeyRejectedThroughTokenAuthWithRedis is
// the REAL-CHAIN oracle: a warm Redis-backed token cache must NOT keep
// authenticating a key that a plan change just revoked. Mutation: replacing
// the sibling disable's (*repo.Token).Update() with a bulk UPDATE leaves the
// cache entry stale and this probe stays 200.
func TestProvisionV2_PlanChange_SiblingKeyRejectedThroughTokenAuthWithRedis(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	prevRDB, prevRedisEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = rdb, true
	t.Cleanup(func() { common.RDB, common.RedisEnabled = prevRDB, prevRedisEnabled })

	tokA := provSignRS256(t, key, "test-kid",
		provClaims("991007", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokA})
	if code != http.StatusOK {
		t.Fatalf("plan A: expected 200, got %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	keyA, _ := data["token"].(string)
	if keyA == "" {
		t.Fatalf("missing token A in response: %v", resp)
	}

	authRouter := gin.New()
	authRouter.Use(gin.Recovery())
	authRouter.POST("/v1/chat/completions", middleware.TokenAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	probe := func(k string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer "+k)
		w := httptest.NewRecorder()
		authRouter.ServeHTTP(w, req)
		return w.Code
	}

	// Warm the Redis cache for token A BEFORE the plan change.
	if c := probe(keyA); c != http.StatusOK {
		t.Fatalf("warm-up relay with token A: want 200, got %d", c)
	}

	tokB := provSignRS256(t, key, "test-kid",
		provClaims("991007", map[string]string{"plan_code": "cc_basic", "quota": "500"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokB})
	if code != http.StatusOK {
		t.Fatalf("plan B: expected 200, got %d body=%v", code, resp)
	}

	// The oracle: token A's warm cache entry must have been refreshed to
	// Disabled by the sibling-revocation path, not just the DB row.
	if c := probe(keyA); c == http.StatusOK {
		t.Fatal("token A must be rejected after a plan change disabled it — the warm Redis cache must not keep authenticating it")
	}
}

// TestProvisionV2_SamePlanReplay_DoesNotSelfDisable: replaying the SAME
// plan_code must not treat the token's own row as a sibling to revoke — the
// sibling query excludes name == tokenName.
func TestProvisionV2_SamePlanReplay_DoesNotSelfDisable(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991008", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991008)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}

	tok2 := provSignRS256(t, key, "test-kid",
		provClaims("991008", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok2})
	if code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d body=%v", code, resp)
	}
	if data, _ := resp["data"].(map[string]any); data["replayed"] != true {
		t.Errorf("expected replayed=true, got %v", resp["data"])
	}

	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.Status != common.TokenStatusEnabled {
		t.Errorf("token status=%d want Enabled(%d) — a same-plan replay must not self-disable", row.Status, common.TokenStatusEnabled)
	}
}

// TestProvisionV2_MalformedModelsClaim_ZeroEntries_RestrictsAll: a PRESENT
// ent.models claim that normalizes to zero usable model names (a blank
// string, a comma with nothing between, or a JSON array of only blank
// strings) must still fail CLOSED — ModelLimitsEnabled=true with an empty
// ModelLimits, which middleware.Distribute's model_blocked gate reads as "no
// model allowed", never the opposite. Before this fix,
// `modelLimitsEnabled := len(models) > 0` made every one of these three
// shapes mint an UNRESTRICTED token (B-F1) — TestProvisionV2_MalformedModelsClaim_FailsClosed's
// single "model-x, model-y" case happened to yield two entries and never
// exercised this hole.
func TestProvisionV2_MalformedModelsClaim_ZeroEntries_RestrictsAll(t *testing.T) {
	// The real-chain probe below drives middleware.TokenAuth()+Distribute(),
	// whose body-size guard defaults to 0 MB (rejecting every body) until
	// config init runs, which unit tests never trigger — same idiom as
	// tenant_model_allowlist_test.go/task_generic_test.go in this package.
	prevMaxMB := constant.MaxRequestBodyMB
	if constant.MaxRequestBodyMB <= 0 {
		constant.MaxRequestBodyMB = 64
	}
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMaxMB })

	cases := []struct {
		name, sub, models string
	}{
		{"blank", "991010", " "},
		{"comma_only", "991011", ","},
		{"json_blank_array", "991012", `[""]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ctx, key := setupProvisionTest(t)
			tok := provSignRS256(t, key, "test-kid",
				provClaims(tc.sub, map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": tc.models}))
			code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
			if code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%v", code, resp)
			}
			data, _ := resp["data"].(map[string]any)
			tokenStr, _ := data["token"].(string)
			if tokenStr == "" {
				t.Fatalf("missing token in response: %v", resp)
			}

			acctID, parseErr := strconv.ParseInt(tc.sub, 10, 64)
			if parseErr != nil {
				t.Fatalf("bad test sub: %v", parseErr)
			}
			user, err := repo.GetUserByLurusAccountID(acctID)
			if err != nil {
				t.Fatalf("bridged user not created: %v", err)
			}
			var row repo.Token
			if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&row).Error; err != nil {
				t.Fatalf("relay token not found: %v", err)
			}
			if !row.ModelLimitsEnabled {
				t.Fatal("a present-but-blank ent.models claim must fail CLOSED (model_limits_enabled=true), not unrestricted")
			}
			if row.ModelLimits != "" {
				t.Errorf("model_limits=%q want empty (zero usable names survive normalization)", row.ModelLimits)
			}

			// Real-chain probe: the token must be blocked for ANY model, not
			// just failing DB assertions.
			authRouter := gin.New()
			authRouter.Use(gin.Recovery())
			relay := authRouter.Group("/v1")
			relay.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
			relay.POST("/chat/completions", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"any-model"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tokenStr)
			w := httptest.NewRecorder()
			authRouter.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 model_blocked for ANY model on a zero-entry restricted token, got %d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"code":"model_blocked"`) {
				t.Errorf("body=%s want error.code model_blocked", w.Body.String())
			}
		})
	}
}

// TestProvisionV2_ModelsClaim_TooLong_500: an ent.models claim that joins to
// more than 1024 characters (tokens.model_limits is varchar(1024)) fails the
// request with 500 PROVISION_FAILED and mints no token row, instead of
// silently truncating or letting the column write fail later (A-F2: this
// guard existed but had no test — deleting the branch turned nothing red).
func TestProvisionV2_ModelsClaim_TooLong_500(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	names := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		names = append(names, "model-too-long-name-"+strconv.Itoa(i))
	}
	claimCSV := strings.Join(names, ",")
	if len(claimCSV) <= 1024 {
		t.Fatalf("test fixture too short: csv len=%d want >1024", len(claimCSV))
	}

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991013", map[string]string{"plan_code": "cc_pro", "quota": "1000", "models": claimCSV}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%v", code, resp)
	}
	if resp["error_code"] != "PROVISION_FAILED" {
		t.Errorf("expected PROVISION_FAILED, got %v", resp["error_code"])
	}

	user, err := repo.GetUserByLurusAccountID(991013)
	if err != nil {
		t.Fatalf("bridged user lookup: %v", err)
	}
	var n int64
	repo.DB.Model(&repo.Token{}).Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").Count(&n)
	if n != 0 {
		t.Errorf("a too-long models claim must not mint a token: count=%d want 0", n)
	}
}

// TestProvisionV2_PlanChange_ReturnToPreviousPlan_Reprovisions: A -> B -> A
// must succeed and leave exactly one ENABLED switch-provision-cc_pro row.
// Before this fix (A-F1), the sibling-disable from A->B wrote
// Status=Disabled on token A under its CANONICAL name, and the very next A
// provision hit the TOKEN_REVOKED guard meant for an administrator's
// disable — permanently locking the account out of both plans. The fix
// renames a plan_changed-superseded sibling away from its canonical name in
// the same write that disables it, so this third call finds no canonical
// row and mints a fresh one; an administratively disabled row (see
// TestProvisionV2_RevokedTokenNotResurrected) keeps its canonical name and
// is still refused.
func TestProvisionV2_PlanChange_ReturnToPreviousPlan_Reprovisions(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tokA1 := provSignRS256(t, key, "test-kid",
		provClaims("991014", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokA1})
	if code != http.StatusOK {
		t.Fatalf("plan A (1st): expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991014)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}

	tokB := provSignRS256(t, key, "test-kid",
		provClaims("991014", map[string]string{"plan_code": "cc_basic", "quota": "500"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokB})
	if code != http.StatusOK {
		t.Fatalf("plan B: expected 200, got %d body=%v", code, resp)
	}

	tokA2 := provSignRS256(t, key, "test-kid",
		provClaims("991014", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokA2})
	if code != http.StatusOK {
		t.Fatalf("plan A (return): expected 200, got %d body=%v (must re-provision, not TOKEN_REVOKED)", code, resp)
	}

	var canonicalA repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&canonicalA).Error; err != nil {
		t.Fatalf("no canonical switch-provision-cc_pro row after returning to plan A: %v", err)
	}
	if canonicalA.Status != common.TokenStatusEnabled {
		t.Errorf("canonical plan-A row status=%d want Enabled(%d)", canonicalA.Status, common.TokenStatusEnabled)
	}

	var enabledCount int64
	repo.DB.Model(&repo.Token{}).
		Where("user_id = ? AND name LIKE ? AND status = ?", user.Id, "switch-provision-%", common.TokenStatusEnabled).
		Count(&enabledCount)
	if enabledCount != 1 {
		t.Errorf("expected exactly 1 enabled switch-provision-* token after A->B->A, got %d", enabledCount)
	}
}

// TestProvisionV2_FailedProvisionKeepsSiblingEnabled pins the ORDER of the
// sibling reconcile. It runs only after the mint/refresh/replay branch has
// succeeded, so a call that ends in an error cannot leave the account with
// zero working keys. The cheapest reachable failure is the administrative
// revocation branch: the plan being provisioned already has a row an admin
// disabled, which answers 403 — and the OTHER plan's key must still work.
func TestProvisionV2_FailedProvisionKeepsSiblingEnabled(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	// Plan A: provisioned normally, stays the user's working key.
	tokA := provSignRS256(t, key, "test-kid",
		provClaims("991009", map[string]string{"plan_code": "cc_pro", "quota": "1000"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokA})
	if code != http.StatusOK {
		t.Fatalf("plan A: expected 200, got %d body=%v", code, resp)
	}
	user, err := repo.GetUserByLurusAccountID(991009)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	var rowA repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").First(&rowA).Error; err != nil {
		t.Fatalf("token A not found: %v", err)
	}

	// Plan B already exists for this user and an admin took it off Enabled.
	rowB := &repo.Token{
		UserId: user.Id, TenantId: ctx.TenantID, Key: common.GetRandomString(48),
		Name: "switch-provision-cc_basic", Status: common.TokenStatusDisabled,
		CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := repo.DB.Create(rowB).Error; err != nil {
		t.Fatalf("seed revoked token B: %v", err)
	}

	tokB := provSignRS256(t, key, "test-kid",
		provClaims("991009", map[string]string{"plan_code": "cc_basic", "quota": "500"}))
	code, resp = provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tokB})
	if code != http.StatusForbidden {
		t.Fatalf("provisioning a plan whose token was administratively revoked: got %d body=%v, want 403", code, resp)
	}

	var rowAAfter repo.Token
	if err := repo.DB.First(&rowAAfter, rowA.Id).Error; err != nil {
		t.Fatalf("token A refetch: %v", err)
	}
	if rowAAfter.Status != common.TokenStatusEnabled {
		t.Errorf("token A status=%d, want Enabled(%d): a provision call that failed must not have revoked the user's working key",
			rowAAfter.Status, common.TokenStatusEnabled)
	}
	if rowAAfter.Name != "switch-provision-cc_pro" {
		t.Errorf("token A name=%q, want the canonical name: a failed call must not have renamed it away", rowAAfter.Name)
	}
}
