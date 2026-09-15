package handler

// video_proxy_test.go — cycle-8 L9's mutation lock for the new guard
// VideoProxy gained in this lane (task_media_guard.go's
// allowedArtifactScheme/isSelfOrLoopURL, called from video_proxy.go right
// after it resolves videoURL). VideoProxy's pre-existing ownership check
// and its 403 are untouched and locked separately by
// TestVideoProxy_ForeignUser403 in task_generic_test.go (same package) —
// this file does not duplicate that test.
//
// Called directly with a hand-populated context rather than through
// video-router.go's real TokenAuth -> PoolBalanceCheck -> Distribute ->
// VideoProxy chain, for the same documented reason
// TestVideoProxy_ForeignUser403 gives: that real chain currently 503s
// inside Distribute() itself before VideoProxy runs (a pre-existing
// defect in distributor.go/video-router.go, out of scope for this lane).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

var videoProxyGuardDBCounter atomic.Int64

// setupVideoProxyGuardDB is setupSQLiteHandlerDB's shape (task_generic_test.go)
// plus a Channel table: this file's test needs a real channel row (VideoProxy
// calls repo.CacheGetChannel(task.ChannelId) before it ever resolves videoURL).
func setupVideoProxyGuardDB(t *testing.T) func() {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	seq := videoProxyGuardDBCounter.Add(1)
	dsn := fmt.Sprintf("file:vpguard%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Task{}, &repo.Channel{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMemCache := common.MemoryCacheEnabled
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false
	return func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.MemoryCacheEnabled = prevMemCache
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
}

// TestVideoProxy_RefusesSelfReferentialURL is the mutation lock: dropping
// isSelfOrLoopURL's self-check in task_media_guard.go (or VideoProxy's call
// to it) makes this red. The channel type is deliberately NOT
// Gemini/OpenAI/Sora, so video_proxy.go's `default:` branch is exercised —
// task.FailReason (a SUCCESS task's stored result URL for every other
// channel type) is used directly as videoURL, the simplest way to control
// the exact URL VideoProxy will dial.
//
// The "self" target is a REAL, reachable httptest.Server (not an
// unresolvable hostname) that would answer 200 if VideoProxy actually
// dialed it: a fake/unresolvable host would make the guard-dropped case
// fail with a DNS/dial error that also happens to produce a 502, which
// would pass this test whether or not the guard fired at all. Asserting
// the upstream server saw zero requests is what actually distinguishes
// "refused before dialing" from "dialed and merely got no reply".
func TestVideoProxy_RefusesSelfReferentialURL(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	var upstreamHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("should-never-be-reached"))
	}))
	defer upstream.Close()
	selfHost := upstream.Listener.Addr().String() // e.g. "127.0.0.1:PORT" — same host:port as below

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-guard-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	owner := 6001
	task := &repo.Task{
		TaskID: "vp-self-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: upstream.URL + "/v1/videos/vp-self-1/content",
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-self-1/content", nil)
	c.Request.Host = selfHost // same host:port as the videoURL above
	c.Params = gin.Params{{Key: "task_id", Value: "vp-self-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Errorf("upstream received %d request(s), want 0 — the guard must refuse before dialing", upstreamHits.Load())
	}
}

// TestVideoProxy_StreamsUpstreamContent is the non-regression counterpart:
// an ordinary external videoURL (different host from the inbound request)
// must still be fetched and streamed byte-identical, proving the new guard
// does not collaterally block the legitimate case.
func TestVideoProxy_StreamsUpstreamContent(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	upstreamBody := []byte("real-video-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(upstreamBody)
	}))
	defer upstream.Close()

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-stream-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	owner := 6002
	task := &repo.Task{
		TaskID: "vp-stream-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: upstream.URL + "/video.mp4",
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-stream-1/content", nil)
	c.Request.Host = "hub.example.test" // deliberately not upstream's host
	c.Params = gin.Params{{Key: "task_id", Value: "vp-stream-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(upstreamBody) {
		t.Errorf("body = %q, want %q (streamed byte-identical)", w.Body.String(), string(upstreamBody))
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff (B-F8)", got)
	}
}

// TestVideoProxy_UnfetchableSchemeKeepsPreLaneMessage is the operator
// ruling's byte-identical requirement for "the previously-502 case": a
// FailReason that does not look like an http(s) URL used to reach
// client.Do() and fail there with "Failed to fetch video content" — this
// lane's new scheme guard intercepts it earlier, but MUST keep that exact
// message (not a distinct "not fetchable" message), and must still count
// the rejection.
//
// This is deliberately NOT a claim that the guard is what causes the 502:
// removing it does not change this test's outcome, because Go's
// http.Client itself refuses to dial a non-http(s) scheme with no network
// I/O, landing in the identical err!=nil branch with the identical message
// (see video_proxy.go's doc comment on this guard for the full reasoning,
// and A-F4's finding in the round-1 report, which this repair accepts
// rather than papering over with a test that would only look
// non-hollow). What this test does lock is the MESSAGE TEXT staying
// byte-identical to pre-lane behaviour, which a careless "improve the
// message" edit could silently break.
func TestVideoProxy_UnfetchableSchemeKeepsPreLaneMessage(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-scheme-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "scheme"))

	owner := 6003
	task := &repo.Task{
		TaskID: "vp-scheme-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: "file:///etc/passwd",
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-scheme-1/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "vp-scheme-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Failed to fetch video content") {
		t.Errorf("body = %s, want the pre-lane message text preserved", w.Body.String())
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "scheme"))
	if after-before != 1 {
		t.Errorf("scheme counter delta = %v, want 1", after-before)
	}
}

