package handler

// task_artifacts_test.go — oracle tests for cycle-8 L9's artefact listing
// (GET .../artifacts) and content proxy (GET .../artifacts/:key/content),
// driven through the REAL middleware chain task-router.go mounts on the
// /v1/tasks group (TokenAuth -> TaskPlatformGuard -> PoolBalanceCheck ->
// CostSpikeLimit -> EntitlementCheck -> ModelRequestRateLimit ->
// BusinessRateLimit -> RelayConcurrencyLimit), the same hand-mirrored
// convention setupTaskGenericRouter (task_generic_test.go, same package)
// already uses for the sibling status route — REAL-CHAIN RULE.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

var taskArtifactsDBCounter atomic.Int64

type taskArtifactsCtx struct {
	router *gin.Engine
	db     *gorm.DB
	user   *repo.User
	token  *repo.Token
}

// setupTaskArtifactsRouter mirrors setupTaskGenericRouter's harness (same
// tables, same middleware chain) but mounts the two artefact routes this
// lane adds instead of the submit/status pair — no upstream/channel is
// needed because neither artefact route calls Distribute or resolves a
// channel (see task-router.go's comment on why Distribute is POST-only).
func setupTaskArtifactsRouter(t *testing.T) *taskArtifactsCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true // let the "streams http" case reach an httptest.Server on 127.0.0.1
	t.Cleanup(func() { *fs = prevFetchSetting })

	seq := taskArtifactsDBCounter.Add(1)
	dsn := fmt.Sprintf("file:taskartifacts%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Task{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMemCache := common.MemoryCacheEnabled
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false

	idBase := 930000000 + int(seq)*1000
	user := &repo.User{
		Id:       idBase,
		Username: fmt.Sprintf("task-artifacts-user-%d", seq), DisplayName: "Task Artifacts",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("taskartifacts%d@test.local", seq), Group: "default", Quota: 100000000,
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
		UserId: user.Id, TenantId: "default", Key: key, Name: "task-artifacts-token",
		Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
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
	taskGroup.GET("/:platform/:task_id/artifacts", ListTaskArtifacts)
	taskGroup.GET("/:platform/:task_id/artifacts/:key/content", GetTaskArtifactContent)

	ctx := &taskArtifactsCtx{router: router, db: db, user: user, token: token}
	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.MemoryCacheEnabled = prevMemCache
		common.RedisEnabled = prevRedis
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return ctx
}

func (ctx *taskArtifactsCtx) authedRequest(method, target, host string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
	if host != "" {
		req.Host = host
	}
	return req
}

type artifactListEnvelope struct {
	Success bool           `json:"success"`
	Data    []TaskArtifact `json:"data"`
}

func dataURL(mimeType string, payload string) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString([]byte(payload))
}

// TestTaskArtifacts_ListProjectsDataAndPrivateData is the listing oracle:
// the fault-simulator task shape (faultsim.go's FaultSimTaskFetch —
// {"url": <data: text>, "image_url": <data: image>}) projects into two
// artifacts with the right type/mime_type/size/status.
func TestTaskArtifacts_ListProjectsDataAndPrivateData(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	textURL := dataURL("text/plain", "hello artefact")
	imageURL := dataURL("image/png", "\x89PNG-not-real-but-bytes")
	task := &repo.Task{
		TaskID: "artifacts-list-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"url": textURL, "image_url": imageURL})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-list-1/artifacts", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var env artifactListEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, w.Body.String())
	}
	if !env.Success || len(env.Data) != 2 {
		t.Fatalf("envelope = %+v, want success=true with 2 artifacts", env)
	}
	byKey := map[string]TaskArtifact{}
	for _, a := range env.Data {
		byKey[a.Key] = a
	}
	textArt, ok := byKey["url"]
	if !ok {
		t.Fatalf("missing 'url' artifact; got %+v", env.Data)
	}
	if textArt.Type != "text" || textArt.MimeType != "text/plain" || textArt.Size != int64(len("hello artefact")) || textArt.Status != string(repo.TaskStatusSuccess) {
		t.Errorf("'url' artifact = %+v, want type=text mime_type=text/plain size=%d status=SUCCESS", textArt, len("hello artefact"))
	}
	imageArt, ok := byKey["image_url"]
	if !ok {
		t.Fatalf("missing 'image_url' artifact; got %+v", env.Data)
	}
	if imageArt.Type != "image" || imageArt.MimeType != "image/png" {
		t.Errorf("'image_url' artifact = %+v, want type=image mime_type=image/png", imageArt)
	}
}

