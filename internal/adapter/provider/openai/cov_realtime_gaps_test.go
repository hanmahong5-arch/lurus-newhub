package openai

// Business-acceptance tests for the two Realtime-mode helpers that don't
// need a live websocket to exercise their own logic: preConsumeUsage's
// running-total accumulation (the numbers billed for a realtime session) and
// streamTTSResponse's raw byte relay to the client.
//
// FINDING: internal/adapter/provider/openai/relay-openai.go:328 -
// streamTTSResponse is unreferenced anywhere in the module (grep across
// internal/ turns up only its own definition) - it is dead code, not wired
// into OpenaiTTSHandler or any other caller. Not fixed here per the "tests
// only" constraint; flagged for owner triage (either wire it in for a
// streaming-TTS path or delete it).

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestPreConsumeUsage_NilUsage_ReturnsError(t *testing.T) {
	total := &dto.RealtimeUsage{}
	err := preConsumeUsage(newRecorderCtx(t).ctx, &relaycommon.RelayInfo{}, nil, total)
	if err == nil {
		t.Fatal("expected an error for a nil usage pointer")
	}
	if !strings.Contains(err.Error(), "invalid usage pointer") {
		t.Errorf("error = %q, want mention of the invalid usage pointer", err.Error())
	}
}

func TestPreConsumeUsage_NilTotalUsage_ReturnsError(t *testing.T) {
	err := preConsumeUsage(newRecorderCtx(t).ctx, &relaycommon.RelayInfo{}, &dto.RealtimeUsage{}, nil)
	if err == nil {
		t.Fatal("expected an error for a nil totalUsage pointer")
	}
}

// UsePrice=true takes the fixed-price billing path (app.PreWssConsumeQuota
// short-circuits to nil without touching the DB), but the running totals
// must still accumulate every field so the eventual usage report is
// accurate regardless of billing mode.
func TestPreConsumeUsage_AccumulatesAllTokenFields(t *testing.T) {
	info := &relaycommon.RelayInfo{UsePrice: true}
	total := &dto.RealtimeUsage{TotalTokens: 10, InputTokens: 4, OutputTokens: 6}
	chunk := &dto.RealtimeUsage{
		TotalTokens:  5,
		InputTokens:  2,
		OutputTokens: 3,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens: 1,
			TextTokens:   2,
			AudioTokens:  3,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  4,
			AudioTokens: 5,
		},
	}
	if err := preConsumeUsage(newRecorderCtx(t).ctx, info, chunk, total); err != nil {
		t.Fatalf("unexpected error under UsePrice fixed-billing short-circuit: %v", err)
	}
	if total.TotalTokens != 15 || total.InputTokens != 6 || total.OutputTokens != 9 {
		t.Errorf("running totals = %+v, want TotalTokens=15 InputTokens=6 OutputTokens=9", total)
	}
	if total.InputTokenDetails.CachedTokens != 1 || total.InputTokenDetails.TextTokens != 2 || total.InputTokenDetails.AudioTokens != 3 {
		t.Errorf("InputTokenDetails = %+v, want cached=1 text=2 audio=3", total.InputTokenDetails)
	}
	if total.OutputTokenDetails.TextTokens != 4 || total.OutputTokenDetails.AudioTokens != 5 {
		t.Errorf("OutputTokenDetails = %+v, want text=4 audio=5", total.OutputTokenDetails)
	}
}

// A second accumulation call must add onto, not replace, the running totals -
// this is what makes it safe to call once per realtime server event.
func TestPreConsumeUsage_SecondCallAddsOntoRunningTotal(t *testing.T) {
	info := &relaycommon.RelayInfo{UsePrice: true}
	total := &dto.RealtimeUsage{}
	first := &dto.RealtimeUsage{TotalTokens: 3}
	second := &dto.RealtimeUsage{TotalTokens: 4}
	if err := preConsumeUsage(newRecorderCtx(t).ctx, info, first, total); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := preConsumeUsage(newRecorderCtx(t).ctx, info, second, total); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total.TotalTokens != 7 {
		t.Errorf("TotalTokens after two calls = %d, want 7 (3+4, proving accumulation not overwrite)", total.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// streamTTSResponse
// ---------------------------------------------------------------------------

func TestStreamTTSResponse_CopiesBodyBytesToClient(t *testing.T) {
	w := newRecorderCtx(t)
	body := io.NopCloser(strings.NewReader("RIFF-fake-audio-bytes"))
	resp := &http.Response{StatusCode: 200, Body: body}
	streamTTSResponse(w.ctx, resp)
	if got := w.rec.Body.String(); got != "RIFF-fake-audio-bytes" {
		t.Errorf("relayed body = %q, want the upstream audio bytes copied verbatim", got)
	}
}

func TestStreamTTSResponse_EmptyBody_WritesNothing(t *testing.T) {
	w := newRecorderCtx(t)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}
	streamTTSResponse(w.ctx, resp)
	if got := w.rec.Body.String(); got != "" {
		t.Errorf("relayed body = %q, want empty for an empty upstream body", got)
	}
}

// NOTE: the `!ok` fallback branch inside streamTTSResponse (io.Copy path for
// a writer that doesn't implement http.Flusher) is unreachable through
// gin.Context: gin.ResponseWriter's interface itself embeds http.Flusher, so
// any value assignable to c.Writer already satisfies the type assertion.
// Not exercised here rather than constructing a contrived double that
// wouldn't reflect a real call path.
