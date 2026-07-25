package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestShouldRetry_NoFailoverAfterStreamStarted pins the streaming-safety
// contract: once ANY byte of the response has been flushed to the client, the
// relay must not fail over to another channel.
//
// Why this is a correctness bug and not a preference: the retry loop in
// RelayHandler reuses the SAME gin.ResponseWriter across attempts. If attempt
// #1 already streamed `data: {...}` SSE frames and then the upstream dropped
// mid-stream, retrying writes a SECOND complete response onto the same wire.
// The client sees two concatenated streams — duplicated/interleaved content
// that no SSE consumer can un-mix — and the user is billed for both attempts.
//
// A stream is only resumable if the gateway can replay upstream state, which
// it cannot: the partial output has already left the process.
func TestShouldRetry_NoFailoverAfterStreamStarted(t *testing.T) {
	// Every one of these errors retries on a fresh (unwritten) response.
	// After a partial write, all of them must stop.
	retryableErrs := []struct {
		name string
		err  *types.NewAPIError
	}{
		{"channel_error", types.NewError(errors.New("x"), types.ErrorCodeChannelNoAvailableKey)},
		{"rate_limited_429", errWithStatus(http.StatusTooManyRequests)},
		{"server_500", errWithStatus(http.StatusInternalServerError)},
		{"bad_gateway_502", errWithStatus(http.StatusBadGateway)},
		{"redirect_307", errWithStatus(307)},
		{"other_4xx", errWithStatus(http.StatusForbidden)},
	}

	for _, tc := range retryableErrs {
		t.Run(tc.name+"/not_written_still_retries", func(t *testing.T) {
			c, _ := newTestCtx()
			if !shouldRetry(c, tc.err, 3) {
				t.Fatalf("guard over-reaches: %s must still retry when nothing was written", tc.name)
			}
		})

		t.Run(tc.name+"/after_partial_stream_must_not_retry", func(t *testing.T) {
			c, w := newTestCtx()
			// Simulate a streaming relay that already emitted one SSE frame.
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			if _, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"); err != nil {
				t.Fatalf("seed write failed: %v", err)
			}
			c.Writer.Flush()
			if !c.Writer.Written() {
				t.Fatalf("test setup broken: Written() must be true after a flush (body=%q)", w.Body.String())
			}

			if shouldRetry(c, tc.err, 3) {
				t.Errorf("%s: retried after %d bytes were already streamed — the second attempt "+
					"would concatenate a duplicate response onto the client's stream", tc.name, w.Body.Len())
			}
		})
	}
}

// TestShouldRetryTaskRelay_NoFailoverAfterStreamStarted mirrors the guard for
// the async-task relay path (Midjourney / Suno / video), which shares the same
// single-ResponseWriter retry loop.
func TestShouldRetryTaskRelay_NoFailoverAfterStreamStarted(t *testing.T) {
	mk := func(status int) *dto.TaskError {
		return &dto.TaskError{StatusCode: status, Error: errors.New("boom")}
	}

	t.Run("not_written_still_retries", func(t *testing.T) {
		c, _ := newTestCtx()
		if !shouldRetryTaskRelay(c, 1, mk(http.StatusInternalServerError), 3) {
			t.Fatal("guard over-reaches: 500 must still retry when nothing was written")
		}
	})

	t.Run("after_partial_write_must_not_retry", func(t *testing.T) {
		c, w := newTestCtx()
		if _, err := c.Writer.WriteString(`{"task_id":"partial"`); err != nil {
			t.Fatalf("seed write failed: %v", err)
		}
		c.Writer.Flush()
		if shouldRetryTaskRelay(c, 1, mk(http.StatusInternalServerError), 3) {
			t.Errorf("retried after %d bytes were already written to the client", w.Body.Len())
		}
	})
}
