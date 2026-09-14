package handler

// task_generic_test.go — oracle tests for the generic async-task surface
// (cycle-8 L8): POST /v1/tasks/:platform submit and GET
// /v1/tasks/:platform/:task_id status, driven through the REAL middleware
// chain task-router.go mounts (TokenAuth -> TaskPlatformGuard ->
// PoolBalanceCheck -> CostSpikeLimit -> EntitlementCheck ->
// ModelRequestRateLimit -> BusinessRateLimit -> RelayConcurrencyLimit, plus
// Distribute() on the POST leg only), mirroring relay_success_fixture_test.go's
// hermetic-sqlite-plus-httptest-upstream harness rather than hand-setting
// context keys — REAL-CHAIN RULE.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var taskGenericDBCounter atomic.Int64

type taskGenericCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	user     *repo.User
	token    *repo.Token
	channel  *repo.Channel
	upstream *httptest.Server
}

// setupTaskGenericRouter opens a hermetic sqlite DB, seeds a user/token/
// channel/ability set that resolves against an httptest Suno-shaped
// upstream, and mounts /v1/tasks/:platform with the SAME middleware chain
// as router.SetTaskRouter (task-router.go) — hand-mirrored rather than
// calling the router package to avoid an import cycle (router already
// imports handler), the same convention relay_success_fixture_test.go uses
// for the main relay chain.
func setupTaskGenericRouter(t *testing.T, upstream http.HandlerFunc) *taskGenericCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB <= 0 {
		constant.MaxRequestBodyMB = 64
	}
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	seq := taskGenericDBCounter.Add(1)
	dsn := fmt.Sprintf("file:taskgeneric%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Log{}, &repo.Channel{}, &repo.Ability{}, &repo.TenantCreditPool{}, &repo.Task{}} {
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
	common.LogConsumeEnabled = true

	srv := httptest.NewServer(upstream)

	idBase := 920000000 + int(seq)*1000
	user := &repo.User{
		Id:       idBase,
		Username: fmt.Sprintf("task-generic-user-%d", seq), DisplayName: "Task Generic",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("taskgeneric%d@test.local", seq), Group: "default", Quota: 100000000,
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
		UserId: user.Id, TenantId: "default", Key: key, Name: "task-generic-token",
		Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
		// Non-zero so TestTaskGeneric_SubmitRecordsProjectAndRequestId has a
		// real, distinguishable value to prove InitTask actually copied it
		// (SetupContextForToken -> RelayInfo.ProjectId -> repo.InitTask).
		ProjectId: 77,
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	weight := uint(10)
	priority := int64(0)
	baseURL := srv.URL
	channel := &repo.Channel{
		TenantId: "default", Type: constant.ChannelTypeSunoAPI, Key: "sk-upstream-dummy",
		Status: common.ChannelStatusEnabled, Name: "fixture-suno",
		BaseURL: &baseURL, Models: "suno-1", Group: "default",
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

	taskGroup := router.Group("/v1/tasks")
	taskGroup.Use(
		middleware.TokenAuth(),
		TaskPlatformGuard(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
	)
	taskGroup.POST("/:platform", middleware.Distribute(), TaskChannelPlatformGuard(), RelayTask)
	taskGroup.GET("/:platform/:task_id", GetTaskGeneric)

	ctx := &taskGenericCtx{router: router, db: db, user: user, token: token, channel: channel, upstream: srv}
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

// sunoSubmitEchoUpstream answers the suno wire's submit shape
// (dto.TaskResponse[string]) that suno.TaskAdaptor.DoResponse expects.
func sunoSubmitEchoUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, `{"code":"success","message":"","data":"upstream-task-123"}`)
}

func (ctx *taskGenericCtx) authedRequest(method, target, body string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
	return req
}

// apiSuccessEnvelope mirrors common.ApiSuccess's wire shape for the generic
// status route's projected response (genericTaskStatusResponse), NOT the
// raw repo.Task row — see TestTaskGeneric_StatusOwner200 for the assertion
// that channel_id/user_id/quota are genuinely absent from the wire.
type apiSuccessEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		Platform  string `json:"platform"`
		ProjectId int    `json:"project_id"`
		RequestId string `json:"request_id"`
	} `json:"data"`
}

// TestTaskGeneric_UnknownPlatform404 proves TaskPlatformGuard is really
// mounted on the live route (not merely present in task_generic.go) and
// rejects a platform with no compiled adaptor with the new
// task_platform_unknown error code.
func TestTaskGeneric_UnknownPlatform404(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/not-a-real-platform/some-task-id", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	if got.Error.Code != "task_platform_unknown" {
		t.Errorf("error.code = %q, want task_platform_unknown", got.Error.Code)
	}
}

