package fakeupstream

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Fault modes. A request selects one by asking for the mode name as its
// model, by ?mode=, or by being next in line after POST /_fake/faults. The
// names of the first five are the UAT fault simulator's, unchanged, so a UAT
// channel seeded with them keeps working.
const (
	// FaultMidStreamAbort accepts the request, streams a few well-formed
	// frames in the caller's wire, then ends the body without a terminator
	// and without usage — an upstream that died mid-answer.
	FaultMidStreamAbort = "mid_stream_abort"
	// FaultSlowHeaders holds the response before the first byte (default
	// 30s, ?delay_ms= up to 120000), for the relay's own idle timeout.
	FaultSlowHeaders = "slow_headers"
	// FaultHTTP500 is a plain upstream failure.
	FaultHTTP500 = "http_500"
	// FaultRateLimit429 answers 429 with Retry-After (default 30,
	// ?retry_after= to change).
	FaultRateLimit429 = "rate_limit_429"
	// FaultInsufficientBalance is an unpaid provider account: DeepSeek's real
	// 402 shape.
	FaultInsufficientBalance = "upstream_insufficient_balance"
	// FaultHTTP401 is a rejected channel key.
	FaultHTTP401 = "http_401"
	// FaultDisconnectBeforeFirstByte accepts the connection and closes it
	// without writing a status line.
	FaultDisconnectBeforeFirstByte = "disconnect_before_first_byte"
)

// FaultModes is the supported set.
var FaultModes = []string{
	FaultMidStreamAbort,
	FaultSlowHeaders,
	FaultHTTP500,
	FaultRateLimit429,
	FaultInsufficientBalance,
	FaultHTTP401,
	FaultDisconnectBeforeFirstByte,
}

// IsFaultMode reports whether mode is one of FaultModes.
func IsFaultMode(mode string) bool {
	for _, m := range FaultModes {
		if m == mode {
			return true
		}
	}
	return false
}

func unknownModeMessage(mode string) string {
	return fmt.Sprintf("unknown fault mode %q; supported: %v", mode, FaultModes)
}

// FaultParams tunes the modes that take a parameter.
type FaultParams struct {
	Frames     int           // mid_stream_abort: frames before the abort
	Delay      time.Duration // slow_headers: wait before the first byte
	RetryAfter string        // rate_limit_429: Retry-After value
}

// FaultParamsFromQuery reads ?frames=, ?delay_ms= and ?retry_after=, keeping
// the defaults for anything absent or out of range.
func FaultParamsFromQuery(r *http.Request) FaultParams {
	p := FaultParams{Frames: 3, Delay: 30 * time.Second, RetryAfter: "30"}
	q := r.URL.Query()
	if n, err := strconv.Atoi(q.Get("frames")); err == nil && n >= 0 && n <= 100 {
		p.Frames = n
	}
	if ms, err := strconv.Atoi(q.Get("delay_ms")); err == nil && ms >= 0 && ms <= 120000 {
		p.Delay = time.Duration(ms) * time.Millisecond
	}
	if s := q.Get("retry_after"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			p.RetryAfter = s
		}
	}
	return p
}

// WriteFault answers r with the given fault in the given wire. It is exported
// for the UAT fault simulator, which serves these modes from inside newhub.
// mode must satisfy IsFaultMode.
func WriteFault(w http.ResponseWriter, r *http.Request, wire, mode string, p FaultParams) {
	switch mode {
	case FaultHTTP500:
		writeWireError(w, wire, http.StatusInternalServerError, "server_error", "simulated upstream failure")

	case FaultRateLimit429:
		w.Header().Set("Retry-After", p.RetryAfter)
		writeWireError(w, wire, http.StatusTooManyRequests, "rate_limit_error", "simulated rate limit")

	case FaultInsufficientBalance:
		writeWireError(w, wire, http.StatusPaymentRequired, "insufficient_quota", "Insufficient Balance")

	case FaultHTTP401:
		writeWireError(w, wire, http.StatusUnauthorized, "authentication_error", "simulated invalid api key")

	case FaultSlowHeaders:
		// Bounded, and abandoned as soon as the caller hangs up: this must
		// never outlive the request it belongs to.
		select {
		case <-time.After(p.Delay):
			writeJSON(w, http.StatusOK, map[string]any{
				"id":      "fake-slow",
				"object":  "chat.completion",
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "late"}}},
			})
		case <-r.Context().Done():
		}

	case FaultDisconnectBeforeFirstByte:
		if closeConn(w) {
			return
		}
		// Nothing to hijack (HTTP/2, an in-memory recorder): the server's own
		// abort, which also sends no response.
		panic(http.ErrAbortHandler)

	case FaultMidStreamAbort:
		writeAbortedStream(w, r, wire, p.Frames)
	}
}

// closeConn hijacks and closes the connection. Some writers (gin's, over a
// recorder) claim Hijacker and then panic, so the attempt is guarded.
func closeConn(w http.ResponseWriter) (closed bool) {
	h, ok := w.(http.Hijacker)
	if !ok {
		return false
	}
	defer func() {
		if recover() != nil {
			closed = false
		}
	}()
	conn, _, err := h.Hijack()
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// writeAbortedStream emits frames in the caller's wire and returns without the
// wire's terminator ([DONE], message_stop, response.completed, finishReason)
// and without usage. Returning ends the body mid-stream.
func writeAbortedStream(w http.ResponseWriter, r *http.Request, wire string, frames int) {
	if wire == WireSystemOne {
		// Not a streaming wire: the equivalent is a body cut off mid-JSON.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"`))
		return
	}
	s := startSSE(w)
	switch wire {
	case WireAnthropic:
		if s.event("message_start", anthropicMessageStart("fake-abort", "fake", Usage{})) != nil {
			return
		}
		if s.event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}) != nil {
			return
		}
	case WireOpenAIResponses:
		if s.event("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "fake-abort", "object": "response", "status": "in_progress"}}) != nil {
			return
		}
	}
	for i := 0; i < frames; i++ {
		if r.Context().Err() != nil {
			return
		}
		text := fmt.Sprintf("part-%d ", i)
		var err error
		switch wire {
		case WireAnthropic:
			err = s.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
		case WireOpenAIResponses:
			err = s.event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": text})
		case WireGemini:
			err = s.data(map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}}})
		default:
			// No finish_reason key at all, not even null: nothing in an
			// aborted stream may read as an ending.
			chunk := openAIChunk("fake-abort", FaultMidStreamAbort, map[string]any{"role": "assistant", "content": text}, nil)
			delete(chunk["choices"].([]any)[0].(map[string]any), "finish_reason")
			err = s.data(chunk)
		}
		if err != nil {
			return
		}
	}
}

// writeWireError writes an error body in the shape the wire's real vendor
// uses, so the relay's own error classification is what gets exercised.
func writeWireError(w http.ResponseWriter, wire string, status int, errType, message string) {
	switch wire {
	case WireAnthropic:
		writeJSON(w, status, map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errType, "message": message},
		})
	case WireGemini:
		writeJSON(w, status, map[string]any{
			"error": map[string]any{"code": status, "message": message, "status": geminiStatus(status)},
		})
	default:
		writeJSON(w, status, map[string]any{
			"error": map[string]any{"message": message, "type": errType},
		})
	}
}

func geminiStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusPaymentRequired:
		return "FAILED_PRECONDITION"
	default:
		return "INTERNAL"
	}
}
