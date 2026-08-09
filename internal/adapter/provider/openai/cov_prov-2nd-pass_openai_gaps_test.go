package openai

// Second-pass business-acceptance tests filling gaps left after the first
// coverage pass: the Realtime websocket relay (the money path that counts
// and bills tokens for live voice/text sessions), the Claude/Gemini output
// branches of OpenaiHandler (non-OpenAI clients talking through an OpenAI
// channel), and the OpenRouter-enterprise malformed-envelope error path.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// OpenaiRealtimeHandler: bidirectional websocket relay between the caller
// (ClientWs) and the upstream vendor (TargetWs). This drives real gorilla
// websocket connections end-to-end through httptest servers - no mocking of
// the relay logic itself - to prove tokens read off a real "response.done"
// event actually land in the billed usage total.
// ---------------------------------------------------------------------------

// prov2ndPassOpenaiWsServer starts an httptest websocket server that hands
// each upgraded server-side connection to connCh, so the test can obtain the
// exact *websocket.Conn the handler-under-test will read/write.
func prov2ndPassOpenaiWsServer(t *testing.T) (url string, connCh chan *websocket.Conn, closeFn func()) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	connCh = make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		connCh <- conn
		// Keep the connection alive until the test closes it; the handler
		// under test owns the read/write loop from here.
		<-r.Context().Done()
	}))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return wsURL, connCh, srv.Close
}

func prov2ndPassOpenaiDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	return conn
}

func TestOpenaiRealtimeHandler_FullDuplexRelay_BillsUsageFromResponseDone(t *testing.T) {
	clientURL, clientConnCh, closeClientSrv := prov2ndPassOpenaiWsServer(t)
	defer closeClientSrv()
	targetURL, targetConnCh, closeTargetSrv := prov2ndPassOpenaiWsServer(t)
	defer closeTargetSrv()

	// testBrowser simulates the real end user's browser; testVendor
	// simulates the upstream (e.g. OpenAI) realtime endpoint.
	testBrowser := prov2ndPassOpenaiDial(t, clientURL)
	defer testBrowser.Close()
	testVendor := prov2ndPassOpenaiDial(t, targetURL)
	defer testVendor.Close()

	// These are the connections OpenaiRealtimeHandler will actually use.
	handlerClientConn := <-clientConnCh
	handlerTargetConn := <-targetConnCh
	defer handlerClientConn.Close()
	defer handlerTargetConn.Close()

	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o-realtime"},
		UsePrice:    true, // fixed-price billing: skips DB quota lookups entirely
		ClientWs:    handlerClientConn,
		TargetWs:    handlerTargetConn,
	}

	type result struct {
		apiErr *types.NewAPIError
		usage  *dto.RealtimeUsage
	}
	resultCh := make(chan result, 1)
	go func() {
		apiErr, usage := OpenaiRealtimeHandler(w.ctx, info)
		resultCh <- result{apiErr, usage}
	}()

	// 1. Browser sends a session.update; the handler must forward the raw
	// bytes upstream verbatim (proves the client->target relay leg works).
	sessionUpdate := `{"type":"session.update","session":{"instructions":"be terse"}}`
	if err := testBrowser.WriteMessage(websocket.TextMessage, []byte(sessionUpdate)); err != nil {
		t.Fatalf("browser write: %v", err)
	}
	testVendor.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, forwarded, err := testVendor.ReadMessage()
	if err != nil {
		t.Fatalf("vendor did not receive forwarded session.update: %v", err)
	}
	if string(forwarded) != sessionUpdate {
		t.Errorf("forwarded upstream message = %q, want exact echo %q", forwarded, sessionUpdate)
	}

	// 2. Vendor sends response.done with real usage numbers; the handler
	// must both bill them (preConsumeUsage under UsePrice fast-path) and
	// relay the raw event back down to the browser.
	responseDone := `{"type":"response.done","response":{"usage":{"total_tokens":42,"input_tokens":30,"output_tokens":12,"input_token_details":{"text_tokens":20,"audio_tokens":10},"output_token_details":{"text_tokens":8,"audio_tokens":4}}}}`
	if err := testVendor.WriteMessage(websocket.TextMessage, []byte(responseDone)); err != nil {
		t.Fatalf("vendor write: %v", err)
	}
	testBrowser.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, echoed, err := testBrowser.ReadMessage()
	if err != nil {
		t.Fatalf("browser did not receive the relayed response.done: %v", err)
	}
	if string(echoed) != responseDone {
		t.Errorf("relayed downstream message = %q, want exact echo %q", echoed, responseDone)
	}

	// 3. Browser disconnects cleanly - the handler's client-reader loop
	// should exit and the handler should return with the accumulated usage.
	_ = testBrowser.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	testBrowser.Close()

	select {
	case res := <-resultCh:
		if res.apiErr != nil {
			t.Fatalf("unexpected error from realtime handler: %v", res.apiErr.Error())
		}
		if res.usage == nil {
			t.Fatal("expected non-nil billed usage")
		}
		if res.usage.TotalTokens != 42 {
			t.Errorf("billed TotalTokens = %d, want 42 (from response.done usage)", res.usage.TotalTokens)
		}
		if res.usage.InputTokens != 30 || res.usage.OutputTokens != 12 {
			t.Errorf("billed Input/Output = %d/%d, want 30/12", res.usage.InputTokens, res.usage.OutputTokens)
		}
		if res.usage.InputTokenDetails.AudioTokens != 10 || res.usage.OutputTokenDetails.AudioTokens != 4 {
			t.Errorf("billed audio details = in:%d out:%d, want in:10 out:4",
				res.usage.InputTokenDetails.AudioTokens, res.usage.OutputTokenDetails.AudioTokens)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OpenaiRealtimeHandler to return after client disconnect")
	}
}

