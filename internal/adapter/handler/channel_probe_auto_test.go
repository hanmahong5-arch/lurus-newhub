package handler

// channel_probe_auto_test.go — L3 oracles that drive the real functions
// end to end (probeChannel against a real httptest upstream, autoProbeChannel
// against the real repo/DB, TestChannel against the real HTTP handler,
// AutomaticallyTestChannelsWithContext against the real leader gate) rather
// than hand-built stand-ins. channel_probe_policy_test.go covers
// evaluateProbeOutcome's pure decision table in isolation.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// seedModelRatio gives model a model_ratio of 1.0 for the duration of the
// test — probeChannel calls helper.ModelPriceHelper before it ever reaches
// the upstream, and an unconfigured model fails there with no network call
// at all, which would make every test below pass for the wrong reason
// (mirrors context_tier_channel_test_test.go's own setup).
func seedModelRatio(t *testing.T, model string) {
	t.Helper()
	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `":1.0}`); err != nil {
		t.Fatalf("seed model ratio for %s: %v", model, err)
	}
}

// withChannelProbeTimeoutSeconds sets CHANNEL_TEST_TIMEOUT_SECONDS for the
// duration of the test and restores whatever was there before.
func withChannelProbeTimeoutSeconds(t *testing.T, seconds string) {
	t.Helper()
	prev, had := os.LookupEnv(channelProbeTimeoutEnv)
	if err := os.Setenv(channelProbeTimeoutEnv, seconds); err != nil {
		t.Fatalf("setenv %s: %v", channelProbeTimeoutEnv, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(channelProbeTimeoutEnv, prev)
		} else {
			_ = os.Unsetenv(channelProbeTimeoutEnv)
		}
	})
}

