package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// BillingPreAuthDuration measures pre-authorization latency.
	BillingPreAuthDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "preauth_duration_seconds",
			Help:      "Pre-authorization call latency in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1},
		},
	)

	// BillingSettleTotal counts settle operations by status.
	BillingSettleTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "settle_total",
			Help:      "Total settle operations by status (success/error)",
		},
		[]string{"status"},
	)

	// BillingOutboxPending tracks the number of pending outbox entries.
	BillingOutboxPending = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "outbox_pending",
			Help:      "Number of pending billing outbox entries",
		},
	)

	// BillingOutboxFailedTotal counts permanently failed outbox entries.
	BillingOutboxFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "outbox_failed_total",
			Help:      "Total billing outbox entries that permanently failed after max retries",
		},
	)

	// BillingCircuitBreakerState tracks circuit breaker state (0=closed, 1=open, 2=halfopen).
	BillingCircuitBreakerState = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state: 0=closed (healthy), 1=open (platform down), 2=halfopen (probing)",
		},
	)

	// BillingDegradedTotal counts relay requests that were admitted on cached
	// wallet balance while the platform billing breaker was open (P1-2 graceful
	// degradation), labeled by outcome:
	//   allowed — admitted on fresh cached credit (breaker open, estimate ≪ balance, under tenant cap)
	//   denied  — degrade declined (no fresh cache / estimate too close to balance / tenant cap hit) → fail-closed 402
	BillingDegradedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "degraded_total",
			Help:      "Relay billing degradation decisions while breaker open, by outcome (allowed/denied)",
		},
		[]string{"outcome"},
	)

	// BillingAdvisoryBypassTotal counts local-ledger gates that were skipped
	// for a platform-governed request while LOCAL_LEDGER_ADVISORY is on
	// (Track A). Labeled by which gate would have blocked:
	//   user_balance_402 — local user quota would have 402'd the request
	//   pre_deduct       — local pre-consume write failed, relay continued
	BillingAdvisoryBypassTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "advisory_bypass_total",
			Help:      "Local-ledger gates bypassed for platform-governed requests under LOCAL_LEDGER_ADVISORY, by gate (user_balance_402/pre_deduct)",
		},
		[]string{"gate"},
	)

	// BillingAdvisoryMeterLost counts shadow-ledger writes that failed while
	// LOCAL_LEDGER_ADVISORY is on — the request/settle proceeded (platform is
	// the ledger of record) but the local mirror lost this data point, so the
	// daily drift reconciliation will show a corresponding gap. Labeled by
	// which write was lost (user_quota/token_quota).
	BillingAdvisoryMeterLost = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "advisory_meter_lost_total",
			Help:      "Shadow local-ledger writes lost under LOCAL_LEDGER_ADVISORY (settle proceeded), by write (user_quota/token_quota)",
		},
		[]string{"write"},
	)

	// BillingZeroAmountChargeTotal counts platform-wallet settlements where a
	// strictly-positive local quota converted to a wallet amount that will
	// round to 0.0000 under the platform wallet's numeric(14,4) column (i.e.
	// < 0.00005 LB). Pure observation — the settle call itself is unchanged —
	// so this is a leak DETECTOR, not a fix (the wallet-side precision fix is
	// tracked separately, out of scope here).
	BillingZeroAmountChargeTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "zero_amount_charge_total",
			Help:      "Positive local quota settlements whose wallet amount rounds to 0.0000 under numeric(14,4)",
		},
	)

	// BillingUsageMirrorTotal counts usage-event mirror reports to the
	// platform (POST /internal/v1/usage/events, metric=llm_relay) by status
	// (success/error). The mirror feeds the Track A drift reconciliation;
	// error only means this data point is missing from usage_events, never
	// that money moved or was lost.
	BillingUsageMirrorTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "usage_mirror_total",
			Help:      "Usage-event mirror reports to the platform by status (success/error)",
		},
		[]string{"status"},
	)
)
