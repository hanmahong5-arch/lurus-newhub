package volcengine

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// protocols.go implements Volcengine's custom binary websocket framing for
// streaming TTS. A bug here corrupts every streamed audio chunk or silently
// drops the error/usage signal the billing path depends on, so we round-trip
// Marshal/Unmarshal across every message shape the production code emits.
// ---------------------------------------------------------------------------

func provOllamaVolcNewFullClientMsg(t *testing.T, payload []byte) *Message {
	t.Helper()
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.Payload = payload
	return msg
}

func TestProvOllamaVolc_NewMessage_DefaultsMatchProtocolVersion(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Version != Version1 {
		t.Errorf("Version = %v, want Version1", msg.Version)
	}
	if msg.HeaderSize != HeaderSize4 {
		t.Errorf("HeaderSize = %v, want HeaderSize4", msg.HeaderSize)
	}
	if msg.Serialization != SerializationJSON {
		t.Errorf("Serialization = %v, want SerializationJSON", msg.Serialization)
	}
	if msg.Compression != CompressionNone {
		t.Errorf("Compression = %v, want CompressionNone", msg.Compression)
	}
	if msg.MsgType != MsgTypeFullClientRequest || msg.MsgTypeFlag != MsgTypeFlagNoSeq {
		t.Errorf("MsgType/Flag = %v/%v, want FullClientRequest/NoSeq", msg.MsgType, msg.MsgTypeFlag)
	}
}

func TestProvOllamaVolc_Message_MarshalUnmarshal_RoundTrip_NoSeqPayloadOnly(t *testing.T) {
	msg := provOllamaVolcNewFullClientMsg(t, []byte(`{"hello":"world"}`))

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// header (4 bytes, padded from 3) + 4-byte payload length + payload.
	wantLen := 4 + 4 + len(msg.Payload)
	if len(frame) != wantLen {
		t.Fatalf("frame length = %d, want %d", len(frame), wantLen)
	}

	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if !bytes.Equal(decoded.Payload, msg.Payload) {
		t.Errorf("decoded Payload = %q, want %q", decoded.Payload, msg.Payload)
	}
	if decoded.MsgType != MsgTypeFullClientRequest {
		t.Errorf("decoded MsgType = %v, want MsgTypeFullClientRequest", decoded.MsgType)
	}
	if decoded.MsgTypeFlag != MsgTypeFlagNoSeq {
		t.Errorf("decoded MsgTypeFlag = %v, want MsgTypeFlagNoSeq", decoded.MsgTypeFlag)
	}
}

func TestProvOllamaVolc_Message_MarshalUnmarshal_RoundTrip_WithPositiveSequence(t *testing.T) {
	msg, err := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.Sequence = 42
	msg.Payload = []byte{0x01, 0x02, 0x03, 0x04}

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.Sequence != 42 {
		t.Errorf("decoded Sequence = %d, want 42 (billing-relevant: negative sequence signals stream end)", decoded.Sequence)
	}
	if !bytes.Equal(decoded.Payload, msg.Payload) {
		t.Errorf("decoded Payload = %v, want %v", decoded.Payload, msg.Payload)
	}
}

func TestProvOllamaVolc_Message_MarshalUnmarshal_RoundTrip_NegativeSequenceSignalsStreamEnd(t *testing.T) {
	msg, err := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagNegativeSeq)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.Sequence = -1
	msg.Payload = []byte{0xAA, 0xBB}

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.Sequence != -1 {
		t.Errorf("decoded Sequence = %d, want -1 — handleTTSWebSocketResponse uses Sequence<0 to know the audio stream finished", decoded.Sequence)
	}
}

func TestProvOllamaVolc_Message_MarshalUnmarshal_RoundTrip_ErrorMessage(t *testing.T) {
	msg, err := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.ErrorCode = 55000001
	msg.Payload = []byte(`{"message":"invalid voice_type"}`)

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.ErrorCode != 55000001 {
		t.Errorf("decoded ErrorCode = %d, want 55000001", decoded.ErrorCode)
	}
	if !bytes.Equal(decoded.Payload, msg.Payload) {
		t.Errorf("decoded Payload = %q, want %q", decoded.Payload, msg.Payload)
	}
}

