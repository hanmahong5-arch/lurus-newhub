package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func monCh(id int, status int, tenant, models string) *repo.Channel {
	return &repo.Channel{Id: id, Status: status, TenantId: tenant, Models: models, Key: "sk-test-" + strconv.Itoa(id)}
}

func withMonitoringChannels(t *testing.T, chans []*repo.Channel) {
	t.Helper()
	prev := listChannelsForMonitoring
	listChannelsForMonitoring = func() ([]*repo.Channel, error) { return chans, nil }
	t.Cleanup(func() { listChannelsForMonitoring = prev; app.ClearChannelCooldowns() })
}

func TestRefreshChannelMetrics_StateGauge(t *testing.T) {
	cooling := monCh(9101, common.ChannelStatusEnabled, "default", "model-a")
	healthy := monCh(9102, common.ChannelStatusEnabled, "default", "model-a")
	manual := monCh(9103, common.ChannelStatusManuallyDisabled, "default", "model-a")
	withMonitoringChannels(t, []*repo.Channel{cooling, healthy, manual})
	app.MarkChannelCooldown(cooling.Id, 0, time.Now().Add(time.Hour).Unix())

	RefreshChannelMetrics(time.Now())

	if got := testutil.ToFloat64(metrics.ChannelState.WithLabelValues("9101")); got != 2 {
		t.Errorf("cooling single-key channel state = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metrics.ChannelState.WithLabelValues("9102")); got != 0 {
		t.Errorf("healthy channel state = %v, want 0", got)
	}
	// a hand-disabled channel is not a fault: no series is created for it
	if n := testutil.CollectAndCount(metrics.ChannelState); n != 2 {
		t.Errorf("state series = %d, want 2 (manual-disabled channel must carry none)", n)
	}

	// the cooling channel recovers and the manual one is deleted: the stale series must go
	app.ClearChannelCooldowns()
	withMonitoringChannels(t, []*repo.Channel{healthy})
	RefreshChannelMetrics(time.Now())
	if n := testutil.CollectAndCount(metrics.ChannelState); n != 1 {
		t.Errorf("state series after channel removal = %d, want 1", n)
	}
}

func TestRefreshChannelMetrics_ModelsUnroutableAndExpiry(t *testing.T) {
	down := monCh(9111, common.ChannelStatusAutoDisabled, "default", "model-x")
	up := monCh(9112, common.ChannelStatusEnabled, "default", "model-y")
	exp := time.Now().Add(48 * time.Hour).Unix()
	setting := "{\"expires_at\":" + strconv.FormatInt(exp, 10) + "}"
	up.Setting = &setting
	withMonitoringChannels(t, []*repo.Channel{down, up})

	RefreshChannelMetrics(time.Now())

	if got := testutil.ToFloat64(metrics.ModelsUnroutable); got != 1 {
		t.Errorf("models_unroutable = %v, want 1 (model-x)", got)
	}
	left := testutil.ToFloat64(metrics.ChannelExpiresInSeconds.WithLabelValues("9112"))
	if left < 47*3600 || left > 48*3600 {
		t.Errorf("expires_in_seconds = %v, want about 48h", left)
	}
}

func TestClassifyModel(t *testing.T) {
	cases := []struct {
		s    modelSummary
		want string
	}{
		{modelSummary{Total: 1, Routable: 1}, ModelStatusOperational},
		{modelSummary{Total: 4, Routable: 4}, ModelStatusOperational},
		{modelSummary{Total: 4, Routable: 3}, ModelStatusOperational},
		{modelSummary{Total: 4, Routable: 2}, ModelStatusDegraded},
		{modelSummary{Total: 4, Routable: 1}, ModelStatusDegraded},
		{modelSummary{Total: 2, Routable: 0}, ModelStatusDown},
	}
	for _, c := range cases {
		if got := classifyModel(c.s); got != c.want {
			t.Errorf("classifyModel(%+v) = %s, want %s", c.s, got, c.want)
		}
	}
}

func TestSummarizeModels_ScopesToSharedActiveChannels(t *testing.T) {
	chans := []*repo.Channel{
		monCh(9121, common.ChannelStatusEnabled, "default", "model-a,model-b"),
		monCh(9122, common.ChannelStatusAutoDisabled, "", "model-a"),
		monCh(9123, common.ChannelStatusManuallyDisabled, "default", "model-a"),
		monCh(9124, common.ChannelStatusEnabled, "tenant-private", "model-secret"),
	}
	got := summarizeModels(chans, time.Now().Unix())
	if _, leaked := got["model-secret"]; leaked {
		t.Fatal("a tenant-private model must not appear in the public summary")
	}
	if s := got["model-a"]; s.Total != 2 || s.Routable != 1 {
		t.Errorf("model-a = %+v, want Total 2 (enabled + auto-disabled; manual excluded) Routable 1", s)
	}
}

func TestGetPublicModelStatus_KeyWhitelist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withMonitoringChannels(t, []*repo.Channel{
		monCh(9131, common.ChannelStatusEnabled, "default", "model-a"),
		monCh(9132, common.ChannelStatusAutoDisabled, "default", "model-b"),
		monCh(9133, common.ChannelStatusEnabled, "tenant-private", "model-secret"),
	})
	modelStatusCache = publicModelStatusCache{} // drop any cached payload
	prevNow := publicModelStatusNow
	publicModelStatusNow = func() time.Time { return time.Unix(1_800_000_000, 0) }
	t.Cleanup(func() { publicModelStatusNow = prevNow; modelStatusCache = publicModelStatusCache{} })

	r := gin.New()
	r.GET("/api/v2/public/model-status", GetPublicModelStatus)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/public/model-status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=30") {
		t.Errorf("Cache-Control = %q, want max-age=30", cc)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "top level", body, "success", "data")
	var data map[string]json.RawMessage
	_ = json.Unmarshal(body["data"], &data)
	assertKeys(t, "data", data, "models", "updated_at")
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(data["models"], &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2 (private tenant model excluded): %s", len(models), data["models"])
	}
	want := map[string]string{"model-a": ModelStatusOperational, "model-b": ModelStatusDown}
	for _, m := range models {
		assertKeys(t, "model entry", m, "model", "status")
		var name, st string
		_ = json.Unmarshal(m["model"], &name)
		_ = json.Unmarshal(m["status"], &st)
		if want[name] != st {
			t.Errorf("model %s status = %s, want %s", name, st, want[name])
		}
	}
	for _, leak := range []string{"channel", "9131", "sk-test", "tenant", "private", "secret"} {
		if strings.Contains(strings.ToLower(w.Body.String()), leak) {
			t.Errorf("response leaks %q: %s", leak, w.Body.String())
		}
	}
}