// TestVideoProxy_RefusesOversizedKnownContentLength is B-F3's oracle: an
// upstream that declares a Content-Length over the cap must be rejected
// with a 502 before any header/body is forwarded — the previous behaviour
// (copyCapped truncating at 200MiB while the upstream Content-Length was
// still forwarded unmodified) handed the client a corrupt file under a
// 200. The upstream deliberately writes far fewer bytes than it declares;
// this handler must reject on the declared length alone.
func TestVideoProxy_RefusesOversizedKnownContentLength(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", maxProxiedArtifactBytes+1))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short-body-declared-length-is-what-matters"))
	}))
	defer upstream.Close()

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-oversize-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "size_cap"))

	owner := 6004
	task := &repo.Task{
		TaskID: "vp-oversize-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: upstream.URL + "/huge.mp4",
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-oversize-1/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "vp-oversize-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exceeds the proxy size limit") {
		t.Errorf("body = %s, want it to mention the size limit", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "short-body-declared-length-is-what-matters") {
		t.Fatalf("body leaked the upstream bytes instead of refusing")
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "size_cap"))
	if after-before != 1 {
		t.Errorf("size_cap counter delta = %v, want 1", after-before)
	}
}

// TestVideoProxy_TruncatesUnknownLengthStreamAtCapAndCounts is B-F3/B-F4's
// oracle for the UNKNOWN-length half on the legacy route: a chunked
// upstream (no Content-Length) cannot be rejected before headers are
// written, so it is still stopped at the cap — but that truncation must be
// counted. The cap is lowered via the package-level var seam
// (maxProxiedArtifactBytes) so this test does not need to transfer hundreds
// of megabytes.
func TestVideoProxy_TruncatesUnknownLengthStreamAtCapAndCounts(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	prevCap := maxProxiedArtifactBytes
	maxProxiedArtifactBytes = 8
	t.Cleanup(func() { maxProxiedArtifactBytes = prevCap })

	fullBody := strings.Repeat("y", 40)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Del("Content-Length") // force unknown length (chunked)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(fullBody))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-chunked-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "size_cap"))

	owner := 6005
	task := &repo.Task{
		TaskID: "vp-chunked-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: upstream.URL + "/chunked.bin",
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-chunked-1/content", nil)
	c.Params = gin.Params{{Key: "task_id", Value: "vp-chunked-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (truncation happens after headers are already written); body=%s", w.Code, w.Body.String())
	}
	if int64(w.Body.Len()) != maxProxiedArtifactBytes {
		t.Errorf("body length = %d, want %d (truncated at the lowered cap)", w.Body.Len(), maxProxiedArtifactBytes)
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "size_cap"))
	if after-before != 1 {
		t.Errorf("size_cap counter delta = %v, want 1 (truncation must still be counted)", after-before)
	}
}