// slowUpstreamChannel seeds a channel whose BaseURL points at srv, matching
// the shape context_tier_channel_test_test.go's setupContextTierChannelTestDB
// harness expects (OpenAI channel type, user 1 already seeded).
func slowUpstreamChannel(t *testing.T, id int, name, model, baseURL string) *repo.Channel {
	t.Helper()
	u := baseURL
	autoBan := 1 // GetAutoBan() reads this pointer explicitly; the GORM "default:1" tag is not reliably re-hydrated into the Go struct by every dialect after Create.
	channel := &repo.Channel{
		Id: id, Type: 1, Status: common.ChannelStatusEnabled,
		Name: name, Key: "sk-" + name, Models: model, Group: "default",
		BaseURL: &u, AutoBan: &autoBan,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	return channel
}

// TestProbeChannel_DeadlineBoundsHangingUpstream drives the real probeChannel
// against a real upstream that blocks until the request's context is
// cancelled. Before the deadline plumbing, probeChannel's predecessor built
// its *http.Request with no context, so this upstream would hang the probe
// indefinitely.
func TestProbeChannel_DeadlineBoundsHangingUpstream(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()
	withChannelProbeTimeoutSeconds(t, "1")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never respond on its own. The bounded 5s fallback (rather than
		// only r.Context().Done()) is test hygiene, not part of what's
		// under test: the client side (probeChannel's own 1s deadline,
		// asserted below) aborts well before either branch here matters,
		// but a canceled client request does not reliably close the
		// underlying connection promptly, which would otherwise leave this
		// handler — and upstream.Close() in the defer — blocked for the
		// life of the test binary.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer upstream.Close()

	const model = "rt-alpha"
	seedModelRatio(t, model)
	channel := slowUpstreamChannel(t, 9801, "probe-deadline-channel", model, upstream.URL)

	start := time.Now()
	result := probeChannel(channel, model, "", channelProbeOptions{RecordConsumeLog: false})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("probeChannel took %s, want bounded by the 1s deadline (well under the 3s test budget)", elapsed)
	}
	if result.localErr == nil {
		t.Fatalf("localErr = nil, want an error (the deadline firing)")
	}
	// Not errors.Is(result.localErr, context.DeadlineExceeded) directly:
	// internal/adapter/provider/api_request.go's doRequest deliberately
	// sanitizes every client.Do failure (ErrOptionWithHideErrMsg), which
	// replaces the error chain with a generic message so a probe failure
	// never leaks upstream connection internals — by design, not a bug to
	// route around here. result.context carries the *http.Request this
	// probe actually sent; its own Context().Err() reports the deadline
	// directly, which IS a legitimate errors.Is(..., context.DeadlineExceeded)
	// target, and is the exact signal probeResultIsTimeout/classifyProbeResult
	// consult in production.
	if result.context == nil || result.context.Request == nil {
		t.Fatalf("result.context/.Request = nil, want the *http.Request probeChannel sent (needed to observe its deadline)")
	}
	if !errors.Is(result.context.Request.Context().Err(), context.DeadlineExceeded) {
		t.Fatalf("result.context.Request.Context().Err() = %v, want errors.Is(..., context.DeadlineExceeded)", result.context.Request.Context().Err())
	}
	if got := classifyProbeResult(result); got != probeOutcomeTimeout {
		t.Fatalf("classifyProbeResult(result) = %q, want %q (the production classification this deadline must drive)", got, probeOutcomeTimeout)
	}
}

// TestAutoProbe_WritesMetricsNotConsumeLog drives autoProbeChannel (the
// automatic pass's real per-channel body) against a real fast upstream and
// asserts it writes zero repo.Log rows while incrementing
// metrics.ChannelProbeTotal; then drives the real TestChannel HTTP handler
// (the manual GET /api/channel/test/:id path) against the same channel and
// asserts it writes exactly one row.
func TestAutoProbe_WritesMetricsNotConsumeLog(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer upstream.Close()

	const model = "rt-beta"
	seedModelRatio(t, model)
	channel := slowUpstreamChannel(t, 9802, "probe-metrics-channel", model, upstream.URL)

	okBefore := testutil.ToFloat64(metrics.ChannelProbeTotal.WithLabelValues("ok"))

	autoProbeChannel(channel, time.Hour) // threshold far above any real latency here

	var autoLogCount int64
	if err := repo.DB.Model(&repo.Log{}).Where("channel_id = ?", channel.Id).Count(&autoLogCount).Error; err != nil {
		t.Fatalf("count log rows after automatic pass: %v", err)
	}
	if autoLogCount != 0 {
		t.Fatalf("automatic pass wrote %d consume-log rows, want 0", autoLogCount)
	}
	if okAfterAuto := testutil.ToFloat64(metrics.ChannelProbeTotal.WithLabelValues("ok")); okAfterAuto != okBefore+1 {
		t.Fatalf("ChannelProbeTotal{outcome=ok} = %v after the automatic pass, want %v (+1)", okAfterAuto, okBefore+1)
	}

	// Manual path: drive the real HTTP handler, not testChannel directly —
	// TestChannel is what GET /api/channel/test/:id actually calls. Snapshot
	// ChannelProbeTotal around this call too (L3 repair-round oracle finding
	// #10 hollow item: metrics.RecordChannelProbe in TestChannel — the
	// manual path's ONLY metrics call — was deletable with the suite green
	// because nothing re-read the counter after driving the manual handler).
	okBeforeManual := testutil.ToFloat64(metrics.ChannelProbeTotal.WithLabelValues("ok"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", common.RoleRootUser) // bypass enforceTenantScope's tenant check
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
	req, _ := http.NewRequest(http.MethodGet, "/api/channel/test/"+strconv.Itoa(channel.Id)+"?model="+model, nil)
	c.Request = req

	TestChannel(c)

	if w.Code != http.StatusOK {
		t.Fatalf("TestChannel handler status = %d, body = %s", w.Code, w.Body.String())
	}
	var manualLogCount int64
	if err := repo.DB.Model(&repo.Log{}).Where("channel_id = ?", channel.Id).Count(&manualLogCount).Error; err != nil {
		t.Fatalf("count log rows after manual path: %v", err)
	}
	if manualLogCount != 1 {
		t.Fatalf("manual path wrote %d consume-log rows, want 1 (body: %s)", manualLogCount, w.Body.String())
	}
	if okAfterManual := testutil.ToFloat64(metrics.ChannelProbeTotal.WithLabelValues("ok")); okAfterManual != okBeforeManual+1 {
		t.Fatalf("ChannelProbeTotal{outcome=ok} = %v after the manual TestChannel handler, want %v (+1)", okAfterManual, okBeforeManual+1)
	}
}

// TestAutoProbe_ThirdBreachFlipsStatusToAutoDisabled seeds two channels
// serving the same model (so neither is the sole enabled channel for it),
// points both at a slow-but-successful upstream, and drives three real
// autoProbeChannel passes against a threshold the upstream always exceeds:
// only the third flips the probed channel to AutoDisabled — and the
// disable_latency counter moves exactly then, not on breach 1 or 2. A
// fourth pass, with the same upstream now judged "clean" against a raised
// threshold, re-enables the channel and moves the enable counter (L3
// repair-round oracle for verdict finding #10's hollow "enable"/
// "disable_latency" counter calls).
func TestAutoProbe_ThirdBreachFlipsStatusToAutoDisabled(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()
	// setupContextTierChannelTestDB's fixed table list (User/Channel/Log/
	// Option) predates SoleEnabledModelsForChannel — abilities is this
	// test's own addition, not something the shared harness owns.
	if err := repo.DB.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("auto migrate Ability: %v", err)
	}

	prevAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prevAutoDisable })

	prevAutoEnable := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = prevAutoEnable })

	const model = "rt-gamma"
	seedModelRatio(t, model)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) // always over the 1ms threshold below
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer upstream.Close()

	channelA := slowUpstreamChannel(t, 9803, "probe-breach-a", model, upstream.URL)
	channelB := slowUpstreamChannel(t, 9804, "probe-breach-b", model, upstream.URL) // sibling: A is never the sole channel for model
	for _, ch := range []*repo.Channel{channelA, channelB} {
		if err := repo.DB.Create(&repo.Ability{Group: "default", Model: model, ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("seed ability for channel %d: %v", ch.Id, err)
		}
	}

	tracker := newLatencyBreachTracker()
	prevTracker := autoLatencyBreachTracker
	autoLatencyBreachTracker = tracker
	t.Cleanup(func() { autoLatencyBreachTracker = prevTracker })

	const threshold = time.Millisecond

	reloadStatus := func() int {
		t.Helper()
		var status int
		if err := repo.DB.Model(&repo.Channel{}).Select("status").Where("id = ?", channelA.Id).Scan(&status).Error; err != nil {
			t.Fatalf("reload channel %d status: %v", channelA.Id, err)
		}
		return status
	}
	// processChannelError dispatches app.DisableChannel through
	// gopool.Go — the status flip lands asynchronously, not before
	// autoProbeChannel returns. Only the breach-3 assertion (which expects
	// a change) needs to wait for it.
	waitForStatus := func(want int) int {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		var got int
		for time.Now().Before(deadline) {
			got = reloadStatus()
			if got == want {
				return got
			}
			time.Sleep(10 * time.Millisecond)
		}
		return got
	}

	disableLatencyBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency"))
	enableBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("enable"))

	autoProbeChannel(channelA, threshold)
	if got := reloadStatus(); got != common.ChannelStatusEnabled {
		t.Fatalf("after breach 1: status = %d, want still enabled (%d)", got, common.ChannelStatusEnabled)
	}
	if got := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency")); got != disableLatencyBefore {
		t.Fatalf("after breach 1: ChannelAutoStatusTotal{action=disable_latency} = %v, want unchanged at %v (no ban yet)", got, disableLatencyBefore)
	}

	autoProbeChannel(channelA, threshold)
	if got := reloadStatus(); got != common.ChannelStatusEnabled {
		t.Fatalf("after breach 2: status = %d, want still enabled (%d)", got, common.ChannelStatusEnabled)
	}
	if got := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency")); got != disableLatencyBefore {
		t.Fatalf("after breach 2: ChannelAutoStatusTotal{action=disable_latency} = %v, want unchanged at %v (still no ban)", got, disableLatencyBefore)
	}

	autoProbeChannel(channelA, threshold)
	if got := waitForStatus(common.ChannelStatusAutoDisabled); got != common.ChannelStatusAutoDisabled {
		t.Fatalf("after breach 3: status = %d, want AutoDisabled (%d)", got, common.ChannelStatusAutoDisabled)
	}
	if got := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency")); got != disableLatencyBefore+1 {
		t.Fatalf("after breach 3: ChannelAutoStatusTotal{action=disable_latency} = %v, want %v (+1, exactly on the ban pass)", got, disableLatencyBefore+1)
	}

	// Pass 4: the in-memory struct's Status field does not track the async
	// DB flip processChannelError just made — sync it so autoProbeChannel's
	// isChannelEnabled check reads reality, the same way a fresh
	// repo.GetAllChannels() read would on the next real tick. A threshold
	// far above the upstream's 20ms sleep makes this pass "clean".
	channelA.Status = common.ChannelStatusAutoDisabled
	autoProbeChannel(channelA, time.Hour)
	if got := waitForStatus(common.ChannelStatusEnabled); got != common.ChannelStatusEnabled {
		t.Fatalf("after the clean pass 4: status = %d, want re-enabled (%d)", got, common.ChannelStatusEnabled)
	}
	if got := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("enable")); got != enableBefore+1 {
		t.Fatalf("after the clean pass 4: ChannelAutoStatusTotal{action=enable} = %v, want %v (+1)", got, enableBefore+1)
	}
}

