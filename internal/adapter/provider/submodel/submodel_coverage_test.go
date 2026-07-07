package submodel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(got), len(ModelList))
	}
	want := []string{
		"NousResearch/Hermes-4-405B-FP8",
		"Qwen/Qwen3-235B-A22B-Thinking-2507",
		"Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8",
		"Qwen/Qwen3-235B-A22B-Instruct-2507",
		"zai-org/GLM-4.5-FP8",
		"openai/gpt-oss-120b",
		"deepseek-ai/DeepSeek-R1-0528",
		"deepseek-ai/DeepSeek-R1",
		"deepseek-ai/DeepSeek-V3-0324",
		"deepseek-ai/DeepSeek-V3.1",
	}
	for i, m := range want {
		if got[i] != m {
			t.Errorf("ModelList[%d] = %q, want %q", i, got[i], m)
		}
	}
}

func TestGetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "submodel" {
		t.Errorf("GetChannelName() = %q, want %q", got, "submodel")
	}
	if ChannelName != "submodel" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "submodel")
	}
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name           string
		channelBaseUrl string
		requestURLPath string
		channelType    int
		want           string
	}{
		{
			name:           "plain concat",
			channelBaseUrl: "https://api.submodel.ai",
			requestURLPath: "/v1/chat/completions",
			channelType:    constant.ChannelTypeOpenAI,
			want:           "https://api.submodel.ai/v1/chat/completions",
		},
		{
			name:           "empty base url",
			channelBaseUrl: "",
			requestURLPath: "/v1/chat/completions",
			channelType:    constant.ChannelTypeOpenAI,
			want:           "/v1/chat/completions",
		},
		{
			name:           "cloudflare gateway openai channel trims /v1 prefix",
			channelBaseUrl: "https://gateway.ai.cloudflare.com/xxx",
			requestURLPath: "/v1/chat/completions",
			channelType:    constant.ChannelTypeOpenAI,
			want:           "https://gateway.ai.cloudflare.com/xxx/chat/completions",
		},
		{
			name:           "cloudflare gateway non-special channel keeps path",
			channelBaseUrl: "https://gateway.ai.cloudflare.com/xxx",
			requestURLPath: "/v1/chat/completions",
			channelType:    999999,
			want:           "https://gateway.ai.cloudflare.com/xxx/v1/chat/completions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RequestURLPath: tt.requestURLPath,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: tt.channelBaseUrl,
					ChannelType:    tt.channelType,
				},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("sets Authorization + forwarded Content-Type/Accept for normal relay mode", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		c.Request = req

		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeChatCompletions,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"},
		}
		header := http.Header{}
		err := a.SetupRequestHeader(c, &header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test-key")
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want %q", got, "application/json")
		}
	})

	t.Run("stream request with no Accept header defaults to text/event-stream", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request = req

		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeChatCompletions,
			IsStream:    true,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-stream-key"},
		}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}
		if got := header.Get("Authorization"); got != "Bearer sk-stream-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-stream-key")
		}
	})

	t.Run("audio transcription mode skips Content-Type/Accept forwarding", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
		req.Header.Set("Content-Type", "multipart/form-data")
		c.Request = req

		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioTranscription,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-audio-key"},
		}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type = %q, want empty", got)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-audio-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-audio-key")
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("err = %v, want %q", err, "request is nil")
		}
	})

	t.Run("non-nil request is passed through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-ai/DeepSeek-V3.1"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("got type %T, want *dto.GeneralOpenAIRequest", got)
		}
		if gotReq != req {
			t.Errorf("got a different pointer, want the identical request passed through")
		}
		if gotReq.Model != "deepseek-ai/DeepSeek-V3.1" {
			t.Errorf("Model = %q, want %q", gotReq.Model, "deepseek-ai/DeepSeek-V3.1")
		}
	})
}

// ---------------------------------------------------------------------------
// Unsupported-endpoint stubs: exact error strings
// ---------------------------------------------------------------------------

func TestUnsupportedEndpointStubs(t *testing.T) {
	a := &Adaptor{}
	const wantErr = "submodel channel: endpoint not supported"

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		got, err := a.ConvertGeminiRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertClaudeRequest", func(t *testing.T) {
		got, err := a.ConvertClaudeRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		got, err := a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertRerankRequest", func(t *testing.T) {
		got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
		if err == nil || err.Error() != wantErr {
			t.Errorf("err = %v, want %q", err, wantErr)
		}
	})
}

// ---------------------------------------------------------------------------
// DoResponse: exercise both IsStream branches on already-received, in-memory
// http.Response objects (no network I/O is performed by these tests).
// ---------------------------------------------------------------------------

func TestDoResponse(t *testing.T) {
	a := &Adaptor{}

	t.Run("streaming branch: invalid/empty response body short-circuits", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, apiErr := a.DoResponse(c, nil, info)
		if apiErr == nil {
			t.Fatal("expected a non-nil *types.NewAPIError for a nil response")
		}
	})

	t.Run("non-streaming branch: malformed JSON body returns a bad-response error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("not-json")),
			Header:     http.Header{},
		}
		info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr == nil {
			t.Fatal("expected a non-nil *types.NewAPIError for a malformed JSON body")
		}
	})
}

// ---------------------------------------------------------------------------
// Init: no-op, but must not panic
// ---------------------------------------------------------------------------

func TestInitIsNoOp(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{})
	a.Init(nil)
}
