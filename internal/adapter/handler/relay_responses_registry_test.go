package handler

// relay_responses_registry_test.go — oracle tests for GET/DELETE
// /v1/responses/:response_id (cycle-8 L7, tasks-plugins-12), driven through
// the REAL middleware chain relay-router.go mounts on the responses-state
// group (StampRelayFormat -> TokenAuth -> ResponsesStateRateLimit), a real
// hermetic-sqlite DB, and a real httptest upstream — REAL-CHAIN RULE, not
// hand-set context keys. Mirrors task_generic_test.go's
// setupTaskGenericRouter pattern (hand-mirrored chain to avoid the
// handler<->router import cycle).

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var responsesRegistryDBCounter atomic.Int64

type responsesRegistryCtx struct {
	router                   *gin.Engine
	db                       *gorm.DB
	userA, userB, userC      *repo.User
	tokenA, tokenB, tokenC   *repo.Token
	channel, decoy, disabled *repo.Channel
	upstream                 *httptest.Server
	capturedAuth             *string
}

// setupResponsesRegistryRouter seeds userA/tokenA (tenant "default"),
// userB/tokenB (a DIFFERENT user, SAME tenant "default"), userC/tokenC (a
// different tenant "acme"), an enabled channel pointed at upstream with key
// "sk-pinned-key", a DECOY enabled channel with a higher weight and a
// DIFFERENT key ("sk-decoy-key") that weighted selection would prefer, and
// a disabled channel. capturedAuth records the Authorization header the
// upstream actually received on the most recent request.
func setupResponsesRegistryRouter(t *testing.T, upstream http.HandlerFunc) *responsesRegistryCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	seq := responsesRegistryDBCounter.Add(1)
	dsn := fmt.Sprintf("file:respregistry%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Channel{}, &repo.Log{}, &repo.Tenant{}, &entity.ResponseRegistry{}, &entity.AuditEvent{}, &entity.AuditChainHead{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	prevMemCache, prevRedis := common.MemoryCacheEnabled, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	// pinnedAuditWriter (v2_pricing_write_test.go) so
	// governance.RecordAuditEvent's async write lands in THIS db, not
	// whatever repo.DB happened to be for a previously-run test package-wide.
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = false

	srv := httptest.NewServer(upstream)

	idBase := 930000000 + int(seq)*1000
	mkUser := func(offset int, tenant string) *repo.User {
		u := &repo.User{
			Id: idBase + offset, Username: fmt.Sprintf("resp-registry-user-%d-%d", seq, offset),
			DisplayName: "Resp Registry User", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			Email: fmt.Sprintf("resp-registry-%d-%d@test.local", seq, offset), Group: "default", Quota: 10_000_000,
			TenantId: tenant,
		}
		if cErr := db.Create(u).Error; cErr != nil {
			t.Fatalf("seed user: %v", cErr)
		}
		return u
	}
	mkToken := func(u *repo.User, offset int) *repo.Token {
		key, kErr := common.GenerateRandomKey(48)
		if kErr != nil {
			t.Fatalf("generate token key: %v", kErr)
		}
		tok := &repo.Token{
			Id: idBase + offset, UserId: u.Id, TenantId: u.TenantId, Key: key,
			Name: "resp-registry-token", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
		}
		if cErr := db.Create(tok).Error; cErr != nil {
			t.Fatalf("seed token: %v", cErr)
		}
		return tok
	}

	userA := mkUser(1, "default")
	tokenA := mkToken(userA, 1)
	userB := mkUser(2, "default")
	tokenB := mkToken(userB, 2)
	userC := mkUser(3, "acme")
	tokenC := mkToken(userC, 3)

	weightLow := uint(1)
	weightHigh := uint(100)
	priority := int64(0)
	baseURL := srv.URL
	channel := &repo.Channel{
		Id: idBase + 10, TenantId: "default", Type: constant.ChannelTypeOpenAI, Key: "sk-pinned-key",
		Status: common.ChannelStatusEnabled, Name: "pinned-channel",
		BaseURL: &baseURL, Models: "gpt-4o-mini", Group: "default", Weight: &weightLow, Priority: &priority,
	}
	if cErr := db.Create(channel).Error; cErr != nil {
		t.Fatalf("seed channel: %v", cErr)
	}
	decoy := &repo.Channel{
		Id: idBase + 11, TenantId: "default", Type: constant.ChannelTypeOpenAI, Key: "sk-decoy-key",
		Status: common.ChannelStatusEnabled, Name: "decoy-channel",
		BaseURL: &baseURL, Models: "gpt-4o-mini", Group: "default", Weight: &weightHigh, Priority: &priority,
	}
	if cErr := db.Create(decoy).Error; cErr != nil {
		t.Fatalf("seed decoy channel: %v", cErr)
	}
	disabled := &repo.Channel{
		Id: idBase + 12, TenantId: "default", Type: constant.ChannelTypeOpenAI, Key: "sk-disabled-key",
		Status: common.ChannelStatusManuallyDisabled, Name: "disabled-channel",
		BaseURL: &baseURL, Models: "gpt-4o-mini", Group: "default", Weight: &weightLow, Priority: &priority,
	}
	if cErr := db.Create(disabled).Error; cErr != nil {
		t.Fatalf("seed disabled channel: %v", cErr)
	}

	router := gin.New()
	group := router.Group("/v1/responses")
	// ResponsesStateRateLimit, not CriticalRateLimit — matches
	// relay-router.go's real mount (cycle-8 L7 repair round, finding B-F1).
	group.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.ResponsesStateRateLimit())
	group.GET("/:response_id", RelayResponsesRetrieve)
	group.DELETE("/:response_id", RelayResponsesDelete)

	ctx := &responsesRegistryCtx{
		router: router, db: db,
		userA: userA, userB: userB, userC: userC,
		tokenA: tokenA, tokenB: tokenB, tokenC: tokenC,
		channel: channel, decoy: decoy, disabled: disabled,
		upstream: srv, capturedAuth: new(string),
	}
	t.Cleanup(func() {
		srv.Close()
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		common.MemoryCacheEnabled, common.RedisEnabled = prevMemCache, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return ctx
}

// respRegistryUpstream is shared by every test in this file: GET requests
// get a 200 JSON echo and have their Authorization header captured into
// *capturedAuth; DELETE requests answer 204 unless the path contains
// "missing", in which case they answer 404 (simulating the vendor having
// already lost the resource).
func respRegistryUpstream(capturedAuth *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*capturedAuth = r.Header.Get("Authorization")
		if r.Method == http.MethodDelete {
			if strings.Contains(r.URL.Path, "missing") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_owned_by_a","object":"response","status":"completed"}`))
	}
}

func (c *responsesRegistryCtx) request(method, target string, tok *repo.Token) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+tok.Key)
	w := httptest.NewRecorder()
	c.router.ServeHTTP(w, req)
	return w
}