// TestAutoProbe_ErrorClassFlipsStatusToAutoDisabled drives autoProbeChannel
// against a real httptest upstream answering 401 with an invalid_api_key
// body and asserts the channel is AutoDisabled on the FIRST pass through
// the real app.DisableChannel chain — no streak needed for an error-class
// ban, unlike the latency path's three-breach hysteresis. This is the
// real-chain half of the L3 repair-round oracle for verdict finding #1
// (deleting evaluateProbeOutcome's error-class branch escaped every
// existing test in the lane).
func TestAutoProbe_ErrorClassFlipsStatusToAutoDisabled(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()

	prevAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prevAutoDisable })

	const model = "rt-zeta"
	seedModelRatio(t, model)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer upstream.Close()

	channel := slowUpstreamChannel(t, 9806, "probe-error-ban-channel", model, upstream.URL)

	errBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_error"))

	autoProbeChannel(channel, time.Hour) // threshold is irrelevant: an error-class ban never consults it

	deadline := time.Now().Add(2 * time.Second)
	var status int
	for time.Now().Before(deadline) {
		if err := repo.DB.Model(&repo.Channel{}).Select("status").Where("id = ?", channel.Id).Scan(&status).Error; err != nil {
			t.Fatalf("reload channel status: %v", err)
		}
		if status == common.ChannelStatusAutoDisabled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != common.ChannelStatusAutoDisabled {
		t.Fatalf("status = %d after one pass, want AutoDisabled (%d) — invalid_api_key must ban on the first occurrence", status, common.ChannelStatusAutoDisabled)
	}
	if errAfter := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_error")); errAfter != errBefore+1 {
		t.Fatalf("ChannelAutoStatusTotal{action=disable_error} = %v, want %v (+1)", errAfter, errBefore+1)
	}
}