// TestTaskGeneric_StatusForeignUser404_BodyIdenticalToAbsent is the
// fail-closed lock: a task that exists but belongs to another user must be
// indistinguishable from a task_id that does not exist at all.
func TestTaskGeneric_StatusForeignUser404_BodyIdenticalToAbsent(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	foreignTask := &repo.Task{
		TaskID: "foreign-task-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id + 1, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	if err := ctx.db.Create(foreignTask).Error; err != nil {
		t.Fatalf("seed foreign task: %v", err)
	}

	foreignReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/foreign-task-1", "")
	foreignW := httptest.NewRecorder()
	ctx.router.ServeHTTP(foreignW, foreignReq)

	absentReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/does-not-exist-at-all", "")
	absentW := httptest.NewRecorder()
	ctx.router.ServeHTTP(absentW, absentReq)

	if foreignW.Code != http.StatusNotFound {
		t.Fatalf("foreign-user status = %d, want 404; body=%s", foreignW.Code, foreignW.Body.String())
	}
	if absentW.Code != http.StatusNotFound {
		t.Fatalf("absent-task status = %d, want 404; body=%s", absentW.Code, absentW.Body.String())
	}
	if foreignW.Body.String() != absentW.Body.String() {
		t.Errorf("bodies differ: foreign=%s absent=%s (must be byte-identical — no existence leak)",
			foreignW.Body.String(), absentW.Body.String())
	}
	const want = `{"error":{"message":"Task not found","type":"invalid_request_error"}}`
	if foreignW.Body.String() != want {
		t.Errorf("body = %s, want %s", foreignW.Body.String(), want)
	}
}

// TestTaskGeneric_StatusOwner200 is the positive counterpart: the owning
// user reads their own task's status successfully, and (B-F2) the response
// carries the relay.TaskModel2Dto projection plus platform/project_id/
// request_id — NOT the raw repo.Task row (no channel_id, user_id or quota
// on the wire).
func TestTaskGeneric_StatusOwner200(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	task := &repo.Task{
		TaskID: "owner-task-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, ChannelId: ctx.channel.Id, Quota: 4242,
		Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
		ProjectId: 88, RequestId: "req-owner-status-1",
	}
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/owner-task-1", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var env apiSuccessEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	if !env.Success || env.Data.TaskID != "owner-task-1" {
		t.Errorf("envelope = %+v, want success=true data.task_id=owner-task-1", env)
	}
	if env.Data.Platform != "suno" {
		t.Errorf("data.platform = %q, want suno", env.Data.Platform)
	}
	if env.Data.ProjectId != 88 {
		t.Errorf("data.project_id = %d, want 88", env.Data.ProjectId)
	}
	if env.Data.RequestId != "req-owner-status-1" {
		t.Errorf("data.request_id = %q, want req-owner-status-1", env.Data.RequestId)
	}

	// Decode into a raw map to prove channel_id/user_id/quota are truly
	// absent from the wire, not just unmapped by apiSuccessEnvelope's
	// struct tags.
	var raw struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw body: %v; body=%s", err, w.Body.String())
	}
	for _, forbidden := range []string{"channel_id", "user_id", "quota", "group", "properties"} {
		if _, present := raw.Data[forbidden]; present {
			t.Errorf("data contains forbidden key %q — full body=%s", forbidden, w.Body.String())
		}
	}
}

// TestTaskGeneric_SubmitRecordsProjectAndRequestId drives a real POST
// through the full chain (TokenAuth -> TaskPlatformGuard -> ... ->
// Distribute -> RelayTask -> relay.RelayTaskSubmit -> repo.InitTask) against
// a real httptest Suno-shaped upstream, and asserts the resulting task row
// carries the token's ProjectId and the inbound request id — the two
// columns migration 035 added.
func TestTaskGeneric_SubmitRecordsProjectAndRequestId(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	req := ctx.authedRequest(http.MethodPost, "/v1/tasks/suno", `{"model":"suno-1","action":"MUSIC"}`)
	req.Header.Set(common.RequestIdHeader, "req-generic-submit-1")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var tasks []repo.Task
	if err := ctx.db.Find(&tasks).Error; err != nil {
		t.Fatalf("query tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task rows = %d, want 1; body=%s", len(tasks), w.Body.String())
	}
	got := tasks[0]
	if got.Platform != constant.TaskPlatformSuno {
		t.Errorf("Platform = %q, want suno", got.Platform)
	}
	if got.TaskID != "upstream-task-123" {
		t.Errorf("TaskID = %q, want the upstream-issued id", got.TaskID)
	}
	if got.ProjectId != 77 {
		t.Errorf("ProjectId = %d, want 77 (the token's ProjectId, per SetupContextForToken -> RelayInfo.ProjectId -> InitTask)", got.ProjectId)
	}
	if got.RequestId != "req-generic-submit-1" {
		t.Errorf("RequestId = %q, want the inbound X-Request-Id (req-generic-submit-1)", got.RequestId)
	}
	// Pricing key: this route is billed by the request body's `model`, unlike
	// the dedicated /suno/submit/:action route, which prices the name derived
	// from the action (e.g. suno_music). relay.RelayTaskSubmit prices
	// info.OriginModelName, which is what lands in the row's properties here —
	// so an operator must have a price row for the body model. Documented in
	// docs/openapi/relay.json and the integration guide; asserted here so the
	// two cannot drift apart silently.
	if got.Properties.OriginModelName != "suno-1" {
		t.Errorf("Properties.OriginModelName = %q, want the request body's model (suno-1) — the generic route's billing key",
			got.Properties.OriginModelName)
	}
}