func (c *responsesRegistryCtx) seedRow(responseId, tenantId string, userId int, tokenId, channelId int) {
	now := common.GetTimestamp()
	row := &entity.ResponseRegistry{
		ResponseId: responseId, TenantId: tenantId, UserId: userId, TokenId: tokenId,
		ChannelId: channelId, UpstreamModel: "gpt-4o-mini", CreatedAt: now, ExpiresAt: now + 86400,
	}
	if err := repo.UpsertResponseRegistry(row); err != nil {
		panic(err) // seeding helper, test setup failure — not a table-driven assertion
	}
}

// TestRelayResponsesRetrieve_SameUserPinsChannel is the oracle for the core
// pin behaviour: the owning user's GET is routed to the SPECIFIC channel the
// row names — proved by the Authorization header the upstream sees
// (sk-pinned-key, never the higher-weight decoy's sk-decoy-key) — not by
// weighted selection (there is no Distribute() on this chain at all, but a
// regression that swapped SetupContextForSelectedChannel for a
// weighted-pick helper would still show up here).
func TestRelayResponsesRetrieve_SameUserPinsChannel(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_owned_by_a", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodGet, "/v1/responses/resp_owned_by_a", ctx.tokenA)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "resp_owned_by_a") {
		t.Errorf("body = %s, want the upstream's JSON forwarded verbatim", w.Body.String())
	}
	if *captured != "Bearer sk-pinned-key" {
		t.Errorf("upstream Authorization = %q, want %q (the pinned channel's key, not the decoy's)", *captured, "Bearer sk-pinned-key")
	}

	// A-F1 (cycle-8 L7 repair round): the success-path audit row is
	// deletable with the package green unless asserted directly — the
	// lane's uat_probe depends on relay.response_retrieved carrying
	// channel_id=<seeded> and upstream_status=200.
	ev := pollAuditRow(t, governance.ActionResponseRetrieved, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionResponseRetrieved)
	}
	if !strings.Contains(ev.Details, fmt.Sprintf(`"channel_id":%d`, ctx.channel.Id)) {
		t.Errorf("audit details = %s, want channel_id=%d", ev.Details, ctx.channel.Id)
	}
	if !strings.Contains(ev.Details, `"upstream_status":200`) {
		t.Errorf("audit details = %s, want upstream_status=200", ev.Details)
	}
}

