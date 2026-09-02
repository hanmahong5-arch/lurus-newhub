package openai

// stream_terminal_crosswire_test.go — how an OpenAI-wire stream ENDS for a
// client speaking another wire.
//
// The relay asks every channel in streamSupportedChannels for
// stream_options.include_usage, so the upstream's last two frames are the
// finish_reason chunk and then a usage-only chunk (choices: []). Until
// 2026-09-02 both cross-wire re-framings mishandled that pair:
//
//   - Claude wire: the finish chunk emitted content_block_stop + message_stop
//     with NO message_delta (usage was not known yet) and marked the
//     conversion Done, so the usage chunk was then discarded. The Claude-wire
//     caller got no stop_reason, no usage and no cache figures — the fields
//     the official SDKs read the final message from.
//   - Gemini wire: the usage-only chunk has no content and no finish_reason,
//     so the converter dropped it as a "leading empty frame". The Gemini-wire
//     caller saw one STOP frame carrying the pre-request estimate and never
//     the real counts.
//
// These tests drive the full OaiStreamHandler over that exact frame sequence
// and pin the terminal shape each wire defines.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const crossWireUsageChunk = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50}}}`

func crossWireSSE(frames ...string) *http.Response {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: " + f + "\n\n")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(b.String()))}
}

// The production sequence: content, finish_reason, usage-only, [DONE].
func crossWireStandardStream() *http.Response {
	return crossWireSSE(
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		crossWireUsageChunk,
		"[DONE]",
	)
}

func crossWireInfo(format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        format,
		IsStream:           true,
		ShouldIncludeUsage: true,
		ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{},
		ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "m"},
	}
}

func withStreamingTimeout(t *testing.T) {
	t.Helper()
	prev := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	t.Cleanup(func() { constant.StreamingTimeout = prev })
}

// claudeEvents parses the SSE the Claude-wire client received into typed events.
func claudeEvents(t *testing.T, body string) []dto.ClaudeResponse {
	t.Helper()
	var out []dto.ClaudeResponse
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev dto.ClaudeResponse
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("unparseable Claude event %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func TestOaiStreamHandler_ClaudeWire_TrailingUsageChunkClosesTheMessage(t *testing.T) {
	withStreamingTimeout(t)
	w := newRecorderCtx(t)
	info := crossWireInfo(types.RelayFormatClaude)

	usage, apiErr := OaiStreamHandler(w.ctx, info, crossWireStandardStream())
	if apiErr != nil {
		t.Fatalf("handler: %v", apiErr.Error())
	}
	if usage.PromptTokensDetails.CachedTokens != 50 {
		t.Fatalf("settlement usage CachedTokens = %d, want 50 (fixture sanity)", usage.PromptTokensDetails.CachedTokens)
	}

	events := claudeEvents(t, w.rec.Body.String())
	var deltas, stops []dto.ClaudeResponse
	for _, ev := range events {
		switch ev.Type {
		case "message_delta":
			deltas = append(deltas, ev)
		case "message_stop":
			stops = append(stops, ev)
		}
	}
	if len(deltas) != 1 || len(stops) != 1 {
		t.Fatalf("message_delta×%d message_stop×%d, want exactly one each; events: %v", len(deltas), len(stops), claudeEventTypes(events))
	}
	if events[len(events)-1].Type != "message_stop" || events[len(events)-2].Type != "message_delta" {
		t.Errorf("stream must end message_delta, message_stop; got %v", claudeEventTypes(events))
	}
	d := deltas[0]
	if d.Delta == nil || d.Delta.StopReason == nil || *d.Delta.StopReason != "end_turn" {
		t.Errorf("message_delta.delta.stop_reason = %+v, want end_turn (the SDKs read stop_reason from here)", d.Delta)
	}
	if d.Usage == nil || d.Usage.OutputTokens != 30 || d.Usage.CacheReadInputTokens != 50 {
		t.Errorf("message_delta.usage = %+v, want output_tokens 30 and cache_read_input_tokens 50 from the trailing usage chunk", d.Usage)
	}
}

// No usage chunk at all (upstream ignored include_usage): the finish chunk is
// the last frame and must still close the message with the settled usage.
func TestOaiStreamHandler_ClaudeWire_NoUsageChunkStillCloses(t *testing.T) {
	withStreamingTimeout(t)
	w := newRecorderCtx(t)
	info := crossWireInfo(types.RelayFormatClaude)
	resp := crossWireSSE(
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
		"[DONE]",
	)
	if _, apiErr := OaiStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("handler: %v", apiErr.Error())
	}
	events := claudeEvents(t, w.rec.Body.String())
	seq := claudeEventTypes(events)
	if len(seq) < 2 || seq[len(seq)-2] != "message_delta" || seq[len(seq)-1] != "message_stop" {
		t.Fatalf("stream must end message_delta, message_stop; got %v", seq)
	}
	if sr := events[len(events)-2].Delta.StopReason; sr == nil || *sr != "max_tokens" {
		t.Errorf("stop_reason = %v, want max_tokens (finish_reason length)", sr)
	}
}

