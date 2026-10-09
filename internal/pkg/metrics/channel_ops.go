package metrics

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// channel_ops.go - account-pool operations series: per-channel traffic, state,
// cooldowns, plan windows and expiry, plus the content-rule guard counters.
//
// Cardinality contract: channel_id is the finest label here. Per-key detail
// never becomes a label (it is served by the internal health endpoint). When a
// deployment has so many channels that channel_id hurts, METRICS_DISABLED_LABELS
// (comma separated, e.g. "channel_id") collapses the label to "_" at write time.
//
// Names are declared as lurus_channel_* / lurus_content_* (Namespace "lurus",
// own Subsystem) on purpose: they are the operations-facing series the Netdata
// alarms in deploy/r6-host-netdata/health.d/newhub.conf bind to.

// MetricsDisabledLabelsEnv names the env var read once at start-up.
const MetricsDisabledLabelsEnv = "METRICS_DISABLED_LABELS"

// DisabledLabelValue is what a disabled label is written as.
const DisabledLabelValue = "_"

var disabledLabels atomic.Pointer[map[string]struct{}]

func init() {
	SetDisabledLabels(os.Getenv(MetricsDisabledLabelsEnv))
}

// SetDisabledLabels replaces the disabled-label set from a comma separated
// list. Empty (the default) disables nothing. Production reads the env var
// once at init; this is exposed for tests.
func SetDisabledLabels(csv string) {
	set := map[string]struct{}{}
	for _, p := range strings.Split(csv, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			set[p] = struct{}{}
		}
	}
	disabledLabels.Store(&set)
}

// TrimLabel returns value unchanged unless label is disabled, in which case it
// returns "_" so every series of that label collapses onto one.
func TrimLabel(label, value string) string {
	if m := disabledLabels.Load(); m != nil {
		if _, off := (*m)[label]; off {
			return DisabledLabelValue
		}
	}
	return value
}

func chanLabel(channelID int) string { return TrimLabel("channel_id", strconv.Itoa(channelID)) }

var (
	// ChannelRequestsTotal counts every upstream attempt (one per try of the
	// relay retry loop) by the status class of the answer: 2xx|3xx|4xx|429|5xx|none
	// ("none" = transport failure, no HTTP status).
	ChannelRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "requests_total",
		Help: "Upstream attempts per channel by status class (2xx/3xx/4xx/429/5xx/none)",
	}, []string{"channel_id", "status_class"})

	// ChannelErrorsTotal counts failed upstream attempts by a small reason set.
	ChannelErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "errors_total",
		Help: "Failed upstream attempts per channel by reason",
	}, []string{"channel_id", "reason"})

	// ChannelState is 0 usable, 1 some keys unusable, 2 no key usable. Channels
	// an operator switched off by hand carry no series.
	ChannelState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "state",
		Help: "Channel routability (0=usable, 1=some keys unusable, 2=no key usable)",
	}, []string{"channel_id"})

	// ChannelCooldownTotal counts cooldowns put on a channel or key.
	ChannelCooldownTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "cooldown_total",
		Help: "Cooldowns applied per channel by cause (rate_limit/plan_window/balance_low)",
	}, []string{"channel_id", "cause"})

	// ChannelPlanWindowUsedRatio is the used fraction (0..1) of a plan window.
	ChannelPlanWindowUsedRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "plan_window_used_ratio",
		Help: "Used fraction (0..1) of a plan channel quota window",
	}, []string{"channel_id", "window"})

	// ChannelExpiresInSeconds is time to plan expiry (0 once expired).
	ChannelExpiresInSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "expires_in_seconds",
		Help: "Seconds until a plan channel expires (0 once expired)",
	}, []string{"channel_id"})

	// ChannelBalance is the last upstream balance probed for a channel (USD).
	ChannelBalance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "balance",
		Help: "Last probed upstream balance per channel (USD)",
	}, []string{"channel_id"})

	// ChannelErrorRatio5m is errors/attempts over the last 5 minutes, non-zero
	// only for channels with at least ChannelErrorRatioMinRequests attempts in
	// that window: the minimum-volume gate lives here because Netdata cannot
	// divide two charts.
	ChannelErrorRatio5m = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "error_ratio_5m",
		Help: "Upstream error ratio over 5m, 0 when fewer than 20 attempts",
	}, []string{"channel_id"})

	// ModelsUnroutable is the number of platform-shared models that have
	// channels configured but none routable right now.
	ModelsUnroutable = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "models_unroutable",
		Help: "Models with configured channels but none routable",
	})

	// ContentRuleHitsTotal counts content-rule matches (sum of match counts).
	ContentRuleHitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "content", Name: "rule_hits_total",
		Help: "Content rule matches by scope (builtin/custom), kind (mask/reject) and mode (observe/enforce)",
	}, []string{"rule_scope", "kind", "mode"})

	// ContentRejectedTotal counts requests refused by an enforce-mode reject rule.
	ContentRejectedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "content", Name: "rejected_total",
		Help: "Requests refused by an enforce-mode content reject rule",
	})

	// KeyAffinityTotal counts per-key sticky-session lookups: hit/miss/rebind.
	KeyAffinityTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lurus", Subsystem: "channel", Name: "key_affinity_total",
		Help: "Per-key sticky session lookups by outcome (hit/miss/rebind)",
	}, []string{"outcome"})
)