func TestGetPublicModelStatus_CachesFor30Seconds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	prev := listChannelsForMonitoring
	listChannelsForMonitoring = func() ([]*repo.Channel, error) {
		calls++
		return []*repo.Channel{monCh(9151, common.ChannelStatusEnabled, "default", "model-a")}, nil
	}
	modelStatusCache = publicModelStatusCache{}
	now := time.Unix(1_800_000_000, 0)
	prevNow := publicModelStatusNow
	publicModelStatusNow = func() time.Time { return now }
	t.Cleanup(func() {
		listChannelsForMonitoring, publicModelStatusNow = prev, prevNow
		modelStatusCache = publicModelStatusCache{}
	})

	r := gin.New()
	r.GET("/s", GetPublicModelStatus)
	hit := func() { r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/s", nil)) }
	hit()
	now = now.Add(10 * time.Second)
	hit()
	if calls != 1 {
		t.Errorf("channel list read %d times within 30s, want 1", calls)
	}
	now = now.Add(25 * time.Second)
	hit()
	if calls != 2 {
		t.Errorf("channel list read %d times after the TTL, want 2", calls)
	}
}

func assertKeys(t *testing.T, where string, got map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s keys = %v, want exactly %v", where, keysOf(got), want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("%s missing key %q", where, k)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestRecordChannelAttempt_ClassifiesOutcomes(t *testing.T) {
	const id = 9141
	lbl := strconv.Itoa(id)
	req := func(class string) float64 {
		return testutil.ToFloat64(metrics.ChannelRequestsTotal.WithLabelValues(lbl, class))
	}
	errs := func(reason string) float64 {
		return testutil.ToFloat64(metrics.ChannelErrorsTotal.WithLabelValues(lbl, reason))
	}

	recordChannelAttempt(id, nil)
	if req("2xx") != 1 {
		t.Errorf("success must count one 2xx attempt, got %v", req("2xx"))
	}

	recordChannelAttempt(id, types.NewErrorWithStatusCode(errTest("limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests))
	if req("429") != 1 || errs("rate_limited") != 1 {
		t.Errorf("429: requests=%v errors=%v, want 1/1", req("429"), errs("rate_limited"))
	}

	recordChannelAttempt(id, types.NewErrorWithStatusCode(errTest("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway))
	if req("5xx") != 1 || errs("upstream_5xx") != 1 {
		t.Errorf("5xx: requests=%v errors=%v, want 1/1", req("5xx"), errs("upstream_5xx"))
	}

	recordChannelAttempt(id, types.NewError(errTest("dial"), types.ErrorCodeDoRequestFailed))
	if req("none") != 1 || errs("network") != 1 {
		t.Errorf("transport failure: requests=%v errors=%v, want 1/1", req("none"), errs("network"))
	}

	// not an upstream exchange (caller quota): nothing recorded against the channel
	before := testutil.CollectAndCount(metrics.ChannelRequestsTotal)
	recordChannelAttempt(id, types.NewErrorWithStatusCode(errTest("quota"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden))
	if got := testutil.CollectAndCount(metrics.ChannelRequestsTotal); got != before {
		t.Errorf("caller-quota failure created series (%d -> %d)", before, got)
	}
}

func TestContentMetricsSink_FeedsSeries(t *testing.T) {
	hits := func() float64 {
		return testutil.ToFloat64(metrics.ContentRuleHitsTotal.WithLabelValues("builtin", "mask", "enforce"))
	}
	h0, r0 := hits(), testutil.ToFloat64(metrics.ContentRejectedTotal)
	s := contentMetricsSink{}
	s.RuleHit(5, "mask", "enforce", "phone_cn", 2)
	s.RuleReject(5)
	if hits() != h0+2 || testutil.ToFloat64(metrics.ContentRejectedTotal) != r0+1 {
		t.Errorf("sink did not move the series (hits %v->%v)", h0, hits())
	}
}
