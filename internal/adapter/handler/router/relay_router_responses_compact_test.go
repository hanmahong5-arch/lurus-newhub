package router

// relay_router_responses_compact_test.go — business-acceptance tests for
// wire-formats-03's route mount (cycle-8 L6): POST /v1/responses/compact
// must be registered by the real SetRelayRouter wiring (the same function
// cmd/server/main.go reaches via SetRouter), mounted in the same
// relayV1Router group as /v1/responses (so it inherits StampRelayFormat,
// TokenAuth, PoolBalanceCheck, CostSpikeLimit, EntitlementCheck,
// ModelRequestRateLimit, BusinessRateLimit, Distribute,
// BusinessModelRateLimit, RelayConcurrencyLimit — relay-router.go's
// httpRouter sub-group), not a bespoke unauthenticated route.
//
// TestRelayRouter_ResponsesCompactMounted only exercises the FIRST gate on
// that chain (TokenAuth, via a key-less 401) — reusing
// relayWireStampEmptyDB from relay_wire_stamp_test.go so the miss is genuine
// rather than a short-circuit on a missing DB. It does not by itself prove
// the endpoint functions past TokenAuth; that requires a real request to
// actually resolve a channel and reach an upstream, which
// TestRelayRouter_ResponsesCompactReachesUpstream below drives end to end.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRelayRouter_ResponsesCompactMounted(t *testing.T) {
	cleanup := relayWireStampEmptyDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	if !routerRelayHasRoute(engine, http.MethodPost, "/v1/responses/compact") {
		t.Fatal("POST /v1/responses/compact is not registered on the real relay router")
	}

	// No key: TokenAuth, the first gate on the chain, must reject this
	// request the same way it rejects a key-less /v1/responses request.
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-4o-mini","input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/responses/compact no key: status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

var respCompactRouterDBCounter atomic.Int64

