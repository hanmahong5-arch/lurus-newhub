// Package metrics provides Prometheus metrics for the API gateway.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "lurus"
	subsystem = "gateway"
)

var (
	// RequestsTotal counts total requests by method, path, and status
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// RequestDuration measures request latency in seconds
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)

	// RelayRequestsTotal counts relay requests by provider and model
	RelayRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_requests_total",
			Help:      "Total number of relay requests to upstream providers",
		},
		[]string{"provider", "model", "status"},
	)

	// RelayDuration measures upstream API latency
	RelayDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_duration_seconds",
			Help:      "Upstream provider API latency in seconds",
			Buckets:   []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"provider", "model"},
	)

	// ChannelSelectDuration measures channel selection latency
	ChannelSelectDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_select_duration_seconds",
			Help:      "Channel selection latency in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25},
		},
	)

	// TokensProcessed counts tokens processed
	TokensProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "tokens_processed_total",
			Help:      "Total tokens processed (input + output)",
		},
		[]string{"provider", "model", "type"}, // type: input, output
	)

	// QuotaConsumed tracks quota consumption. Labeled by tenant only: a per-user
	// label would make the series count grow with the (never-shrinking) user
	// population × tenants, which unbounds Prometheus cardinality on the relay
	// hot path. Per-user quota detail lives in the consumption log and audit
	// trail, not in a metric label.
	QuotaConsumed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "quota_consumed_total",
			Help:      "Total quota consumed, by tenant",
		},
		[]string{"tenant_id"},
	)

	// RetryAttempts counts retry attempts
	RetryAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "retry_attempts_total",
			Help:      "Total retry attempts",
		},
		[]string{"provider", "reason"},
	)

	// RelayErrorsTotal classifies terminal relay failures by provider, model, and
	// error_type (O1). Recorded once per failed request at the relay's final-error
	// defer — terminal outcomes only, never per-retry (RetryAttempts covers that),
	// and success skips it. error_type is one of {upstream_5xx, upstream_4xx,
	// upstream_timeout, upstream_rate_limit, insufficient_quota, internal}; see
	// types.RelayErrorType. This is the missing "WHY is a provider failing" signal —
	// RelayRequestsTotal{status="error"} only says THAT it failed.
	RelayErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_errors_total",
			Help:      "Terminal relay errors by provider, model, and error_type",
		},
		[]string{"provider", "model", "error_type"},
	)

	// RelayFailoverTotal counts failover events: a request abandoning one channel
	// for another (O2). reason is one of {breaker_open (skipped a channel whose
	// circuit breaker is Open), upstream_error (retried onto a new channel after an
	// upstream/provider failure)}. A separate series from RetryAttempts — this is
	// the churn signal: sustained failover means capacity is thin or a provider is
	// flapping. provider labels the channel being failed OVER FROM.
	RelayFailoverTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_failover_total",
			Help:      "Relay failover events by provider (failed-over-from) and reason",
		},
		[]string{"provider", "reason"},
	)

	// FailoverSuppressedTotal counts retries that were deliberately NOT attempted
	// because failing over would have corrupted the client's response. The only
	// reason today is stream_already_started: bytes were already flushed, so a
	// second attempt would concatenate a duplicate body onto the same writer.
	//
	// Operationally this is a demand signal, not an error: a rising rate means
	// upstreams are dropping requests MID-STREAM, which retries cannot paper over
	// and which relay_failover_total structurally cannot show (those requests
	// never reach a second channel). No provider label — the counter is read as a
	// gateway-wide rate, and the per-channel attribution already lives on
	// relay_errors_total.
	FailoverSuppressedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_failover_suppressed_total",
			Help:      "Retries suppressed to protect an already-started client response, by reason",
		},
		[]string{"reason"},
	)

	// SessionAffinityTotal tracks conversation-to-channel pinning outcomes.
	// result: hit (pinned channel reused), miss (no binding yet), stale (binding
	// existed but the channel is no longer eligible → fell back to weighted
	// selection). A hit rate that collapses toward zero means bindings are
	// expiring faster than conversations turn, i.e. the upstream prompt cache is
	// being paid for repeatedly.
	SessionAffinityTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "session_affinity_total",
			Help:      "Session-affinity channel pinning outcomes by result (hit/miss/stale)",
		},
		[]string{"result"},
	)

	// ActiveConnections tracks current active connections
	ActiveConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "active_connections",
			Help:      "Number of active connections",
		},
	)

	// ChannelHealth tracks channel availability
	ChannelHealth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_health",
			Help:      "Channel health status (1=healthy, 0=unhealthy)",
		},
		[]string{"channel_id", "channel_name", "provider"},
	)

	// CacheHits tracks cache hit/miss ratio
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "cache_hits_total",
			Help:      "Cache hit/miss counts",
		},
		[]string{"cache_type", "result"}, // result: hit, miss
	)

	// ChannelConsecutiveErrors tracks consecutive errors per channel
	// Reset to 0 on successful request, incremented on each error
	ChannelConsecutiveErrors = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_consecutive_errors",
			Help:      "Consecutive error count per channel (resets on success)",
		},
		[]string{"channel_id", "channel_name", "provider"},
	)

	// ChannelErrorsTotal tracks total channel errors
	ChannelErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "channel_errors_total",
			Help:      "Total errors per channel",
		},
		[]string{"channel_id", "channel_name", "provider", "error_type"},
	)

	// CircuitBreakerState tracks per-channel breaker state (0=closed, 1=open, 2=half_open)
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state per channel (0=closed, 1=open, 2=half_open)",
		},
		[]string{"channel_id"},
	)

	// CircuitBreakerTrips counts how many times each breaker has been tripped open
	CircuitBreakerTrips = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "circuit_breaker_trips_total",
			Help:      "Total times a channel circuit breaker tripped to Open",
		},
		[]string{"channel_id"},
	)

	// CircuitBreakerRejections counts requests rejected by open breakers
	CircuitBreakerRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "circuit_breaker_rejections_total",
			Help:      "Total requests rejected by open circuit breaker",
		},
		[]string{"channel_id"},
	)

	// RelayOverheadDuration measures newhub-side latency before the first
	// upstream attempt (requestStart → first channel_select). SLO target:
	// how much delay newhub itself adds. Excludes upstream wall time.
	RelayOverheadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_overhead_duration_seconds",
			Help:      "Newhub overhead before first upstream call (excludes upstream wall time)",
			Buckets:   []float64{.0005, .001, .002, .005, .01, .025, .05, .1, .25, .5, 1},
		},
	)

	// RelayTotalDuration measures end-to-end Relay() handler duration including
	// retries and upstream wall time. Labeled per provider/model so SLOs can be
	// sliced by which upstream is degrading.
	RelayTotalDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_total_duration_seconds",
			Help:      "End-to-end Relay() handler duration including retries and upstream wall time",
			Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"provider", "model", "status"},
	)

	// CreditPoolDebitTotal counts every successful debit against a tenant
	// credit pool (ADR 2026-05-18 §3.1). Bumped from the post-consume quota
	// path that joins quota_consume + DebitPoolInTx in one transaction.
	CreditPoolDebitTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "credit_pool_debit_total",
			Help:      "Total debits from tenant credit pools, by tenant",
		},
		[]string{"tenant_id"},
	)

	// CreditPoolBalance reflects the current_balance column of each pool.
	// Set from DebitPoolInTx + TopupPool call-sites. Resellers watch this on
	// their Grafana panel; the CreditPoolBalanceLow alert fires under 20% ceiling.
	CreditPoolBalance = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "credit_pool_balance",
			Help:      "Current tenant credit pool balance",
		},
		[]string{"tenant_id"},
	)

	// CreditPoolOverdraftTotal counts post-consume debits that found the pool
	// already exhausted and were recorded as overdraft (negative balance +
	// relay_overdraft draw row) instead of being dropped. A non-zero rate means
	// the relay gate admitted requests that out-raced the pool balance — debt
	// is repaid by the next topup. P0-3 fix (2026-06-10).
	CreditPoolOverdraftTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "credit_pool_overdraft_total",
			Help:      "Post-consume pool debits recorded as overdraft (pool was exhausted), by tenant",
		},
		[]string{"tenant_id"},
	)

	// CreditPoolDebitLostTotal counts post-consume pool debits that failed with
	// a hard DB error (not exhaustion) and could NOT be recorded — the honest
	// residual gap after P0-3. Every increment is a known conservation-law
	// violation that needs manual reconciliation; alert on any increase.
	CreditPoolDebitLostTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "credit_pool_debit_lost_total",
			Help:      "Post-consume pool debits dropped due to hard DB errors (conservation-law violations)",
		},
	)

	// CreditPoolLookupMissTotal counts post-consume debits that never reached the
	// debit step because the tenant's pool row could not be resolved, labeled by
	// reason:
	//   no_pool      — ErrPoolNotFound: the tenant has no credit pool row. Either a
	//                  legitimately un-pooled (unlimited) tenant, or a tenant-id
	//                  drift where an orphaned token points at a tenant that never
	//                  got a pool row. A rising rate on a tenant that SHOULD be
	//                  pooled is the drift signal — the relay silently under-bills.
	//   lookup_error — a hard DB error resolving the pool row; pool-gating state is
	//                  unknown and the debit was skipped. Alert on any increase.
	CreditPoolLookupMissTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "credit_pool_lookup_miss_total",
			Help:      "Post-consume pool debits skipped because the pool row was unresolved, by tenant and reason (no_pool/lookup_error)",
		},
		[]string{"tenant_id", "reason"},
	)

	// ProvisioningKeysCreatedTotal counts Provisioning-API key creations.
	// Bumped from the POST /internal/v1/provisioning/tenants/:slug/keys handler.
	// Used to measure Switch-Reseller adoption rate during Q3 pilot.
	ProvisioningKeysCreatedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "provisioning_keys_created_total",
			Help:      "Provisioning-API issued keys, by tenant",
		},
		[]string{"tenant_id"},
	)

	// PoolExhaustedRejections counts relay requests rejected because the
	// tenant credit pool balance reached zero. Labeled by tenant_id and
	// pool_kind (currently always "relay" — extensible for future pool types).
	//
	// Alert: NewhubPoolExhaustedRejections fires when rate > 5/min sustained
	// for 5 minutes (see deploy/k8s/r6-stage/newhub-prometheus-rule.yaml).
	PoolExhaustedRejections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "pool_exhausted_rejections_total",
			Help:      "Total relay requests denied because the tenant credit pool is exhausted",
		},
		[]string{"tenant_id", "pool_kind"},
	)

	// BillingDebitAmountCNY observes the CNY amount of every successful
	// WalletDebit call to lurus-platform. Labeled by tenant_id.
	// Buckets cover typical LLM cost range: fractions of fen to hundreds of yuan.
	BillingDebitAmountCNY = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "billing_debit_amount_cny",
			Help:      "CNY amount per successful WalletDebit call to lurus-platform, by tenant",
			Buckets:   []float64{0.0001, 0.001, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 50, 100},
		},
		[]string{"tenant_id"},
	)
)

