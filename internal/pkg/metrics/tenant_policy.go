package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// TenantModelDeniedTotal counts relay requests that reach
// middleware.Distribute and would be denied by the per-tenant model
// allow-list (internal/app/tenantpolicy, TENANT_MODEL_ALLOWLIST_MODE);
// requests without a resolvable tenant context or without a model are
// skipped and not counted. Labeled by tenant and action: "observed"
// (default mode — the request still proceeded) or "enforced" (the request
// was aborted with a 403 model_blocked). The default mode is observe, so a
// fresh deployment's series is all "observed" until an operator turns
// enforcement on for that environment — mirrors CreditPoolResetTotal's
// tenant/action shape above.
var TenantModelDeniedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "tenant_model_denied_total",
		Help:      "Relay requests the tenant model allow-list would deny, by tenant and action (observed/enforced)",
	},
	[]string{"tenant_id", "action"},
)

// RecordTenantModelDenied increments the denial counter for the given tenant
// and action ("observed" or "enforced"). The production writer is
// middleware.Distribute's allow-list check (distributor.go).
func RecordTenantModelDenied(tenantID, action string) {
	TenantModelDeniedTotal.WithLabelValues(tenantID, action).Inc()
}
