package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRelayRequest(t *testing.T) {
	// Record a successful request
	RecordRelayRequest("openai", "gpt-4", "success", "llm-api", 0.5)

	// Verify counter incremented
	count := testutil.ToFloat64(RelayRequestsTotal.WithLabelValues("openai", "gpt-4", "success", "llm-api"))
	if count != 1 {
		t.Errorf("expected count 1, got %f", count)
	}

	// Record an error request
	RecordRelayRequest("openai", "gpt-4", "error", "llm-api", 1.0)
	errorCount := testutil.ToFloat64(RelayRequestsTotal.WithLabelValues("openai", "gpt-4", "error", "llm-api"))
	if errorCount != 1 {
		t.Errorf("expected error count 1, got %f", errorCount)
	}

	// client_gone is a distinct status from success/error — the caller hung
	// up before the request completed, and must not be double-counted into
	// either of the other two series.
	RecordRelayRequest("openai", "gpt-4", "client_gone", "switch", 0.2)
	goneCount := testutil.ToFloat64(RelayRequestsTotal.WithLabelValues("openai", "gpt-4", "client_gone", "switch"))
	if goneCount != 1 {
		t.Errorf("expected client_gone count 1, got %f", goneCount)
	}
}

func TestRecordTokens(t *testing.T) {
	RecordTokens("claude", "claude-3", 100, 50)

	inputCount := testutil.ToFloat64(TokensProcessed.WithLabelValues("claude", "claude-3", "input"))
	if inputCount != 100 {
		t.Errorf("expected input tokens 100, got %f", inputCount)
	}

	outputCount := testutil.ToFloat64(TokensProcessed.WithLabelValues("claude", "claude-3", "output"))
	if outputCount != 50 {
		t.Errorf("expected output tokens 50, got %f", outputCount)
	}
}

func TestRecordQuotaConsumed(t *testing.T) {
	// Tenant-only label: WithLabelValues panics on wrong arity, so this call
	// also locks the cardinality fix — re-adding a user_id label turns it red.
	RecordQuotaConsumed("tenant1", 1000)

	quota := testutil.ToFloat64(QuotaConsumed.WithLabelValues("tenant1"))
	if quota != 1000 {
		t.Errorf("expected quota 1000, got %f", quota)
	}
}

func TestActiveConnections(t *testing.T) {
	initial := testutil.ToFloat64(ActiveConnections)

	ActiveConnections.Inc()
	after := testutil.ToFloat64(ActiveConnections)
	if after != initial+1 {
		t.Errorf("expected %f, got %f", initial+1, after)
	}

	ActiveConnections.Dec()
	final := testutil.ToFloat64(ActiveConnections)
	if final != initial {
		t.Errorf("expected %f, got %f", initial, final)
	}
}

// channel_health / channel_consecutive_errors / channel_errors_total and their
// Record/Set/Reset helpers were deleted (2026-09-07): no production call-site
// ever wrote them (grep across internal/ turned up zero non-test callers), so
// they were three declared, dashboarded-nowhere series a scraper could read as
// "channel health is being tracked" when nothing populated them. See
// declared_series_written_test.go, which fails go test if a metric var is
// declared here with no real writer.

// TestPoolExhaustedRejections verifies that RecordPoolExhaustedRejection increments
// the counter with the expected label combination. Simulates the pool_balance_check
// middleware rejecting a relay request for an exhausted pool.
func TestPoolExhaustedRejections(t *testing.T) {
	tenantID := "tenant-pool-test-1"
	poolKind := "relay"

	before := testutil.ToFloat64(PoolExhaustedRejections.WithLabelValues(tenantID, poolKind))

	RecordPoolExhaustedRejection(tenantID, poolKind)
	RecordPoolExhaustedRejection(tenantID, poolKind)

	after := testutil.ToFloat64(PoolExhaustedRejections.WithLabelValues(tenantID, poolKind))
	if after-before != 2 {
		t.Errorf("expected 2 increments, got delta %f", after-before)
	}

	// Different tenant — must be independent
	otherTenant := "tenant-pool-test-2"
	RecordPoolExhaustedRejection(otherTenant, poolKind)
	otherCount := testutil.ToFloat64(PoolExhaustedRejections.WithLabelValues(otherTenant, poolKind))
	if otherCount != 1 {
		t.Errorf("expected 1 for other tenant, got %f", otherCount)
	}
	// Ensure first tenant unchanged
	if testutil.ToFloat64(PoolExhaustedRejections.WithLabelValues(tenantID, poolKind)) != after {
		t.Errorf("first tenant count changed unexpectedly")
	}
}