// RecordRelayRequest records a relay request with its outcome
func RecordRelayRequest(provider, model, status string, durationSec float64) {
	RelayRequestsTotal.WithLabelValues(provider, model, status).Inc()
	RelayDuration.WithLabelValues(provider, model).Observe(durationSec)
}

// RecordRelayError records a terminal relay failure classified by error_type (O1).
// Call once per failed request from the relay's final-error path.
func RecordRelayError(provider, model, errorType string) {
	RelayErrorsTotal.WithLabelValues(provider, model, errorType).Inc()
}

// RecordRelayFailover records a failover event (O2). reason is "breaker_open"
// (skipped an Open-breaker channel) or "upstream_error" (retried onto a new
// channel after an upstream failure).
func RecordRelayFailover(provider, reason string) {
	RelayFailoverTotal.WithLabelValues(provider, reason).Inc()
}

// RecordFailoverSuppressed records a retry that was withheld to keep an
// already-started client response intact. reason is "stream_already_started".
func RecordFailoverSuppressed(reason string) {
	FailoverSuppressedTotal.WithLabelValues(reason).Inc()
}

// RecordSessionAffinity records one affinity lookup outcome: "hit", "miss" or
// "stale". Called once per first-attempt channel selection that carried a
// session key.
func RecordSessionAffinity(result string) {
	SessionAffinityTotal.WithLabelValues(result).Inc()
}

