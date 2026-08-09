package openai

// Business-acceptance tests for Adaptor.DoResponse and Adaptor.DoRequest:
// the top-level dispatch that routes a relay-mode + stream-vs-not
// combination to the correct handler function, and the transport dispatch
// that picks form/api/websocket request construction. A wrong branch here
// means (for example) an image generation response gets parsed as a plain
// chat completion, silently corrupting usage/billing for that traffic
// class.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func TestAdaptor_DoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("Realtime mode delegates to OpenaiRealtimeHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeRealtime,
			// ClientWs/TargetWs nil -> immediate error, proving this branch
			// (not the default chat-completions branch) actually ran.
		}
		_, apiErr := a.DoResponse(w.ctx, nil, info)
		if apiErr == nil {
			t.Fatal("expected error: realtime handler requires websocket connections")
		}
		if !strings.Contains(apiErr.Error(), "websocket") {
			t.Errorf("error = %q, want mention of websocket (proves Realtime branch, not a JSON-parse branch)", apiErr.Error())
		}
	})

	t.Run("AudioSpeech mode delegates to OpenaiTTSHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			Request:     &dto.AudioRequest{Model: "tts-1", ResponseFormat: "mp3"},
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("fake-audio")), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("TTS dispatch should not itself produce a NewAPIError: %v", apiErr.Error())
		}
		if usage == nil {
			t.Fatal("expected non-nil usage from TTS handler")
		}
	})

	t.Run("AudioTranscription mode delegates to OpenaiSTTHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeAudioTranscription,
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"hi"}`)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du, ok := usage.(*dto.Usage)
		if !ok || du == nil {
			t.Fatalf("usage type = %T, want *dto.Usage", usage)
		}
	})

	t.Run("ImagesGenerations mode delegates to OpenaiHandlerWithUsage", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeImagesGenerations,
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.PromptTokens != 1 || du.CompletionTokens != 2 {
			t.Errorf("usage = %+v, want prompt=1 completion=2 (proves ImagesGenerations went through OpenaiHandlerWithUsage's input/output mapping)", du)
		}
	})

	t.Run("Rerank mode delegates to common_handler.RerankHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeRerank,
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[],"usage":{"total_tokens":3}}`)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.TotalTokens != 3 {
			t.Errorf("TotalTokens = %d, want 3 (from rerank response usage)", du.TotalTokens)
		}
	})

	t.Run("Responses mode non-stream delegates to OaiResponsesHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeResponses,
			IsStream:    false,
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":5,"total_tokens":10}}`)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.TotalTokens != 10 {
			t.Errorf("TotalTokens = %d, want 10", du.TotalTokens)
		}
	})

	t.Run("Responses mode stream delegates to OaiResponsesStreamHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
			RelayMode:   relayconstant.RelayModeResponses,
			IsStream:    true,
		}
		body := `data: {"type":"response.completed","response":{"id":"r2","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.TotalTokens != 2 {
			t.Errorf("TotalTokens = %d, want 2 (proves streaming Responses branch ran, not the non-stream one)", du.TotalTokens)
		}
	})

	t.Run("default mode non-stream delegates to OpenaiHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			RelayMode:   relayconstant.RelayModeChatCompletions,
			IsStream:    false,
			RelayFormat: "openai",
		}
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.TotalTokens != 2 {
			t.Errorf("TotalTokens = %d, want 2", du.TotalTokens)
		}
	})

	t.Run("default mode stream delegates to OaiStreamHandler", func(t *testing.T) {
		w := newRecorderCtx(t)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
			RelayMode:   relayconstant.RelayModeChatCompletions,
			IsStream:    true,
			RelayFormat: "openai",
		}
		info.SetEstimatePromptTokens(1)
		body := `data: {"id":"c1","model":"gpt-4o","choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
		usage, apiErr := a.DoResponse(w.ctx, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Error())
		}
		du := usage.(*dto.Usage)
		if du.TotalTokens == 0 {
			t.Error("TotalTokens should be non-zero: sawDone=true, proves the streaming default branch ran with billable output")
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest transport dispatch
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_Dispatch(t *testing.T) {
	app.InitHttpClient()
	a := &Adaptor{}

	// The stub upstreams below listen on loopback, which the relay SSRF dial
	// guard blocks by design (internal/app/relay_dial_guard.go). Allow
	// private destinations for the duration of this test only so it exercises
	// the DoApiRequest/DoFormRequest branches themselves rather than the
	// (separately-tested) dial guard.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	t.Run("default relay mode uses DoApiRequest against the channel base url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		c := newCovTestContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: srv.URL,
				ApiKey:         "sk-test",
			},
			RequestURLPath: "/v1/chat/completions",
			RelayMode:      relayconstant.RelayModeChatCompletions,
		}
		result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp, ok := result.(*http.Response)
		if !ok {
			t.Fatalf("result type = %T, want *http.Response", result)
		}
		defer func() {
			if resp != nil {
				_ = resp.Body.Close()
			}
		}()
		if resp.StatusCode != 200 {
			t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("audio transcription relay mode uses DoFormRequest", func(t *testing.T) {
		var gotContentType string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotContentType = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()

		c := newCovTestContext()
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: srv.URL,
				ApiKey:         "sk-test",
			},
			RequestURLPath: "/v1/audio/transcriptions",
			RelayMode:      relayconstant.RelayModeAudioTranscription,
		}
		result, err := a.DoRequest(c, info, strings.NewReader("--abc--"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := result.(*http.Response)
		defer func() {
			if resp != nil {
				_ = resp.Body.Close()
			}
		}()
		if !strings.HasPrefix(gotContentType, "multipart/form-data") {
			t.Errorf("upstream Content-Type = %q, want multipart/form-data (proves DoFormRequest branch, not DoApiRequest)", gotContentType)
		}
	})

	t.Run("realtime relay mode attempts a websocket dial (DoWssRequest)", func(t *testing.T) {
		c := newCovTestContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "ws://127.0.0.1:1", // nothing listening -> dial must fail
				ApiKey:         "sk-test",
			},
			RequestURLPath: "/v1/realtime",
			RelayMode:      relayconstant.RelayModeRealtime,
		}
		_, err := a.DoRequest(c, info, strings.NewReader(""))
		if err == nil {
			t.Fatal("expected websocket dial failure against an unreachable address")
		}
		if !strings.Contains(err.Error(), "dial") {
			t.Errorf("error = %q, want a dial failure (proves DoWssRequest branch ran, not DoApiRequest)", err.Error())
		}
	})
}