// TestRecordRelayError verifies the O1 terminal-error classifier counter
// increments per (provider, model, error_type, product) and keeps error_types
// and products independent.
func TestRecordRelayError(t *testing.T) {
	provider, model := "openai", "gpt-4o"

	before := testutil.ToFloat64(RelayErrorsTotal.WithLabelValues(provider, model, "upstream_5xx", "llm-api"))
	RecordRelayError(provider, model, "upstream_5xx", "llm-api")
	RecordRelayError(provider, model, "upstream_5xx", "llm-api")
	if got := testutil.ToFloat64(RelayErrorsTotal.WithLabelValues(provider, model, "upstream_5xx", "llm-api")) - before; got != 2 {
		t.Errorf("expected upstream_5xx delta 2, got %f", got)
	}

	// A distinct error_type is an independent series.
	RecordRelayError(provider, model, "upstream_timeout", "llm-api")
	if got := testutil.ToFloat64(RelayErrorsTotal.WithLabelValues(provider, model, "upstream_timeout", "llm-api")); got != 1 {
		t.Errorf("expected upstream_timeout series 1, got %f", got)
	}

	// A distinct product is also an independent series, same error_type.
	RecordRelayError(provider, model, "upstream_5xx", "switch")
	if got := testutil.ToFloat64(RelayErrorsTotal.WithLabelValues(provider, model, "upstream_5xx", "switch")); got != 1 {
		t.Errorf("expected switch-product series 1, got %f", got)
	}
}

// TestRecordRelayFailover verifies the O2 failover counter increments per
// (provider, reason) with the two reasons kept independent.
func TestRecordRelayFailover(t *testing.T) {
	provider := "anthropic"

	beforeBreaker := testutil.ToFloat64(RelayFailoverTotal.WithLabelValues(provider, "breaker_open"))
	RecordRelayFailover(provider, "breaker_open")
	if got := testutil.ToFloat64(RelayFailoverTotal.WithLabelValues(provider, "breaker_open")) - beforeBreaker; got != 1 {
		t.Errorf("expected breaker_open delta 1, got %f", got)
	}

	beforeUpstream := testutil.ToFloat64(RelayFailoverTotal.WithLabelValues(provider, "upstream_error"))
	RecordRelayFailover(provider, "upstream_error")
	RecordRelayFailover(provider, "upstream_error")
	if got := testutil.ToFloat64(RelayFailoverTotal.WithLabelValues(provider, "upstream_error")) - beforeUpstream; got != 2 {
		t.Errorf("expected upstream_error delta 2, got %f", got)
	}
}

// TestBillingDebitAmountCNY verifies that RecordBillingDebit populates the
// histogram under its (product, op) labels — the two money-moving legs
// (direct debit vs pre-auth settle) must stay distinguishable series, not
// merge into one.
// HistogramVec.WithLabelValues returns Observer (not Collector), so we test via
// testutil.CollectAndCount on the parent vec which counts metric families produced.
func TestBillingDebitAmountCNY(t *testing.T) {
	product := "product-billing-test-3" // unique label to avoid cross-test interference

	// Before any observation the vec may or may not expose this label set.
	// Prime the label set so the next CollectAndCount reflects our observations.
	RecordBillingDebit(product, "debit", 0.05)
	RecordBillingDebit(product, "debit", 1.50)
	RecordBillingDebit(product, "debit", 10.0)

	// The HistogramVec itself is a Collector; CollectAndCount counts the number of
	// unique metric time series it emits. After three observations the vec must have
	// at least one time series with this product/op label.
	count := testutil.CollectAndCount(BillingDebitAmountCNY)
	if count == 0 {
		t.Errorf("expected at least one metric series from BillingDebitAmountCNY, got 0")
	}

	// op is part of the series identity: settling the SAME product is a
	// distinct time series from debiting it.
	RecordBillingDebit(product, "settle", 0.01)

	countAfter := testutil.CollectAndCount(BillingDebitAmountCNY)
	if countAfter <= count {
		t.Errorf("expected a new series after recording the settle op, count %d -> %d", count, countAfter)
	}
}