// TestTaskArtifacts_ForeignUser404 locks the fail-closed ownership check:
// a foreign user's request and a request for an absent task_id must both
// 404 with the byte-identical body task_generic.go's respondTaskNotFound
// produces, on BOTH artifact routes.
func TestTaskArtifacts_ForeignUser404(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	foreignTask := &repo.Task{
		TaskID: "artifacts-foreign-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id + 1, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	foreignTask.SetData(map[string]string{"url": dataURL("text/plain", "not yours")})
	if err := ctx.db.Create(foreignTask).Error; err != nil {
		t.Fatalf("seed foreign task: %v", err)
	}

	const wantBody = `{"error":{"message":"Task not found","type":"invalid_request_error"}}`

	listReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-foreign-1/artifacts", "")
	listW := httptest.NewRecorder()
	ctx.router.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusNotFound || listW.Body.String() != wantBody {
		t.Errorf("list foreign: status=%d body=%s, want 404 %s", listW.Code, listW.Body.String(), wantBody)
	}

	absentListReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/does-not-exist/artifacts", "")
	absentListW := httptest.NewRecorder()
	ctx.router.ServeHTTP(absentListW, absentListReq)
	if absentListW.Code != http.StatusNotFound || absentListW.Body.String() != wantBody {
		t.Errorf("list absent: status=%d body=%s, want 404 %s", absentListW.Code, absentListW.Body.String(), wantBody)
	}

	contentReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-foreign-1/artifacts/url/content", "")
	contentW := httptest.NewRecorder()
	ctx.router.ServeHTTP(contentW, contentReq)
	if contentW.Code != http.StatusNotFound || contentW.Body.String() != wantBody {
		t.Errorf("content foreign: status=%d body=%s, want 404 %s", contentW.Code, contentW.Body.String(), wantBody)
	}
}

// TestTaskArtifacts_ContentInlinesDataUrlAndStreamsHttp is the positive
// content-proxy oracle: a data: URL is served inline with zero network
// calls, and an http:// URL is streamed byte-identical from a real
// upstream with its Content-Type carried through. It also asserts B-F8's
// security headers on both the data: and http(s) branches: this route is
// browser-navigable (TokenAuth also accepts the bearer token as a `?key=`
// query parameter — middleware/auth.go), so a vendor-supplied artefact must
// not be MIME-sniffed, cached, or leak this URL via Referer.
func TestTaskArtifacts_ContentInlinesDataUrlAndStreamsHttp(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	const payload = "the seeded payload"
	textURL := dataURL("text/plain", payload)

	upstreamBody := []byte("streamed-from-upstream-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(upstreamBody)
	}))
	defer upstream.Close()

	task := &repo.Task{
		TaskID: "artifacts-content-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"text": textURL, "remote": upstream.URL + "/artefact.bin"})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	textReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-content-1/artifacts/text/content", "")
	textW := httptest.NewRecorder()
	ctx.router.ServeHTTP(textW, textReq)
	if textW.Code != http.StatusOK {
		t.Fatalf("data: content status = %d, want 200; body=%s", textW.Code, textW.Body.String())
	}
	if textW.Body.String() != payload {
		t.Errorf("data: content body = %q, want %q (byte-identical to the seeded payload)", textW.Body.String(), payload)
	}
	if ct := textW.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("data: content Content-Type = %q, want text/plain", ct)
	}
	assertArtifactSecurityHeaders(t, "data: branch", textW.Header())

	remoteReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-content-1/artifacts/remote/content", "")
	remoteW := httptest.NewRecorder()
	ctx.router.ServeHTTP(remoteW, remoteReq)
	if remoteW.Code != http.StatusOK {
		t.Fatalf("http content status = %d, want 200; body=%s", remoteW.Code, remoteW.Body.String())
	}
	if remoteW.Body.String() != string(upstreamBody) {
		t.Errorf("http content body = %q, want %q (streamed byte-identical)", remoteW.Body.String(), string(upstreamBody))
	}
	if ct := remoteW.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("http content Content-Type = %q, want application/octet-stream (carried through from upstream)", ct)
	}
	assertArtifactSecurityHeaders(t, "http(s) branch", remoteW.Header())
}

