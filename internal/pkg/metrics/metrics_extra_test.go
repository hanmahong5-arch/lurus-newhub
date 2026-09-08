package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRecordCacheHit covers both the hit and miss branches of the cache
// recorder. Netdata's wallet-cache-effectiveness panel depends on the two
// (cache_type,result) series being counted independently.
func TestRecordCacheHit(t *testing.T) {
	cacheType := "wallet_balance"

	beforeHit := testutil.ToFloat64(CacheHits.WithLabelValues(cacheType, "hit"))
	beforeMiss := testutil.ToFloat64(CacheHits.WithLabelValues(cacheType, "miss"))

	// hit branch
	RecordCacheHit(cacheType, true)
	RecordCacheHit(cacheType, true)
	// miss branch
	RecordCacheHit(cacheType, false)

	if got := testutil.ToFloat64(CacheHits.WithLabelValues(cacheType, "hit")) - beforeHit; got != 2 {
		t.Errorf("expected hit delta 2, got %f", got)
	}
	if got := testutil.ToFloat64(CacheHits.WithLabelValues(cacheType, "miss")) - beforeMiss; got != 1 {
		t.Errorf("expected miss delta 1, got %f", got)
	}
}

// TestRecordTokensZeroSkip verifies the guard branches: a non-positive token
// count must NOT emit an Add (avoids polluting billing math with zero rows).
func TestRecordTokensZeroSkip(t *testing.T) {
	provider, model := "zhipu", "glm-4"

	beforeIn := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "input"))
	beforeOut := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "output"))

	// Only input positive; output zero → output series untouched.
	RecordTokens(provider, model, 42, 0)
	if got := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "input")) - beforeIn; got != 42 {
		t.Errorf("expected input delta 42, got %f", got)
	}
	if got := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "output")) - beforeOut; got != 0 {
		t.Errorf("expected output unchanged, got delta %f", got)
	}

	// Only output positive; input zero → input series untouched.
	beforeIn = testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "input"))
	RecordTokens(provider, model, 0, 7)
	if got := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "input")) - beforeIn; got != 0 {
		t.Errorf("expected input unchanged, got delta %f", got)
	}
	if got := testutil.ToFloat64(TokensProcessed.WithLabelValues(provider, model, "output")) - beforeOut; got != 7 {
		t.Errorf("expected output delta 7, got %f", got)
	}
}

// TestRecordCircuitBreakerState verifies each numeric state (0/1/2) is written
// to the per-channel gauge. Netdata's breaker-state alert reads this gauge, so
// the recorded value must equal the state code, and be per-channel isolated.
func TestRecordCircuitBreakerState(t *testing.T) {
	ch := "ch-cb-state-1"

	RecordCircuitBreakerState(ch, 0)
	if got := testutil.ToFloat64(CircuitBreakerState.WithLabelValues(ch)); got != 0 {
		t.Errorf("expected state 0 (closed), got %f", got)
	}
	RecordCircuitBreakerState(ch, 1)
	if got := testutil.ToFloat64(CircuitBreakerState.WithLabelValues(ch)); got != 1 {
		t.Errorf("expected state 1 (open), got %f", got)
	}
	RecordCircuitBreakerState(ch, 2)
	if got := testutil.ToFloat64(CircuitBreakerState.WithLabelValues(ch)); got != 2 {
		t.Errorf("expected state 2 (half_open), got %f", got)
	}

	// Independent channel keeps its own gauge value.
	other := "ch-cb-state-2"
	RecordCircuitBreakerState(other, 1)
	if got := testutil.ToFloat64(CircuitBreakerState.WithLabelValues(ch)); got != 2 {
		t.Errorf("first channel gauge clobbered, got %f", got)
	}
	if got := testutil.ToFloat64(CircuitBreakerState.WithLabelValues(other)); got != 1 {
		t.Errorf("expected other channel state 1, got %f", got)
	}
}

// TestRecordCircuitBreakerTrip verifies the trip counter increments per channel.
func TestRecordCircuitBreakerTrip(t *testing.T) {
	ch := "ch-cb-trip-1"

	before := testutil.ToFloat64(CircuitBreakerTrips.WithLabelValues(ch))
	RecordCircuitBreakerTrip(ch)
	RecordCircuitBreakerTrip(ch)
	RecordCircuitBreakerTrip(ch)
	if got := testutil.ToFloat64(CircuitBreakerTrips.WithLabelValues(ch)) - before; got != 3 {
		t.Errorf("expected trip delta 3, got %f", got)
	}
}

