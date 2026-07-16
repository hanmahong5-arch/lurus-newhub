// Package metrics — credit-pool stranded-debit reconcile instrumentation.
//
// A "stranded debit" is a topup whose platform wallet debit succeeded but
// whose local pool credit AND wallet revert both failed: money left the
// wallet and never reached the pool. These three series make that state
// observable and alertable instead of log-only:
//
//   - newhub_credit_pool_stranded_total    counter — new stranded events recorded
//   - newhub_credit_pool_reconciled_total  counter — stranded events closed (credited)
//   - newhub_credit_pool_stranded_open     gauge   — stranded events still awaiting
//     compensation (alert when > 0 for more than one reconcile interval)
//
// Names are intentionally fully-qualified (no lurus_/gateway_ prefix): they
// are cross-service money-integrity signals consumed by the platform-side
// reconciliation dashboards, which key on the newhub_ prefix.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// CreditPoolStrandedTotal counts stranded wallet debits recorded by the
	// topup handler (debit ok, pool credit failed, wallet revert failed).
	// Incremented once per unique stranded event — idempotent replays of the
	// same event_id do not re-count.
	CreditPoolStrandedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "newhub_credit_pool_stranded_total",
			Help: "Total stranded credit-pool topups recorded (wallet debited, pool credit + revert both failed)",
		},
	)

	// CreditPoolReconciledTotal counts stranded events successfully closed —
	// either by the background reconcile sweep or by an idempotent client
	// retry of the same topup intent.
	CreditPoolReconciledTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "newhub_credit_pool_reconciled_total",
			Help: "Total stranded credit-pool topups compensated (pool credited, event closed)",
		},
	)

	// CreditPoolStrandedOpen reflects the number of stranded events still in
	// the open state after the latest reconcile sweep. Non-zero for more than
	// one sweep interval means compensation keeps failing (e.g. pool ceiling)
	// and a human needs to look.
	CreditPoolStrandedOpen = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "newhub_credit_pool_stranded_open",
			Help: "Stranded credit-pool topups currently awaiting compensation",
		},
	)
)
