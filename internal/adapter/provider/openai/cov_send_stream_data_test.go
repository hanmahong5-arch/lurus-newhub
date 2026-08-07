package openai

// Business-acceptance tests for sendStreamData: the function that decides,
// frame-by-frame, whether an SSE chunk is forwarded raw, re-serialized, or
// rewritten to convert vendor "reasoning_content" deltas into synthetic
// <think>...</think> tags for clients that only understand plain content.
// A bug here corrupts what the end user actually sees streamed back.

import (
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

func TestSendStreamData_EmptyDataIsNoOp(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	if err := sendStreamData(w.ctx, info, "", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.rec.Body.Len() != 0 {
		t.Errorf("body should stay empty for empty data, got %q", w.rec.Body.String())
	}
}

func TestSendStreamData_RawPassthrough_NoForceNoThink(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	// Deliberately non-JSON: the raw-passthrough branch must not attempt to
	// parse it at all.
	if err := sendStreamData(w.ctx, info, "not even json", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.rec.Body.String(), "not even json") {
		t.Errorf("body should contain raw passthrough data verbatim, got %q", w.rec.Body.String())
	}
}

func TestSendStreamData_ForceFormat_MalformedJSON_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	err := sendStreamData(w.ctx, info, "{not-json", true, false)
	if err == nil {
		t.Fatal("expected error for malformed JSON under forceFormat")
	}
}

func TestSendStreamData_ForceFormat_ValidJSON_ReEmitsObject(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	err := sendStreamData(w.ctx, info, `{"id":"c1","choices":[{"delta":{"content":"hi"}}]}`, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.rec.Body.String(), `"content":"hi"`) {
		t.Errorf("body should contain re-serialized content, got %q", w.rec.Body.String())
	}
}

func TestSendStreamData_ThinkToContent_EmptyChoicesPassthrough(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	err := sendStreamData(w.ctx, info, `{"id":"empty-choices","choices":[]}`, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.rec.Body.String(), `"id":"empty-choices"`) {
		t.Errorf("body should echo the chunk unchanged when Choices is empty, got %q", w.rec.Body.String())
	}
	if strings.Contains(w.rec.Body.String(), "<think>") {
		t.Errorf("no <think> tag should be emitted for an empty-choices chunk, got %q", w.rec.Body.String())
	}
}

// TestSendStreamData_ThinkToContent_FullLifecycle drives a realistic 3-chunk
// reasoning stream through sendStreamData and asserts the whole <think>...
// </think> conversion state machine end to end: first reasoning chunk opens
// the tag, subsequent reasoning chunks are rewritten as plain content, and
// the first real-content chunk closes the tag in a separate SSE frame before
// the content itself is emitted.
func TestSendStreamData_ThinkToContent_FullLifecycle(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	info.ThinkingContentInfo = relaycommon.ThinkingContentInfo{IsFirstThinkingContent: true}

	step1 := `{"id":"c1","choices":[{"delta":{"reasoning_content":"pondering..."}}]}`
	if err := sendStreamData(w.ctx, info, step1, false, true); err != nil {
		t.Fatalf("step1: unexpected error: %v", err)
	}
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		t.Error("step1: IsFirstThinkingContent should flip false after first thinking chunk")
	}
	if !info.ThinkingContentInfo.HasSentThinkingContent {
		t.Error("step1: HasSentThinkingContent should be true after first thinking chunk")
	}
	// encoding/json HTML-escapes '<' and '>' to < / > by default,
	// so the wire-level marker is this escaped form, not literal angle
	// brackets. These raw strings contain the literal backslash-u sequence.
	const openTag = "\\u003cthink\\u003e"
	const closeTag = "\\u003c/think\\u003e"

	body1 := w.rec.Body.String()
	if !strings.Contains(body1, openTag) || !strings.Contains(body1, "pondering...") {
		t.Errorf("step1: body should open <think> tag with content, got %q", body1)
	}

	step2 := `{"id":"c2","choices":[{"delta":{"reasoning_content":"more thoughts"}}]}`
	if err := sendStreamData(w.ctx, info, step2, false, true); err != nil {
		t.Fatalf("step2: unexpected error: %v", err)
	}
	body2 := w.rec.Body.String()
	if !strings.Contains(body2, "more thoughts") {
		t.Errorf("step2: subsequent reasoning content should still be forwarded as content, got %q", body2)
	}
	// no second <think> opening tag should appear
	if strings.Count(body2, openTag) != 1 {
		t.Errorf("step2: <think> should only be opened once total, got body %q", body2)
	}

	step3 := `{"id":"c3","choices":[{"delta":{"content":"final answer"}}]}`
	if err := sendStreamData(w.ctx, info, step3, false, true); err != nil {
		t.Fatalf("step3: unexpected error: %v", err)
	}
	if !info.ThinkingContentInfo.SendLastThinkingContent {
		t.Error("step3: SendLastThinkingContent should be true after the closing tag was sent")
	}
	body3 := w.rec.Body.String()
	if !strings.Contains(body3, closeTag) {
		t.Errorf("step3: body should contain the closing </think> tag, got %q", body3)
	}
	if !strings.Contains(body3, "final answer") {
		t.Errorf("step3: body should still contain the real content chunk, got %q", body3)
	}
	// closing tag must come from a distinct frame before the content, not
	// merged into the content chunk's own text.
	closeIdx := strings.Index(body3, closeTag)
	contentIdx := strings.Index(body3, "final answer")
	if closeIdx == -1 || contentIdx == -1 || closeIdx > contentIdx {
		t.Errorf("expected </think> frame to precede the final content frame; body=%q", body3)
	}
}

func TestSendStreamData_ThinkToContent_NonThinkingModelFlushesEmptyReasoningFields(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	// A plain content-only chunk with no reasoning at all and thinking never
	// having started: hasThinkingContent=false, hasContent=true -> the
	// "flush" else-branch must NOT fire (guarded by !hasContent), content
	// passes through untouched.
	err := sendStreamData(w.ctx, info, `{"id":"plain","choices":[{"delta":{"content":"just text"}}]}`, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.rec.Body.String(), "just text") {
		t.Errorf("plain content chunk should pass through, got %q", w.rec.Body.String())
	}
	if strings.Contains(w.rec.Body.String(), "<think>") {
		t.Errorf("no <think> tag should ever appear when no reasoning content was seen, got %q", w.rec.Body.String())
	}
}