func TestOpenaiRealtimeHandler_NilInfo_ReturnsError(t *testing.T) {
	w := newRecorderCtx(t)
	apiErr, usage := OpenaiRealtimeHandler(w.ctx, nil)
	if apiErr == nil {
		t.Fatal("expected error for nil info")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on the immediate-error path", usage)
	}
}

// ---------------------------------------------------------------------------
// OpenaiHandler: Claude / Gemini output-format branches of the RelayFormat
// switch, and the OpenRouter-enterprise malformed-inner-body error path.
// ---------------------------------------------------------------------------

func TestOpenaiHandler_RelayFormatClaude_ConvertsResponseToClaudeShape(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: types.RelayFormatClaude,
	}
	body := `{"id":"chatcmpl-c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi from claude format"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`
	resp := fakeHTTPResponse(200, body)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 7 {
		t.Errorf("TotalTokens = %d, want 7", usage.TotalTokens)
	}
	var claudeResp dto.ClaudeResponse
	if err := json.Unmarshal(w.rec.Body.Bytes(), &claudeResp); err != nil {
		t.Fatalf("response body is not valid Claude-shaped JSON: %v (%s)", err, w.rec.Body.String())
	}
	if claudeResp.Type != "message" || claudeResp.Role != "assistant" {
		t.Errorf("claudeResp = %+v, want type=message role=assistant", claudeResp)
	}
	if claudeResp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn (mapped from OpenAI stop)", claudeResp.StopReason)
	}
	if len(claudeResp.Content) != 1 || claudeResp.Content[0].GetText() != "hi from claude format" {
		t.Errorf("Content = %+v, want single text block with the message content", claudeResp.Content)
	}
}

func TestOpenaiHandler_RelayFormatGemini_ConvertsResponseToGeminiShape(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: types.RelayFormatGemini,
	}
	body := `{"id":"chatcmpl-g1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi from gemini format"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}`
	resp := fakeHTTPResponse(200, body)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 11 {
		t.Errorf("TotalTokens = %d, want 11", usage.TotalTokens)
	}
	var geminiResp dto.GeminiChatResponse
	if err := json.Unmarshal(w.rec.Body.Bytes(), &geminiResp); err != nil {
		t.Fatalf("response body is not valid Gemini-shaped JSON: %v (%s)", err, w.rec.Body.String())
	}
	if len(geminiResp.Candidates) != 1 {
		t.Fatalf("Candidates = %d, want 1", len(geminiResp.Candidates))
	}
	if len(geminiResp.Candidates[0].Content.Parts) == 0 || geminiResp.Candidates[0].Content.Parts[0].Text != "hi from gemini format" {
		t.Errorf("Candidates[0] = %+v, want text part with the message content", geminiResp.Candidates[0])
	}
}

func TestOpenaiHandler_OpenRouterEnterprise_MalformedEnvelope_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	enterpriseEnabled := true
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenRouter,
			ChannelOtherSettings: dto.ChannelOtherSettings{OpenRouterEnterprise: &enterpriseEnabled},
		},
		RelayFormat: "openai",
	}
	// Not valid JSON at all - the enterprise-envelope Unmarshal itself must fail.
	resp := fakeHTTPResponse(200, `not-json-at-all`)
	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for a malformed openrouter enterprise envelope")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on the malformed-envelope error path", usage)
	}
}

// NOTE: this file used to carry a streamTTSResponse non-EOF read-error test.
// That helper had no production caller and was deleted; fix_dead_tts_stream_test.go
// now fails if it ever reappears unreferenced, so the test went with it.
