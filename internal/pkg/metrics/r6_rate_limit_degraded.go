package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RateLimitDegradedTotal counts every time a rate limiter's Redis backend
// call failed and the limiter fell back to failing OPEN (admitting the
// request) instead of rejecting it. This is the visibility half of operator
// decision D1 (2026-08-27): fail-open on Redis errors is the accepted
// tradeoff for middleware.redisRateLimitHandler (model-rate-limit.go), but
// the tradeoff must not be silent, so this counter increments UNCONDITIONALLY
// on every occurrence — it is never throttled, unlike the paired log line in
// r6aRateLimitDegradedLogf, which is rate-limited to avoid one ERROR line per
// relay request during a sustained outage.
//
// Labeled by which check degraded:
//
//	model_rate_limit_success — redisRateLimitHandler's success-count check
//	                            (checkRedisRateLimit against the MRRLS key)
//	model_rate_limit_total   — redisRateLimitHandler's total-count token
//	                            bucket check (limiter.Allow)
//	model_rate_limit_record  — recordRedisRequest's post-response success
//	                            recording (the LPush to the MRRLS key). Not a
//	                            fail-open (the request already succeeded);
//	                            counts silently LOST recordings, i.e. the
//	                            success dimension under-counting.
//
// Composite risk (see the callers' comments for the full statement): while
// this counter is climbing, BusinessRateLimit/BusinessModelRateLimit and
// RelayConcurrencyLimit are degrading the same way for the same Redis
// outage, and CostSpikeLimit's default observe-only mode does not stop
// spending either — so a sustained Redis outage means no rate or cost
// ceiling of any kind is being enforced on relay traffic.
var RateLimitDegradedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "rate_limit_degraded_total",
		Help:      "Rate limiter Redis-backend failures that fell back to fail-open, by which check degraded",
	},
	[]string{"check"},
)

// RecordRateLimitDegraded increments the degradation counter for the given
// check ("model_rate_limit_success", "model_rate_limit_total" or
// "model_rate_limit_record").
func RecordRateLimitDegraded(check string) {
	RateLimitDegradedTotal.WithLabelValues(check).Inc()
}

// init pre-registers the known label values with a zero count, matching the
// pattern in r4_cost_spike.go: a CounterVec child series only exists in
// /metrics once its first Inc() fires, so an absent series would otherwise be
// ambiguous between "no degradation has happened yet" and "this counter
// isn't wired into redisRateLimitHandler at all".
func init() {
	RateLimitDegradedTotal.WithLabelValues("model_rate_limit_success")
	RateLimitDegradedTotal.WithLabelValues("model_rate_limit_total")
	RateLimitDegradedTotal.WithLabelValues("model_rate_limit_record")
}
