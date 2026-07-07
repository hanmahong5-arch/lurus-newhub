package minimax

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		wantURL string
		wantErr bool
	}{
		{
			name: "chat completions mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
			},
			wantURL: "https://api.minimax.chat/v1/text/chatcompletion_v2",
		},
		{
			name: "audio speech mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
			},
			wantURL: "https://api.minimax.chat/v1/t2a_v2",
		},
		{
			name: "unsupported relay mode returns error",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
			},
			wantErr: true,
		},
		{
			name: "empty base url falls back to channel default",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			wantURL: "https://api.minimax.chat/v1/text/chatcompletion_v2",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (url=%q)", got)
				}
				wantErrMsg := fmt.Sprintf("unsupported relay mode: %d", tt.info.RelayMode)
				if err.Error() != wantErrMsg {
					t.Errorf("error = %q, want %q", err.Error(), wantErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestSetupRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-minimax-key"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := header.Get("Authorization"); got != "Bearer sk-minimax-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-minimax-key")
	}
}

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("unsupported relay mode returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
		got, err := a.ConvertAudioRequest(nil, info, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "unsupported audio relay mode" {
			t.Errorf("error = %v, want %q", err, "unsupported audio relay mode")
		}
	})

	t.Run("builds tts request with url output format by default", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeAudioSpeech,
			OriginModelName: "speech-01-turbo",
		}
		req := dto.AudioRequest{
			Voice:          "male-voice",
			Speed:          1.5,
			ResponseFormat: "mp3",
			Input:          "hello world",
		}

		got, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		body, readErr := io.ReadAll(got)
		if readErr != nil {
			t.Fatalf("failed to read returned reader: %v", readErr)
		}

		var parsed MiniMaxTTSRequest
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("failed to unmarshal produced request: %v", err)
		}

		if parsed.Model != "speech-01-turbo" {
			t.Errorf("Model = %q, want %q", parsed.Model, "speech-01-turbo")
		}
		if parsed.Text != "hello world" {
			t.Errorf("Text = %q, want %q", parsed.Text, "hello world")
		}
		if parsed.VoiceSetting.VoiceID != "male-voice" {
			t.Errorf("VoiceSetting.VoiceID = %q, want %q", parsed.VoiceSetting.VoiceID, "male-voice")
		}
		if parsed.VoiceSetting.Speed != 1.5 {
			t.Errorf("VoiceSetting.Speed = %v, want %v", parsed.VoiceSetting.Speed, 1.5)
		}
		if parsed.AudioSetting == nil || parsed.AudioSetting.Format != "mp3" {
			t.Errorf("AudioSetting.Format = %v, want %q", parsed.AudioSetting, "mp3")
		}
		if parsed.OutputFormat != "mp3" {
			t.Errorf("OutputFormat = %q, want %q", parsed.OutputFormat, "mp3")
		}

		if got := c.GetString("response_format"); got != "url" {
			t.Errorf("response_format context value = %q, want %q (non-hex format is normalized to url)", got, "url")
		}
	})

	t.Run("hex output format is preserved in context", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech}
		req := dto.AudioRequest{ResponseFormat: "hex"}

		_, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := c.GetString("response_format"); got != "hex" {
			t.Errorf("response_format context value = %q, want %q", got, "hex")
		}
	})

	t.Run("metadata overrides fields via json unmarshal", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, OriginModelName: "speech-01-turbo"}
		req := dto.AudioRequest{
			Voice:          "male-voice",
			ResponseFormat: "mp3",
			Input:          "hi",
			Metadata:       json.RawMessage(`{"language_boost":"English"}`),
		}

		got, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body, _ := io.ReadAll(got)
		var parsed MiniMaxTTSRequest
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if parsed.LanguageBoost != "English" {
			t.Errorf("LanguageBoost = %q, want %q (metadata should override)", parsed.LanguageBoost, "English")
		}
	})

	t.Run("invalid metadata returns error", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech}
		req := dto.AudioRequest{Metadata: json.RawMessage(`{not-valid-json`)}

		got, err := a.ConvertAudioRequest(c, info, req)
		if got != nil {
			t.Errorf("expected nil result on metadata unmarshal error, got %v", got)
		}
		if err == nil || !strings.Contains(err.Error(), "error unmarshalling metadata to minimax request") {
			t.Errorf("error = %v, want message containing %q", err, "error unmarshalling metadata to minimax request")
		}
	})
}

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.ImageRequest{Model: "image-01", Prompt: "a cat"}
	got, err := a.ConvertImageRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.ImageRequest)
	if !ok {
		t.Fatalf("expected dto.ImageRequest, got %T", got)
	}
	if gotReq.Model != "image-01" || gotReq.Prompt != "a cat" {
		t.Errorf("ImageRequest passthrough mismatch: got %+v", gotReq)
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
	})

	t.Run("non-nil request passed through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "abab6.5-chat"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok || gotReq != req {
			t.Fatalf("expected same pointer *dto.GeneralOpenAIRequest to be returned, got %T", got)
		}
	})
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "embo-01"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok || gotReq.Model != "embo-01" {
		t.Fatalf("expected passthrough dto.EmbeddingRequest, got %T / %+v", got, got)
	}
}

func TestNotImplementedStubs(t *testing.T) {
	a := &Adaptor{}

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		got, err := a.ConvertGeminiRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertClaudeRequest", func(t *testing.T) {
		got, err := a.ConvertClaudeRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic even with nil info.
	a.Init(nil)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(ModelList))
	}
	for i, m := range ModelList {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}

	if got := a.GetChannelName(); got != "minimax" {
		t.Errorf("GetChannelName() = %q, want %q", got, "minimax")
	}
}