// TestRelayResponsesRetrieve_OtherUserSameTenant404 is the oracle for O7's
// same-tenant-AND-same-user ownership rule: a different user in the SAME
// tenant gets the generic 404, never 403, and the denial is audited.
func TestRelayResponsesRetrieve_OtherUserSameTenant404(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_owned_by_a", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodGet, "/v1/responses/resp_owned_by_a", ctx.tokenB)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response_not_found") {
		t.Errorf("body = %s, want response_not_found", w.Body.String())
	}
	if ev := pollAuditRow(t, governance.ActionResponseDenied, 2*time.Second); ev == nil {
		t.Errorf("no %s audit row found within timeout", governance.ActionResponseDenied)
	}
	if *captured != "" {
		t.Errorf("upstream was called (Authorization=%q) — an ownership mismatch must never reach the channel", *captured)
	}
}

// TestRelayResponsesRetrieve_OtherTenant404 covers the other half of O7:
// a different tenant entirely, same 404 shape.
func TestRelayResponsesRetrieve_OtherTenant404(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_owned_by_a", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodGet, "/v1/responses/resp_owned_by_a", ctx.tokenC)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response_not_found") {
		t.Errorf("body = %s, want response_not_found", w.Body.String())
	}
}

// TestRelayResponsesRetrieve_Absent404 proves the absent-id case is
// byte-identical to the ownership-mismatch cases above.
func TestRelayResponsesRetrieve_Absent404(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))

	wOwner := ctx.request(http.MethodGet, "/v1/responses/resp_never_existed", ctx.tokenA)
	if wOwner.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", wOwner.Code, wOwner.Body.String())
	}

	ctx.seedRow("resp_owned_by_a", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)
	wMismatch := ctx.request(http.MethodGet, "/v1/responses/resp_owned_by_a", ctx.tokenB)

	if wOwner.Code != wMismatch.Code || wOwner.Body.String() != wMismatch.Body.String() {
		t.Errorf("absent id and ownership-mismatch bodies differ:\nabsent=%s\nmismatch=%s", wOwner.Body.String(), wMismatch.Body.String())
	}
}

// TestRelayResponsesRetrieve_ChannelDisabled404 covers the row-exists,
// channel-not-usable branch.
func TestRelayResponsesRetrieve_ChannelDisabled404(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_disabled_channel", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.disabled.Id)

	w := ctx.request(http.MethodGet, "/v1/responses/resp_disabled_channel", ctx.tokenA)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if *captured != "" {
		t.Errorf("upstream was called (Authorization=%q) for a disabled channel", *captured)
	}
}

// TestRelayResponsesRetrieve_NoQuotaRowWritten proves the "GET/DELETE write
// no quota row" acceptance criterion: the owning user's balance/request
// count and the log table are untouched by a successful GET.
func TestRelayResponsesRetrieve_NoQuotaRowWritten(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_owned_by_a", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	quotaBefore := ctx.userA.Quota
	w := ctx.request(http.MethodGet, "/v1/responses/resp_owned_by_a", ctx.tokenA)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var refreshed repo.User
	if err := ctx.db.First(&refreshed, ctx.userA.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if refreshed.RequestCount != 0 {
		t.Errorf("RequestCount = %d, want 0 (no quota consumption on GET)", refreshed.RequestCount)
	}
	if refreshed.Quota != quotaBefore {
		t.Errorf("Quota = %d, want unchanged %d", refreshed.Quota, quotaBefore)
	}
	var logCount int64
	if err := ctx.db.Model(&repo.Log{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 0 {
		t.Errorf("log rows = %d, want 0", logCount)
	}
}

// TestRelayResponsesDelete_RemovesRowOn2xx proves the row is deleted after a
// successful (2xx) vendor DELETE.
func TestRelayResponsesDelete_RemovesRowOn2xx(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_delete_ok", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodDelete, "/v1/responses/resp_delete_ok", ctx.tokenA)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (upstream's own status passed through); body=%s", w.Code, w.Body.String())
	}
	if _, err := repo.GetResponseRegistry("resp_delete_ok"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry after 2xx delete = %v, want ErrResponseRegistryNotFound", err)
	}

	// A-F1 (cycle-8 L7 repair round): same assertion as the retrieve oracle
	// above, for the delete side's audit row.
	ev := pollAuditRow(t, governance.ActionResponseDeleted, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionResponseDeleted)
	}
	if !strings.Contains(ev.Details, fmt.Sprintf(`"channel_id":%d`, ctx.channel.Id)) {
		t.Errorf("audit details = %s, want channel_id=%d", ev.Details, ctx.channel.Id)
	}
	if !strings.Contains(ev.Details, `"upstream_status":204`) {
		t.Errorf("audit details = %s, want upstream_status=204", ev.Details)
	}
}

