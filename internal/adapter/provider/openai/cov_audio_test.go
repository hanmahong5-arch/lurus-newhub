package openai

// Business-acceptance tests for the audio response handlers: TTS
// (OpenaiTTSHandler, non-stream + stream) and STT (OpenaiSTTHandler). These
// compute the billable usage for audio minutes / transcription tokens, so a
// wrong duration-to-token conversion directly skews audio billing.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func ttsResponse(status int, contentType, body string) *http.Response {
	h := make(http.Header)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h}
}

func TestOpenaiTTSHandler_NonStream_FallsBackToSizeEstimateOnUndecodableAudio(t *testing.T) {
	w := newRecorderCtx(t)
	// An unrecognized response_format maps to an unsupported extension in
	// common.GetAudioDuration, which returns a hard decode error -- this
	// exercises the business fallback: size-based token estimate rather
	// than a crash or a silently-free response.
	info := &relaycommon.RelayInfo{
		Request: &dto.AudioRequest{Model: "tts-1", ResponseFormat: "xyz"},
	}
	info.SetEstimatePromptTokens(4)
	resp := ttsResponse(200, "audio/mpeg", "this-is-not-a-real-mp3-file-but-has-some-bytes")

	usage := OpenaiTTSHandler(w.ctx, resp, info)
	if usage == nil {
		t.Fatal("usage should never be nil")
	}
	if usage.PromptTokens != 4 {
		t.Errorf("PromptTokens = %d, want 4 (from estimate)", usage.PromptTokens)
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (size-based fallback estimate)", usage.CompletionTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("TotalTokens = %d, want sum of prompt+completion", usage.TotalTokens)
	}
	if w.rec.Body.String() != "this-is-not-a-real-mp3-file-but-has-some-bytes" {
		t.Errorf("audio bytes should be written through to the client verbatim, got %q", w.rec.Body.String())
	}
}

func TestOpenaiTTSHandler_NonStream_PCMFormat_DeterministicDuration(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		Request: &dto.AudioRequest{Model: "tts-1", ResponseFormat: "pcm"},
	}
	info.SetEstimatePromptTokens(0)
	// PCM: 24000 Hz * 2 bytes/sample * 1 channel = 48000 bytes/sec.
	// 48000 bytes -> exactly 1.0 second -> ceil(1)/60*1000 = 16.666 -> round -> 17 tokens.
	pcmBytes := strings.Repeat("x", 48000)
	resp := ttsResponse(200, "audio/pcm", pcmBytes)

	usage := OpenaiTTSHandler(w.ctx, resp, info)
	if usage.CompletionTokens != 17 {
		t.Errorf("CompletionTokens = %d, want 17 (deterministic PCM duration formula for exactly 1s of audio)", usage.CompletionTokens)
	}
}

func TestOpenaiTTSHandler_Stream_ExtractsUsageFromSSEChunk(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		IsStream: true,
		Request:  &dto.AudioRequest{Model: "gpt-4o-audio-preview", ResponseFormat: "pcm"},
	}
	info.SetEstimatePromptTokens(1)
	body := `data: {"type":"speech.audio.delta","audio":"AAAA"}` + "\n\n" +
		`data: {"type":"speech.audio.done","usage":{"input_tokens":3,"output_tokens":9,"total_tokens":12}}` + "\n\n"
	resp := ttsResponse(200, "text/event-stream", body)

	usage := OpenaiTTSHandler(w.ctx, resp, info)
	if usage.TotalTokens != 12 {
		t.Errorf("TotalTokens = %d, want 12 (extracted from the usage-bearing SSE chunk)", usage.TotalTokens)
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 9 {
		t.Errorf("usage = %+v, want prompt=3 completion=9", usage)
	}
	if !strings.Contains(w.rec.Body.String(), "speech.audio.delta") {
		t.Errorf("stream frames should be forwarded to the client, got %q", w.rec.Body.String())
	}
}

func TestOpenaiTTSHandler_Stream_NoUsageChunk_KeepsEstimateBaseline(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		IsStream: true,
		Request:  &dto.AudioRequest{Model: "tts-1", ResponseFormat: "pcm"},
	}
	info.SetEstimatePromptTokens(6)
	body := `data: {"type":"speech.audio.delta","audio":"AAAA"}` + "\n\n"
	resp := ttsResponse(200, "text/event-stream", body)

	usage := OpenaiTTSHandler(w.ctx, resp, info)
	if usage.PromptTokens != 6 {
		t.Errorf("PromptTokens = %d, want 6 (estimate baseline retained when no usage chunk arrives)", usage.PromptTokens)
	}
	if usage.TotalTokens != 6 {
		t.Errorf("TotalTokens = %d, want 6 (unchanged baseline)", usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// OpenaiSTTHandler
// ---------------------------------------------------------------------------

func TestOpenaiSTTHandler_UsagePresent_FallsBackToInputOutputWhenPromptCompletionZero(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	body := `{"text":"hello world","usage":{"total_tokens":9,"input_tokens":3,"output_tokens":6}}`
	resp := ttsResponse(200, "application/json", body)

	apiErr, usage := OpenaiSTTHandler(w.ctx, resp, info, "json")
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 3 {
		t.Errorf("PromptTokens = %d, want 3 (fallback from input_tokens since prompt_tokens was 0)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6 (fallback from output_tokens)", usage.CompletionTokens)
	}
	if w.rec.Body.String() != body {
		t.Errorf("STT response body should be forwarded verbatim, got %q", w.rec.Body.String())
	}
}

func TestOpenaiSTTHandler_UsageMissing_UsesEstimateBaseline(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(11)
	body := `hello world` // some STT response formats (plain text) return no usage at all
	resp := ttsResponse(200, "text/plain", body)

	apiErr, usage := OpenaiSTTHandler(w.ctx, resp, info, "text")
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 11 {
		t.Errorf("PromptTokens = %d, want 11 (estimate baseline when upstream has no usage)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (STT has no completion notion when usage absent)", usage.CompletionTokens)
	}
}

func TestOpenaiSTTHandler_UsageWithZeroTotal_TreatedAsMissing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(2)
	// usage object present but total_tokens explicitly 0 -- must be treated
	// the same as "no usage" (falls back to estimate), not billed as free.
	body := `{"text":"x","usage":{"total_tokens":0}}`
	resp := ttsResponse(200, "application/json", body)

	apiErr, usage := OpenaiSTTHandler(w.ctx, resp, info, "json")
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 2 {
		t.Errorf("PromptTokens = %d, want 2 (fallback estimate for zero-total usage)", usage.PromptTokens)
	}
}
