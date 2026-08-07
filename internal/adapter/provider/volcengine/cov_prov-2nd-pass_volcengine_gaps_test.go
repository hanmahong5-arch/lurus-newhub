package volcengine

// Second-pass business-acceptance tests filling gaps left after the first
// coverage pass: Adaptor.Init (documenting the intentional no-op contract),
// the Claude-special-base branch of ConvertClaudeRequest, the Claude-stream
// branch of DoResponse, EventType.String's full observability vocabulary
// (every wire event a TTS/ASR/chat session can emit must render as a named
// string, not a numeric fallback, for incident debugging), the
// ConnectID wire-protocol read/write round trip, and FullClientRequest's
// write-error path against a closed websocket.

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	// ClaudeStreamHandler (reached via the special-base + stream branch of
	// DoResponse) delegates to helper.StreamScannerHandler, which builds a
	// time.NewTicker(StreamingTimeout*time.Second); a zero/unset value
	// panics ("non-positive interval"), so ensure a safe default.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

// NOTE: Adaptor.Init (adaptor.go:235) is a zero-statement no-op on an
// empty struct (`type Adaptor struct{}`) - there is nothing to assert about
// its effect (the struct has no fields to observe, and every implementation
// that doesn't panic would pass the same check), so a dedicated test for it
// would be vacuous. Left uncovered rather than faking an assertion.

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest: the special-base branch must delegate to the
// Claude-native (pass-through) converter, not the OpenAI-compatible one.
// ---------------------------------------------------------------------------