// assertArtifactSecurityHeaders is B-F8's oracle assertion, shared by both
// branches of the content route.
func assertArtifactSecurityHeaders(t *testing.T, label string, h http.Header) {
	t.Helper()
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", label, got)
	}
	if got := h.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("%s: Cache-Control = %q, want %q", label, got, "private, no-store")
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("%s: Referrer-Policy = %q, want no-referrer", label, got)
	}
}

// TestTaskArtifacts_ContentRefusesSelfUrlAndUnknownKey covers both refusal
// paths: an artifact URL whose host matches the inbound request's own Host
// header (the loop guard), and a :key the listing would never produce.
func TestTaskArtifacts_ContentRefusesSelfUrlAndUnknownKey(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	const selfHost = "gateway.invalid"
	task := &repo.Task{
		TaskID: "artifacts-self-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"loop": "http://" + selfHost + "/v1/tasks/suno/artifacts-self-1/artifacts"})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	selfReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-self-1/artifacts/loop/content", selfHost)
	selfW := httptest.NewRecorder()
	ctx.router.ServeHTTP(selfW, selfReq)
	if selfW.Code != http.StatusBadGateway {
		t.Errorf("self-url content status = %d, want 502; body=%s", selfW.Code, selfW.Body.String())
	}
	if !strings.Contains(selfW.Body.String(), "self-referential") {
		t.Errorf("self-url content body = %s, want it to mention the self-referential refusal", selfW.Body.String())
	}
	if !strings.Contains(selfW.Body.String(), `"code":"artifact_request_rejected"`) {
		t.Errorf("self-url content body = %s, want code=artifact_request_rejected (B-F6)", selfW.Body.String())
	}

	unknownReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-self-1/artifacts/no-such-key/content", "")
	unknownW := httptest.NewRecorder()
	ctx.router.ServeHTTP(unknownW, unknownReq)
	if unknownW.Code != http.StatusNotFound {
		t.Errorf("unknown-key content status = %d, want 404; body=%s", unknownW.Code, unknownW.Body.String())
	}
	if !strings.Contains(unknownW.Body.String(), "Artifact not found") {
		t.Errorf("unknown-key content body = %s, want it to mention Artifact not found", unknownW.Body.String())
	}
	if !strings.Contains(unknownW.Body.String(), `"code":"artifact_not_found"`) {
		t.Errorf("unknown-key content body = %s, want code=artifact_not_found (B-F6)", unknownW.Body.String())
	}
}

// TestTaskArtifacts_ListProjectsFailReasonForVideoPlatforms is B-F1/A-F1's
// oracle: a Kling-shaped task (nested Data, real result URL stored in
// FailReason by task_video.go's poller -- see artifactURLsForTask's doc
// comment) must list one "video" artifact, and the content route must
// stream it, even though extractArtifactURLs alone finds nothing at the
// top level of Data.
func TestTaskArtifacts_ListProjectsFailReasonForVideoPlatforms(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	upstreamBody := []byte("kling-video-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(upstreamBody)
	}))
	defer upstream.Close()

	klingPlatform := constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling))
	task := &repo.Task{
		TaskID: "artifacts-kling-1", Platform: klingPlatform,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
		FailReason: upstream.URL + "/kling-result.mp4",
	}
	// The nested shape kling/adaptor.go's Data.TaskResult.Videos[0].Url
	// actually is -- no top-level string field, so extractArtifactURLs
	// alone (without the FailReason projection) would find nothing here.
	task.SetData(map[string]any{
		"data": map[string]any{
			"task_result": map[string]any{
				"videos": []map[string]any{{"url": "https://vendor.example/ignored-nested.mp4"}},
			},
		},
	})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	listReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/kling/artifacts-kling-1/artifacts", "")
	listW := httptest.NewRecorder()
	ctx.router.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listW.Code, listW.Body.String())
	}
	var env artifactListEnvelope
	if err := json.Unmarshal(listW.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, listW.Body.String())
	}
	if len(env.Data) != 1 || env.Data[0].Key != "video" {
		t.Fatalf("data = %+v, want exactly one entry with key=video", env.Data)
	}
	if env.Data[0].Type != "video" || env.Data[0].Status != string(repo.TaskStatusSuccess) {
		t.Errorf("video artifact = %+v, want type=video status=SUCCESS", env.Data[0])
	}

	contentReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/kling/artifacts-kling-1/artifacts/video/content", "")
	contentW := httptest.NewRecorder()
	ctx.router.ServeHTTP(contentW, contentReq)
	if contentW.Code != http.StatusOK {
		t.Fatalf("content status = %d, want 200; body=%s", contentW.Code, contentW.Body.String())
	}
	if contentW.Body.String() != string(upstreamBody) {
		t.Errorf("content body = %q, want %q (streamed from FailReason's URL)", contentW.Body.String(), string(upstreamBody))
	}
}