// TestVideoProxy_RefusesPrivateAddress is cycle-9 L4's oracle: a literal
// private-IP videoURL, with allow_private_ip explicitly turned back off for
// this test (setupVideoProxyGuardDB's default of true exists so the OTHER
// tests in this file can reach an httptest.Server on 127.0.0.1), must be
// refused by the same app.ValidateOutboundURL gate the artefact-content
// route already applies (TestTaskArtifacts_ContentRefusesEgressBlockedURL,
// task_artifacts_test.go) — before this lane, VideoProxy never called it at
// all. Deleting the ValidateOutboundURL call from VideoProxy turns this red.
func TestVideoProxy_RefusesPrivateAddress(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	fs := system_setting.GetFetchSetting()
	prevAllowPrivate := fs.AllowPrivateIp
	fs.AllowPrivateIp = false // re-enable the private-IP block this specific test needs
	t.Cleanup(func() { fs.AllowPrivateIp = prevAllowPrivate })

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-egress-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "egress_check"))

	owner := 6006
	task := &repo.Task{
		TaskID: "vp-egress-1", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: "http://10.0.0.1/x", // literal private-IP target, not a self/loop URL
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-egress-1/content", nil)
	c.Request.Host = "hub.example.test" // deliberately not the target — isolates the egress check from the self-URL check
	c.Params = gin.Params{{Key: "task_id", Value: "vp-egress-1"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "egress check") {
		t.Errorf("body = %s, want it to mention the egress check refusal (same shape as the artefact route)", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"artifact_request_rejected"`) {
		t.Errorf("body = %s, want code=artifact_request_rejected (the artefact route's shape, reused rather than a second one invented)", w.Body.String())
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "egress_check"))
	if after-before != 1 {
		t.Errorf("egress_check counter delta = %v, want 1", after-before)
	}
}

// TestVideoProxy_RefusesDNSRebindShape covers the OTHER half of what
// app.ValidateOutboundURL checks: a domain name (not a literal IP) that
// resolves to a private address. fetch_setting's ApplyIPFilterForDomain is
// forced on in the default blacklist posture (see
// app.ValidateOutboundURL's doc comment in ssrf_guard.go), so "localhost"
// is rejected exactly like a 127.0.0.1 literal once resolved — this is the
// half TestVideoProxy_RefusesPrivateAddress's literal-IP target does not
// exercise, so the two routes cannot drift on only ONE of the two shapes
// app.ValidateOutboundURL covers.
func TestVideoProxy_RefusesDNSRebindShape(t *testing.T) {
	cleanup := setupVideoProxyGuardDB(t)
	defer cleanup()

	fs := system_setting.GetFetchSetting()
	prevAllowPrivate := fs.AllowPrivateIp
	fs.AllowPrivateIp = false
	t.Cleanup(func() { fs.AllowPrivateIp = prevAllowPrivate })

	weight := uint(10)
	priority := int64(0)
	baseURL := "https://example.invalid"
	channel := &repo.Channel{
		Type: constant.ChannelTypeKling, Key: "sk-unused", Status: common.ChannelStatusEnabled,
		Name: "video-proxy-dns-rebind-channel", BaseURL: &baseURL, Weight: &weight, Priority: &priority,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	before := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "egress_check"))

	owner := 6007
	task := &repo.Task{
		TaskID: "vp-egress-2", Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
		UserId: owner, ChannelId: int(channel.Id), Status: repo.TaskStatusSuccess,
		FailReason: "http://localhost:80/x", // domain that resolves to a loopback address, not a literal IP
		SubmitTime: time.Now().Unix(),
	}
	if err := repo.DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/vp-egress-2/content", nil)
	c.Request.Host = "hub.example.test" // distinct from "localhost" — isolates the egress check from the self-URL check
	c.Params = gin.Params{{Key: "task_id", Value: "vp-egress-2"}}
	c.Set("id", owner)
	c.Set("role", common.RoleCommonUser)

	VideoProxy(c)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "egress check") {
		t.Errorf("body = %s, want it to mention the egress check refusal", w.Body.String())
	}
	after := testutil.ToFloat64(metrics.TaskMediaGuardRejectionsTotal.WithLabelValues("video_proxy", "egress_check"))
	if after-before != 1 {
		t.Errorf("egress_check counter delta = %v, want 1", after-before)
	}
}

// vendorURLGuardHandlers is cycle-9 L4's maintained registry of the handler
// functions that dial a vendor-supplied URL taken from task/channel-instance
// data (as opposed to a channel's admin-configured base_url, already gated
// at write time by channel.go/v2_channel_actions.go's own
// app.ValidateOutboundURL calls). TestTaskMediaRoutes_BothCallTheEgressGuard
// only enforces the guard on the functions enumerated here — this is a
// registry lock, not automatic discovery of new routes; a THIRD handler that
// serves this class of URL must be added to this slice for the oracle to
// cover it, the same convention internal/pkg/metrics/
// declared_series_written_test.go uses for its own textual scan.
var vendorURLGuardHandlers = []struct {
	file, funcName string
}{
	{"video_proxy.go", "VideoProxy"},
	{"task_media_guard.go", "streamMediaContent"},
}

// extractTopLevelFuncSource returns funcName's full source text (signature
// through its closing brace) from path, relying on gofmt's convention that a
// top-level function's closing brace is a lone "}" at column 0.
func extractTopLevelFuncSource(t *testing.T, path, funcName string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	prefix := "func " + funcName + "("
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimRight(lines[j], "\r") != "}" {
			j++
		}
		if j >= len(lines) {
			j = len(lines) - 1
		}
		return strings.Join(lines[i:j+1], "\n")
	}
	t.Fatalf("function %s not found in %s", funcName, path)
	return ""
}

// TestTaskMediaRoutes_BothCallTheEgressGuard is cycle-9 L4's anti-drift
// oracle: every handler in vendorURLGuardHandlers above must call
// app.ValidateOutboundURL directly in its own source (not merely
// transitively through a helper this test does not also check), so the two
// routes cannot drift apart again. Deleting the ValidateOutboundURL call
// from EITHER VideoProxy or streamMediaContent turns this red.
func TestTaskMediaRoutes_BothCallTheEgressGuard(t *testing.T) {
	for _, h := range vendorURLGuardHandlers {
		body := extractTopLevelFuncSource(t, h.file, h.funcName)
		if !strings.Contains(body, "ValidateOutboundURL(") {
			t.Errorf("%s (%s) does not call app.ValidateOutboundURL — a handler serving a vendor-supplied URL must egress-check it before dialing", h.funcName, h.file)
		}
	}
}