// TestAutoProbe_SoleChannelSkipIncrementsMetricAndStaysEnabled drives three
// real autoProbeChannel passes against a slow-but-successful upstream with
// soleEnabledModelsFn stubbed to report the probed channel as the sole
// route for a (group, model) pair, and asserts the third breach — which
// would otherwise ban — instead increments
// latency_ban_skipped_sole_channel and leaves the channel enabled. L3
// repair-round oracle for verdict finding #10's hollow
// latency_ban_skipped_sole_channel counter call (deletable with the suite
// green before this test existed).
func TestAutoProbe_SoleChannelSkipIncrementsMetricAndStaysEnabled(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()

	prevAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prevAutoDisable })

	stubSoleEnabledModels(t, func(int, string) ([]repo.SoleModel, error) {
		return []repo.SoleModel{{Group: "default", Model: "rt-epsilon"}}, nil
	})

	const model = "rt-epsilon"
	seedModelRatio(t, model)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) // always over the 1ms threshold below
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer upstream.Close()

	channel := slowUpstreamChannel(t, 9805, "probe-sole-channel", model, upstream.URL)

	tracker := newLatencyBreachTracker()
	prevTracker := autoLatencyBreachTracker
	autoLatencyBreachTracker = tracker
	t.Cleanup(func() { autoLatencyBreachTracker = prevTracker })

	const threshold = time.Millisecond
	skipBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("latency_ban_skipped_sole_channel"))

	autoProbeChannel(channel, threshold)
	autoProbeChannel(channel, threshold)
	autoProbeChannel(channel, threshold) // third breach — would ban if not sole

	if got := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("latency_ban_skipped_sole_channel")); got != skipBefore+1 {
		t.Fatalf("ChannelAutoStatusTotal{action=latency_ban_skipped_sole_channel} = %v, want %v (+1)", got, skipBefore+1)
	}

	var status int
	if err := repo.DB.Model(&repo.Channel{}).Select("status").Where("id = ?", channel.Id).Scan(&status).Error; err != nil {
		t.Fatalf("reload channel status: %v", err)
	}
	if status != common.ChannelStatusEnabled {
		t.Fatalf("channel status = %d, want still enabled (%d) — sole channel must never be latency-banned", status, common.ChannelStatusEnabled)
	}
}