func TestProv2ndPassVolc_ConvertClaudeRequest_SpecialBase_DelegatesToClaudeAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"}}
	req := &dto.ClaudeRequest{
		Model:     "doubao-pro-32k",
		MaxTokens: 77,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	out, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("expected *dto.ClaudeRequest (Claude-native passthrough) for a registered special base, got %T", out)
	}
	if got != req {
		t.Errorf("special-base ConvertClaudeRequest must return the same request pointer unchanged (pure passthrough), got a different value: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: the Claude-format + special-base + streaming
// combination must delegate to claude.ClaudeStreamHandler (SSE parsing),
// not the non-stream ClaudeHandler (whole-body JSON parsing) - using the
// wrong one on a streamed body would corrupt or drop the billed usage.
// ---------------------------------------------------------------------------

func TestProv2ndPassVolc_DoResponse_ClaudeSpecialBase_Stream_DelegatesToClaudeStreamHandler(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	sse := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_ve_s","type":"message","role":"assistant","content":[],"model":"claude-3-opus-20240229","usage":{"input_tokens":9,"output_tokens":0}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage from the Claude stream handler, got %T", usage)
	}
	if u.PromptTokens != 9 {
		t.Errorf("PromptTokens = %d, want 9 (from the message_start event's input_tokens)", u.PromptTokens)
	}
	if u.CompletionTokens != 3 {
		t.Errorf("CompletionTokens = %d, want 3 (from the message_delta event's output_tokens)", u.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "message_stop") {
		t.Errorf("client body should carry the relayed SSE stream, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// EventType.String: every named wire event must render its symbolic name -
// this is what shows up in production logs when diagnosing a stuck TTS/ASR
// session, so a silent fallback to "EventType_(154)" for a known value would
// hide the real event from whoever's debugging the incident.
// ---------------------------------------------------------------------------

func TestProv2ndPassVolc_EventType_String_AllNamedConstants(t *testing.T) {
	cases := map[EventType]string{
		EventType_None:                        "EventType_None",
		EventType_StartConnection:             "EventType_StartConnection",
		EventType_FinishConnection:            "EventType_FinishConnection",
		EventType_ConnectionStarted:           "EventType_ConnectionStarted",
		EventType_ConnectionFailed:            "EventType_ConnectionFailed",
		EventType_ConnectionFinished:          "EventType_ConnectionFinished",
		EventType_StartSession:                "EventType_StartSession",
		EventType_CancelSession:               "EventType_CancelSession",
		EventType_FinishSession:               "EventType_FinishSession",
		EventType_SessionStarted:              "EventType_SessionStarted",
		EventType_SessionCanceled:             "EventType_SessionCanceled",
		EventType_SessionFinished:             "EventType_SessionFinished",
		EventType_SessionFailed:               "EventType_SessionFailed",
		EventType_UsageResponse:               "EventType_UsageResponse",
		EventType_TaskRequest:                 "EventType_TaskRequest",
		EventType_UpdateConfig:                "EventType_UpdateConfig",
		EventType_AudioMuted:                  "EventType_AudioMuted",
		EventType_SayHello:                    "EventType_SayHello",
		EventType_TTSSentenceStart:            "EventType_TTSSentenceStart",
		EventType_TTSSentenceEnd:              "EventType_TTSSentenceEnd",
		EventType_TTSResponse:                 "EventType_TTSResponse",
		EventType_TTSEnded:                    "EventType_TTSEnded",
		EventType_PodcastRoundStart:           "EventType_PodcastRoundStart",
		EventType_PodcastRoundResponse:        "EventType_PodcastRoundResponse",
		EventType_PodcastRoundEnd:             "EventType_PodcastRoundEnd",
		EventType_ASRInfo:                     "EventType_ASRInfo",
		EventType_ASRResponse:                 "EventType_ASRResponse",
		EventType_ASREnded:                    "EventType_ASREnded",
		EventType_ChatTTSText:                 "EventType_ChatTTSText",
		EventType_ChatResponse:                "EventType_ChatResponse",
		EventType_ChatEnded:                   "EventType_ChatEnded",
		EventType_SourceSubtitleStart:         "EventType_SourceSubtitleStart",
		EventType_SourceSubtitleResponse:      "EventType_SourceSubtitleResponse",
		EventType_SourceSubtitleEnd:           "EventType_SourceSubtitleEnd",
		EventType_TranslationSubtitleStart:    "EventType_TranslationSubtitleStart",
		EventType_TranslationSubtitleResponse: "EventType_TranslationSubtitleResponse",
		EventType_TranslationSubtitleEnd:      "EventType_TranslationSubtitleEnd",
	}
	if len(cases) != 37 {
		t.Fatalf("test table has %d entries, want 37 (one per named EventType constant in protocols.go) - update this table if a constant was added/removed", len(cases))
	}
	for value, want := range cases {
		if got := value.String(); got != want {
			t.Errorf("EventType(%d).String() = %q, want %q", int32(value), got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Message.readConnectID / writeSessionID's ConnectID sibling: only the
// Connection-lifecycle event types carry a ConnectID on the wire; every
// other event type must skip the field entirely rather than misreading
// unrelated payload bytes as a bogus length-prefixed string.
// ---------------------------------------------------------------------------

// FINDING: the codec is asymmetric for the three connection-lifecycle events.
// readConnectID consumes a 4-byte big-endian length prefix (plus that many
// bytes) for EventType_ConnectionStarted / _ConnectionFailed / _ConnectionFinished,
// but there is no writeConnectID anywhere in the package — Marshal never emits
// those bytes. A frame this package produces for one of those event types is
// therefore not parseable by this package's own decoder: readConnectID reads the
// payload's first four bytes as a length, then readPayload hits EOF.
//
// This is latent rather than live: those three events only ever arrive from the
// upstream server, which does emit the field, so the decode path works against
// real traffic and the encode path is never asked to produce them. The tests
// below exercise readConnectID directly rather than through an impossible
// round trip, and lock in the asymmetry so it is visible if anyone ever tries
// to originate a lifecycle frame locally.
func TestProv2ndPassVolc_Message_ReadConnectID_LifecycleEventsConsumeLengthPrefixedString(t *testing.T) {
	for _, evt := range []EventType{
		EventType_ConnectionStarted,
		EventType_ConnectionFailed,
		EventType_ConnectionFinished,
	} {
		const want = "connect-xyz-789"
		buf := &bytes.Buffer{}
		if err := binary.Write(buf, binary.BigEndian, uint32(len(want))); err != nil {
			t.Fatalf("write length prefix: %v", err)
		}
		buf.WriteString(want)
		buf.WriteString("TRAILING-PAYLOAD")

		m := &Message{EventType: evt}
		if err := m.readConnectID(buf); err != nil {
			t.Fatalf("EventType %v: readConnectID() error = %v", evt, err)
		}
		if m.ConnectID != want {
			t.Errorf("EventType %v: ConnectID = %q, want %q", evt, m.ConnectID, want)
		}
		// The reader must consume exactly the prefix plus the string, leaving
		// the rest of the frame for readPayload.
		if rest := buf.String(); rest != "TRAILING-PAYLOAD" {
			t.Errorf("EventType %v: remaining buffer = %q, want %q (over- or under-consumed)", evt, rest, "TRAILING-PAYLOAD")
		}
	}
}

func TestProv2ndPassVolc_Message_ReadConnectID_ZeroLengthLeavesFieldEmpty(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := binary.Write(buf, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("write length prefix: %v", err)
	}
	buf.WriteString("PAYLOAD")

	m := &Message{EventType: EventType_ConnectionStarted}
	if err := m.readConnectID(buf); err != nil {
		t.Fatalf("readConnectID() error = %v", err)
	}
	if m.ConnectID != "" {
		t.Errorf("ConnectID = %q, want empty for a zero-length field", m.ConnectID)
	}
	if rest := buf.String(); rest != "PAYLOAD" {
		t.Errorf("remaining buffer = %q, want %q", rest, "PAYLOAD")
	}
}

func TestProv2ndPassVolc_Message_ReadConnectID_TruncatedPrefixIsAnError(t *testing.T) {
	// Only two of the four length bytes arrived: the decoder must report the
	// short read rather than silently treating it as a zero-length field.
	buf := bytes.NewBuffer([]byte{0x00, 0x01})
	m := &Message{EventType: EventType_ConnectionFailed}
	if err := m.readConnectID(buf); err == nil {
		t.Fatal("readConnectID() error = nil, want a short-read error for a truncated length prefix")
	}
}

// TestProv2ndPassVolc_Marshal_LifecycleEventFrameIsNotSelfParseable locks in the
// asymmetry described above: it is the current, real behaviour, not the desired
// one. If someone adds writeConnectID this test is the one that should fail.
func TestProv2ndPassVolc_Marshal_LifecycleEventFrameIsNotSelfParseable(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.EventType = EventType_ConnectionStarted
	msg.ConnectID = "connect-xyz-789"
	msg.Payload = []byte(`{}`)

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := NewMessageFromBytes(frame); err == nil {
		t.Fatal("NewMessageFromBytes() error = nil; a writeConnectID must have been added — " +
			"update this test and drop the FINDING note above")
	}
}

func TestProv2ndPassVolc_Message_ConnectID_SkippedForNonLifecycleEvents(t *testing.T) {
	// EventType_StartSession is not a connection-lifecycle event: readConnectID
	// must return immediately without consuming any bytes, leaving ConnectID
	// empty and the rest of the frame (SessionID, Payload) intact.
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.EventType = EventType_StartSession
	msg.ConnectID = "should-never-be-written" // writeConnectID doesn't exist; ConnectID is read-only on the wire
	msg.SessionID = "sess-1"
	msg.Payload = []byte(`{"k":"v"}`)

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.ConnectID != "" {
		t.Errorf("decoded ConnectID = %q, want empty for a non-lifecycle event (nothing was written for it on the wire)", decoded.ConnectID)
	}
	if decoded.SessionID != "sess-1" {
		t.Errorf("decoded SessionID = %q, want %q (must not have been consumed by a spurious ConnectID read)", decoded.SessionID, "sess-1")
	}
	if !bytes.Equal(decoded.Payload, msg.Payload) {
		t.Errorf("decoded Payload = %q, want %q (must not have been corrupted by a spurious ConnectID read)", decoded.Payload, msg.Payload)
	}
}

// ---------------------------------------------------------------------------
// FullClientRequest: the conn.WriteMessage error path (e.g. the underlying
// websocket already closed) must surface as a real error, not be swallowed.
// ---------------------------------------------------------------------------

func TestProv2ndPassVolc_FullClientRequest_WriteToClosedConn_Errors(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close() // close immediately server-side
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Give the server time to close its side so the client write observes
	// a broken pipe rather than racing the close.
	time.Sleep(100 * time.Millisecond)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	// Send enough frames that at least one hits the broken connection - a
	// single write can occasionally still succeed into the OS buffer before
	// the RST is observed.
	var lastErr error
	for i := 0; i < 20; i++ {
		if lastErr = FullClientRequest(conn, []byte(`{"probe":true}`)); lastErr != nil {
			break
		}
	}
	if lastErr == nil {
		t.Fatal("expected FullClientRequest to eventually error writing to a closed connection")
	}
}
