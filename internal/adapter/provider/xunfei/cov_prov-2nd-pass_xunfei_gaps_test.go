package xunfei

// Business-acceptance tests for xunfeiMakeRequest, the websocket-transport
// seam behind xunfeiHandler/xunfeiStreamHandler. authUrl is a plain function
// parameter here (unlike xunfeiHandler/xunfeiStreamHandler, which always
// build it via the hardcoded wss://spark-api.xf-yun.com host through
// getXunfeiAuthUrl and therefore cannot be redirected to a local test server
// without either live external network access or modifying production code
// -- both out of scope per task constraints). xunfeiMakeRequest itself takes
// authUrl directly, so it is the one function in this file's dependency
// chain that IS unit-testable against a local httptest websocket server.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gorilla/websocket"
)

// prov_2nd_pass_xunfei_wsURL converts an httptest.Server's http(s):// base
// URL into the ws(s):// form the gorilla websocket dialer expects.
func prov_2nd_pass_xunfei_wsURL(t *testing.T, httpURL string) string {
	t.Helper()
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

var prov_2nd_pass_xunfei_upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func TestProv2ndPass_Xunfei_MakeRequest_StreamsUntilTerminalStatusThenStops(t *testing.T) {
	var gotAppId string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := prov_2nd_pass_xunfei_upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("server upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		var req XunfeiChatRequest
		if err := conn.ReadJSON(&req); err != nil {
			t.Errorf("server failed to read client request: %v", err)
			return
		}
		gotAppId = req.Header.AppId

		mid := XunfeiChatResponse{}
		mid.Payload.Choices.Status = 1
		mid.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "Hel"}}
		mid.Payload.Usage.Text.PromptTokens = 3
		if err := conn.WriteJSON(mid); err != nil {
			t.Errorf("server write (mid) failed: %v", err)
			return
		}

		final := XunfeiChatResponse{}
		final.Payload.Choices.Status = 2 // terminal
		final.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "lo"}}
		final.Payload.Usage.Text.CompletionTokens = 2
		final.Payload.Usage.Text.TotalTokens = 5
		if err := conn.WriteJSON(final); err != nil {
			t.Errorf("server write (final) failed: %v", err)
			return
		}
	}))
	defer srv.Close()

	textReq := dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.1", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	dataChan, stopChan, err := xunfeiMakeRequest(textReq, "generalv3", prov_2nd_pass_xunfei_wsURL(t, srv.URL), "app-under-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dataChan == nil || stopChan == nil {
		t.Fatal("expected non-nil dataChan/stopChan on a successful dial")
	}

	var received []XunfeiChatResponse
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case resp := <-dataChan:
			received = append(received, resp)
		case <-stopChan:
			break loop
		case <-timeout:
			t.Fatal("timed out waiting for stopChan; the read goroutine never signalled completion")
		}
	}

	if len(received) != 2 {
		t.Fatalf("received %d frames, want 2 (mid + terminal)", len(received))
	}
	if received[0].Payload.Choices.Text[0].Content != "Hel" {
		t.Errorf("first frame content = %q, want Hel", received[0].Payload.Choices.Text[0].Content)
	}
	if received[1].Payload.Choices.Status != 2 {
		t.Errorf("last frame status = %d, want 2 (terminal)", received[1].Payload.Choices.Status)
	}
	if gotAppId != "app-under-test" {
		t.Errorf("server observed AppId = %q, want app-under-test (proves the outbound WriteJSON carried the real request payload)", gotAppId)
	}
}

func TestProv2ndPass_Xunfei_MakeRequest_MalformedFrameBreaksLoopAndStillSignalsStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := prov_2nd_pass_xunfei_upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var req XunfeiChatRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		// Send a text frame that is not valid JSON for XunfeiChatResponse.
		_ = conn.WriteMessage(websocket.TextMessage, []byte("not json at all"))
	}))
	defer srv.Close()

	textReq := dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.1"}
	dataChan, stopChan, err := xunfeiMakeRequest(textReq, "generalv3", prov_2nd_pass_xunfei_wsURL(t, srv.URL), "app1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	timeout := time.After(5 * time.Second)
	select {
	case resp := <-dataChan:
		t.Fatalf("expected no data frame for a malformed upstream message, got %+v", resp)
	case <-stopChan:
		// expected: the read loop's unmarshal error breaks it, and the
		// deferred `stopChan <- true` still fires so the caller's select
		// doesn't hang forever on a permanently-broken connection.
	case <-timeout:
		t.Fatal("timed out: a malformed frame must still signal stopChan, not hang the caller forever")
	}
}

func TestProv2ndPass_Xunfei_MakeRequest_DialFailureReturnsError(t *testing.T) {
	textReq := dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.1"}
	dataChan, stopChan, err := xunfeiMakeRequest(textReq, "generalv3", "ws://127.0.0.1:1/nope", "app1")
	if err == nil {
		t.Fatal("expected a dial error for an unreachable websocket endpoint, got nil")
	}
	if dataChan != nil || stopChan != nil {
		t.Errorf("expected nil channels on dial failure, got dataChan=%v stopChan=%v", dataChan, stopChan)
	}
}

// NOTE: the conn.WriteJSON failure branch (server tears down the connection
// between handshake and the client's first write) was deliberately dropped
// from this suite: on loopback, an immediate server-side Close() races the
// client's outbound write and the OS write buffer routinely accepts the
// write before the RST/FIN is observed, so the scenario is not
// deterministically reproducible without an artificial delay/mock transport.
// The dial-failure and malformed-frame tests above already cover
// xunfeiMakeRequest's other two error-return branches.
