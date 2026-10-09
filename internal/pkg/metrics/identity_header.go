package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Counters for the enterprise attribution headers (X-Lurus-Employee /
// X-Lurus-Dept, migration 045). The headers are client-controlled and the
// host nginx forwards them verbatim, so every way a request can carry one
// that we then do NOT honour is counted: a rising untrusted count means a
// customer's gateway forgot to use its trusted key, a rising unknown-dept
// count means its department codes drifted from projects.external_code.
var (
	// IdentityHeaderUntrustedTotal counts requests that carried an identity
	// header on a key without trusted_identity_headers. One increment per
	// request, however many headers it carried.
	IdentityHeaderUntrustedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "identity_header",
		Name:      "untrusted_total",
		Help:      "Requests whose X-Lurus-Employee/X-Lurus-Dept headers were ignored because the key is not a trusted gateway key",
	})

	// IdentityHeaderUnknownDeptTotal counts trusted requests whose
	// X-Lurus-Dept matched no live project of the tenant (neither by
	// external_code nor by name). The request keeps the token's own project;
	// no project is ever auto-created.
	IdentityHeaderUnknownDeptTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "identity_header",
		Name:      "unknown_dept_total",
		Help:      "Trusted requests whose X-Lurus-Dept matched no project of the tenant",
	})

	// IdentityHeaderInvalidTotal counts trusted requests whose identity header
	// failed validation (charset / length) and was dropped.
	IdentityHeaderInvalidTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "identity_header",
		Name:      "invalid_total",
		Help:      "Trusted requests whose identity header failed validation and was dropped, by header",
	}, []string{"header"})
)

// RecordIdentityHeaderUntrusted counts one request that sent identity
// headers on an untrusted key.
func RecordIdentityHeaderUntrusted() { IdentityHeaderUntrustedTotal.Inc() }

// RecordIdentityHeaderUnknownDept counts one trusted request whose
// department header matched no project.
func RecordIdentityHeaderUnknownDept() { IdentityHeaderUnknownDeptTotal.Inc() }

// RecordIdentityHeaderInvalid counts one dropped header ("employee"/"dept").
func RecordIdentityHeaderInvalid(header string) {
	IdentityHeaderInvalidTotal.WithLabelValues(header).Inc()
}

// Pre-register the known label values so the series is visible at zero.
func init() {
	IdentityHeaderInvalidTotal.WithLabelValues("employee")
	IdentityHeaderInvalidTotal.WithLabelValues("dept")
}
