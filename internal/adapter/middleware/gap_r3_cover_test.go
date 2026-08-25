package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// gzip.go: readCloser.Close nil-closeFn arm (:22) + Decompress maxMB<=0 (:32)
// ---------------------------------------------------------------------------

func TestReadCloser_Close_BothArms(t *testing.T) {
	// closeFn == nil → returns nil (the uncovered production arm; every prod
	// construction sets closeFn, so only a direct test exercises it).
	rc := &readCloser{Reader: strings.NewReader("payload")}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close with nil closeFn = %v, want nil", err)
	}
	// closeFn set → its error propagates verbatim.
	sentinel := errors.New("close-boom")
	rc2 := &readCloser{Reader: strings.NewReader("x"), closeFn: func() error { return sentinel }}
	if err := rc2.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close with closeFn = %v, want %v", err, sentinel)
	}
}

func TestDecompressMiddleware_DefaultCapWhenUnset(t *testing.T) {
	// The maxMB<=0 → default(32) guard only runs when the config global is
	// non-positive. Force that state, drive an uncompressed request through,
	// and assert the body still reaches the handler intact.
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 0
	defer func() { constant.MaxRequestBodyMB = prev }()

	r := gin.New()
	r.POST("/x", DecompressRequestMiddleware(), func(c *gin.Context) {
		b, _ := c.GetRawData()
		c.String(http.StatusOK, string(b))
	})
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello-body"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hello-body" {
		t.Fatalf("status=%d body=%q, want 200/hello-body", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// auth.go: SetupContextForToken nil guard (:456)
// ---------------------------------------------------------------------------

func TestSetupContextForToken_NilToken_Errors(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if err := SetupContextForToken(c, nil); err == nil {
		t.Fatal("SetupContextForToken(nil) = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// auth.go TokenAuth: gemini x-goog-api-key (:341) + mj-api-secret Bearer (:352)
// ---------------------------------------------------------------------------

func TestTokenAuth_GeminiGoogHeaderExtraction(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	_, key, _ := seedUserToken(t)
	// Gemini path with no ?key= query but an x-goog-api-key header → mapped to
	// a bearer identity (relay auth parity across Gemini credential styles).
	r := mountTokenAuth("/v1beta/models/gemini-2.0-flash")
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models/gemini-2.0-flash", nil)
	req.Header.Set("x-goog-api-key", "sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for x-goog-api-key auth; body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuth_MjApiSecretBearerExtraction(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	_, key, _ := seedUserToken(t)
	// No Authorization header → fall to mj-api-secret, which itself carries the
	// "Bearer " prefix (the uncovered strip branch).
	r := mountTokenAuth("/mj/submit/imagine")
	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{"prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("mj-api-secret", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for mj-api-secret Bearer auth; body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// entitlement.go: live fetch returns quota_remaining=0 → deny 429 (:77)
// ---------------------------------------------------------------------------

func TestEntitlementCheck_LiveFetch_QuotaExhausted_429(t *testing.T) {
	const acct = int64(990077)
	entitlementCache.Delete(acct)
	defer entitlementCache.Delete(acct)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"quota_remaining":"0"}`))
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("identity_account_id", acct); c.Next() })
	r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 when platform reports quota exhausted; body=%s", w.Code, w.Body.String())
	}
	// The deny decision must be cached so the next request short-circuits.
	val, ok := entitlementCache.Load(acct)
	if !ok || val.(entitlementEntry).allowed {
		t.Errorf("expected cached deny entry after live exhausted fetch, got %+v (ok=%v)", val, ok)
	}
}

// ---------------------------------------------------------------------------
// pool_balance_check.go: transient DB error → fail open (:55)
// ---------------------------------------------------------------------------

func TestPoolBalanceCheck_DBError_FailsOpen(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	// Drop the pool table so the lookup returns a non-ErrPoolNotFound error.
	if err := db.Migrator().DropTable(&entity.TenantCreditPool{}); err != nil {
		t.Fatalf("drop pool table: %v", err)
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "t-db-err", UserID: 1})
		c.Next()
	})
	r.GET("/relay", PoolBalanceCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/relay", nil))

	// Fail-open contract: a transient repo error must NOT block traffic (the
	// atomic debit remains the over-spend safety net).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (pool gate fails open on DB error); body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// cost_spike.go: breach where auto-disable itself errors (:55) still 429
// ---------------------------------------------------------------------------

func TestCostSpikeLimit_Breach_DisableError_Still429(t *testing.T) {
	db, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 50000
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
	}()

	const uid = 553311
	nowMs := time.Now().UnixMilli()
	if err := rdb.ZAdd(context.Background(), costSpikeKey(uid), redis.Z{
		Score:  float64(nowMs),
		Member: fmt.Sprintf("%d:%d", nowMs, 90000), // over limit
	}).Err(); err != nil {
		t.Fatalf("seed zset: %v", err)
	}

	// Drop users so DisableUserById errors — the breach must still 429 (the
	// disable failure is logged, never swallowing the safety rejection).
	if err := db.Migrator().DropTable(&repo.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}

	w := runCostSpike(uid)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 on breach even when disable errors; body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// distributor.go Distribute: bad body → 400 (:34); model-limit non-map (:66)
// ---------------------------------------------------------------------------

func TestDistribute_BadBody_400(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountDistribute(nil)
	w := doDistribute(r, `{not-json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unparseable request body; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_ModelLimit_NonMapValue_Rejects(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// ContextKeyTokenModelLimit set to a non-map value → the type assertion
	// falls back to an empty allowlist → deny-all → 403 (fail closed).
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, "not-a-map")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when model-limit value is not a map; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_AutoGroup_ModelNeverConfigured_404(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// The old name and comment here claimed this exercised "no auto-groups
	// configured → selection returns an error → 503". It never did: autoGroups
	// defaults to ["default"] at package scope (internal/pkg/setting/auto_group.go)
	// and nothing in this test clears it, so the len(GetAutoGroups())==0
	// fail-fast in channel_select.go is unreachable from here. What actually
	// runs is the loop over ["default"], which finds nothing in the empty test
	// DB and falls out to (channel=nil, err=nil) — the branch this file's
	// sibling change is about. With no ability row ever configured for
	// (default, this model), 404 is the correct answer; the previous 503 was
	// the conflated behavior, not a property worth preserving.
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an auto group whose concrete groups never configured the model; body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// distributor.go getModelRequest: MJ + video + audio error/model branches
// ---------------------------------------------------------------------------

func TestGetModelRequest_MJImagine_BadBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/mj/submit/imagine", `{bad`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for malformed midjourney body")
	}
}

func TestGetModelRequest_MJAction_MissingCustomId_Errors(t *testing.T) {
	// /mj/submit/action with no customId → GetMjRequestModel returns a mj error.
	c, _ := newTestContext(http.MethodPost, "/mj/submit/action", `{}`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for mj action without custom_id")
	}
}

func TestGetModelRequest_MJImagine_ResolvesModel(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/mj/submit/imagine", `{"prompt":"a cat"}`, "application/json")
	mr, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model == "" {
		t.Errorf("expected a resolved mj model for imagine, got empty")
	}
	if !shouldSelect {
		t.Errorf("imagine submit should select a channel")
	}
}

func TestGetModelRequest_VideosPost_ModelFromBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/videos", `{"model":"sora-2"}`, "application/json")
	mr, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "sora-2" {
		t.Errorf("model = %q, want sora-2", mr.Model)
	}
	if !shouldSelect {
		t.Errorf("POST /v1/videos should select a channel")
	}
}

func TestGetModelRequest_VideosPost_BadBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/videos", `{bad`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for malformed /v1/videos POST body")
	}
}

func TestGetModelRequest_VideoGenerationsPost_BadBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/video/generations", `{bad`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for malformed /v1/video/generations POST body")
	}
}

func TestGetModelRequest_AudioMusicSubmit_BadBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/audio/music", `{bad`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for malformed /v1/audio/music submit body")
	}
}

