package handler

// channel_test_log_attribution_test.go pins who owns the consume-log row that
// the manual probe GET /api/channel/test/:id writes (cycle-12 plan §L5).
//
// probeChannel builds its own gin.Context (channel-test.go:84-85) and loads
// user 1 into it, so the row it wrote carried user_id 1 and whatever tenant
// resolveLogTenantID (repo/log.go:101) picked for user 1 — the operator who
// clicked "test" saw nothing in their own log list, and the row landed in a
// tenant that did not ask for it.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var probeAttributionDBCounter int

const (
	probeActorTenant = "acme-probe-tenant"
	probeTestModel   = "probe-attribution-model"
)

type probeAttributionCtx struct {
	db    *gorm.DB
	actor *repo.User
}

// setupProbeAttributionDB is a hermetic SQLite harness for the manual probe
// path. probeChannel hardcodes repo.GetUserCache(1) for the relay identity, so
// user 1 must exist; the actor is a separate role-10 user in its own tenant so
// "attributed to the caller" and "attributed to user 1" cannot be confused.
func setupProbeAttributionDB(t *testing.T) *probeAttributionCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	probeAttributionDBCounter++
	dsn := fmt.Sprintf("file:probeattribution%d?mode=memory&cache=shared", probeAttributionDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Channel{}, &repo.Log{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevLogEnabled := common.LogConsumeEnabled
	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		common.LogConsumeEnabled = prevLogEnabled
	})
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = true

	// User 1 sits in the platform-shared tenant, which is exactly the
	// attribution the unfixed path produced.
	if err := db.Create(&repo.User{
		Id: 1, Username: "probe-relay-identity", Group: "default",
		Status: common.UserStatusEnabled, TenantId: "default",
	}).Error; err != nil {
		t.Fatalf("seed user 1: %v", err)
	}
	actor := &repo.User{
		Username: "probe-acme-admin", Group: "default", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, TenantId: probeActorTenant, Quota: 1_000_000,
	}
	if err := db.Create(actor).Error; err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if actor.Id == 1 {
		t.Fatalf("test precondition broken: the actor must not be user 1")
	}
	return &probeAttributionCtx{db: db, actor: actor}
}

// runProbeAsActor drives the real handler (GET /api/channel/test/:id) against
// a loopback upstream as actor, with requestTenant on the request context, and
// returns the consume-log row it wrote.
func runProbeAsActor(t *testing.T, ctx *probeAttributionCtx, actor *repo.User, requestTenant string) repo.Log {
	t.Helper()
	allowLoopbackEgress(t)
	app.InitHttpClient()

	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + probeTestModel + `":1.0}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-probe","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`))
	}))
	t.Cleanup(upstream.Close)

	channel := &repo.Channel{
		Type:     1, // OpenAI
		Status:   common.ChannelStatusEnabled,
		Name:     "probe-attribution-channel",
		Key:      "sk-probe-attribution",
		Models:   probeTestModel,
		Group:    "default",
		TenantId: probeActorTenant,
		BaseURL:  func() *string { u := upstream.URL; return &u }(),
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	c, w := v1Ctx(http.MethodGet, "/api/channel/test/"+fmt.Sprint(channel.Id)+"?model="+probeTestModel,
		nil, common.RoleAdminUser, requestTenant, actor.Id)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channel.Id)}}
	TestChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("probe must succeed against the loopback upstream, code=%d body=%s", w.Code, w.Body.String())
	}

	var row repo.Log
	if err := repo.LOG_DB.Where("channel_id = ?", channel.Id).Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("query consume log row: %v", err)
	}
	return row
}

// TestV1ChannelTest_ConsumeLogAttributedToCaller asserts the row the manual
// probe writes belongs to the operator who asked for it, in that operator's
// tenant, and is marked as a probe.
//
// Mutation: drop the ActorUserID plumbing in TestChannel/probeChannel (pin
// logUserID back to 1) and user_id, username AND tenant_id all go red — the
// tenant follows the actor because repo.resolveLogTenantID falls back to the
// actor's user row, which is the single thing that decides it here.
// Mutation: delete `other["source"] = channelProbeLogSource` and the last
// assertion goes red.
func TestV1ChannelTest_ConsumeLogAttributedToCaller(t *testing.T) {
	ctx := setupProbeAttributionDB(t)
	row := runProbeAsActor(t, ctx, ctx.actor, probeActorTenant)

	if row.UserId != ctx.actor.Id {
		t.Errorf("probe consume log user_id = %d, want %d (the operator who asked for the probe)", row.UserId, ctx.actor.Id)
	}
	if row.TenantId != probeActorTenant {
		t.Errorf("probe consume log tenant_id = %q, want %q", row.TenantId, probeActorTenant)
	}
	if row.Username != ctx.actor.Username {
		t.Errorf("probe consume log username = %q, want %q", row.Username, ctx.actor.Username)
	}
	if !strings.Contains(row.Other, `"source":"`+channelProbeLogSource+`"`) {
		t.Errorf("probe consume log other = %q, want it to carry source=%q so stats can tell probe rows "+
			"from customer traffic (this row spends no wallet money)", row.Other, channelProbeLogSource)
	}
}

// TestV1ChannelTest_ConsumeLogTenantFollowsActorUserRow names the one thing
// that decides the row's tenant: repo.RecordConsumeLog stamps
// resolveLogTenantID(c.GetString("tenant_id"), userId), and probeChannel's
// own gin.Context carries no tenant_id — so the value comes from the ACTOR'S
// USER ROW, not from the request context and not from the channel. Here the
// actor's row says one thing while the request (and the channel it is allowed
// to probe) says another; if a future edit copies the request value onto the
// probe context, this goes red and there are two sources again.
//
// In production the two agree by construction: middleware/auth.go derives the
// session's tenant_id from this same user row, which is why the explicit
// c.Set the first round added here was deleted as a no-op.
func TestV1ChannelTest_ConsumeLogTenantFollowsActorUserRow(t *testing.T) {
	ctx := setupProbeAttributionDB(t)

	const actorRowTenant = "acme-probe-actor-row-tenant"
	actor := &repo.User{
		Username: "probe-acme-admin-2", Group: "default", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, TenantId: actorRowTenant, Quota: 1_000_000,
	}
	if err := ctx.db.Create(actor).Error; err != nil {
		t.Fatalf("seed second actor: %v", err)
	}

	// The request context carries the CHANNEL's tenant (enforceTenantScope
	// demands that), which is deliberately not the actor row's tenant.
	row := runProbeAsActor(t, ctx, actor, probeActorTenant)

	if row.TenantId != actorRowTenant {
		t.Errorf("probe consume log tenant_id = %q, want %q (the actor's user row, via resolveLogTenantID) — "+
			"the request context said %q", row.TenantId, actorRowTenant, probeActorTenant)
	}
	if row.UserId != actor.Id {
		t.Errorf("probe consume log user_id = %d, want %d", row.UserId, actor.Id)
	}
}