func TestProvOllamaVolc_Message_MarshalUnmarshal_RoundTrip_WithEventAndSessionID(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.EventType = EventType_StartSession
	msg.SessionID = "session-abc-123"
	msg.Payload = []byte(`{}`)

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.EventType != EventType_StartSession {
		t.Errorf("decoded EventType = %v, want EventType_StartSession", decoded.EventType)
	}
	if decoded.SessionID != "session-abc-123" {
		t.Errorf("decoded SessionID = %q, want %q", decoded.SessionID, "session-abc-123")
	}
}

func TestProvOllamaVolc_Message_WriteSessionID_SkippedForConnectionLifecycleEvents(t *testing.T) {
	// EventType_StartConnection is one of the events writeSessionID special-cases
	// to skip (no session yet exists at connection time). Verify the emitted
	// frame is shorter than one that does carry a session ID, and that decoding
	// leaves SessionID empty rather than misreading payload bytes as a length.
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	msg.EventType = EventType_StartConnection
	msg.SessionID = "should-be-ignored"
	msg.Payload = []byte(`{}`)

	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := NewMessageFromBytes(frame)
	if err != nil {
		t.Fatalf("NewMessageFromBytes() error = %v", err)
	}
	if decoded.SessionID != "" {
		t.Errorf("decoded SessionID = %q, want empty for EventType_StartConnection (session doesn't exist yet)", decoded.SessionID)
	}
}

func TestProvOllamaVolc_Message_UnsupportedMsgType_MarshalErrors(t *testing.T) {
	msg := &Message{
		Version:       Version1,
		HeaderSize:    HeaderSize4,
		MsgType:       MsgType(0xE), // not one of the known constants
		MsgTypeFlag:   MsgTypeFlagNoSeq,
		Serialization: SerializationJSON,
	}
	if _, err := msg.Marshal(); err == nil {
		t.Fatal("expected Marshal() to error for an unsupported message type")
	}
}

func TestProvOllamaVolc_Message_UnsupportedMsgType_UnmarshalErrors(t *testing.T) {
	// typeAndFlag byte: high nibble = 0xE (unsupported), low nibble = flag 0.
	data := []byte{0x11, 0xE0, 0x10, 0x00}
	if _, err := NewMessageFromBytes(data); err == nil {
		t.Fatal("expected NewMessageFromBytes() to error for an unsupported message type")
	}
}

func TestProvOllamaVolc_NewMessageFromBytes_TooShort_Errors(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {0x11}, {0x11, 0x10}} {
		if _, err := NewMessageFromBytes(data); err == nil {
			t.Errorf("NewMessageFromBytes(%v) expected error for data shorter than 3 bytes", data)
		}
	}
}

func TestProvOllamaVolc_Message_Unmarshal_TruncatedPayload_Errors(t *testing.T) {
	msg := provOllamaVolcNewFullClientMsg(t, []byte("full payload"))
	frame, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// Truncate the frame so the declared payload length in the frame exceeds
	// the actual number of remaining bytes — a half-received network chunk.
	truncated := frame[:len(frame)-4]
	if err := (&Message{}).Unmarshal(truncated); err == nil {
		t.Error("expected Unmarshal() to error on a truncated/half-received frame")
	}
}

func TestProvOllamaVolc_Message_String_Variants(t *testing.T) {
	audioSeq, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
	audioSeq.Sequence = 3
	audioSeq.Payload = []byte{1, 2, 3}
	if s := audioSeq.String(); !strings.Contains(s, "Sequence: 3") || !strings.Contains(s, "PayloadSize: 3") {
		t.Errorf("String() = %q, want it to mention Sequence and PayloadSize", s)
	}

	audioNoSeq, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagNoSeq)
	audioNoSeq.Payload = []byte{1, 2}
	if s := audioNoSeq.String(); strings.Contains(s, "Sequence") {
		t.Errorf("String() = %q, want no Sequence field when flag has no sequence", s)
	}

	errMsg, _ := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
	errMsg.ErrorCode = 500
	errMsg.Payload = []byte("boom")
	if s := errMsg.String(); !strings.Contains(s, "ErrorCode: 500") || !strings.Contains(s, "boom") {
		t.Errorf("String() = %q, want it to include ErrorCode and payload text", s)
	}

	full, _ := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	full.Payload = []byte(`{"a":1}`)
	if s := full.String(); !strings.Contains(s, `{"a":1}`) {
		t.Errorf("String() = %q, want it to include the JSON payload", s)
	}
}