// RecordTokens records token usage
func RecordTokens(provider, model string, inputTokens, outputTokens int) {
	if inputTokens > 0 {
		TokensProcessed.WithLabelValues(provider, model, "input").Add(float64(inputTokens))
	}
	if outputTokens > 0 {
		TokensProcessed.WithLabelValues(provider, model, "output").Add(float64(outputTokens))
	}
}

// RecordQuotaConsumed records quota consumption for a tenant. Per-user
// attribution is intentionally not a metric label (see QuotaConsumed).
func RecordQuotaConsumed(tenantID string, quota int64) {
	QuotaConsumed.WithLabelValues(tenantID).Add(float64(quota))
}

// RecordCacheHit records a cache hit or miss for the given cache type. Used to
// measure wallet-balance cache effectiveness, which directly gauges how often
// the P1-2 cached-balance degrade path even has data to work with.
func RecordCacheHit(cacheType string, hit bool) {
	result := "miss"
	if hit {
		result = "hit"
	}
	CacheHits.WithLabelValues(cacheType, result).Inc()
}

// RecordChannelError increments consecutive error count for a channel
func RecordChannelError(channelID, channelName, provider, errorType string) {
	ChannelConsecutiveErrors.WithLabelValues(channelID, channelName, provider).Inc()
	ChannelErrorsTotal.WithLabelValues(channelID, channelName, provider, errorType).Inc()
}