// ChannelErrorRatioMinRequests is the attempt floor for ChannelErrorRatio5m.
const ChannelErrorRatioMinRequests = 20

// init zero-registers every label value that does not depend on a channel id
// (a never-fired series must read 0, not be absent), and one placeholder
// channel "_" for the channel-labelled counters.
func init() {
	for _, sc := range []string{"2xx", "3xx", "4xx", "429", "5xx", "none"} {
		ChannelRequestsTotal.WithLabelValues(DisabledLabelValue, sc)
	}
	for _, r := range []string{"rate_limited", "auth", "quota", "upstream_5xx", "client_4xx", "timeout", "network", "other"} {
		ChannelErrorsTotal.WithLabelValues(DisabledLabelValue, r)
	}
	for _, c := range []string{"rate_limit", "plan_window", "balance_low"} {
		ChannelCooldownTotal.WithLabelValues(DisabledLabelValue, c)
	}
	for _, s := range []string{"builtin", "custom"} {
		for _, k := range []string{"mask", "reject"} {
			for _, m := range []string{"observe", "enforce"} {
				ContentRuleHitsTotal.WithLabelValues(s, k, m)
			}
		}
	}
	for _, o := range []string{"hit", "miss", "rebind"} {
		KeyAffinityTotal.WithLabelValues(o)
	}
	ContentRejectedTotal.Add(0)
	ModelsUnroutable.Set(0)
}

// StatusClass buckets an HTTP status; 0 (no response) is "none".
func StatusClass(status int) string {
	switch {
	case status == 429:
		return "429"
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	}
	return "none"
}

// ErrorReason maps a failed attempt to the small reason set. timeout/network
// are decided by the caller (it knows the error), the rest by status.
func ErrorReason(status int, timeout, network bool) string {
	switch {
	case timeout:
		return "timeout"
	case network:
		return "network"
	case status == 429:
		return "rate_limited"
	case status == 401 || status == 403:
		return "auth"
	case status == 402:
		return "quota"
	case status >= 500:
		return "upstream_5xx"
	case status >= 400:
		return "client_4xx"
	}
	return "other"
}

// RecordChannelAttempt records one upstream attempt of the relay retry loop.
// Failed attempts additionally count into ChannelErrorsTotal. A plain 4xx
// (not 401/402/403/429) is the caller's fault: it is visible in the
// status-class series but does not count against the channel error ratio.
func RecordChannelAttempt(channelID, status int, failed, timeout, network bool) {
	id := chanLabel(channelID)
	ChannelRequestsTotal.WithLabelValues(id, StatusClass(status)).Inc()
	isErr := false
	if failed {
		reason := ErrorReason(status, timeout, network)
		ChannelErrorsTotal.WithLabelValues(id, reason).Inc()
		isErr = reason != "client_4xx"
	}
	channelWindows.add(channelID, isErr, time.Now())
}

// RecordChannelCooldown counts one cooldown (cause: rate_limit|plan_window|balance_low).
func RecordChannelCooldown(channelID int, cause string) {
	ChannelCooldownTotal.WithLabelValues(chanLabel(channelID), cause).Inc()
}

// RecordKeyAffinity counts one per-key sticky lookup outcome.
func RecordKeyAffinity(outcome string) { KeyAffinityTotal.WithLabelValues(outcome).Inc() }