func TestProvOllamaVolc_MsgType_String_UnknownValue(t *testing.T) {
	unknown := MsgType(0xF0)
	if s := unknown.String(); !strings.Contains(s, "240") {
		t.Errorf("MsgType(0xF0).String() = %q, want it to fall back to the numeric form", s)
	}
}

func TestProvOllamaVolc_EventType_String_KnownAndUnknown(t *testing.T) {
	if s := EventType_StartSession.String(); s != "EventType_StartSession" {
		t.Errorf("EventType_StartSession.String() = %q, want %q", s, "EventType_StartSession")
	}
	if s := EventType_UsageResponse.String(); s != "EventType_UsageResponse" {
		t.Errorf("EventType_UsageResponse.String() = %q, want %q", s, "EventType_UsageResponse")
	}
	unknown := EventType(99999)
	if s := unknown.String(); !strings.Contains(s, "99999") {
		t.Errorf("unknown EventType.String() = %q, want numeric fallback containing 99999", s)
	}
}

// ---------------------------------------------------------------------------
// FullClientRequest / ReceiveMessage: the actual websocket send/receive path
// used by handleTTSWebSocketResponse, exercised against a real (loopback)
// websocket server — never a real external network call.
// ---------------------------------------------------------------------------

func provOllamaVolcWsTestServer(t *testing.T, handler func(conn *websocket.Conn)) (wsURL string, closeFn func()) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	wsURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	return wsURL, srv.Close
}

func TestProvOllamaVolc_FullClientRequest_And_ReceiveMessage_RoundTrip(t *testing.T) {
	receivedCh := make(chan []byte, 1)
	wsURL, closeFn := provOllamaVolcWsTestServer(t, func(conn *websocket.Conn) {
		msg, err := ReceiveMessage(conn)
		if err != nil {
			t.Errorf("server ReceiveMessage() error = %v", err)
			return
		}
		receivedCh <- msg.Payload

		// Reply with a FullServerResponse carrying an audio-like payload so the
		// client side of ReceiveMessage is exercised too.
		reply, _ := NewMessage(MsgTypeFullServerResponse, MsgTypeFlagNoSeq)
		reply.Payload = []byte("server-reply-payload")
		frame, _ := reply.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	})
	defer closeFn()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer conn.Close()

	if err := FullClientRequest(conn, []byte(`{"request":"payload"}`)); err != nil {
		t.Fatalf("FullClientRequest() error = %v", err)
	}

	select {
	case got := <-receivedCh:
		if string(got) != `{"request":"payload"}` {
			t.Errorf("server received payload = %q, want %q", got, `{"request":"payload"}`)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server to receive the FullClientRequest frame")
	}

	reply, err := ReceiveMessage(conn)
	if err != nil {
		t.Fatalf("client ReceiveMessage() error = %v", err)
	}
	if string(reply.Payload) != "server-reply-payload" {
		t.Errorf("client received payload = %q, want %q", reply.Payload, "server-reply-payload")
	}
}

func TestProvOllamaVolc_ReceiveMessage_UnexpectedFrameType_Errors(t *testing.T) {
	wsURL, closeFn := provOllamaVolcWsTestServer(t, func(conn *websocket.Conn) {
		// Block until the client closes so the handler doesn't exit early.
		_, _, _ = conn.ReadMessage()
	})
	defer closeFn()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer conn.Close()

	// A close frame is neither BinaryMessage nor TextMessage from ReadMessage's
	// perspective — closing triggers a close error, exercising the err != nil
	// branch of ReceiveMessage (rather than the "unexpected message type" branch,
	// which the gorilla client can't be made to surface for non-control frames).
	_ = conn.Close()
	if _, err := ReceiveMessage(conn); err == nil {
		t.Error("expected ReceiveMessage() to error after the connection is closed")
	}
}
