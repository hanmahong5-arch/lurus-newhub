package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// channel_probe.go — L3 (cycle 11): every automatic channel probe used to
// write a real consume-log row as user 1 (handler.testChannel, before it
// split into probeChannel), polluting the leaderboard and quota_data on
// every tick regardless of whether a human ever ran or looked at it. These
// three series replace that log-row-as-signal for the automatic pass; the
// manual GET /api/channel/test/:id path still writes its log row AND
// reports through ChannelProbeTotal/ChannelProbeDuration.
var (
	// ChannelProbeTotal counts every probe that goes through
	// handler.probeChannel — manual (GET /api/channel/test[/:id], both the
	// single-channel and "test all" forms) and automatic (the leader-gated
	// pass) — by outcome. "latency_breach" is distinct from "timeout": a
	// breach is a technically successful response that exceeded the
	// automatic pass's disable threshold (common.ChannelDisableThreshold); a
	// timeout is channelProbeTimeout() (CHANNEL_TEST_TIMEOUT_SECONDS) firing
	// before any response arrived. Only the automatic pass ever reports
	// "latency_breach" — the manual path has no threshold to compare
	// against (handler.classifyProbeResult never returns it).
	//
	// NOT covered: POST /api/v2/:tenant_slug/channels/:id/test
	// (handler.TestChannelV2, internal/adapter/handler/v2_channel_actions.go)
	// is a second, pre-existing, separately implemented probe mechanism with
	// its own HTTP client (channelTestHTTPClient) — it reports into none of
	// this file's series and got no deadline from this cycle's work. Known
	// gap, not a regression this cycle introduced; a follow-up item, not
	// this cycle's scope.
	ChannelProbeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_probe_total",
			Help:      "Channel test probes by outcome (ok/error/timeout/latency_breach)",
		},
		[]string{"outcome"},
	)

	// ChannelProbeDuration measures how long one probe took, wall clock,
	// regardless of outcome — manual and automatic both report into it.
	ChannelProbeDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_probe_duration_seconds",
			Help:      "Channel test probe latency in seconds",
			Buckets:   []float64{.1, .5, 1, 2.5, 5, 10, 30, 60, 120},
		},
	)

	// ChannelAutoStatusTotal counts every status change the automatic
	// probe pass makes, or deliberately withholds, labeled by action.
	// latency_ban_skipped_sole_channel is the hysteresis guard: a channel
	// that would otherwise be banned on its third consecutive latency
	// breach, but is the only enabled channel serving at least one
	// (group, model) pair it owns, is left enabled instead — see
	// handler.evaluateProbeOutcome and repo.SoleEnabledModelsForChannel.
	ChannelAutoStatusTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_auto_status_total",
			Help:      "Automatic channel status changes by action",
		},
		[]string{"action"},
	)
)

// init pre-registers every label value with a zero count — without this a
// CounterVec child series only exists in /metrics once its first Inc()
// fires, which would make "latency_ban_skipped_sole_channel absent from a
// scrape" ambiguous between "never happened yet" and "this build doesn't
// wire the guard at all" (same reasoning as CostSpikeBreachTotal's init,
// r4_cost_spike.go).
func init() {
	for _, outcome := range []string{"ok", "error", "timeout", "latency_breach"} {
		ChannelProbeTotal.WithLabelValues(outcome)
	}
	for _, action := range []string{"disable_error", "disable_latency", "enable", "latency_ban_skipped_sole_channel"} {
		ChannelAutoStatusTotal.WithLabelValues(action)
	}
}

// RecordChannelProbe writes ChannelProbeTotal and ChannelProbeDuration. Its
// callers today are the manual GET /api/channel/test/:id path
// (handler.TestChannel) and the automatic pass (handler.autoProbeChannel),
// each once per probe; the v2 per-tenant channel test (handler.TestChannelV2)
// has its own client and does not report here.
func RecordChannelProbe(outcome string, elapsed time.Duration) {
	ChannelProbeTotal.WithLabelValues(outcome).Inc()
	ChannelProbeDuration.Observe(elapsed.Seconds())
}

// RecordChannelAutoStatus is the single writer for ChannelAutoStatusTotal —
// only the automatic pass (handler.autoProbeChannel) calls it.
func RecordChannelAutoStatus(action string) {
	ChannelAutoStatusTotal.WithLabelValues(action).Inc()
}
