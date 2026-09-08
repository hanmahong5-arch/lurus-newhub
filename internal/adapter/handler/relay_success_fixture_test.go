package handler

// relay_success_fixture_test.go — hermetic success-path harness (plan doc
// §14 amendment): seeds a real entity.Channel behind an httptest
// OpenAI-shaped upstream and drives a POST through the REAL middleware chain
// (TokenAuth -> Distribute -> Relay), so RecordConsumeLog actually fires
// with a non-zero Quota and populated ChannelMeta — every prior fixture in
// this package (relay_error_log_fallback_test.go, cov_h5b_relay_dispatch_
// test.go) only exercises failure paths that never reach settlement. This is
// what GET /v1/generation and GET /v1/key need a real row to look up.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var relaySuccessDBCounter atomic.Int64

type relaySuccessCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	user     *repo.User
	token    *repo.Token
	channel  *repo.Channel
	upstream *httptest.Server
}

// setupRelaySuccessRouter opens a hermetic sqlite DB, seeds a user/token/
// channel/ability set that resolves against an httptest OpenAI-shaped
// upstream (common.MemoryCacheEnabled=false forces the direct-DB channel
// selection path, same as cov_h5b_relay_dispatch_test.go), and mounts:
//   - POST /v1/chat/completions behind StampRelayFormat -> TokenAuth ->
//     Distribute -> Relay (the real chain, per the amendment)
//   - GET /v1/generation and GET /v1/key behind TokenAuth only, mirroring
//     dashboard.go's group (no Distribute — these are not relay routes)
func setupRelaySuccessRouter(t *testing.T, upstream http.HandlerFunc) *relaySuccessCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB <= 0 {
		constant.MaxRequestBodyMB = 64
	}
	// The relay egress client is a package-level var only initialised at boot
	// (cmd/server/main.go); a hermetic test process never runs that, so the
	// first relay attempt without this would nil-panic in provider.doRequest.
	// Idempotent and cheap — safe to call once per test.
	app.InitHttpClient()
	// The relay egress SSRF guard (relay_dial_guard.go) defaults ON and
	// blocks private addresses — including the httptest upstream this
	// fixture points channels at (127.0.0.1). Same save/restore convention
	// as cov_handler-channel_upstream_test.go's handler_channel_withFetchSetting.
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	dsn := fmt.Sprintf("file:relaysuccess%d?mode=memory&cache=shared", relaySuccessDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Log{}, &repo.Channel{}, &repo.Ability{}, &repo.TenantCreditPool{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMemCache := common.MemoryCacheEnabled
	prevRedis := common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	// Another test elsewhere in this package (v2_testutil_test.go's
	// SetupV2TestRouter) flips this off with no restore; this harness's own
	// oracle depends on RecordConsumeLog actually writing, so pin it true
	// for the fixture's lifetime rather than inherit whatever a prior test
	// left behind.
	common.LogConsumeEnabled = true

	srv := httptest.NewServer(upstream)

	// The relay's real settlement path records into middleware/app's
	// in-memory rate-limit windows (bizTPMMem/bizMemLimiter), which are
	// process-global with no per-test reset — a low, small-integer id
	// (SQLite's default auto-increment starting point) can collide with an
	// unrelated test elsewhere in this package that happens to seed a token
	// with the same small id in ITS OWN isolated DB. Explicit high, per-call
	// unique ids sidestep that collision without needing a reset hook this
	// package doesn't expose.
	idBase := 900000000 + int(relaySuccessDBCounter.Load())*1000
	user := &repo.User{
		Id:       idBase,
		Username: "relay-success-user", DisplayName: "Relay Success",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "relaysuccess@test.local", Group: "default", Quota: 100000000,
		TenantId: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	key, err := common.GenerateRandomKey(48)
	if err != nil {
		t.Fatalf("generate token key: %v", err)
	}
	token := &repo.Token{
		Id:     idBase,
		UserId: user.Id, TenantId: "default", Key: key, Name: "relay-success-token",
		Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	weight := uint(10)
	priority := int64(0)
	baseURL := srv.URL
	channel := &repo.Channel{
		TenantId: "default", Type: constant.ChannelTypeOpenAI, Key: "sk-upstream-dummy",
		Status: common.ChannelStatusEnabled, Name: "fixture-openai",
		BaseURL: &baseURL, Models: "gpt-4o", Group: "default",
		Weight: &weight, Priority: &priority,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("add abilities: %v", err)
	}

	router := gin.New()
	router.Use(middleware.RequestId())

	relayGroup := router.Group("/v1")
	relayGroup.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	relayGroup.POST("/chat/completions", func(c *gin.Context) {
		Relay(c, types.RelayFormatOpenAI)
	})

	dashGroup := router.Group("/v1")
	dashGroup.Use(middleware.TokenAuth())
	dashGroup.GET("/generation", GetGeneration)
	dashGroup.GET("/key", GetKeyInfo)

	ctx := &relaySuccessCtx{router: router, db: db, user: user, token: token, channel: channel, upstream: srv}
	t.Cleanup(func() {
		srv.Close()
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.MemoryCacheEnabled = prevMemCache
		common.RedisEnabled = prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return ctx
}

// openAIChatEchoUpstream answers an OpenAI-shaped non-streaming completion
// with usage, so the settlement path (app.PostConsumeQuota ->
// governance.EnrichLogParams -> repo.RecordConsumeLog) computes and writes a
// non-zero Quota.
func openAIChatEchoUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, `{
		"id": "chatcmpl-fixture",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4o",
		"choices": [{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
	}`)
}

// postChat drives POST /v1/chat/completions through the router built by
// setupRelaySuccessRouter, authenticated as the fixture token, with any
// extra request headers layered on top.
func (ctx *relaySuccessCtx) postChat(t *testing.T, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// newAuthedRequest builds a GET/no-body request authenticated as the
// fixture's own token — for the TokenAuth-only routes (GET /v1/generation,
// GET /v1/key) mounted by setupRelaySuccessRouter.
func (ctx *relaySuccessCtx) newAuthedRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	return ctx.newRequestWithKey(t, method, target, body, ctx.token.Key)
}

// newRequestWithKey is newAuthedRequest with an explicit bearer key, so a
// test can authenticate as a DIFFERENT token (foreign-caller ownership
// checks).
func (ctx *relaySuccessCtx) newRequestWithKey(t *testing.T, method, target, body, key string) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Authorization", "Bearer "+key)
	return req
}

// serve runs a pre-built request through the fixture's router.
func (ctx *relaySuccessCtx) serve(req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// TestRelaySuccessFixture_WritesConsumeLogRow is the harness's own oracle:
// a plain successful relay through the real chain must leave exactly one
// consume-log row with non-zero Quota and a populated ChannelType — proving
// the fixture actually reaches settlement, not just a 200 status code.
func TestRelaySuccessFixture_WritesConsumeLogRow(t *testing.T) {
	ctx := setupRelaySuccessRouter(t, openAIChatEchoUpstream)

	w := ctx.postChat(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var logs []repo.Log
	if err := ctx.db.Where("type = ?", repo.LogTypeConsume).Find(&logs).Error; err != nil {
		t.Fatalf("query consume logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("consume log rows = %d, want 1", len(logs))
	}
	if logs[0].Quota <= 0 {
		t.Errorf("Quota = %d, want > 0 — the fixture must reach real settlement, not a free/zero-cost path", logs[0].Quota)
	}
	if logs[0].ChannelType != constant.ChannelTypeOpenAI {
		t.Errorf("ChannelType = %d, want %d (ChannelMeta populated from the real selected channel)", logs[0].ChannelType, constant.ChannelTypeOpenAI)
	}
}
