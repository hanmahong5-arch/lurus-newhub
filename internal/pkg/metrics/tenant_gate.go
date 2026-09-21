package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Outcome label values for TenantGateTotal. Every writer uses these constants
// rather than a literal, so a typo cannot silently mint a new series that no
// dashboard or alert is watching.
const (
	// TenantGateOutcomeDisabledDenied — the tenant row exists and
	// Tenant.IsDisabled(); denied in every mode. Before cycle 12 this was
	// the uncounted behaviour of the middleware/auth.go call sites; since
	// cycle 13 (L9) the switch raw-token, heartbeat, credit-pool cookie and
	// provision paths reach the same counter (see TenantGateTotal below).
	TenantGateOutcomeDisabledDenied = "disabled_denied"
	// TenantGateOutcomeMissingObserved — no tenant row, and
	// TENANT_MISSING_MODE is not "enforce" (the default): the request was
	// ALLOWED and only recorded. A non-zero rate here means traffic is
	// flowing for tenants that no longer exist — exactly what an operator
	// must see before flipping the mode to enforce.
	TenantGateOutcomeMissingObserved = "missing_observed"
	// TenantGateOutcomeMissingDenied — no tenant row, and
	// TENANT_MISSING_MODE=enforce: denied.
	TenantGateOutcomeMissingDenied = "missing_denied"

	// TenantGateOutcomeOrgClaimAbsent — an OIDC callback whose verified ID
	// token carries no organization claim at all. This is the series that
	// answers owner item O-org ("does the IdP emit org_id?") from one day of
	// production logins instead of from an IdP configuration review: if this
	// is the only org_* series moving, the organization guard can never fire.
	TenantGateOutcomeOrgClaimAbsent = "org_claim_absent"
	// TenantGateOutcomeOrgTenantUnbound — the token named an organization but
	// the tenant it is logging into still carries a *_PLACEHOLDER stand-in in
	// tenants.zitadel_org_id, so there is nothing to compare against. Both
	// tenants a live deployment has today ("default" and "switch") are seeded
	// that way (migrations/021:172, migrations/030:71), so until O-org
	// replaces those values this series — not org_mismatch_denied — is where
	// cross-organization logins land.
	TenantGateOutcomeOrgTenantUnbound = "org_tenant_unbound"
	// TenantGateOutcomeOrgMatched — both sides named a real organization and
	// they agreed; the login proceeded.
	TenantGateOutcomeOrgMatched = "org_matched"
	// TenantGateOutcomeOrgMismatchDenied — both sides named a real
	// organization and they disagreed: 403 TENANT_ORG_MISMATCH.
	TenantGateOutcomeOrgMismatchDenied = "org_mismatch_denied"

	// TenantGateOutcomeSeatLimitDenied — a first-ever login was refused
	// because the tenant is at tenants.max_users: 403 TENANT_SEAT_LIMIT.
	TenantGateOutcomeSeatLimitDenied = "seat_limit_denied"
)

// TenantGateTotal counts the tenant-lifecycle and tenant-binding decisions the
// gateway makes on the authentication paths, by outcome:
//
//   - repo.TenantGate (internal/adapter/repo/tenant.go), reached from the
//     middleware/auth.go gates (session console, relay token, playground
//     token), the cookie arm of middleware/oidc_auth.go, and the raw-token /
//     provision handlers listed in that function's doc comment
//     — for a request whose owning tenant row is disabled/suspended, or whose
//     tenant id resolves to no row at all (never existed, or soft-deleted by
//     repo.DeleteTenant; tenants use gorm.DeletedAt, so a deleted tenant's
//     rows keep working while the row itself is invisible to every lookup):
//     disabled_denied / missing_observed / missing_denied.
//   - handler.OIDCCallback's organization binding, once per browser login:
//     org_claim_absent / org_tenant_unbound / org_matched /
//     org_mismatch_denied.
//   - handler.autoCreateBridgedUser's seat cap, on a first-ever login:
//     seat_limit_denied.
//
// A plain tenant-gate pass is deliberately not counted: it is the common case
// on the request path and the per-request HTTP counters already carry that
// volume. The organization outcomes ARE counted on both sides, because the
// operator question they answer ("does the IdP emit an organization at all,
// and are the tenants bound to one yet") needs the denominator.
//
// There is no tenant_id label — the id would come from a token or user row and
// the cardinality would follow the tenant table, while the questions this
// series answers do not need it. The paired log line at each writer carries
// the tenant id, and so does the audit row for the two 403s.
var TenantGateTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "tenant_gate_total",
		Help:      "Tenant lifecycle and organisation-binding decisions on the authentication paths, by outcome",
	},
	[]string{"outcome"},
)

// RecordTenantGate increments the tenant-gate counter for one of the outcomes
// documented above. Writers: repo.TenantGate (see its doc comment for the
// current caller list), handler.OIDCCallback (organization binding) and
// handler.autoCreateBridgedUser (seat cap).
func RecordTenantGate(outcome string) {
	TenantGateTotal.WithLabelValues(outcome).Inc()
}

// init pre-registers the known label values at zero, matching r4_cost_spike.go
// and r6_rate_limit_degraded.go: a CounterVec child series does NOT appear in
// /metrics until its first Inc(), so an absent series would otherwise be
// ambiguous between "this has not happened yet" and "this counter is not wired
// up at all".
func init() {
	for _, outcome := range []string{
		TenantGateOutcomeDisabledDenied,
		TenantGateOutcomeMissingObserved,
		TenantGateOutcomeMissingDenied,
		TenantGateOutcomeOrgClaimAbsent,
		TenantGateOutcomeOrgTenantUnbound,
		TenantGateOutcomeOrgMatched,
		TenantGateOutcomeOrgMismatchDenied,
		TenantGateOutcomeSeatLimitDenied,
	} {
		TenantGateTotal.WithLabelValues(outcome)
	}
}