// TestAutoProbe_LocalErrOnlyResultDoesNotReenableAutoDisabledChannel drives
// autoProbeChannel against a channel whose type probeChannel refuses before
// it ever reaches the upstream (channel-test.go's unsupportedTestChannelTypes
// check) — the result carries only localErr, newAPIError stays nil, so
// verdict.Err is nil too (see probeVerdict.Err's doc). Before the
// result.localErr == nil guard, this was indistinguishable from a genuine
// clean pass and could silently re-enable a channel that was never actually
// probed. L3 repair-round oracle for verdict finding #9 / operator decision
// #4 (mirrors upstream New API's own "result.localErr == nil && ..." guard).
func TestAutoProbe_LocalErrOnlyResultDoesNotReenableAutoDisabledChannel(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()

	prevAutoEnable := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = prevAutoEnable })

	autoBan := 1
	channel := &repo.Channel{
		Id: 9807, Type: constant.ChannelTypeMidjourney, Status: common.ChannelStatusAutoDisabled,
		Name: "probe-unsupported-type-channel", Key: "sk-unsupported", Models: "mj_fast",
		Group: "default", AutoBan: &autoBan,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	enableBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("enable"))

	autoProbeChannel(channel, time.Hour)

	var status int
	if err := repo.DB.Model(&repo.Channel{}).Select("status").Where("id = ?", channel.Id).Scan(&status).Error; err != nil {
		t.Fatalf("reload channel status: %v", err)
	}
	if status != common.ChannelStatusAutoDisabled {
		t.Fatalf("status = %d, want still AutoDisabled (%d) — a probe that never reached the upstream must not re-enable the channel", status, common.ChannelStatusAutoDisabled)
	}
	if enableAfter := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("enable")); enableAfter != enableBefore {
		t.Fatalf("ChannelAutoStatusTotal{action=enable} = %v, want unchanged at %v", enableAfter, enableBefore)
	}
}