// TestRecordCircuitBreakerRejection verifies the rejection counter increments
// per channel and is a distinct series from trips.
func TestRecordCircuitBreakerRejection(t *testing.T) {
	ch := "ch-cb-reject-1"

	before := testutil.ToFloat64(CircuitBreakerRejections.WithLabelValues(ch))
	RecordCircuitBreakerRejection(ch)
	if got := testutil.ToFloat64(CircuitBreakerRejections.WithLabelValues(ch)) - before; got != 1 {
		t.Errorf("expected rejection delta 1, got %f", got)
	}
	// Rejection must not have bumped the trip counter for the same channel.
	if got := testutil.ToFloat64(CircuitBreakerTrips.WithLabelValues(ch)); got != 0 {
		t.Errorf("rejection leaked into trips counter, got %f", got)
	}
}

// TestRecordRelayOverhead verifies the overhead histogram records an observation.
// The histogram is unlabeled, so we assert its sample count grows by exactly the
// number of observations.
func TestRecordRelayOverhead(t *testing.T) {
	before := testutil.CollectAndCount(RelayOverheadDuration)
	// Histogram always emits one series once created; assert observations land by
	// gathering the sum via a metric dump is heavy — instead assert count via the
	// dedicated sample-count helper below.
	sumBefore := histogramSampleCount(t, RelayOverheadDuration)
	RecordRelayOverhead(0.0007)
	RecordRelayOverhead(0.02)
	sumAfter := histogramSampleCount(t, RelayOverheadDuration)
	if sumAfter-sumBefore != 2 {
		t.Errorf("expected 2 new observations, got %d", sumAfter-sumBefore)
	}
	if testutil.CollectAndCount(RelayOverheadDuration) < before {
		t.Errorf("histogram series disappeared")
	}
}

// TestRecordRelayTotal verifies the end-to-end duration histogram records under
// the correct (provider,model,status) label set and keeps success/error separate.
func TestRecordRelayTotal(t *testing.T) {
	provider, model := "gemini", "gemini-1.5-pro"

	sc := RelayTotalDuration.WithLabelValues(provider, model, "success", "llm-api")
	ec := RelayTotalDuration.WithLabelValues(provider, model, "error", "llm-api")

	beforeSuccess := observerSampleCount(t, sc)
	beforeError := observerSampleCount(t, ec)

	RecordRelayTotal(provider, model, "success", "llm-api", 0.3)
	RecordRelayTotal(provider, model, "success", "llm-api", 1.2)
	RecordRelayTotal(provider, model, "error", "llm-api", 5.0)

	if got := observerSampleCount(t, sc) - beforeSuccess; got != 2 {
		t.Errorf("expected 2 success observations, got %d", got)
	}
	if got := observerSampleCount(t, ec) - beforeError; got != 1 {
		t.Errorf("expected 1 error observation, got %d", got)
	}
}

// TestMiddleware drives the gin metrics middleware through a real httptest
// request and asserts that RequestsTotal (by method/path/status) and the
// RequestDuration histogram both observe the completed request. This is the
// per-route request signal ops watches for traffic and latency SLOs.
func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/ping/:id", func(c *gin.Context) {
		c.String(http.StatusTeapot, "pong")
	})

	fullPath := "/ping/:id"
	method := http.MethodGet
	status := "418"

	beforeCount := testutil.ToFloat64(RequestsTotal.WithLabelValues(method, fullPath, status))
	beforeDur := observerSampleCount(t, RequestDuration.WithLabelValues(method, fullPath))
	beforeConns := testutil.ToFloat64(ActiveConnections)

	req := httptest.NewRequest(method, "/ping/42", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", w.Code)
	}

	// RequestsTotal must have counted this route with the templated FullPath.
	if got := testutil.ToFloat64(RequestsTotal.WithLabelValues(method, fullPath, status)) - beforeCount; got != 1 {
		t.Errorf("expected requests_total delta 1 for %s %s %s, got %f", method, fullPath, status, got)
	}
	// RequestDuration must have one new observation on the same label set.
	if got := observerSampleCount(t, RequestDuration.WithLabelValues(method, fullPath)) - beforeDur; got != 1 {
		t.Errorf("expected 1 duration observation, got %d", got)
	}
	// ActiveConnections is Inc/Dec'd around c.Next() so net change is zero.
	if got := testutil.ToFloat64(ActiveConnections); got != beforeConns {
		t.Errorf("active_connections not balanced, before %f after %f", beforeConns, got)
	}
}

// TestMiddlewareUnknownPath verifies the fallback branch: a request that matches
// no registered route has an empty gin FullPath and must be recorded under the
// literal "unknown" path label (with a 404 status) rather than an empty label.
func TestMiddlewareUnknownPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/known", func(c *gin.Context) { c.Status(http.StatusOK) })

	method := http.MethodGet
	before := testutil.ToFloat64(RequestsTotal.WithLabelValues(method, "unknown", "404"))

	req := httptest.NewRequest(method, "/definitely/not/registered", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if got := testutil.ToFloat64(RequestsTotal.WithLabelValues(method, "unknown", "404")) - before; got != 1 {
		t.Errorf("expected unknown-path 404 delta 1, got %f", got)
	}
}