func TestGetModelRequest_AudioTranslations_ModelFromBody(t *testing.T) {
	// A model present in the body overrides the whisper-1 default.
	c, _ := newTestContext(http.MethodPost, "/v1/audio/translations", `{"model":"whisper-large"}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "whisper-large" {
		t.Errorf("model = %q, want whisper-large", mr.Model)
	}
}

func TestGetModelRequest_PGChatCompletions_BadBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/pg/chat/completions", `{bad`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatal("expected error for malformed /pg/chat/completions body")
	}
}

// Distribute /pg path where the requested group IS allowed (equals the using
// group) → the group is adopted and channel selection proceeds (:96).
func TestDistribute_Playground_AllowedGroup_Routes(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Id: 30, Name: "pg-svc", TenantId: "default", Key: "k",
		Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("add abilities: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	r.POST("/pg/chat/completions", Distribute(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","group":"default"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for in-scope playground group; body=%s", w.Code, w.Body.String())
	}
}

// SetupContextForSelectedChannel: a multi-key channel with no keys surfaces the
// GetNextEnabledKey error verbatim (:345).
func TestSetupContextForSelectedChannel_MultiKeyNoKeys_Errors(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	ch := &repo.Channel{Id: 9, Name: "mk", Type: constant.ChannelTypeOpenAI, Key: "", Status: common.ChannelStatusEnabled}
	ch.ChannelInfo.IsMultiKey = true // multi-key with an empty key list → error
	if err := SetupContextForSelectedChannel(c, ch, "gpt-4o"); err == nil {
		t.Fatal("expected error for multi-key channel with no keys")
	}
}

// ---------------------------------------------------------------------------
// optional_zita_identity.go: nil ZitaClient short-circuit (:28)
// ---------------------------------------------------------------------------

func TestOptionalZitaIdentity_NilClient_PassThrough(t *testing.T) {
	prev := common.ZitaClient
	common.ZitaClient = nil
	defer func() { common.ZitaClient = prev }()

	r := gin.New()
	r.Use(OptionalZitaIdentity())
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (nil ZitaClient must pass through)", w.Code)
	}
}