// TestTaskArtifacts_ListExcludesFailReasonForSoraAndGemini locks the
// exclusion: sora and gemini store THIS GATEWAY'S OWN /v1/videos/.../content
// URL in FailReason (not a vendor asset URL -- see
// failReasonArtifactExcludedRoutes' doc comment), so projecting it as an
// artifact must not happen even though it looks like an artifact URL.
func TestTaskArtifacts_ListExcludesFailReasonForSoraAndGemini(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	cases := []struct {
		route    string
		platform constant.TaskPlatform
	}{
		{"sora", constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeSora))},
		{"gemini", constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini))},
	}
	for i, tc := range cases {
		taskID := fmt.Sprintf("artifacts-excluded-%d", i)
		task := &repo.Task{
			TaskID: taskID, Platform: tc.platform,
			UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
			FailReason: "https://hub.example.test/v1/videos/" + taskID + "/content",
		}
		task.SetData(map[string]any{"raw": "vendor-native-body-not-a-url"})
		if err := ctx.db.Create(task).Error; err != nil {
			t.Fatalf("seed %s task: %v", tc.route, err)
		}

		listReq := ctx.authedRequest(http.MethodGet, "/v1/tasks/"+tc.route+"/"+taskID+"/artifacts", "")
		listW := httptest.NewRecorder()
		ctx.router.ServeHTTP(listW, listReq)
		if listW.Code != http.StatusOK {
			t.Fatalf("%s: list status = %d, want 200; body=%s", tc.route, listW.Code, listW.Body.String())
		}
		var env artifactListEnvelope
		if err := json.Unmarshal(listW.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode body: %v; body=%s", tc.route, err, listW.Body.String())
		}
		if len(env.Data) != 0 {
			t.Errorf("%s: data = %+v, want empty (FailReason is the gateway's own URL, must not be listed)", tc.route, env.Data)
		}
	}
}