// TestAutoProbe_TimeoutNeverBansWhenAutoDisableIsOff is the real-chain half
// of TestEvaluateProbeOutcome_TimeoutDoesNotBanWhenAutoDisableIsOff: three
// real timeouts (a real httptest upstream that never responds, a 1s probe
// deadline) against autoProbeChannel with AutomaticDisableChannelEnabled
// off must never move disable_latency or change the channel's status.
func TestAutoProbe_TimeoutNeverBansWhenAutoDisableIsOff(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()
	withChannelProbeTimeoutSeconds(t, "1")

	prevAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = false
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prevAutoDisable })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer upstream.Close()

	const model = "rt-eta"
	seedModelRatio(t, model)
	channel := slowUpstreamChannel(t, 9808, "probe-disabled-flag-channel", model, upstream.URL)
	// A real abilities table with an enabled sibling: the sole-channel
	// lookup must succeed and return empty, so the only thing standing
	// between the third breach and a ban is the AutomaticDisableChannelEnabled
	// gate this test exists to prove. Without the table the lookup errors
	// and the fail-closed branch withholds the ban for the wrong reason.
	if err := repo.DB.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("auto migrate Ability: %v", err)
	}
	sibling := slowUpstreamChannel(t, 9809, "probe-disabled-flag-sibling", model, upstream.URL)
	for _, ch := range []*repo.Channel{channel, sibling} {
		if err := repo.DB.Create(&repo.Ability{Group: "default", Model: model, ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("seed ability for channel %d: %v", ch.Id, err)
		}
	}

	tracker := newLatencyBreachTracker()
	prevTracker := autoLatencyBreachTracker
	autoLatencyBreachTracker = tracker
	t.Cleanup(func() { autoLatencyBreachTracker = prevTracker })

	latencyBefore := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency"))

	for i := 0; i < latencyBreachStreakThreshold; i++ {
		autoProbeChannel(channel, time.Millisecond)
	}

	if latencyAfter := testutil.ToFloat64(metrics.ChannelAutoStatusTotal.WithLabelValues("disable_latency")); latencyAfter != latencyBefore {
		t.Fatalf("ChannelAutoStatusTotal{action=disable_latency} = %v, want unchanged at %v (AutomaticDisableChannelEnabled is off)", latencyAfter, latencyBefore)
	}
	var status int
	if err := repo.DB.Model(&repo.Channel{}).Select("status").Where("id = ?", channel.Id).Scan(&status).Error; err != nil {
		t.Fatalf("reload channel status: %v", err)
	}
	if status != common.ChannelStatusEnabled {
		t.Fatalf("status = %d, want still enabled (%d)", status, common.ChannelStatusEnabled)
	}
}

// TestChannelHealthTest_FollowerNeverLaunchesPass proves the leader gate
// added to AutomaticallyTestChannelsWithContext's tick case: with
// common.SetLeader(false), a fast-firing ticker never advances the
// heartbeat within 300ms, and the task registers itself LeaderOnly=true.
func TestChannelHealthTest_FollowerNeverLaunchesPass(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = prevMaster })

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ms := operation_setting.GetMonitorSetting()
	prevEnabled, prevMinutes := ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes
	ms.AutoTestChannelEnabled = true
	ms.AutoTestChannelMinutes = 0 // rounds to a 0s wait — fires as fast as possible
	t.Cleanup(func() { ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes = prevEnabled, prevMinutes })

	metrics.LeaderTaskLastSuccess.WithLabelValues(channelHealthTestTaskName).Set(0)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		AutomaticallyTestChannelsWithContext(ctx)
		close(done)
	}()
	<-done

	if got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(channelHealthTestTaskName)); got != 0 {
		t.Errorf("LeaderTaskLastSuccess{task=channel-health-test} = %v on a follower, want 0 (a follower must never launch the pass)", got)
	}

	found := false
	for _, task := range taskreg.Snapshot() {
		if task.Name == channelHealthTestTaskName {
			found = true
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true", channelHealthTestTaskName)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q", channelHealthTestTaskName)
	}
}
