package claude

// r5a_stream_web_search_test.go — G2 (lane L-A, 2026-08-27 live-defect
// round). Drives the full streaming chain (ClaudeStreamHandler ->
// helper.StreamScannerHandler -> HandleStreamResponseData ->
// FormatClaudeResponseInfo -> HandleStreamFinalResponse), NOT
// HandleStreamFinalResponse directly — per this round's hard requirement,
// a test that calls the guarded function directly cannot prove the wiring
// from "upstream SSE bytes" to "context key set" ever fires in production.
// Before this fix, ClaudeStreamHandler never set claude_web_search_requests
// at all: a streamed /v1/messages Claude native web-search call went
// unbilled by internal/app.PostClaudeConsumeQuota (:349) and
// internal/app/relay's postConsumeQuota (:280), while the identical
// non-streaming call (relay-claude.go's HandleClaudeResponseData) was
// already fixed and charged. See internal/app/r1_claude_web_search_fee_test.go
// for the "does the fee formula debit and log correctly once the key is
// set" coverage this file's Set call now feeds on the streaming path too.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func init() {
	// ClaudeStreamHandler delegates to helper.StreamScannerHandler, which
	// builds a time.NewTicker(StreamingTimeout*time.Second); a zero/unset
	// value panics ("non-positive interval") outside full server bootstrap.
	// Same guard as internal/adapter/provider/openai/cov_stream_handler_test.go.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

// r5aClaudeSSEResp builds a minimal but realistic Claude /v1/messages SSE
// stream: message_start (no server_tool_use) + content_block events +
// message_delta (server_tool_use.web_search_requests=2) + message_stop.
func r5aClaudeSSEResp(messageDeltaExtra string) *http.Response {
	body := `data: {"type":"message_start","message":{"id":"msg_r5a","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":11,"output_tokens":0}}}` + "\n\n" +
		`data: {"type":"content_block_start","content_block":{"type":"text"}}` + "\n\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7` + messageDeltaExtra + `}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestR5AClaudeStreamHandler_WebSearchRequestsWiredToContextKey is the G2
// positive lock: a message_delta usage.server_tool_use.web_search_requests
// count must reach c.Get("claude_web_search_requests") after the full
// streaming handler runs, for BOTH RelayFormatClaude (native passthrough,
// helper.ClaudeChunkData) and RelayFormatOpenAI (converted response,
// StreamResponseClaude2OpenAI) — both formats share the same
// HandleStreamFinalResponse completion path.
func TestR5AClaudeStreamHandler_WebSearchRequestsWiredToContextKey(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		t.Run(string(format), func(t *testing.T) {
			c, _ := fixClaudeGuardsContext()
			info := fixClaudeGuardsInfo(format, "claude-sonnet-4-5-20250929")

			resp := r5aClaudeSSEResp(`,"server_tool_use":{"web_search_requests":2}`)
			defer func() { _ = resp.Body.Close() }()

			usage, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)
			if apiErr != nil {
				t.Fatalf("[%s] unexpected error: %v", format, apiErr)
			}
			if usage == nil {
				t.Fatalf("[%s] expected non-nil usage", format)
			}

			got, exists := c.Get("claude_web_search_requests")
			if !exists {
				t.Fatalf("[%s] claude_web_search_requests not set — the streaming completion path (HandleStreamFinalResponse) did not wire the count into the gin context", format)
			}
			if got != 2 {
				t.Errorf("[%s] claude_web_search_requests = %v, want 2", format, got)
			}
		})
	}
}

// TestR5AClaudeStreamHandler_NoWebSearchLeavesContextKeyUnset is the
// companion negative: a stream with no server_tool_use anywhere must NOT set
// the key at all (mirrors the existing non-streaming lock at
// fix_claude_relay_guards_test.go's message-mode-with-usage test, which
// checks `exists` for the same reason: a spurious present-but-zero key would
// still be wrong if any future reader started checking existence rather than
// value).
func TestR5AClaudeStreamHandler_NoWebSearchLeavesContextKeyUnset(t *testing.T) {
	c, _ := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "claude-sonnet-4-5-20250929")

	resp := r5aClaudeSSEResp("") // no server_tool_use field at all
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}

	if _, exists := c.Get("claude_web_search_requests"); exists {
		t.Error("claude_web_search_requests should not be set when no server_tool_use was reported on the stream")
	}
}

// TestR5AClaudeStreamHandler_MessageStartServerToolUseSurvivesMessageDelta
// locks the OTHER event arm through the same full handler: Anthropic may
// report server_tool_use on message_start instead of (or as well as)
// message_delta (undocumented which — grepped dto/claude.go: no such
// guarantee documented there), so FormatClaudeResponseInfo must pick it up
// on that arm too, and a later message_delta that reports no
// server_tool_use of its own must not erase it (max, not last-write-wins;
// see the WebSearchRequests field comment on ClaudeResponseInfo).
func TestR5AClaudeStreamHandler_MessageStartServerToolUseSurvivesMessageDelta(t *testing.T) {
	c, _ := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "claude-sonnet-4-5-20250929")

	body := `data: {"type":"message_start","message":{"id":"msg_r5a2","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":11,"output_tokens":0,"server_tool_use":{"web_search_requests":3}}}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := ClaudeStreamHandler(c, resp, info, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}

	got, exists := c.Get("claude_web_search_requests")
	if !exists || got != 3 {
		t.Errorf("claude_web_search_requests = %v (exists=%v), want 3 (message_start's count must survive a message_delta that reports none)", got, exists)
	}
}
