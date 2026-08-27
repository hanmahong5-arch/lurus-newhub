package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CostSpikeBreachTotal counts every time the per-user 5-minute cost-spike
// window (middleware.CostSpikeLimit) crosses common.CostSpikeHardLimitPer5Min,
// labeled by what actually happened:
//
//	observed — common.CostSpikeEnforce is false: the breach was logged and
//	           counted, but the request was admitted and the account was
//	           left enabled (D-A6 observe-by-default).
//	enforced — common.CostSpikeEnforce is true: the account was disabled
//	           via repo.DisableUserById and the request was rejected 429.
//
// Observe mode is NOT silent otherwise: middleware.CostSpikeLimit also emits
// a structured "cost_spike_triggered" SysLogf line on every breach (see
// cost_spike.go), throttled to at most one per user per minute. This counter
// and that log line are the only two signals observe mode produces — this
// counter alone is not "the only place the breach becomes visible".
var CostSpikeBreachTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "cost_spike_breach_total",
		Help:      "Cost-spike 5-minute window breaches by action (observed/enforced)",
	},
	[]string{"action"},
)

// init pre-registers both "action" label values with a zero count. Without
// this, a CounterVec child series only exists in /metrics once its first
// Inc() fires, so "observed"/"enforced" being absent from a scrape is
// ambiguous between "no breach has happened yet" and "CostSpikeLimit isn't
// wired into the router at all". Emitting the zero up front removes that
// ambiguity for anyone reading the raw metric (e.g. a PromQL alert querying
// this series directly, before any rate()).
func init() {
	CostSpikeBreachTotal.WithLabelValues("observed")
	CostSpikeBreachTotal.WithLabelValues("enforced")
}