// TestTaskGeneric_StatusOwnedNotShadowedByForeignDuplicateTaskId is the
// A-F2 lock: task_id is only an index, not a unique constraint, so two rows
// (one foreign, one owned) can legitimately share a task_id. Before the fix
// GetTaskGeneric resolved by task_id alone via repo.GetByOnlyTaskId — whose
// unscoped query GORM defaults to ORDER BY id ASC, so the foreign row
// (seeded first, lower id) would win First() and shadow the requester's own
// task entirely (not merely trigger the ownership-mismatch branch).
// repo.GetByTaskId scopes the query itself to (user_id, task_id), so the
// owner's row is found regardless of insertion order. The two rows carry
// different FailReason values so the assertion proves WHICH row came back,
// not just that some row with a matching task_id did.
func TestTaskGeneric_StatusOwnedNotShadowedByForeignDuplicateTaskId(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	foreign := &repo.Task{
		TaskID: "dup-task-id", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id + 999, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
		FailReason: "foreign-row-must-not-be-returned",
	}
	if err := ctx.db.Create(foreign).Error; err != nil {
		t.Fatalf("seed foreign task: %v", err)
	}
	owned := &repo.Task{
		TaskID: "dup-task-id", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
		FailReason: "owned-row",
	}
	if err := ctx.db.Create(owned).Error; err != nil {
		t.Fatalf("seed owned task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/dup-task-id", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (owner's row must not be shadowed by a foreign row sharing task_id); body=%s", w.Code, w.Body.String())
	}
	var env apiSuccessEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	var raw struct {
		Data struct {
			FailReason string `json:"fail_reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw body: %v; body=%s", err, w.Body.String())
	}
	if raw.Data.FailReason != "owned-row" {
		t.Errorf("data.fail_reason = %q, want %q (the requester's own row, not the foreign row sharing task_id) — full body=%s", raw.Data.FailReason, "owned-row", w.Body.String())
	}
}

// TestTaskGeneric_StatusWrongPlatform404 is the A-F4 lock: a task the
// requester owns, but submitted under a different platform than the one in
// the GET URL, must 404 with the same fail-closed body as an absent task —
// not leak that the task_id exists under a different platform.
func TestTaskGeneric_StatusWrongPlatform404(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	task := &repo.Task{
		TaskID: "wrong-platform-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/music/wrong-platform-1", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (task exists but under a different platform); body=%s", w.Code, w.Body.String())
	}
	const want = `{"error":{"message":"Task not found","type":"invalid_request_error"}}`
	if w.Body.String() != want {
		t.Errorf("body = %s, want %s", w.Body.String(), want)
	}
}

// TestTaskGeneric_StatusSoraAcceptsOpenAIChannelType is the B-F3 lock: a
// Sora task submitted through an OpenAI-typed channel (e.g. via /v1/videos)
// is stored with Platform == the OpenAI ChannelType, because
// relay_adaptor.go's GetTaskAdaptor maps both ChannelTypeSora and
// ChannelTypeOpenAI to tasksora.TaskAdaptor. GET /v1/tasks/sora/:task_id
// must still find it.
func TestTaskGeneric_StatusSoraAcceptsOpenAIChannelType(t *testing.T) {
	ctx := setupTaskGenericRouter(t, sunoSubmitEchoUpstream)

	task := &repo.Task{
		TaskID:   "sora-via-openai-1",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAI)),
		UserId:   ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/sora/sora-via-openai-1", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (sora route must accept a task submitted through an OpenAI-typed channel); body=%s", w.Code, w.Body.String())
	}
}

// TestTaskGeneric_SubmitPlatformChannelMismatchRejected is the B-F1/A-F5
// lock: the declared :platform must match the channel Distribute actually
// selected for the request body's model. ctx's only seeded channel/ability
// is a Suno-typed channel serving model "suno-1"; POSTing that model under
// the kling platform must be rejected by TaskChannelPlatformGuard BEFORE
// any upstream call — asserted by the zero-hit upstream counter, not just
// the status code.
func TestTaskGeneric_SubmitPlatformChannelMismatchRejected(t *testing.T) {
	var upstreamHits atomic.Int64
	ctx := setupTaskGenericRouter(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		sunoSubmitEchoUpstream(w, r)
	})

	req := ctx.authedRequest(http.MethodPost, "/v1/tasks/kling", `{"model":"suno-1"}`)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "task_platform_unknown") {
		t.Errorf("body = %s, want it to contain task_platform_unknown", w.Body.String())
	}
	if hits := upstreamHits.Load(); hits != 0 {
		t.Errorf("upstream hits = %d, want 0 (must be rejected before any upstream call)", hits)
	}

	var tasks []repo.Task
	if err := ctx.db.Find(&tasks).Error; err != nil {
		t.Fatalf("query tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("task rows = %d, want 0 (rejected submit must not create a task row)", len(tasks))
	}
}

// TestGenericTaskPlatforms_CoverCompiledAdaptors is the B-F5 lock for the
// "all 11 compiled adaptors" doc/relay.json claim: genericTaskPlatforms
// must have exactly one entry per internal/adapter/provider/task/* adaptor
// package, and every entry must resolve to a real adaptor via
// relay.GetTaskAdaptor (the same resolver RelayTaskSubmit uses).
func TestGenericTaskPlatforms_CoverCompiledAdaptors(t *testing.T) {
	entries, err := os.ReadDir("../provider/task")
	if err != nil {
		t.Fatalf("read internal/adapter/provider/task: %v", err)
	}
	var adaptorDirs int
	for _, e := range entries {
		if e.IsDir() {
			adaptorDirs++
		}
	}
	if adaptorDirs == 0 {
		t.Fatal("found 0 adaptor directories under internal/adapter/provider/task — this test would pass vacuously")
	}
	if len(genericTaskPlatforms) != adaptorDirs {
		t.Fatalf("genericTaskPlatforms has %d entries, want %d (one per internal/adapter/provider/task/* adaptor package)", len(genericTaskPlatforms), adaptorDirs)
	}
	for name, key := range genericTaskPlatforms {
		if relay.GetTaskAdaptor(key) == nil {
			t.Errorf("genericTaskPlatforms[%q] = %q resolves to no adaptor via relay.GetTaskAdaptor", name, key)
		}
	}
}

// TestVideoProxy_ForeignUser403 locks video_proxy.go's existing ownership
// check (lines ~61-66), which this lane leaves untouched for consumer
// compatibility. It calls VideoProxy directly with a hand-populated
// context rather than through video-router.go's real TokenAuth ->
// PoolBalanceCheck -> Distribute -> VideoProxy chain, for a reason worth
// recording rather than hiding: driving that real chain in this same
// harness style (sqlite + httptest, GET /v1/videos/:task_id/content)
// currently returns 503 "channel is nil" from Distribute() itself, BEFORE
// VideoProxy ever runs — distributor.go's getModelRequest sets
// shouldSelectChannel=false for this path (the same branch that serves the
// OpenAI-compatible GET /v1/videos/:task_id status endpoint), and
// SetupContextForSelectedChannel(c, nil, ...) treats a nil channel as an
// abort condition rather than a no-op for fetch-style routes. That is a
// pre-existing defect in distributor.go/video-router.go, independent of
// this lane (neither file is in scope here, and fixing it is a materially
// larger, higher-risk change than this lane's mandate) — reported to the
// operator rather than fixed in this lane. This test therefore locks the
// ownership branch itself (revert it and TestVideoProxy_ForeignUser403
// goes red), which is what the plan's "must stay red on revert" asked for;
// it does not claim the branch is reachable via the live router today.
func TestVideoProxy_ForeignUser403(t *testing.T) {
	cleanup := setupSQLiteHandlerDB(t)
	defer cleanup()

	owner := 5001
	requester := 5002
	task := &repo.Task{
		TaskID: "vp-foreign-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeSora)),
		UserId: owner, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-foreign-1/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "vp-foreign-1"}}
	c.Set("id", requester)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// setupSQLiteHandlerDB is a minimal hermetic sqlite fixture for
// TestVideoProxy_ForeignUser403 — it needs only a tasks table, unlike
// setupTaskGenericRouter's full user/token/channel/ability set.
func setupSQLiteHandlerDB(t *testing.T) func() {
	t.Helper()
	seq := taskGenericDBCounter.Add(1)
	dsn := fmt.Sprintf("file:vpforeign%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	return func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
}