// SetChannelState writes ChannelState (id trimmed per METRICS_DISABLED_LABELS).
func SetChannelState(channelID int, state float64) {
	ChannelState.WithLabelValues(chanLabel(channelID)).Set(state)
}

// RecordContentRuleHit counts count matches of one rule. builtin is "" for a
// custom regex rule; kind is mask|reject, mode is observe|enforce.
func RecordContentRuleHit(kind, mode, builtin string, count int) {
	scope := "custom"
	if builtin != "" {
		scope = "builtin"
	}
	ContentRuleHitsTotal.WithLabelValues(scope, kind, mode).Add(float64(count))
}

// RecordContentRejected counts one request refused by an enforce-mode reject rule.
func RecordContentRejected() { ContentRejectedTotal.Inc() }

// ---- 5 minute sliding window per channel (for the gated error ratio) ----

const windowMinutes = 5

type minuteBucket struct {
	minute int64 // unix minute this slot currently holds
	total  int
	errs   int
}

type chanWindow [windowMinutes]minuteBucket

type windowSet struct {
	mu sync.Mutex
	m  map[int]*chanWindow
}

var channelWindows = &windowSet{m: map[int]*chanWindow{}}

func (w *windowSet) add(id int, isErr bool, now time.Time) {
	min := now.Unix() / 60
	w.mu.Lock()
	defer w.mu.Unlock()
	cw := w.m[id]
	if cw == nil {
		cw = &chanWindow{}
		w.m[id] = cw
	}
	b := &cw[min%windowMinutes]
	if b.minute != min {
		*b = minuteBucket{minute: min}
	}
	b.total++
	if isErr {
		b.errs++
	}
}

// ChannelWindowStats returns attempts and errors over the last 5 minutes.
func ChannelWindowStats(id int, now time.Time) (total, errs int) {
	min := now.Unix() / 60
	channelWindows.mu.Lock()
	defer channelWindows.mu.Unlock()
	cw := channelWindows.m[id]
	if cw == nil {
		return 0, 0
	}
	for _, b := range cw {
		if b.minute > min-windowMinutes && b.minute <= min {
			total += b.total
			errs += b.errs
		}
	}
	return total, errs
}

// RefreshChannelErrorRatios writes ChannelErrorRatio5m for every channel seen
// in the window: the ratio when attempts >= the floor, else 0.
func RefreshChannelErrorRatios(now time.Time) {
	channelWindows.mu.Lock()
	ids := make([]int, 0, len(channelWindows.m))
	for id := range channelWindows.m {
		ids = append(ids, id)
	}
	channelWindows.mu.Unlock()
	// With channel_id trimmed several channels share one label value; keep the
	// worst ratio rather than whichever channel happened to be written last.
	worst := map[string]float64{}
	var idle []int
	for _, id := range ids {
		total, errs := ChannelWindowStats(id, now)
		if total == 0 {
			idle = append(idle, id)
			continue
		}
		ratio := 0.0
		if total >= ChannelErrorRatioMinRequests {
			ratio = float64(errs) / float64(total)
		}
		l := chanLabel(id)
		if cur, ok := worst[l]; !ok || ratio > cur {
			worst[l] = ratio
		}
	}
	for l, r := range worst {
		ChannelErrorRatio5m.WithLabelValues(l).Set(r)
	}
	// A channel with no attempt left in the window (deleted, or idle for 5
	// minutes) must not export a stale 0 series forever: forget it and drop
	// its series unless another live channel shares the (trimmed) label.
	for _, id := range idle {
		if !channelWindows.forgetIfIdle(id, now) {
			continue
		}
		l := chanLabel(id)
		if _, shared := worst[l]; !shared {
			ChannelErrorRatio5m.DeleteLabelValues(l)
		}
	}
}

// forgetIfIdle drops id's window when it holds no attempt inside the last 5
// minutes, re-checked under the lock so a concurrent add() is never lost.
func (w *windowSet) forgetIfIdle(id int, now time.Time) bool {
	min := now.Unix() / 60
	w.mu.Lock()
	defer w.mu.Unlock()
	cw := w.m[id]
	if cw == nil {
		return false
	}
	for _, b := range cw {
		if b.minute > min-windowMinutes && b.minute <= min && b.total > 0 {
			return false
		}
	}
	delete(w.m, id)
	return true
}