// A vendor that never sends finish_reason (content, then the usage chunk):
// the open text block must be closed and the message terminated anyway.
func TestOaiStreamHandler_ClaudeWire_NoFinishReasonIsClosedAtEnd(t *testing.T) {
	withStreamingTimeout(t)
	w := newRecorderCtx(t)
	info := crossWireInfo(types.RelayFormatClaude)
	resp := crossWireSSE(
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		crossWireUsageChunk,
		"[DONE]",
	)
	if _, apiErr := OaiStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("handler: %v", apiErr.Error())
	}
	got := claudeEventTypes(claudeEvents(t, w.rec.Body.String()))
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func claudeEventTypes(events []dto.ClaudeResponse) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

// geminiFrames parses the SSE the Gemini-wire client received.
func geminiFrames(t *testing.T, body string) []dto.GeminiChatResponse {
	t.Helper()
	var out []dto.GeminiChatResponse
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var fr dto.GeminiChatResponse
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr); err != nil {
			t.Fatalf("unparseable Gemini frame %q: %v", line, err)
		}
		out = append(out, fr)
	}
	return out
}

func TestOaiStreamHandler_GeminiWire_SingleStopFrameCarriesBilledUsage(t *testing.T) {
	withStreamingTimeout(t)
	w := newRecorderCtx(t)
	info := crossWireInfo(types.RelayFormatGemini)

	if _, apiErr := OaiStreamHandler(w.ctx, info, crossWireStandardStream()); apiErr != nil {
		t.Fatalf("handler: %v", apiErr.Error())
	}
	frames := geminiFrames(t, w.rec.Body.String())
	var stops []dto.GeminiChatResponse
	stopAt := -1
	for i, fr := range frames {
		for _, cand := range fr.Candidates {
			if cand.FinishReason != nil {
				stops = append(stops, fr)
				stopAt = i
			}
		}
	}
	if len(stops) != 1 {
		t.Fatalf("frames with a finishReason = %d, want exactly 1 (Gemini's wire ends on one terminal frame); body:\n%s", len(stops), w.rec.Body.String())
	}
	if fr := *stops[0].Candidates[0].FinishReason; fr != "STOP" {
		t.Errorf("finishReason = %s, want STOP", fr)
	}
	if stopAt != len(frames)-1 {
		t.Errorf("the STOP frame is frame %d of %d, want the last one", stopAt+1, len(frames))
	}
	um := stops[0].UsageMetadata
	if um.PromptTokenCount != 120 || um.CandidatesTokenCount != 30 || um.TotalTokenCount != 150 || um.CachedContentTokenCount != 50 {
		t.Errorf("terminal usageMetadata = %+v, want prompt 120 / candidates 30 / total 150 / cached 50 from the trailing usage chunk", um)
	}
}

// Vendor remaps live on the settled usage, not on the raw frame: a DeepSeek
// usage chunk (prompt_cache_hit_tokens, no cached_tokens) must still surface
// as cachedContentTokenCount on the terminal frame.
func TestOaiStreamHandler_GeminiWire_TerminalFrameShowsRemappedCache(t *testing.T) {
	withStreamingTimeout(t)
	w := newRecorderCtx(t)
	info := crossWireInfo(types.RelayFormatGemini)
	info.ChannelMeta.ChannelType = constant.ChannelTypeDeepSeek
	resp := crossWireSSE(
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":70}}`,
		"[DONE]",
	)
	if _, apiErr := OaiStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("handler: %v", apiErr.Error())
	}
	frames := geminiFrames(t, w.rec.Body.String())
	if got := frames[len(frames)-1].UsageMetadata.CachedContentTokenCount; got != 50 {
		t.Errorf("terminal cachedContentTokenCount = %d, want 50 (DeepSeek remap applied to the settled usage)", got)
	}
}
