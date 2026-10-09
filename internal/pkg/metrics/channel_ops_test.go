package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// collectLabelSets returns, per series currently existing in the vec (Collect
// does not create children, unlike WithLabelValues), its label map.
func collectLabelSets(c prometheus.Collector) []map[string]string {
	ch := make(chan prometheus.Metric, 256)
	c.Collect(ch)
	close(ch)
	var out []map[string]string
	for m := range ch {
		pb := &dto.Metric{}
		_ = m.Write(pb)
		set := map[string]string{}
		for _, lp := range pb.GetLabel() {
			set[lp.GetName()] = lp.GetValue()
		}
		out = append(out, set)
	}
	return out
}

func TestChannelOps_ZeroInitLabels(t *testing.T) {
	has := func(c prometheus.Collector, k, v string) bool {
		for _, set := range collectLabelSets(c) {
			if set[k] == v {
				return true
			}
		}
		return false
	}
	for _, sc := range []string{"2xx", "3xx", "4xx", "429", "5xx", "none"} {
		if !has(ChannelRequestsTotal, "status_class", sc) {
			t.Errorf("status_class=%s not pre-registered", sc)
		}
	}
	for _, r := range []string{"rate_limited", "auth", "quota", "upstream_5xx", "client_4xx", "timeout", "network", "other"} {
		if !has(ChannelErrorsTotal, "reason", r) {
			t.Errorf("reason=%s not pre-registered", r)
		}
	}
	for _, c := range []string{"rate_limit", "plan_window", "balance_low"} {
		if !has(ChannelCooldownTotal, "cause", c) {
			t.Errorf("cause=%s not pre-registered", c)
		}
	}
	if n := len(collectLabelSets(ContentRuleHitsTotal)); n < 8 {
		t.Errorf("content_rule_hits_total pre-registered %d series, want the 2x2x2 grid", n)
	}
	if n := len(collectLabelSets(KeyAffinityTotal)); n < 3 {
		t.Errorf("key_affinity_total pre-registered %d series, want 3", n)
	}
	if n := len(collectLabelSets(ContentRejectedTotal)); n != 1 {
		t.Errorf("content_rejected_total series = %d, want 1 (zero-valued)", n)
	}
}

func TestRecordChannelAttempt_MovesCounters(t *testing.T) {
	const id = 7001
	lbl := "7001"
	RecordChannelAttempt(id, 200, false, false, false)
	RecordChannelAttempt(id, 429, true, false, false)
	RecordChannelAttempt(id, 401, true, false, false)
	RecordChannelAttempt(id, 504, true, true, false)
	RecordChannelAttempt(id, 0, true, false, true)
	RecordChannelAttempt(id, 400, true, false, false)

	check := func(name string, got, want float64) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("2xx", testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues(lbl, "2xx")), 1)
	check("429", testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues(lbl, "429")), 1)
	check("5xx", testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues(lbl, "5xx")), 1)
	check("none", testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues(lbl, "none")), 1)
	check("err rate_limited", testutil.ToFloat64(ChannelErrorsTotal.WithLabelValues(lbl, "rate_limited")), 1)
	check("err auth", testutil.ToFloat64(ChannelErrorsTotal.WithLabelValues(lbl, "auth")), 1)
	check("err timeout", testutil.ToFloat64(ChannelErrorsTotal.WithLabelValues(lbl, "timeout")), 1)
	check("err network", testutil.ToFloat64(ChannelErrorsTotal.WithLabelValues(lbl, "network")), 1)
	check("err client_4xx", testutil.ToFloat64(ChannelErrorsTotal.WithLabelValues(lbl, "client_4xx")), 1)
}

func TestMetricsDisabledLabels_CollapsesChannelID(t *testing.T) {
	t.Cleanup(func() { SetDisabledLabels("") })
	if got := TrimLabel("channel_id", "42"); got != "42" {
		t.Fatalf("default must not trim, got %q", got)
	}
	SetDisabledLabels(" Channel_ID , other ")
	if got := TrimLabel("channel_id", "42"); got != "_" {
		t.Errorf("disabled label value = %q, want _", got)
	}
	if got := TrimLabel("status_class", "5xx"); got != "5xx" {
		t.Errorf("a label that is not disabled was trimmed: %q", got)
	}

	before := testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues("_", "2xx"))
	RecordChannelAttempt(7101, 200, false, false, false)
	RecordChannelAttempt(7102, 200, false, false, false)
	if got := testutil.ToFloat64(ChannelRequestsTotal.WithLabelValues("_", "2xx")); got != before+2 {
		t.Errorf("collapsed series = %v, want %v", got, before+2)
	}
	for _, set := range collectLabelSets(ChannelRequestsTotal) {
		if set["channel_id"] == "7101" || set["channel_id"] == "7102" {
			t.Errorf("per-channel series %v written while channel_id is disabled", set)
		}
	}
}

