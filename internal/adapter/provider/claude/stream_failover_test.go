package claude

// stream_failover_test.go — a vendor-a stream that ends without its
// message_delta BEFORE any byte reached the caller must fail over: retryable
// error, nothing written, nothing billed. Once frames have gone out the
// in-band error frame stays (stream_incomplete_test.go).

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestClaudeStreamHandler_IncompleteBeforeFirstByte_FailsOver(t *testing.T) {
	bodies := map[string]string{
		"empty body":             "",
		"keepalive comment only": ": keepalive\n\n",
	}
	for name, body := range bodies {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
			t.Run(name+"/"+string(format), func(t *testing.T) {
				c, w := fixClaudeGuardsContext()
				info := fixClaudeGuardsInfo(format, "model-a")
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
				defer func() { _ = resp.Body.Close() }()

				usage, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)

				if apiErr == nil {
					t.Fatalf("incomplete stream with nothing delivered must return an error (nil is recorded as breaker success and never fails over); body=%q", w.Body.String())
				}
				if types.IsSkipRetryError(apiErr) || types.IsChannelError(apiErr) || !types.IsUpstreamFailure(apiErr) || apiErr.StatusCode != http.StatusBadGateway {
					t.Errorf("error must be a retryable 502 upstream failure: skipRetry=%v channel=%v upstream=%v status=%d",
						types.IsSkipRetryError(apiErr), types.IsChannelError(apiErr), types.IsUpstreamFailure(apiErr), apiErr.StatusCode)
				}
				if c.Writer.Written() || w.Body.Len() != 0 {
					t.Errorf("nothing may be written when failing over: %q", w.Body.String())
				}
				if usage != nil && (usage.TotalTokens != 0 || usage.PromptTokens != 0 || usage.CompletionTokens != 0) {
					t.Errorf("nothing delivered, nothing billed: usage = %+v", usage)
				}
			})
		}
	}
}

// Frames already delivered: the in-band frame stays (no failover: a retry
// would append a second answer) and the same failure is now RETURNED, marked as
// already rendered, so the relay loop records a breaker failure and bills
// nothing instead of settling the truncated answer as a success.
func TestClaudeStreamHandler_IncompleteAfterFirstByte_StaysInBand(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "model-a")
	body := `data: {"type":"message_start","message":{"id":"msg_1","model":"model-a","usage":{"input_tokens":4,"output_tokens":0}}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("frames already delivered: the truncation must be returned (nil is recorded as a breaker success)")
	}
	if !helper.IsIncompleteStreamSurfaced(apiErr) {
		t.Errorf("returned error must carry the surfaced marker, got %v", apiErr)
	}
	if !types.IsSkipRetryError(apiErr) || !types.IsUpstreamFailure(apiErr) {
		t.Errorf("must be skip-retry (bytes are out) and an upstream failure (breaker): %v", apiErr)
	}
	if usage == nil || usage.TotalTokens != 0 || usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
		t.Errorf("a truncated stream is not billed, usage = %+v", usage)
	}
	if got := strings.Count(w.Body.String(), "event: error"); got != 1 {
		t.Errorf("exactly one in-band error frame expected, got %d:\n%s", got, w.Body.String())
	}
}

// A caller that hung up mid-stream is not the channel's failure: no frame and
// no error.
func TestClaudeStreamHandler_ClientGoneMidStream_NoErrorNoFrame(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "model-a")
	body := `data: {"type":"message_start","message":{"id":"msg_1","model":"model-a","usage":{"input_tokens":4,"output_tokens":0}}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	defer func() { _ = resp.Body.Close() }()

	_, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("a hung-up caller is not a channel failure, got %v", apiErr)
	}
	if strings.Contains(w.Body.String(), "event: error") {
		t.Errorf("no frame for a caller that hung up:\n%s", w.Body.String())
	}
}
