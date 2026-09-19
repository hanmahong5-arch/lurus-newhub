package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CSRFRejectedTotal counts requests middleware.BrowserOriginGuard refused
// because a cookie-authenticated, state-changing request did not come from
// this site (cycle-12 L4). A climbing counter is either a real cross-site
// attempt or a legitimate integration that authenticates with cookies from
// another origin — the second is a configuration bug worth seeing, which is
// why the counter is labelled by which check refused rather than being a
// single number:
//
//	sec_fetch_site — the browser's own Sec-Fetch-Site header said the
//	                 request was initiated by a different site
//	                 (same-site or cross-site). Authoritative: the browser,
//	                 not the caller, writes this header.
//	origin         — no Sec-Fetch-Site header, and the Origin header named
//	                 a site absent from ALLOWED_ORIGINS
//	                 (config.Get().CORS.AllowedOrigins).
//
// Requests with neither header are admitted and counted by neither label:
// see BrowserOriginGuard for why (pre-Fetch-Metadata clients).
var CSRFRejectedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "csrf_rejected_total",
		Help:      "Cookie-authenticated state-changing requests refused by the browser origin guard, by which check refused",
	},
	[]string{"reason"},
)

// RecordCSRFRejected increments the guard's rejection counter for the given
// reason ("sec_fetch_site" or "origin").
func RecordCSRFRejected(reason string) {
	CSRFRejectedTotal.WithLabelValues(reason).Inc()
}

// init pre-registers both label values at zero, matching r4_cost_spike.go
// and r6_rate_limit_degraded.go: a CounterVec child series only appears in
// /metrics after its first Inc(), so an absent series would otherwise be
// ambiguous between "nothing has been refused yet" and "the guard is not
// mounted at all".
func init() {
	CSRFRejectedTotal.WithLabelValues("sec_fetch_site")
	CSRFRejectedTotal.WithLabelValues("origin")
}