func TestChannelErrorRatio_MinimumVolumeGate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	// 10 attempts, all failures: below the 20-attempt floor, so the ratio stays 0.
	for i := 0; i < 10; i++ {
		channelWindows.add(7201, true, now)
	}
	// 20 attempts, 15 failures: ratio 0.75.
	for i := 0; i < 20; i++ {
		channelWindows.add(7202, i < 15, now)
	}
	RefreshChannelErrorRatios(now)
	if got := testutil.ToFloat64(ChannelErrorRatio5m.WithLabelValues("7201")); got != 0 {
		t.Errorf("low-volume channel ratio = %v, want 0", got)
	}
	if got := testutil.ToFloat64(ChannelErrorRatio5m.WithLabelValues("7202")); got != 0.75 {
		t.Errorf("ratio = %v, want 0.75", got)
	}

	// 6 minutes later the window has slid past every bucket.
	RefreshChannelErrorRatios(now.Add(6 * time.Minute))
	if got := testutil.ToFloat64(ChannelErrorRatio5m.WithLabelValues("7202")); got != 0 {
		t.Errorf("ratio after the window slid = %v, want 0", got)
	}
}

func TestContentCounters_CountHitsAndRejections(t *testing.T) {
	hits := func(scope, kind, mode string) float64 {
		return testutil.ToFloat64(ContentRuleHitsTotal.WithLabelValues(scope, kind, mode))
	}
	b0, c0, r0 := hits("builtin", "mask", "enforce"), hits("custom", "reject", "observe"), testutil.ToFloat64(ContentRejectedTotal)
	RecordContentRuleHit("mask", "enforce", "phone_cn", 3)
	RecordContentRuleHit("reject", "observe", "", 1)
	RecordContentRejected()
	if got := hits("builtin", "mask", "enforce"); got != b0+3 {
		t.Errorf("builtin mask enforce = %v, want %v", got, b0+3)
	}
	if got := hits("custom", "reject", "observe"); got != c0+1 {
		t.Errorf("custom reject observe = %v, want %v", got, c0+1)
	}
	if got := testutil.ToFloat64(ContentRejectedTotal); got != r0+1 {
		t.Errorf("content_rejected_total = %v, want %v", got, r0+1)
	}
}

func TestRecordChannelCooldownAndKeyAffinity(t *testing.T) {
	c0 := testutil.ToFloat64(ChannelCooldownTotal.WithLabelValues("7301", "plan_window"))
	RecordChannelCooldown(7301, "plan_window")
	if got := testutil.ToFloat64(ChannelCooldownTotal.WithLabelValues("7301", "plan_window")); got != c0+1 {
		t.Errorf("cooldown_total = %v, want %v", got, c0+1)
	}
	k0 := testutil.ToFloat64(KeyAffinityTotal.WithLabelValues("hit"))
	RecordKeyAffinity("hit")
	if got := testutil.ToFloat64(KeyAffinityTotal.WithLabelValues("hit")); got != k0+1 {
		t.Errorf("key_affinity_total hit = %v, want %v", got, k0+1)
	}
}

func hasRatioSeries(t *testing.T, label string) bool {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(ChannelErrorRatio5m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "channel_id" && lp.GetValue() == label {
					return true
				}
			}
		}
	}
	return false
}

func TestRefreshChannelErrorRatios_DropsIdleChannelSeries(t *testing.T) {
	const id = 9171
	now := time.Now()
	channelWindows.add(id, true, now)
	RefreshChannelErrorRatios(now)
	l := chanLabel(id)
	if !hasRatioSeries(t, l) {
		t.Fatal("active channel must export a ratio series")
	}
	// Six minutes later the window is empty: window entry and series vanish.
	RefreshChannelErrorRatios(now.Add(6 * time.Minute))
	if hasRatioSeries(t, l) {
		t.Errorf("idle channel %q still exports a stale series", l)
	}
	channelWindows.mu.Lock()
	_, still := channelWindows.m[id]
	channelWindows.mu.Unlock()
	if still {
		t.Errorf("idle window for %q was not forgotten", l)
	}
}
