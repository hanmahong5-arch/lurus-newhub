package gemini

// stream_incomplete_test.go — a native Gemini upstream that stops without a
// finishReason has abandoned the answer. Before 2026-09-02 the OpenAI-wire
// caller got a usage frame + [DONE], the Claude-wire caller a message_stop and
// the Gemini-wire passthrough caller a bare EOF: every SDK read a complete
// answer. Since 2026-09-30 the truncation is also RETURNED (after the in-band
// frame is written, helper.SurfaceIncompleteStream): the relay loop then records
// a breaker failure and bills nothing, where the old nil return settled the
// partial output from the last usageMetadata as a success.

import (
	"context"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// assertSurfacedIncomplete pins the contract of the error a truncated stream
// returns once its in-band frame is out: marked as rendered (so relay.go's
// error renderer writes no second frame), skip-retry (bytes are out) and an
// upstream failure (breaker).
func assertSurfacedIncomplete(t *testing.T, apiErr *types.NewAPIError) {
	t.Helper()
	if apiErr == nil {
		t.Fatal("truncated stream with content delivered must return the incomplete-stream error (nil is recorded as a breaker success)")
	}
	if !helper.IsIncompleteStreamSurfaced(apiErr) {
		t.Errorf("error must carry the surfaced marker: %v", apiErr)
	}
	if !types.IsSkipRetryError(apiErr) || !types.IsUpstreamFailure(apiErr) {
		t.Errorf("must be skip-retry and an upstream failure: %v", apiErr)
	}
}

const (
	geminiChunkA        = `{"candidates":[{"content":{"parts":[{"text":"Hel"}]},"index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`
	geminiChunkB        = `{"candidates":[{"content":{"parts":[{"text":"lo"}]},"index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"totalTokenCount":4}}`
	geminiFinishChunk   = `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"totalTokenCount":4}}`
	geminiMaxTokenChunk = `{"candidates":[{"content":{"parts":[{"text":"!"}]},"finishReason":"MAX_TOKENS","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`
)

func TestGeminiChatStreamHandler_Incomplete_ErrorFrameOnEveryWire(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
		t.Run(string(format), func(t *testing.T) {
			c, w := newHandlerContext()
			info := &relaycommon.RelayInfo{
				ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gemini-x"},
				RelayFormat:        format,
				ShouldIncludeUsage: true,
				ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{},
			}
			resp := respFromBody(200, sseBody(geminiChunkA, geminiChunkB))
			defer func() { _ = resp.Body.Close() }()
			usage, apiErr := GeminiChatStreamHandler(c, info, resp)
			assertSurfacedIncomplete(t, apiErr)
			if usage.CompletionTokens != 0 || usage.TotalTokens != 0 {
				t.Errorf("a truncated stream is not billed: usage = %+v", usage)
			}
			if got := strings.Count(w.Body.String(), "upstream stream ended before completion"); got != 1 {
				t.Errorf("exactly one in-band error frame expected, got %d", got)
			}
			out := w.Body.String()
			if !strings.Contains(out, `"lo"`) {
				t.Errorf("content delivered before the stop must reach the caller:\n%s", out)
			}
			if !strings.Contains(out, "upstream stream ended before completion") {
				t.Errorf("%s: caller must get the incomplete-stream error:\n%s", format, out)
			}
			switch format {
			case types.RelayFormatOpenAI:
				if !strings.Contains(out, `data: {"error":{"message":`) || !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
					t.Errorf("openai wire: error frame then [DONE]:\n%s", out)
				}
				if strings.Contains(out, `"usage":{"prompt_tokens"`) {
					t.Errorf("no usage frame dressed up as a normal end:\n%s", out)
				}
			case types.RelayFormatClaude:
				if !strings.Contains(out, "event: error\ndata: {\"type\":\"error\"") {
					t.Errorf("claude wire: `event: error` frame:\n%s", out)
				}
				if strings.Contains(out, "message_stop") || strings.Contains(out, `"stop_reason":"end_turn"`) {
					t.Errorf("no invented normal end:\n%s", out)
				}
			}
		})
	}
}

func TestGeminiChatStreamHandler_FinishReason_KeepsNormalEnd(t *testing.T) {
	for name, finish := range map[string]string{"STOP": geminiFinishChunk, "MAX_TOKENS": geminiMaxTokenChunk} {
		t.Run(name, func(t *testing.T) {
			c, w := newHandlerContext()
			info := &relaycommon.RelayInfo{
				ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gemini-x"},
				RelayFormat:        types.RelayFormatOpenAI,
				ShouldIncludeUsage: true,
			}
			resp := respFromBody(200, sseBody(geminiChunkA, finish))
			defer func() { _ = resp.Body.Close() }()
			if _, apiErr := GeminiChatStreamHandler(c, info, resp); apiErr != nil {
				t.Fatalf("unexpected error: %v", apiErr)
			}
			out := w.Body.String()
			if strings.Contains(out, "upstream_stream_incomplete") {
				t.Errorf("%s: any finishReason means the upstream finished; no error frame:\n%s", name, out)
			}
			if !strings.Contains(out, `"usage":{"prompt_tokens":2`) || !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
				t.Errorf("%s: normal end lost:\n%s", name, out)
			}
		})
	}
}

func TestGeminiTextGenerationStreamHandler_Incomplete_GeminiErrorEnvelope(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatGemini}
	resp := respFromBody(200, sseBody(geminiChunkA, geminiChunkB))
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiTextGenerationStreamHandler(c, info, resp)
	assertSurfacedIncomplete(t, apiErr)
	if usage.CompletionTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("a truncated stream is not billed: usage = %+v", usage)
	}
	out := w.Body.String()
	if !strings.HasSuffix(strings.TrimSpace(out), `data: {"error":{"code":502,"message":"upstream stream ended before completion","status":"UNAVAILABLE"}}`) {
		t.Errorf("gemini-wire caller must get the Gemini error envelope as the last frame:\n%s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Errorf("no [DONE] on the gemini wire:\n%s", out)
	}

	c, w = newHandlerContext()
	info = &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatGemini}
	resp = respFromBody(200, sseBody(geminiChunkA, geminiFinishChunk))
	defer func() { _ = resp.Body.Close() }()
	if _, apiErr := GeminiTextGenerationStreamHandler(c, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("finished stream must not carry an error frame:\n%s", w.Body.String())
	}
}

func TestGeminiStreamHandlers_Incomplete_ClientGoneWritesNoError(t *testing.T) {
	c, w := newHandlerContext()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatGemini}
	resp := respFromBody(200, sseBody(geminiChunkA, geminiChunkB))
	defer func() { _ = resp.Body.Close() }()
	if _, apiErr := GeminiTextGenerationStreamHandler(c, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("no error frame for a caller that hung up:\n%s", w.Body.String())
	}
}
