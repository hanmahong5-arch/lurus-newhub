package openai

// stream_incomplete_test.go — an upstream that stops mid-answer must reach the
// caller as an error, on every wire.
//
// Before 2026-09-02 a stream with no [DONE] and no finish_reason (idle timeout,
// reset, EOF) was not billed — correct — but every client wire was told the
// answer was complete: the OpenAI wire got a zero-usage frame + [DONE], the
// Claude wire content_block_stop + message_delta{stop_reason:end_turn} +
// message_stop, the Gemini wire a bare EOF. None of the official SDKs raises on
// any of those; each raises on exactly one in-band shape (helper.StreamError).
//
// A stream that delivered its finish_reason but not [DONE] is complete for the
// caller and keeps the normal end. A caller that hung up gets nothing.
//
// Since 2026-09-30 the truncated-with-content case also RETURNS the failure
// (helper.SurfaceIncompleteStream) after writing the frame: nil meant the relay
// loop recorded a breaker success for a channel that had just cut an answer in
// half. Complete streams and hung-up callers still return nil.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const (
	incompleteChunk1 = `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"},"finish_reason":null}]}`
	incompleteChunk2 = `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`
	finishChunk      = `{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
)

func truncatedStream() string {
	return "data: " + incompleteChunk1 + "\n\ndata: " + incompleteChunk2 + "\n\n"
}

func runIncompleteStream(t *testing.T, format types.RelayFormat, body string, clientGone bool) (*relaycommon.RelayInfo, string, int, *types.NewAPIError) {
	t.Helper()
	prev := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	defer func() { constant.StreamingTimeout = prev }()

	w := newRecorderCtx(t)
	if clientGone {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w.ctx.Request = w.ctx.Request.WithContext(ctx)
	}
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
	if apiErr != nil && !helper.IsIncompleteStreamSurfaced(apiErr) {
		t.Fatalf("the only error a stream that delivered bytes may return is the surfaced incomplete-stream one, got %v", apiErr.Error())
	}
	return info, w.rec.Body.String(), usage.TotalTokens, apiErr
}

// assertSurfacedIncomplete pins the returned error's contract: marked as
// already rendered, still skip-retry (the frames are out, a failover would
// append a second answer), an upstream failure for the breaker, and never an
// auto-ban code.
func assertSurfacedIncomplete(t *testing.T, apiErr *types.NewAPIError) {
	t.Helper()
	if apiErr == nil {
		t.Fatal("truncated stream with content delivered must return the incomplete-stream error (nil is recorded as a breaker success)")
	}
	if !helper.IsIncompleteStreamSurfaced(apiErr) {
		t.Errorf("error must carry the surfaced marker so the relay's error renderer does not write a second frame: %v", apiErr)
	}
	if !types.IsSkipRetryError(apiErr) {
		t.Error("must stay skip-retry: bytes are already on the caller's stream")
	}
	if !types.IsUpstreamFailure(apiErr) {
		t.Error("must count as an upstream failure for the channel breaker")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete {
		t.Errorf("error code = %q", apiErr.GetErrorCode())
	}
}

func TestOaiStreamHandler_IncompleteStream_ErrorFrameOnEveryWire(t *testing.T) {
	t.Run("openai wire", func(t *testing.T) {
		info, body, total, apiErr := runIncompleteStream(t, types.RelayFormatOpenAI, truncatedStream(), false)
		assertSurfacedIncomplete(t, apiErr)
		if total != 0 {
			t.Errorf("billing rule unchanged: incomplete stream must not be billed, TotalTokens = %d", total)
		}
		if info.StreamEndReason != relaycommon.StreamEndUpstreamClosed {
			t.Errorf("StreamEndReason = %q", info.StreamEndReason)
		}
		if !strings.Contains(body, `"content":"lo"`) {
			t.Errorf("the held-back last content chunk must still be delivered:\n%s", body)
		}
		if !strings.Contains(body, `data: {"error":{"message":"upstream stream ended before completion"`) ||
			!strings.Contains(body, `"code":"upstream_stream_incomplete"`) {
			t.Errorf("openai-wire caller must get an error frame:\n%s", body)
		}
		if strings.Contains(body, `"usage":{"prompt_tokens":0`) || strings.Contains(body, "x_lurus") {
			t.Errorf("no invented zero-usage frame on an incomplete stream:\n%s", body)
		}
		if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
			t.Errorf("openai wire still terminates with [DONE] after the error:\n%s", body)
		}
	})

	t.Run("claude wire", func(t *testing.T) {
		_, body, total, apiErr := runIncompleteStream(t, types.RelayFormatClaude, truncatedStream(), false)
		assertSurfacedIncomplete(t, apiErr)
		if total != 0 {
			t.Errorf("TotalTokens = %d, want 0", total)
		}
		if !strings.Contains(body, `"text":"lo"`) {
			t.Errorf("the held-back last content chunk must still be delivered:\n%s", body)
		}
		if !strings.Contains(body, "event: error\ndata: {\"type\":\"error\",\"error\":{") {
			t.Errorf("claude-wire caller must get an `event: error` frame (the only shape the SDK raises on):\n%s", body)
		}
		for _, fabricated := range []string{`"stop_reason":"end_turn"`, "message_delta", "message_stop", "[DONE]"} {
			if strings.Contains(body, fabricated) {
				t.Errorf("incomplete stream presented as a normal end (%s):\n%s", fabricated, body)
			}
		}
	})

	t.Run("gemini wire", func(t *testing.T) {
		_, body, total, apiErr := runIncompleteStream(t, types.RelayFormatGemini, truncatedStream(), false)
		assertSurfacedIncomplete(t, apiErr)
		if total != 0 {
			t.Errorf("TotalTokens = %d, want 0", total)
		}
		if !strings.Contains(body, `"text":"lo"`) {
			t.Errorf("the held-back last content chunk must still be delivered:\n%s", body)
		}
		if !strings.Contains(body, `data: {"error":{"code":502,"message":"upstream stream ended before completion","status":"UNAVAILABLE"}}`) {
			t.Errorf("gemini-wire caller must get a Gemini error envelope:\n%s", body)
		}
		if strings.Contains(body, "[DONE]") || strings.Contains(body, `"finishReason":"STOP"`) {
			t.Errorf("no [DONE] and no invented STOP on the gemini wire:\n%s", body)
		}
	})
}

// finish_reason delivered, [DONE] missing: complete for the caller. Some
// OpenAI-compatible upstreams end this way; the caller keeps the normal end
// (and the stream is still not billed, the pre-existing rule).
func TestOaiStreamHandler_FinishWithoutDone_KeepsNormalEnd(t *testing.T) {
	body := truncatedStream() + "data: " + finishChunk + "\n\n"
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			_, out, _, apiErr := runIncompleteStream(t, format, body, false)
			if apiErr != nil {
				t.Errorf("%s: a complete stream returns no error, got %v", format, apiErr)
			}
			if strings.Contains(out, "upstream_stream_incomplete") || strings.Contains(out, "event: error") || strings.Contains(out, `{"error":{"code"`) {
				t.Errorf("%s: a stream that delivered finish_reason is complete; no error frame:\n%s", format, out)
			}
		})
	}
	_, out, _, _ := runIncompleteStream(t, types.RelayFormatClaude, body, false)
	if !strings.Contains(out, `"stop_reason":"end_turn"`) || !strings.Contains(out, "message_stop") {
		t.Errorf("claude wire normal end lost:\n%s", out)
	}
}

// The caller hung up: there is nobody to tell, and writing would only feed a
// dead socket.
func TestOaiStreamHandler_IncompleteStream_ClientGoneWritesNothing(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		info, out, _, apiErr := runIncompleteStream(t, format, truncatedStream(), true)
		if apiErr != nil {
			t.Errorf("%s: a hung-up caller is not a channel failure, the handler must return nil, got %v", format, apiErr)
		}
		if info.StreamEndReason != relaycommon.StreamEndClientGone {
			t.Errorf("%s: StreamEndReason = %q, want %q", format, info.StreamEndReason, relaycommon.StreamEndClientGone)
		}
		if strings.Contains(out, "error") {
			t.Errorf("%s: no error frame for a caller that hung up:\n%s", format, out)
		}
	}
}

func TestStreamSawFinish(t *testing.T) {
	if streamSawFinish([]string{incompleteChunk1, incompleteChunk2}) {
		t.Error("no finish_reason anywhere: want false")
	}
	if !streamSawFinish([]string{incompleteChunk1, finishChunk}) {
		t.Error("finish chunk present: want true")
	}
	if !streamSawFinish([]string{incompleteChunk1, finishChunk, `{"id":"c1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`}) {
		t.Error("finish before a trailing usage chunk: want true")
	}
	if streamSawFinish([]string{"not json", `{"choices":[{"index":0,"delta":{},"finish_reason":"null"}]}`}) {
		t.Error(`the literal string "null" is not a finish reason`)
	}
	if streamSawFinish(nil) {
		t.Error("empty stream: want false")
	}
}
