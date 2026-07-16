package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RateLimitedTotal counts relay requests rejected by the business rate
// limiter (middleware.BusinessRateLimit / BusinessModelRateLimit). Labels:
//
//	scope — which bucket rejected the request: "token" (per-token window),
//	        "tenant" (aggregate window across the tenant's tokens) or
//	        "model" (the tenant's per-model window, migration 026)
//	type  — which limit tripped: "rpm" (requests/min) or "tpm" (settled
//	        LLM tokens/min, window fed by PostConsumeQuota).
//
// Deliberately named without the lurus_gateway prefix so dashboards can
// address it as newhub_rate_limited_total alongside the newhub alert rules.
var RateLimitedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "newhub",
		Name:      "rate_limited_total",
		Help:      "Relay requests rejected by the business rate limiter, by scope (token|tenant|model) and type (rpm|tpm)",
	},
	[]string{"scope", "type"},
)

// RecordRateLimited increments the business rate-limit rejection counter.
// scope is "token", "tenant" or "model"; limitType is "rpm" or "tpm".
func RecordRateLimited(scope, limitType string) {
	RateLimitedTotal.WithLabelValues(scope, limitType).Inc()
}
