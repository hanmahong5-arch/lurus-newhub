package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RelayTimeToFirstToken measures the wall-clock gap between StartTime and
	// the first token received from upstream on a streaming call (RelayInfo's
	// FirstResponseTime). It is stamped by whichever streaming adaptor's
	// first RelayInfo.SetFirstResponseTime() call fires for that request —
	// the capture point is provider-side and varies by adaptor:
	// helper/stream_scanner.go parses the first non-terminal SSE event before
	// forwarding it (most text/chat providers), the Cloudflare adaptor calls
	// it after the first chunk is forwarded, and the AWS and Cohere adaptors
	// call it at their own first-chunk point. At each of these sites it is
	// "the adaptor observed a first token from upstream", not "the byte
	// reached the caller's socket". OpenAI Realtime websocket sessions are
	// excluded by the writer (their first upstream message is not a token).
	// The series name vocabulary-matches
	// "time to first token" as used by OTel GenAI semantic conventions and
	// Helicone/Langfuse dashboards, so an operator coming from those tools
	// finds the same concept here.
	//
	// OpenAI Realtime sessions (RelayFormatOpenAIRealtime) are excluded from
	// this histogram: their SetFirstResponseTime call site is the first
	// inbound websocket message from upstream, which measures the same
	// thing in principle but on a fundamentally different traffic shape
	// (long-lived bidirectional session, not a request/response call), so
	// mixing it into a request-latency histogram would be misleading. The
	// exclusion is enforced in handler.observeRelayOutcome, not here.
	//
	// Non-streaming calls and requests that failed before receiving a first
	// token never call SetFirstResponseTime, so FirstResponseTime stays at
	// its "never happened" sentinel (StartTime minus one second) —
	// handler.observeRelayOutcome only observes this histogram when
	// RelayInfo.HasSendResponse() is true, so those requests correctly
	// contribute nothing rather than a bogus negative or zero duration. A
	// request that DID receive a first token and then failed or had the
	// client disconnect still observes — HasSendResponse() does not look at
	// outcome status, only at whether a first token arrived.
	//
	// Declared in this package's `var (...)` block (not a standalone `var
	// Name = ...` line) so the honesty gate's declaredMetricVarRe — anchored
	// on `^\s*(\w+)\s*=\s*promauto` — actually sees this series.
	RelayTimeToFirstToken = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "relay_time_to_first_token_seconds",
			Help:      "Wall-clock time from relay start to the first token received from upstream (not the byte reaching the caller); OpenAI Realtime sessions excluded",
			Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"provider", "model", "product"},
	)
)

// RecordTimeToFirstToken observes one streamed request's time-to-first-token.
// Call exactly once per request that actually produced a first byte —
// observeRelayOutcome is the single call site (relay_outcome.go).
func RecordTimeToFirstToken(provider, model, product string, seconds float64) {
	RelayTimeToFirstToken.WithLabelValues(provider, model, product).Observe(seconds)
}
