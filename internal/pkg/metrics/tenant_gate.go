package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// TenantGateTotal counts the decisions repo.TenantGate made that were NOT a
// plain pass: a request whose owning tenant row is disabled/suspended, or whose
// tenant id resolves to no row at all (never existed, or soft-deleted by
// repo.DeleteTenant — tenants use gorm.DeletedAt, so a deleted tenant's rows
// keep working while the row itself is invisible to every lookup).
//
// Outcomes:
//
//	disabled_denied   — the row exists and Tenant.IsDisabled(); denied in
//	                    every mode. This is the pre-cycle-12 behaviour of the
//	                    three middleware/auth.go call sites, now counted.
//	missing_observed  — no row, and TENANT_MISSING_MODE is not "enforce"
//	                    (the default): the request was ALLOWED and only
//	                    recorded. A non-zero rate here means traffic is
//	                    flowing for tenants that no longer exist — which is
//	                    exactly what an operator must see before flipping the
//	                    mode to enforce.
//	missing_denied    — no row, and TENANT_MISSING_MODE=enforce: denied.
//
// A plain pass is deliberately not counted: it is the common case on the
// request path, and the per-request HTTP counters already carry that volume. There is no tenant_id
// label either — the id would come from a token or user row and the cardinality
// would follow the tenant table, while the question this series answers
// ("is anything being denied, and would enforce deny it") does not need it.
// The paired log line in repo.TenantGate carries the tenant id.
var TenantGateTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "tenant_gate_total",
		Help:      "Tenant-gate decisions that denied a request or would deny it under enforce, by outcome",
	},
	[]string{"outcome"},
)

// RecordTenantGate increments the tenant-gate counter for one of the outcomes
// documented on TenantGateTotal. The production writer is repo.TenantGate
// (internal/adapter/repo/tenant.go), reached from the three
// middleware/auth.go gates (session console, relay token, playground token).
func RecordTenantGate(outcome string) {
	TenantGateTotal.WithLabelValues(outcome).Inc()
}

// init pre-registers the known label values at zero, matching r4_cost_spike.go
// and r6_rate_limit_degraded.go: a CounterVec child series only appears in
// /metrics until its first Inc(), so an absent series would otherwise be
// ambiguous between "nothing has been denied yet" and "this counter is not
// wired up at all".
func init() {
	TenantGateTotal.WithLabelValues("disabled_denied")
	TenantGateTotal.WithLabelValues("missing_observed")
	TenantGateTotal.WithLabelValues("missing_denied")
}
