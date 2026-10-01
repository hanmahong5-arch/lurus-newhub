package openai

// stream_failover_test.go — an upstream stream that ends incomplete BEFORE a
// single byte reached the caller must fail over, not answer in-band.
//
// The in-band error frame (stream_incomplete_test.go) is right once frames
// have left the process: the status line is spent and a retry would append a
// second response. With nothing written it was wrong twice over: the handler
// returned (usage, nil), so the relay loop counted the attempt as a breaker
// SUCCESS and never tried another channel, and the caller got an error frame on
// a 200 stream that a retry on a healthy channel would have answered.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// roleOnlyChunk is the kind of first chunk an upstream sends before any text:
// the handler holds the newest chunk back, so it has not been flushed when the
// stream dies.
const roleOnlyChunk = `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`

func TestOaiStreamHandler_IncompleteBeforeFirstByte_FailsOver(t *testing.T) {
	bodies := map[string]string{
		"empty body":              "",
		"keepalive comment only":  ": keepalive\n\n",
		"single held role chunk":  "data: " + roleOnlyChunk + "\n\n",
		"held chunk plus comment": ": keepalive\n\ndata: " + roleOnlyChunk + "\n\n",
	}
	for name, body := range bodies {
		for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
			t.Run(name+"/"+string(format), func(t *testing.T) {
				prev := constant.StreamingTimeout
				constant.StreamingTimeout = 60
				defer func() { constant.StreamingTimeout = prev }()

				w := newRecorderCtx(t)
				info := &relaycommon.RelayInfo{
					ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
					RelayFormat:        format,
					IsStream:           true,
					ShouldIncludeUsage: true,
					ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{},
				}
				info.SetEstimatePromptTokens(5)
				resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}

				usage, apiErr := OaiStreamHandler(w.ctx, info, resp)

				if apiErr == nil {
					t.Fatalf("incomplete stream with nothing delivered must return an error (relay.go records breaker success on nil and never fails over); body=%q", w.rec.Body.String())
				}
				assertFailoverError(t, apiErr)
				if w.ctx.Writer.Written() || w.rec.Body.Len() != 0 {
					t.Errorf("nothing may be written when failing over (a retry would append a second response): %q", w.rec.Body.String())
				}
				if usage != nil && (usage.TotalTokens != 0 || usage.PromptTokens != 0 || usage.CompletionTokens != 0) {
					t.Errorf("nothing delivered, nothing billed: usage = %+v", usage)
				}
			})
		}
	}
}

// assertFailoverError pins the properties relay.go's shouldRetry and breaker
// bookkeeping key on: retryable (no skip-retry flag, not a channel-disabling
// code, a 5xx that is not one of the never-retried timeout statuses) and an
// upstream fault (so the breaker records a failure).
func assertFailoverError(t *testing.T, apiErr *types.NewAPIError) {
	t.Helper()
	if types.IsSkipRetryError(apiErr) {
		t.Errorf("error must be retryable, got skip-retry")
	}
	if types.IsChannelError(apiErr) {
		t.Errorf("must not be a channel: error (those auto-disable the channel)")
	}
	if !types.IsUpstreamFailure(apiErr) {
		t.Errorf("must count as an upstream failure for the breaker")
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502 (504/524 are never retried by shouldRetry)", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete {
		t.Errorf("error code = %q", apiErr.GetErrorCode())
	}
}

// An idle timeout before the first byte is just as retryable: IncompleteStreamError
// would be 504 + skip-retry here, which shouldRetry refuses.
func TestOaiStreamHandler_IncompleteBeforeFirstByte_TimeoutStillRetryable(t *testing.T) {
	prev := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	defer func() { constant.StreamingTimeout = prev }()

	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
		RelayFormat: types.RelayFormatOpenAI,
		IsStream:    true,
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := &http.Response{StatusCode: 200, Body: pr, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}

	_, apiErr := OaiStreamHandler(w.ctx, info, resp)
	if info.StreamEndReason != relaycommon.StreamEndTimeout {
		t.Fatalf("precondition: StreamEndReason = %q, want timeout", info.StreamEndReason)
	}
	if apiErr == nil {
		t.Fatalf("idle timeout with nothing delivered must fail over")
	}
	assertFailoverError(t, apiErr)
	if !strings.Contains(apiErr.Error(), "idle timeout") {
		t.Errorf("message should still name the idle timeout: %q", apiErr.Error())
	}
	if w.ctx.Writer.Written() {
		t.Errorf("nothing may be written when failing over")
	}
}

// Once content has gone out the in-band frame stays and so does the no-failover
// rule. The handler now also returns the failure (marked as already rendered,
// so relay.go's error renderer writes no second frame) so the breaker records
// it instead of a success.
func TestOaiStreamHandler_IncompleteAfterFirstByte_StaysInBand(t *testing.T) {
	_, body, _, apiErr := runIncompleteStream(t, types.RelayFormatOpenAI, truncatedStream(), false)
	assertSurfacedIncomplete(t, apiErr)
	if strings.Count(body, `"code":"upstream_stream_incomplete"`) != 1 {
		t.Errorf("exactly one in-band error frame expected:\n%s", body)
	}
	if !strings.Contains(body, `"code":"upstream_stream_incomplete"`) {
		t.Errorf("mid-stream truncation must keep its in-band error frame:\n%s", body)
	}
}