// TestRelayResponsesDelete_KeepsRowOnUpstreamError proves the row survives
// when the vendor's own DELETE fails — the local registry must stay
// consistent with "the vendor still thinks it exists" rather than
// optimistically dropping it.
func TestRelayResponsesDelete_KeepsRowOnUpstreamError(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_delete_missing", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodDelete, "/v1/responses/resp_delete_missing", ctx.tokenA)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (upstream's own status passed through); body=%s", w.Code, w.Body.String())
	}
	if _, err := repo.GetResponseRegistry("resp_delete_missing"); err != nil {
		t.Errorf("GetResponseRegistry after failed upstream delete = %v, want the row to survive", err)
	}
}

// TestRelayResponsesRetrieve_ChannelRetypedAfterWrite404_ZeroUpstreamHits is
// the oracle for cycle-8 L7 repair round finding A-F6: a channel retyped
// (e.g. to a non-stateful-eligible type) AFTER the registry row was written
// must answer the same 404 with zero upstream calls, not fall through to a
// nonsense URL. Channel type 999 is enabled and would otherwise pass every
// other check (status, model) — only the type re-check catches this.
func TestRelayResponsesRetrieve_ChannelRetypedAfterWrite404_ZeroUpstreamHits(t *testing.T) {
	captured := new(string)
	ctx := setupResponsesRegistryRouter(t, respRegistryUpstream(captured))
	ctx.seedRow("resp_retyped_channel", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	// Retype the channel AFTER the row was seeded — the row still names
	// this channel id, but the channel no longer qualifies.
	if err := ctx.db.Model(&repo.Channel{}).Where("id = ?", ctx.channel.Id).Update("type", 999).Error; err != nil {
		t.Fatalf("retype channel: %v", err)
	}

	w := ctx.request(http.MethodGet, "/v1/responses/resp_retyped_channel", ctx.tokenA)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response_not_found") {
		t.Errorf("body = %s, want response_not_found", w.Body.String())
	}
	if *captured != "" {
		t.Errorf("upstream was called (Authorization=%q) for a channel that no longer supports the stateful surface", *captured)
	}
}

// TestRelayResponsesRetrieve_StreamingBodyCopiedIncrementally is the oracle
// for cycle-8 L7 repair round finding B-F5: when the upstream responds with
// Content-Type: text/event-stream, the handler must flush at least once
// during the copy (helper.FlushWriter) instead of buffering the whole body
// with io.ReadAll before writing anything. httptest.ResponseRecorder
// implements http.Flusher and records whether Flush was ever called
// (w.Flushed) — the mutation "revert to io.ReadAll+c.Data for every content
// type" makes this red without changing the final body content, which is
// why this test cannot rely on body comparison alone.
func TestRelayResponsesRetrieve_StreamingBodyCopiedIncrementally(t *testing.T) {
	captured := new(string)
	streamUpstream := func(w http.ResponseWriter, r *http.Request) {
		*captured = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}
	ctx := setupResponsesRegistryRouter(t, streamUpstream)
	ctx.seedRow("resp_stream_get", "default", ctx.userA.Id, ctx.tokenA.Id, ctx.channel.Id)

	w := ctx.request(http.MethodGet, "/v1/responses/resp_stream_get", ctx.tokenA)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !w.Flushed {
		t.Error("Flush was never called — a text/event-stream retrieval must be copied incrementally, not buffered whole with io.ReadAll")
	}
	if !strings.Contains(w.Body.String(), "response.completed") {
		t.Errorf("body = %s, want the upstream SSE payload forwarded", w.Body.String())
	}
}
