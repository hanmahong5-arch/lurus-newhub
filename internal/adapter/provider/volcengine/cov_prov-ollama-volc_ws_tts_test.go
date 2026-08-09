package volcengine

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// handleTTSWebSocketResponse: streams audio chunks over Volcengine's binary
// websocket protocol back to the client, and is the only place the streaming
// TTS billing usage gets populated. Every branch here is either a money path
// (usage) or a client-facing correctness path (audio bytes actually reach
// the response body in the right order).
// ---------------------------------------------------------------------------

func provOllamaVolcTestInfo(apiKey string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey}}
	info.SetEstimatePromptTokens(7)
	return info
}

func provOllamaVolcTestGinContext(t *testing.T, w http.ResponseWriter) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader("{}"))
	return c
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_StreamsAudioChunksAndEndsOnNegativeSequence(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		if _, err := ReceiveMessage(conn); err != nil {
			t.Errorf("server failed to receive FullClientRequest: %v", err)
			return
		}

		// A front-end-result frame that must be skipped, not written to output.
		frontend, _ := NewMessage(MsgTypeFrontEndResultServer, MsgTypeFlagNoSeq)
		frontend.Payload = []byte("should-not-appear-in-body")
		frame, _ := frontend.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)

		chunk1, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
		chunk1.Sequence = 0
		chunk1.Payload = []byte("chunk1-")
		frame1, _ := chunk1.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame1)

		final, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagNegativeSeq)
		final.Sequence = -1
		final.Payload = []byte("chunk2-final")
		frame2, _ := final.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame2)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("app1|token1")
	volcReq := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{Text: "hello"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, volcReq, info, "mp3")
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.PromptTokens != 7 || u.TotalTokens != 7 {
		t.Errorf("usage = %+v, want PromptTokens=TotalTokens=7 (estimated)", u)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("usage.CompletionTokens = %d, want 0 (TTS bills no completion tokens)", u.CompletionTokens)
	}
	if got := w.Body.String(); got != "chunk1-chunk2-final" {
		t.Errorf("body = %q, want %q (frontend-result frame must be skipped, chunks written in order)", got, "chunk1-chunk2-final")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg for mp3 encoding", ct)
	}
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_GracefulCloseWithoutFinalChunk_StillReturnsUsage(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := ReceiveMessage(conn); err != nil {
			t.Errorf("server failed to receive FullClientRequest: %v", err)
			return
		}
		chunk, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
		chunk.Sequence = 0
		chunk.Payload = []byte("only-chunk")
		frame, _ := chunk.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)

		// A graceful close control frame (no trailing negative-sequence chunk):
		// exercises the websocket.IsCloseError "break" branch rather than the
		// Sequence<0 early-return branch.
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")
		_ = conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("app1|token1")
	volcReq := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{Text: "hello"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, volcReq, info, "wav")
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok || u.PromptTokens != 7 {
		t.Errorf("usage = %+v (%T), want *dto.Usage with PromptTokens=7 even on graceful close without a final negative-seq chunk", usage, usage)
	}
	if got := w.Body.String(); got != "only-chunk" {
		t.Errorf("body = %q, want %q", got, "only-chunk")
	}
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_ServerErrorMessage_PropagatesErrorCode(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := ReceiveMessage(conn); err != nil {
			t.Errorf("server failed to receive FullClientRequest: %v", err)
			return
		}
		errMsg, _ := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
		errMsg.ErrorCode = 45000001
		errMsg.Payload = []byte("invalid text")
		frame, _ := errMsg.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("app1|token1")
	volcReq := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{Text: "hello"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, volcReq, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected an error when the upstream sends MsgTypeError")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on upstream error", usage)
	}
	if !strings.Contains(apiErr.Error(), "45000001") || !strings.Contains(apiErr.Error(), "invalid text") {
		t.Errorf("error = %v, want it to surface the upstream error code and message", apiErr)
	}
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_MalformedApiKey_FailsBeforeDialing(t *testing.T) {
	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("no-pipe-key")

	usage, apiErr := handleTTSWebSocketResponse(c, "ws://127.0.0.1:1/", VolcengineTTSRequest{}, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected an error for a malformed appid|token API key")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeChannelInvalidKey {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeChannelInvalidKey)
	}
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_ConnectionRefused_NoHTTPResponse(t *testing.T) {
	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("app1|token1")

	// Port 1 is a reserved/unassigned TCP port: dialing it fails at the TCP
	// connect stage, before any HTTP response can exist (resp == nil branch).
	usage, apiErr := handleTTSWebSocketResponse(c, "ws://127.0.0.1:1/", VolcengineTTSRequest{}, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected a dial error for an unreachable websocket endpoint")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseStatusCode {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseStatusCode)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", apiErr.StatusCode, http.StatusBadGateway)
	}
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_BadHandshake_HTTPResponsePresent(t *testing.T) {
	// A plain (non-upgrading) HTTP server: the dial fails with ErrBadHandshake
	// but gorilla still returns the HTTP response it received, exercising the
	// `resp != nil` branch specifically (distinct from a refused connection).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	w := httptest.NewRecorder()
	c := provOllamaVolcTestGinContext(t, w)
	info := provOllamaVolcTestInfo("app1|token1")

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, VolcengineTTSRequest{}, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected a dial error for a server that never upgrades to websocket")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if !strings.Contains(apiErr.Error(), "status: 404") {
		t.Errorf("error = %v, want it to mention the upstream's HTTP status", apiErr)
	}
}

// failWriteResponseWriter simulates a client that disconnects mid-stream: the
// underlying Write() call fails, which must surface as an error rather than
// silently truncating the audio (and the billed usage that goes with it).
type failWriteResponseWriter struct {
	header http.Header
}

func (f *failWriteResponseWriter) Header() http.Header         { return f.header }
func (f *failWriteResponseWriter) WriteHeader(statusCode int)  {}
func (f *failWriteResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated client disconnect")
}

func TestProvOllamaVolc_HandleTTSWebSocketResponse_ClientWriteFailure_ReturnsError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := ReceiveMessage(conn); err != nil {
			t.Errorf("server failed to receive FullClientRequest: %v", err)
			return
		}
		chunk, _ := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
		chunk.Sequence = 0
		chunk.Payload = []byte("some-audio-bytes")
		frame, _ := chunk.Marshal()
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	fw := &failWriteResponseWriter{header: http.Header{}}
	c := provOllamaVolcTestGinContext(t, fw)
	info := provOllamaVolcTestInfo("app1|token1")

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, VolcengineTTSRequest{}, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected an error when writing the audio chunk to the client fails")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on a write failure", usage)
	}
}