// ResetChannelErrors resets consecutive error count on successful request
func ResetChannelErrors(channelID, channelName, provider string) {
	ChannelConsecutiveErrors.WithLabelValues(channelID, channelName, provider).Set(0)
}

// SetChannelHealth sets channel health status (1=healthy, 0=unhealthy)
func SetChannelHealth(channelID, channelName, provider string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	ChannelHealth.WithLabelValues(channelID, channelName, provider).Set(value)
}

// RecordCircuitBreakerState sets the breaker state gauge for a channel.
func RecordCircuitBreakerState(channelID string, state int) {
	CircuitBreakerState.WithLabelValues(channelID).Set(float64(state))
}

// RecordCircuitBreakerTrip increments the trip counter.
func RecordCircuitBreakerTrip(channelID string) {
	CircuitBreakerTrips.WithLabelValues(channelID).Inc()
}

// RecordCircuitBreakerRejection increments the rejection counter.
func RecordCircuitBreakerRejection(channelID string) {
	CircuitBreakerRejections.WithLabelValues(channelID).Inc()
}

// RecordRelayOverhead records newhub-side overhead before first upstream call.
// SLO target: P99 < 50ms.
func RecordRelayOverhead(durationSec float64) {
	RelayOverheadDuration.Observe(durationSec)
}

// RecordRelayTotal records end-to-end Relay() handler duration.
// status must be "success" or "error".
func RecordRelayTotal(provider, model, status string, durationSec float64) {
	RelayTotalDuration.WithLabelValues(provider, model, status).Observe(durationSec)
}

// RecordPoolExhaustedRejection increments the pool exhaustion rejection counter.
// poolKind should be "relay" for relay-path rejections.
func RecordPoolExhaustedRejection(tenantID, poolKind string) {
	PoolExhaustedRejections.WithLabelValues(tenantID, poolKind).Inc()
}

// RecordBillingDebit records the CNY amount of a successful WalletDebit.
// tenantID may be empty for requests not linked to a tenant (recorded under "").
func RecordBillingDebit(tenantID string, amountCNY float64) {
	BillingDebitAmountCNY.WithLabelValues(tenantID).Observe(amountCNY)
}