func TestDoResponse_AudioSpeechRoutesToTTSHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech}

	// Malformed base_resp status code triggers the TTS-error branch inside
	// handleTTSResponse, proving DoResponse routed to it (not the openai path).
	body := `{"data":{"audio":""},"base_resp":{"status_code":0,"status_msg":"ok"}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	usage, newAPIErr := a.DoResponse(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if newAPIErr == nil {
		t.Fatal("expected error for missing audio data, got nil")
	}
	if !strings.Contains(newAPIErr.Error(), "no audio data in minimax TTS response") {
		t.Errorf("error = %q, want it to contain %q", newAPIErr.Error(), "no audio data in minimax TTS response")
	}
}

func TestDoResponse_DefaultRoutesToOpenAIHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	// Malformed JSON body forces the openai.Adaptor default-branch handler to
	// return a bad-response-body error without needing any network access.
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{not-json")),
		Header:     http.Header{},
	}

	_, newAPIErr := a.DoResponse(c, resp, info)
	if newAPIErr == nil {
		t.Fatal("expected error for malformed upstream body, got nil")
	}
}

func TestGetContentTypeByFormat(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{"mp3", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"flac", "audio/flac"},
		{"aac", "audio/aac"},
		{"pcm", "audio/pcm"},
		{"unknown-format", "audio/mpeg"},
		{"", "audio/mpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			if got := getContentTypeByFormat(tt.format); got != tt.want {
				t.Errorf("getContentTypeByFormat(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestHandleTTSResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("read body error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &http.Response{Body: io.NopCloser(&erroringReader{})}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to read minimax response") {
			t.Errorf("error = %v, want it to contain %q", err, "failed to read minimax response")
		}
	})

	t.Run("invalid json body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &http.Response{Body: io.NopCloser(strings.NewReader("not-json"))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to unmarshal minimax TTS response") {
			t.Errorf("error = %v, want it to contain %q", err, "failed to unmarshal minimax TTS response")
		}
	})

	t.Run("non-zero base_resp status code returns error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if err == nil || !strings.Contains(err.Error(), "minimax TTS error: 1004 - invalid api key") {
			t.Errorf("error = %v, want it to contain %q", err, "minimax TTS error: 1004 - invalid api key")
		}
	})

	t.Run("empty audio data returns error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"data":{"audio":""},"base_resp":{"status_code":0}}`
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if err == nil || !strings.Contains(err.Error(), "no audio data in minimax TTS response") {
			t.Errorf("error = %v, want it to contain %q", err, "no audio data in minimax TTS response")
		}
	})

	t.Run("url-prefixed audio redirects", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		body := `{"data":{"audio":"http://cdn.example.com/a.mp3"},"extra_info":{"usage_characters":5},"base_resp":{"status_code":0}}`
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		u, ok := usage.(*dto.Usage)
		if !ok {
			t.Fatalf("expected *dto.Usage, got %T", usage)
		}
		if u.TotalTokens != 5 {
			t.Errorf("TotalTokens = %d, want %d", u.TotalTokens, 5)
		}
		if w.Code != http.StatusFound {
			t.Errorf("status = %d, want %d (redirect)", w.Code, http.StatusFound)
		}
		if got := w.Header().Get("Location"); got != "http://cdn.example.com/a.mp3" {
			t.Errorf("Location = %q, want %q", got, "http://cdn.example.com/a.mp3")
		}
	})

	t.Run("hex-encoded audio decodes and is written to response", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		hexAudio := hex.EncodeToString([]byte("fake-audio-bytes"))
		body := `{"data":{"audio":"` + hexAudio + `"},"extra_info":{"usage_characters":3},"base_resp":{"status_code":0}}`
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 3 {
			t.Fatalf("expected usage.TotalTokens=3, got %+v", usage)
		}
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if w.Body.String() != "fake-audio-bytes" {
			t.Errorf("body = %q, want %q", w.Body.String(), "fake-audio-bytes")
		}
		if got := w.Header().Get("Content-Type"); got != "audio/mpeg" {
			t.Errorf("Content-Type = %q, want %q", got, "audio/mpeg")
		}
	})

	t.Run("invalid hex audio returns error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"data":{"audio":"not-valid-hex!!"},"base_resp":{"status_code":0}}`
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{}

		usage, err := handleTTSResponse(c, resp, info)
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to decode hex audio data") {
			t.Errorf("error = %v, want it to contain %q", err, "failed to decode hex audio data")
		}
	})
}

func TestHandleChatCompletionResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	resp := &http.Response{
		StatusCode: http.StatusTeapot,
		Header:     http.Header{"X-Custom": []string{"abc"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	info := &relaycommon.RelayInfo{}

	usage, err := handleChatCompletionResponse(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if got := w.Header().Get("X-Custom"); got != "abc" {
		t.Errorf("X-Custom header = %q, want %q (should be copied through)", got, "abc")
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q, want %q", w.Body.String(), `{"ok":true}`)
	}
}

func TestHandleChatCompletionResponse_ReadBodyError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	resp := &http.Response{Body: io.NopCloser(&erroringReader{})}
	info := &relaycommon.RelayInfo{}

	usage, err := handleChatCompletionResponse(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if err == nil || !strings.Contains(err.Error(), "failed to read minimax response") {
		t.Errorf("error = %v, want it to contain %q", err, "failed to read minimax response")
	}
}

// erroringReader always fails on Read, used to exercise io.ReadAll error paths.
type erroringReader struct{}

func (e *erroringReader) Read(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}