// respCompactRouterFixture bundles a seeded, authenticated token plus a real
// OpenAI-type channel pointed at an httptest upstream — everything
// Distribute() needs to admit a genuine POST /v1/responses/compact request
// all the way to an upstream call, driven through the real SetRelayRouter
// engine (not a hand-mirrored chain).
func respCompactRouterFixture(t *testing.T, upstreamURL string) (engine *gin.Engine, authHeader string, cleanup func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	seq := respCompactRouterDBCounter.Add(1)
	dbName := fmt.Sprintf("file:resp_compact_router_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}, &repo.Channel{}, &entity.Ability{}, &entity.Tenant{}, &repo.Log{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	idBase := 940000000 + int(seq)*1000
	user := &repo.User{
		Id: idBase + 1, Username: fmt.Sprintf("resp-compact-router-user-%d", seq),
		DisplayName: "Resp Compact Router User", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("resp-compact-router-%d@test.local", seq), Group: "default", Quota: 10_000_000,
		TenantId: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenKey := common.GetRandomString(48)
	tok := &repo.Token{
		Id: idBase + 2, UserId: user.Id, TenantId: "default", Key: tokenKey, Status: common.TokenStatusEnabled,
		Name: "resp-compact-router-token", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	weight := uint(1)
	priority := int64(0)
	ch := &repo.Channel{
		Id: idBase + 3, Name: "resp-compact-router-channel", TenantId: "default",
		Type: constant.ChannelTypeOpenAI, Key: "sk-resp-compact-router",
		Status: common.ChannelStatusEnabled, Models: "gpt-4o-mini", Group: "default",
		BaseURL: &upstreamURL, Weight: &weight, Priority: &priority,
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("add abilities: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevMaxBody := constant.MaxRequestBodyMB
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	constant.MaxRequestBodyMB = -1 // no limit (default 0 truncates bodies to 1 byte)

	engine = gin.New()
	SetRelayRouter(engine)

	cleanup = func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		constant.MaxRequestBodyMB = prevMaxBody
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
	return engine, "Bearer " + tokenKey, cleanup
}

// TestRelayRouter_ResponsesCompactReachesUpstream is the oracle for
// repair-round finding A-F2: three independent wiring seams — the
// relayHandler dispatch case for RelayModeResponsesCompact
// (internal/adapter/handler/relay.go), the format-decode case in
// helper.GetAndValidateRequest (internal/app/relay/helper/valid_request.go)
// and the GenRelayInfo case (internal/adapter/provider/common/relay_info.go)
// — can each be deleted while every package in the lane's test_packages set
// stays green, because nothing before this test drove a genuine
// authenticated, channel-resolved request through the real router all the
// way to an upstream call. Seeds a real token + a real OpenAI-type channel
// pointed at a fake upstream, POSTs through the real SetRelayRouter engine,
// and asserts the fake upstream actually received the body at the vendor's
// compact path.
func TestRelayRouter_ResponsesCompactReachesUpstream(t *testing.T) {
	var sawPath string
	var sawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"r1","object":"response","model":"gpt-4o-mini","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`))
	}))
	defer srv.Close()

	engine, authHeader, cleanup := respCompactRouterFixture(t, srv.URL)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-4o-mini","input":"router-e2e-marker"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.HasSuffix(sawPath, "/v1/responses/compact") {
		t.Errorf("upstream path = %q, want it to end in /v1/responses/compact — the real router never reached the compact dispatch/decode/GenRelayInfo path", sawPath)
	}
	if !strings.Contains(string(sawBody), "router-e2e-marker") {
		t.Errorf("upstream body = %s, want it to contain the request's input marker", sawBody)
	}
}

// respCompactChainProbe mirrors pg_chain_mount_test.go's pgChainProbe: a
// probe middleware registered ahead of SetRelayRouter's own registrations
// captures gin's actual assembled handler-name chain for the matched route,
// then aborts before TokenAuth/Distribute run (no DB needed).
func respCompactChainProbe(t *testing.T) (engine *gin.Engine, capture func(path string) []string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine = gin.New()

	var captured []string
	engine.Use(func(c *gin.Context) {
		captured = c.HandlerNames()
		c.Abort()
		c.String(http.StatusOK, "probed")
	})
	SetRelayRouter(engine)

	capture = func(path string) []string {
		captured = nil
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return captured
	}
	return engine, capture
}

// TestRelayRouter_ResponsesCompactChainMatchesResponses is the oracle for
// repair-round finding B-F6: POST /v1/responses/compact's assembled gin
// handler chain must carry the same enforcement middlewares, in the same
// order, as POST /v1/responses — proving the two routes share
// relayV1Router's httpRouter sub-group rather than a chain comparison
// resting on a single 401.
func TestRelayRouter_ResponsesCompactChainMatchesResponses(t *testing.T) {
	_, capture := respCompactChainProbe(t)

	responsesChain := capture("/v1/responses")
	compactChain := capture("/v1/responses/compact")

	if len(responsesChain) == 0 {
		t.Fatal("captured zero handler names for /v1/responses — probe wiring broken")
	}
	if len(compactChain) != len(responsesChain) {
		t.Fatalf("/v1/responses/compact chain has %d handlers, /v1/responses has %d:\ncompact:   %v\nresponses: %v",
			len(compactChain), len(responsesChain), compactChain, responsesChain)
	}
	for i := range responsesChain {
		// Compare handler names for exact equality at every index except
		// the last, which is skipped outright below: gin reports closure
		// names like "router.SetRelayRouter.func6", and the two routes
		// register distinct closures for their own final handler, so that
		// index is expected to differ and is not checked at all — every
		// middleware Use()'d on the shared group (every index before it)
		// must match exactly.
		if responsesChain[i] != compactChain[i] {
			// The last handler is each route's own closure (expected to
			// differ); every middleware before it must match exactly.
			if i == len(responsesChain)-1 {
				continue
			}
			t.Errorf("handler[%d]: /v1/responses=%q, /v1/responses/compact=%q — chains diverge before the final route handler", i, responsesChain[i], compactChain[i])
		}
	}
}