// TestTaskArtifacts_ContentRefusesEgressBlockedURL is A-F5's oracle for the
// fetch_setting egress check: a private-IP artifact URL, with
// allow_private_ip explicitly turned back off for this test, must be
// refused BEFORE any dial -- this is the one guard genuinely distinguishable
// from a network-layer failure (app.ValidateOutboundURL rejects the URL by
// content, not by dialing it), unlike the scheme check (see
// TestAllowedArtifactScheme's doc comment and video_proxy.go's).
func TestTaskArtifacts_ContentRefusesEgressBlockedURL(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	fs := system_setting.GetFetchSetting()
	prevAllowPrivate := fs.AllowPrivateIp
	fs.AllowPrivateIp = false // re-enable the private-IP block this specific test needs
	t.Cleanup(func() { fs.AllowPrivateIp = prevAllowPrivate })

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "egress_check"))

	task := &repo.Task{
		TaskID: "artifacts-egress-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"blocked": "http://127.0.0.1:9/private"})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-egress-1/artifacts/blocked/content", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "egress check") {
		t.Errorf("body = %s, want it to mention the egress check refusal", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"artifact_request_rejected"`) {
		t.Errorf("body = %s, want code=artifact_request_rejected", w.Body.String())
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "egress_check"))
	if after-before != 1 {
		t.Errorf("egress_check counter delta = %v, want 1", after-before)
	}
}

// TestTaskArtifacts_ContentRefusesOversizedKnownContentLength is B-F3/B-F4's
// oracle for the KNOWN-length half: an upstream that declares a
// Content-Length over the cap must be refused with a 502 BEFORE any header
// is written to the client -- never a 200 with a body silently truncated
// under a Content-Length that no longer matches what was actually sent.
// The upstream deliberately writes far fewer bytes than it declares (real
// multi-hundred-MiB transfers are not exercised in a unit test); this
// handler must reject on the declared length alone, without reading the
// body.
func TestTaskArtifacts_ContentRefusesOversizedKnownContentLength(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", maxProxiedArtifactBytes+1))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short-body-declared-length-is-what-matters"))
	}))
	defer upstream.Close()

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "size_cap"))

	task := &repo.Task{
		TaskID: "artifacts-oversize-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"big": upstream.URL + "/huge.mp4"})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-oversize-1/artifacts/big/content", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exceeds the proxy size limit") {
		t.Errorf("body = %s, want it to mention the size limit", w.Body.String())
	}
	if w.Body.String() == "short-body-declared-length-is-what-matters" {
		t.Fatalf("body leaked the upstream bytes instead of refusing")
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "size_cap"))
	if after-before != 1 {
		t.Errorf("size_cap counter delta = %v, want 1", after-before)
	}
}

// TestTaskArtifacts_ContentTruncatesUnknownLengthStreamAtCapAndCounts is
// B-F3/B-F4's oracle for the UNKNOWN-length half: a chunked upstream (no
// Content-Length) cannot be rejected before headers are written, so it is
// still stopped at the cap -- but that truncation must be counted. The cap
// is lowered via the package-level var seam (maxProxiedArtifactBytes) so
// this test does not need to transfer hundreds of megabytes.
func TestTaskArtifacts_ContentTruncatesUnknownLengthStreamAtCapAndCounts(t *testing.T) {
	ctx := setupTaskArtifactsRouter(t)

	prevCap := maxProxiedArtifactBytes
	maxProxiedArtifactBytes = 8
	t.Cleanup(func() { maxProxiedArtifactBytes = prevCap })

	fullBody := strings.Repeat("x", 40)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Del("Content-Length") // force unknown length (chunked)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(fullBody))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "size_cap"))

	task := &repo.Task{
		TaskID: "artifacts-chunked-1", Platform: constant.TaskPlatformSuno,
		UserId: ctx.user.Id, Status: repo.TaskStatusSuccess, SubmitTime: time.Now().Unix(),
	}
	task.SetData(map[string]string{"chunked": upstream.URL + "/chunked.bin"})
	if err := ctx.db.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	req := ctx.authedRequest(http.MethodGet, "/v1/tasks/suno/artifacts-chunked-1/artifacts/chunked/content", "")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (truncation happens after headers are already written); body=%s", w.Code, w.Body.String())
	}
	if int64(w.Body.Len()) != maxProxiedArtifactBytes {
		t.Errorf("body length = %d, want %d (truncated at the lowered cap)", w.Body.Len(), maxProxiedArtifactBytes)
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("artifact_content", "size_cap"))
	if after-before != 1 {
		t.Errorf("size_cap counter delta = %v, want 1 (truncation must still be counted)", after-before)
	}
}
