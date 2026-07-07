package gemini

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newGeminiTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, w
}

// allowUnlimitedImages relaxes the global vision-image cap for the duration of a
// test and restores the previous value afterward (the package var defaults to 0
// which would make every image branch fail with "too many images").
func allowUnlimitedImages(t *testing.T) {
	t.Helper()
	old := pkgconstant.GeminiVisionMaxImageNum
	pkgconstant.GeminiVisionMaxImageNum = -1
	t.Cleanup(func() { pkgconstant.GeminiVisionMaxImageNum = old })
}

// withStreamingTimeout sets a positive constant.StreamingTimeout for the
// duration of the test (helper.StreamScannerHandler panics on
// time.NewTicker(0) with the zero-value default) and restores it afterward.
func withStreamingTimeout(t *testing.T) {
	t.Helper()
	old := pkgconstant.StreamingTimeout
	pkgconstant.StreamingTimeout = 30
	t.Cleanup(func() { pkgconstant.StreamingTimeout = old })
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}

	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "imagen model uses predict action",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "imagen-3.0-generate-002",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/imagen-3.0-generate-002:predict",
		},
		{
			name: "audio speech uses generateContent",
			info: &relaycommon.RelayInfo{
				RelayMode: constant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "gemini-2.5-flash-preview-tts",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/gemini-2.5-flash-preview-tts:generateContent",
		},
		{
			name: "text-embedding uses embedContent",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "text-embedding-004",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/text-embedding-004:embedContent",
		},
		{
			name: "embedding batch mode uses batchEmbedContents",
			info: &relaycommon.RelayInfo{
				IsGeminiBatchEmbedding: true,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "embedding-001",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/embedding-001:batchEmbedContents",
		},
		{
			name: "gemini-embedding prefix also treated as embedding",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "gemini-embedding-exp-03-07",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/gemini-embedding-exp-03-07:embedContent",
		},
		{
			name: "non-stream chat uses generateContent",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "gemini-2.0-flash",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent",
		},
		{
			name: "stream chat uses streamGenerateContent with alt=sse",
			info: &relaycommon.RelayInfo{
				IsStream: true,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "gemini-2.0-flash",
				},
			},
			want: "https://gen.googleapis.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
		},
		{
			name: "gemini-1.0-pro uses version v1",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://gen.googleapis.com",
					UpstreamModelName: "gemini-1.0-pro",
				},
			},
			want: "https://gen.googleapis.com/v1/models/gemini-1.0-pro:generateContent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdaptor_GetRequestURL_StreamSetsDisablePingOnlyForGeminiNativeMode(t *testing.T) {
	a := &Adaptor{}

	info := &relaycommon.RelayInfo{
		IsStream:  true,
		RelayMode: constant.RelayModeGemini,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://gen.googleapis.com",
			UpstreamModelName: "gemini-2.0-flash",
		},
	}
	if _, err := a.GetRequestURL(info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.DisablePing {
		t.Error("expected DisablePing = true for native gemini streaming mode")
	}

	info2 := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://gen.googleapis.com",
			UpstreamModelName: "gemini-2.0-flash",
		},
	}
	if _, err := a.GetRequestURL(info2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info2.DisablePing {
		t.Error("expected DisablePing unchanged for non-gemini relay mode")
	}
}

func TestAdaptor_GetRequestURL_ThinkingSuffixStripping(t *testing.T) {
	a := &Adaptor{}

	settings := model_setting.GetGeminiSettings()
	oldEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = oldEnabled })

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"budget suffix stripped", "gemini-2.0-flash-thinking-5000", "https://x/v1beta/models/gemini-2.0-flash:generateContent"},
		{"trailing -thinking stripped", "gemini-2.0-flash-thinking", "https://x/v1beta/models/gemini-2.0-flash:generateContent"},
		{"trailing -nothinking stripped", "gemini-2.0-flash-nothinking", "https://x/v1beta/models/gemini-2.0-flash:generateContent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://x",
					UpstreamModelName: tt.model,
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
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-api-key"},
	}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("x-goog-api-key"); got != "test-api-key" {
		t.Errorf("x-goog-api-key = %q, want %q", got, "test-api-key")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest / ConvertClaudeRequest error surfaces
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
	if err == nil || err.Error() != "request is nil" {
		t.Fatalf("err = %v, want %q", err, "request is nil")
	}
}

func TestAdaptor_ConvertOpenAIRequest_Basic(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	geminiReq, ok := result.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeminiChatRequest", result)
	}
	if len(geminiReq.Contents) != 1 || geminiReq.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("unexpected contents: %+v", geminiReq.Contents)
	}
}

func TestAdaptor_Init_NoOp(t *testing.T) {
	a := &Adaptor{}
	// Init is currently a documented no-op; calling it must not panic.
	a.Init(&relaycommon.RelayInfo{})
}

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	claudeReq := &dto.ClaudeRequest{
		Model:     "claude-3",
		MaxTokens: 100,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello from claude"}},
	}
	result, err := a.ConvertClaudeRequest(c, info, claudeReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	geminiReq, ok := result.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeminiChatRequest", result)
	}
	if len(geminiReq.Contents) != 1 || geminiReq.Contents[0].Parts[0].Text != "hello from claude" {
		t.Errorf("Contents = %+v", geminiReq.Contents)
	}
}

func TestGetSupportedMimeTypesList(t *testing.T) {
	list := getSupportedMimeTypesList()
	if len(list) != len(geminiSupportedMimeTypes) {
		t.Fatalf("len(list) = %d, want %d", len(list), len(geminiSupportedMimeTypes))
	}
	seen := make(map[string]bool, len(list))
	for _, mt := range list {
		if !geminiSupportedMimeTypes[mt] {
			t.Errorf("unexpected mime type %q not in geminiSupportedMimeTypes", mt)
		}
		seen[mt] = true
	}
	if len(seen) != len(list) {
		t.Error("expected no duplicate mime types")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest / ConvertOpenAIResponsesRequest (unsupported stubs)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	result, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if result != nil || err != nil {
		t.Errorf("ConvertRerankRequest() = (%v, %v), want (nil, nil)", result, err)
	}
}

func TestAdaptor_ConvertOpenAIResponsesRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()
	_, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{})
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("err = %v, want %q", err, "not implemented")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertAudioRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()

	t.Run("TTS mode succeeds", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech}
		reader, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{Input: "hello", Voice: "alloy"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reader == nil {
			t.Fatal("expected non-nil reader")
		}
	})

	t.Run("non-TTS mode returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeCompletions}
		_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		if err == nil {
			t.Fatal("expected error for non-TTS relay mode")
		}
		if !strings.Contains(err.Error(), "only supports TTS") {
			t.Errorf("error = %q, want contains 'only supports TTS'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertEmbeddingRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGeminiTestContext()

	t.Run("nil input returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
		if err == nil || err.Error() != "input is required" {
			t.Fatalf("err = %v, want %q", err, "input is required")
		}
	})

	t.Run("empty parsed input returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{Input: []any{}})
		if err == nil || err.Error() != "input is empty" {
			t.Fatalf("err = %v, want %q", err, "input is empty")
		}
	})

	t.Run("valid input sets batch flag and builds requests", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "text-embedding-004"}}
		result, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{Input: "hello world", Dimensions: 256})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !info.IsGeminiBatchEmbedding {
			t.Error("expected IsGeminiBatchEmbedding = true")
		}
		payload, ok := result.(map[string]interface{})
		if !ok {
			t.Fatalf("result type = %T", result)
		}
		requests, ok := payload["requests"].([]map[string]interface{})
		if !ok || len(requests) != 1 {
			t.Fatalf("requests = %#v", payload["requests"])
		}
		if requests[0]["model"] != "models/text-embedding-004" {
			t.Errorf("model = %v, want models/text-embedding-004", requests[0]["model"])
		}
		if requests[0]["outputDimensionality"] != 256 {
			t.Errorf("outputDimensionality = %v, want 256", requests[0]["outputDimensionality"])
		}
	})

	t.Run("dimensions ignored for unsupported model", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"}}
		result, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{Input: "hello", Dimensions: 128})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := result.(map[string]interface{})
		requests := payload["requests"].([]map[string]interface{})
		if _, exists := requests[0]["outputDimensionality"]; exists {
			t.Error("expected outputDimensionality to be absent for embedding-001")
		}
	})
}

// ---------------------------------------------------------------------------
// ThinkingAdaptor
// ---------------------------------------------------------------------------

func TestThinkingAdaptor(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	oldEnabled := settings.ThinkingAdapterEnabled
	oldPct := settings.ThinkingAdapterBudgetTokensPercentage
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() {
		settings.ThinkingAdapterEnabled = oldEnabled
		settings.ThinkingAdapterBudgetTokensPercentage = oldPct
	})

	t.Run("disabled adapter is a no-op", func(t *testing.T) {
		settings.ThinkingAdapterEnabled = false
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking-5000"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Error("expected no thinking config when adapter disabled")
		}
		settings.ThinkingAdapterEnabled = true
	})

	t.Run("explicit budget suffix sets clamped ThinkingBudget", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking-99999"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig == nil {
			t.Fatal("expected ThinkingConfig to be set")
		}
		if *req.GenerationConfig.ThinkingConfig.ThinkingBudget != flash25MaxBudget {
			t.Errorf("ThinkingBudget = %d, want %d", *req.GenerationConfig.ThinkingConfig.ThinkingBudget, flash25MaxBudget)
		}
		if !req.GenerationConfig.ThinkingConfig.IncludeThoughts {
			t.Error("expected IncludeThoughts = true")
		}
	})

	t.Run("invalid budget suffix is ignored", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking-notanumber"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Error("expected nil ThinkingConfig for invalid numeric suffix")
		}
	})

	t.Run("unsupported model -thinking suffix sets IncludeThoughts only", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-preview-05-06-thinking"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.ThinkingBudget != nil {
			t.Fatalf("ThinkingConfig = %+v, want IncludeThoughts-only", req.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("-thinking with MaxOutputTokens computes budget from percentage", func(t *testing.T) {
		settings.ThinkingAdapterBudgetTokensPercentage = 0.5
		req := &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{MaxOutputTokens: 1000}}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
			t.Fatal("expected ThinkingBudget to be set")
		}
		if *req.GenerationConfig.ThinkingConfig.ThinkingBudget != 500 {
			t.Errorf("ThinkingBudget = %d, want 500", *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
		}
	})

	t.Run("-thinking without MaxOutputTokens falls back to reasoning effort", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking"}}
		ThinkingAdaptor(req, info, dto.GeneralOpenAIRequest{ReasoningEffort: "high"})
		if req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
			t.Fatal("expected ThinkingBudget to be set")
		}
		want := clampThinkingBudgetByEffort("gemini-2.0-flash-thinking", "high")
		if *req.GenerationConfig.ThinkingConfig.ThinkingBudget != want {
			t.Errorf("ThinkingBudget = %d, want %d", *req.GenerationConfig.ThinkingConfig.ThinkingBudget, want)
		}
	})

	t.Run("-nothinking sets zero budget for non-pro25 model", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-nothinking"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig == nil || *req.GenerationConfig.ThinkingConfig.ThinkingBudget != 0 {
			t.Fatalf("ThinkingConfig = %+v, want ThinkingBudget=0", req.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("-nothinking is no-op for new 25-pro model", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-latest-nothinking"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Error("expected no ThinkingConfig for 25-pro -nothinking model")
		}
	})

	t.Run("no thinking suffix leaves config untouched", func(t *testing.T) {
		req := &dto.GeminiChatRequest{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
		ThinkingAdaptor(req, info)
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Error("expected nil ThinkingConfig for a model without any thinking suffix")
		}
	})
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_SystemAndUserMessages(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hi there"},
			{Role: "assistant", Content: "hello!"},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SystemInstructions == nil || result.SystemInstructions.Parts[0].Text != "you are helpful" {
		t.Errorf("SystemInstructions = %+v", result.SystemInstructions)
	}
	if len(result.Contents) != 2 {
		t.Fatalf("len(Contents) = %d, want 2", len(result.Contents))
	}
	if result.Contents[0].Role != "user" || result.Contents[0].Parts[0].Text != "hi there" {
		t.Errorf("Contents[0] = %+v", result.Contents[0])
	}
	// assistant role is remapped to "model" for Gemini.
	if result.Contents[1].Role != "model" || result.Contents[1].Parts[0].Text != "hello!" {
		t.Errorf("Contents[1] = %+v", result.Contents[1])
	}
	if len(result.SafetySettings) != len(SafetySettingList) {
		t.Errorf("len(SafetySettings) = %d, want %d", len(result.SafetySettings), len(SafetySettingList))
	}
}

func TestCovertOpenAI2Gemini_ToolMessageWrapping(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	name := "get_weather"
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			// An assistant ("model") message immediately precedes the tool
			// response, so CovertOpenAI2Gemini must synthesize a fresh "user"
			// content entry to carry the FunctionResponse (Gemini disallows two
			// consecutive "model" turns).
			{Role: "assistant", Content: "let me check"},
			{Role: "tool", Content: `{"temp": 70}`, Name: &name, ToolCallId: "call_1"},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 2 {
		t.Fatalf("len(Contents) = %d, want 2 (assistant msg + wrapped tool response)", len(result.Contents))
	}
	toolContent := result.Contents[1]
	if toolContent.Role != "user" {
		t.Errorf("tool wrapper role = %q, want user", toolContent.Role)
	}
	if len(toolContent.Parts) != 1 || toolContent.Parts[0].FunctionResponse == nil {
		t.Fatalf("expected a single FunctionResponse part, got %+v", toolContent.Parts)
	}
	if toolContent.Parts[0].FunctionResponse.Name != "get_weather" {
		t.Errorf("FunctionResponse.Name = %q, want get_weather", toolContent.Parts[0].FunctionResponse.Name)
	}
	if toolContent.Parts[0].FunctionResponse.Response["temp"] != float64(70) {
		t.Errorf("FunctionResponse.Response = %+v", toolContent.Parts[0].FunctionResponse.Response)
	}
}

func TestCovertOpenAI2Gemini_ToolMessageMergesIntoTrailingUserContent(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	name := "get_weather"
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			// The last content entry is already role "user" (not "model"), so
			// the tool response part is appended onto it in-place rather than
			// creating a new content entry.
			{Role: "user", Content: "what's the weather"},
			{Role: "tool", Content: `{"temp": 70}`, Name: &name, ToolCallId: "call_1"},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("len(Contents) = %d, want 1 (tool response merged into user content)", len(result.Contents))
	}
	if len(result.Contents[0].Parts) != 2 {
		t.Fatalf("len(Parts) = %d, want 2 (original text + FunctionResponse)", len(result.Contents[0].Parts))
	}
	if result.Contents[0].Parts[1].FunctionResponse == nil || result.Contents[0].Parts[1].FunctionResponse.Name != "get_weather" {
		t.Errorf("Parts[1].FunctionResponse = %+v", result.Contents[0].Parts[1].FunctionResponse)
	}
}

func TestCovertOpenAI2Gemini_ToolMessageArrayAndPlainTextContent(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}

	t.Run("JSON array tool content wrapped under result key", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "function", Content: `[1,2,3]`, ToolCallId: "call_2"},
			},
		}
		result, err := CovertOpenAI2Gemini(c, req, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := result.Contents[0].Parts[0].FunctionResponse.Response
		arr, ok := resp["result"].([]interface{})
		if !ok || len(arr) != 3 {
			t.Fatalf("Response[result] = %#v", resp["result"])
		}
	})

	t.Run("plain text tool content falls back to content key", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "tool", Content: "not json at all", ToolCallId: "call_3"},
			},
		}
		result, err := CovertOpenAI2Gemini(c, req, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := result.Contents[0].Parts[0].FunctionResponse.Response
		if resp["content"] != "not json at all" {
			t.Errorf("Response[content] = %v", resp["content"])
		}
	})
}

func TestCovertOpenAI2Gemini_ToolCallsFromAssistant(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	toolCalls, _ := json.Marshal([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Arguments: `{"city":"nyc"}`}},
	})
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "assistant", ToolCalls: toolCalls},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("len(Contents) = %d, want 1", len(result.Contents))
	}
	part := result.Contents[0].Parts[0]
	if part.FunctionCall == nil || part.FunctionCall.FunctionName != "get_weather" {
		t.Fatalf("FunctionCall = %+v", part.FunctionCall)
	}
	if part.FunctionCall.Arguments.(map[string]interface{})["city"] != "nyc" {
		t.Errorf("Arguments = %+v", part.FunctionCall.Arguments)
	}
}

func TestCovertOpenAI2Gemini_ToolCallsInvalidArguments(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	toolCalls, _ := json.Marshal([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "broken", Arguments: `not-json`}},
	})
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "assistant", ToolCalls: toolCalls}},
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "invalid arguments for function broken") {
		t.Fatalf("err = %v", err)
	}
}

func TestCovertOpenAI2Gemini_MarkdownImageAndInlineBase64(t *testing.T) {
	allowUnlimitedImages(t)
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}

	req := dto.GeneralOpenAIRequest{
		// NOTE: text trailing the last markdown image in a part is dropped by
		// the converter (only text *before* the first/each match is preserved),
		// so we assert on that documented behavior rather than a naive
		// text-before/image/text-after split.
		Messages: []dto.Message{
			{Role: "user", Content: "before ![image](data:image/png;base64,QUFB) after"},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := result.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2 (text-before, image), got %+v", len(parts), parts)
	}
	if parts[0].Text != "before " {
		t.Errorf("parts[0].Text = %q", parts[0].Text)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "QUFB" {
		t.Errorf("parts[1].InlineData = %+v", parts[1].InlineData)
	}
}

func TestCovertOpenAI2Gemini_ImageURLAndFileAndAudioParts(t *testing.T) {
	allowUnlimitedImages(t)
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}

	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64,YWJj"}},
					map[string]any{"type": "file", "file": map[string]any{"filename": "doc.pdf", "file_data": "data:application/pdf;base64,ZmZm"}},
					map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "data:audio/wav;base64,YXVk", "format": "wav"}},
				},
			},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := result.Contents[0].Parts
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3, got %+v", len(parts), parts)
	}
	if parts[0].InlineData.MimeType != "image/jpeg" || parts[0].InlineData.Data != "YWJj" {
		t.Errorf("image part = %+v", parts[0].InlineData)
	}
	if parts[1].InlineData.MimeType != "application/pdf" || parts[1].InlineData.Data != "ZmZm" {
		t.Errorf("file part = %+v", parts[1].InlineData)
	}
	if parts[2].InlineData.MimeType != "audio/wav" || parts[2].InlineData.Data != "YXVk" {
		t.Errorf("audio part = %+v", parts[2].InlineData)
	}
}

func TestCovertOpenAI2Gemini_FileWithFileIdRejected(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "file", "file": map[string]any{"file_id": "file-123"}},
				},
			},
		},
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "only base64 file is supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestCovertOpenAI2Gemini_InputAudioEmptyDataRejected(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "", "format": "wav"}}},
			},
		},
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "only base64 audio is supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestCovertOpenAI2Gemini_TooManyImagesRejected(t *testing.T) {
	old := pkgconstant.GeminiVisionMaxImageNum
	pkgconstant.GeminiVisionMaxImageNum = 1
	t.Cleanup(func() { pkgconstant.GeminiVisionMaxImageNum = old })

	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64,YWJj"}},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64,ZGVm"}},
				},
			},
		},
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "too many images") {
		t.Fatalf("err = %v", err)
	}
}

// NOTE: the http(s):// image_url branch of CovertOpenAI2Gemini calls
// app.GetFileBase64FromUrl, which performs a live HTTP GET via a nil-checked
// http.Client (it panics without a functioning DefaultClient/proxy setup in a
// hermetic test process). That branch therefore needs a live upstream/proxy
// and is intentionally NOT exercised here per the no-network constraint.

func TestCovertOpenAI2Gemini_Tools(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{Name: "googleSearch"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "codeExecution"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "urlContext"}},
			{Type: "function", Function: dto.FunctionRequest{
				Name: "get_weather",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"city": map[string]interface{}{"type": "string"}},
				},
			}},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := result.GetTools()
	if len(tools) != 4 {
		t.Fatalf("len(tools) = %d, want 4 (search, codeExec, urlContext, functionDeclarations)", len(tools))
	}
	foundFunctions := false
	for _, tool := range tools {
		decls, ok := tool.FunctionDeclarations.([]interface{})
		if !ok || len(decls) == 0 {
			continue
		}
		foundFunctions = true
		fn := decls[0].(map[string]interface{})
		if fn["name"] != "get_weather" {
			t.Errorf("FunctionDeclarations[0].name = %v", fn["name"])
		}
	}
	if !foundFunctions {
		t.Error("expected a FunctionDeclarations tool entry")
	}
}

func TestCovertOpenAI2Gemini_EmptyPropertiesParametersNulled(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{
				Name: "noop",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			}},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := result.GetTools()
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	decls := tools[0].FunctionDeclarations.([]interface{})
	fn := decls[0].(map[string]interface{})
	if _, exists := fn["parameters"]; exists {
		t.Fatalf("expected parameters to be nulled/omitted for empty properties map, got %+v", fn)
	}
}

func TestCovertOpenAI2Gemini_ResponseFormatJSONSchema(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	schema, _ := json.Marshal(dto.FormatJsonSchema{
		Name: "answer",
		Schema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"title":                "Answer",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{"type": "string"},
			},
		},
	})
	req := dto.GeneralOpenAIRequest{
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: schema},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("ResponseMimeType = %q, want application/json", result.GenerationConfig.ResponseMimeType)
	}
	cleaned, ok := result.GenerationConfig.ResponseSchema.(map[string]interface{})
	if !ok {
		t.Fatalf("ResponseSchema type = %T", result.GenerationConfig.ResponseSchema)
	}
	if _, exists := cleaned["additionalProperties"]; exists {
		t.Error("expected additionalProperties to be stripped from cleaned schema")
	}
	if _, exists := cleaned["title"]; exists {
		t.Error("expected title to be stripped from cleaned schema")
	}
}

func TestCovertOpenAI2Gemini_ExtraBodyThinkingAndImageConfig(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}

	extraBody, _ := json.Marshal(map[string]interface{}{
		"google": map[string]interface{}{
			"thinking_config": map[string]interface{}{"thinking_budget": float64(4096), "include_thoughts": true},
			"image_config":    map[string]interface{}{"aspect_ratio": "16:9", "image_size": "2K"},
		},
	})
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: extraBody,
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GenerationConfig.ThinkingConfig == nil || *result.GenerationConfig.ThinkingConfig.ThinkingBudget != 4096 {
		t.Fatalf("ThinkingConfig = %+v", result.GenerationConfig.ThinkingConfig)
	}
	var imgCfg map[string]interface{}
	if err := json.Unmarshal(result.GenerationConfig.ImageConfig, &imgCfg); err != nil {
		t.Fatalf("failed to unmarshal ImageConfig: %v", err)
	}
	if imgCfg["aspectRatio"] != "16:9" || imgCfg["imageSize"] != "2K" {
		t.Errorf("ImageConfig = %+v", imgCfg)
	}
}

func TestCovertOpenAI2Gemini_ExtraBodyErrorParamNames(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}

	tests := []struct {
		name      string
		body      map[string]interface{}
		wantError string
	}{
		{
			name:      "camelCase thinkingConfig rejected",
			body:      map[string]interface{}{"google": map[string]interface{}{"thinkingConfig": map[string]interface{}{}}},
			wantError: "extra_body.google.thinkingConfig is not supported",
		},
		{
			name: "camelCase thinkingBudget rejected",
			body: map[string]interface{}{"google": map[string]interface{}{
				"thinking_config": map[string]interface{}{"thinkingBudget": float64(10)},
			}},
			wantError: "extra_body.google.thinking_config.thinkingBudget is not supported",
		},
		{
			name:      "camelCase imageConfig rejected",
			body:      map[string]interface{}{"google": map[string]interface{}{"imageConfig": map[string]interface{}{}}},
			wantError: "extra_body.google.imageConfig is not supported",
		},
		{
			name: "camelCase aspectRatio rejected",
			body: map[string]interface{}{"google": map[string]interface{}{
				"image_config": map[string]interface{}{"aspectRatio": "1:1"},
			}},
			wantError: "extra_body.google.image_config.aspectRatio is not supported",
		},
		{
			name: "camelCase imageSize rejected",
			body: map[string]interface{}{"google": map[string]interface{}{
				"image_config": map[string]interface{}{"imageSize": "1K"},
			}},
			wantError: "extra_body.google.image_config.imageSize is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extraBody, _ := json.Marshal(tt.body)
			req := dto.GeneralOpenAIRequest{
				Messages:  []dto.Message{{Role: "user", Content: "hi"}},
				ExtraBody: extraBody,
			}
			_, err := CovertOpenAI2Gemini(c, req, info)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("err = %v, want contains %q", err, tt.wantError)
			}
		})
	}
}

func TestCovertOpenAI2Gemini_InvalidExtraBodyJSON(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		// intentionally malformed
		ExtraBody: json.RawMessage(`{not valid json`),
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "invalid extra body") {
		t.Fatalf("err = %v", err)
	}
}

func TestCovertOpenAI2Gemini_ImagineModelSetsResponseModalities(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-exp-image-generation"}}
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "draw a cat"}}}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.GenerationConfig.ResponseModalities) != 2 {
		t.Fatalf("ResponseModalities = %v", result.GenerationConfig.ResponseModalities)
	}
}

func TestCovertOpenAI2Gemini_MessagesWithoutContentSkipped(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "user", Content: ""},
		},
	}
	result, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 0 {
		t.Errorf("expected no contents for an empty-text message, got %+v", result.Contents)
	}
}

// ---------------------------------------------------------------------------
// hasFunctionCallContent
// ---------------------------------------------------------------------------

func TestHasFunctionCallContent(t *testing.T) {
	tests := []struct {
		name string
		call *dto.FunctionCall
		want bool
	}{
		{"nil call", nil, false},
		{"named function", &dto.FunctionCall{FunctionName: "foo"}, true},
		{"blank name, nil args", &dto.FunctionCall{Arguments: nil}, false},
		{"blank name, blank string args", &dto.FunctionCall{Arguments: "   "}, false},
		{"blank name, non-blank string args", &dto.FunctionCall{Arguments: "x"}, true},
		{"blank name, empty map args", &dto.FunctionCall{Arguments: map[string]interface{}{}}, false},
		{"blank name, non-empty map args", &dto.FunctionCall{Arguments: map[string]interface{}{"a": 1}}, true},
		{"blank name, empty slice args", &dto.FunctionCall{Arguments: []interface{}{}}, false},
		{"blank name, non-empty slice args", &dto.FunctionCall{Arguments: []interface{}{1}}, true},
		{"blank name, other-typed args", &dto.FunctionCall{Arguments: 42}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasFunctionCallContent(tt.call); got != tt.want {
				t.Errorf("hasFunctionCallContent(%+v) = %v, want %v", tt.call, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// cleanFunctionParameters / removeAdditionalPropertiesWithDepth
// ---------------------------------------------------------------------------

func TestCleanFunctionParameters(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		if got := cleanFunctionParameters(nil); got != nil {
			t.Errorf("cleanFunctionParameters(nil) = %v, want nil", got)
		}
	})

	t.Run("primitive passthrough", func(t *testing.T) {
		if got := cleanFunctionParameters(42); got != 42 {
			t.Errorf("cleanFunctionParameters(42) = %v, want 42", got)
		}
	})

	t.Run("removes unsupported root fields and recurses", func(t *testing.T) {
		params := map[string]interface{}{
			"type":                 "object",
			"default":              "x",
			"exclusiveMaximum":     10,
			"exclusiveMinimum":     0,
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string", "format": "custom"},
				"date": map[string]interface{}{"type": "string", "format": "date-time"},
				"code": map[string]interface{}{"type": "string", "format": "enum"},
			},
			"items": map[string]interface{}{"type": "string", "default": "y"},
			"allOf": []interface{}{map[string]interface{}{"default": "z"}},
		}
		cleaned := cleanFunctionParameters(params).(map[string]interface{})
		for _, key := range []string{"default", "exclusiveMaximum", "exclusiveMinimum", "$schema", "additionalProperties"} {
			if _, exists := cleaned[key]; exists {
				t.Errorf("expected %q to be removed", key)
			}
		}
		props := cleaned["properties"].(map[string]interface{})
		nameProp := props["name"].(map[string]interface{})
		if _, exists := nameProp["format"]; exists {
			t.Error("expected non-enum/date-time format to be stripped")
		}
		dateProp := props["date"].(map[string]interface{})
		if dateProp["format"] != "date-time" {
			t.Error("expected date-time format to be preserved")
		}
		codeProp := props["code"].(map[string]interface{})
		if codeProp["format"] != "enum" {
			t.Error("expected enum format to be preserved")
		}
		items := cleaned["items"].(map[string]interface{})
		if _, exists := items["default"]; exists {
			t.Error("expected items.default to be recursively removed")
		}
		allOf := cleaned["allOf"].([]interface{})
		if _, exists := allOf[0].(map[string]interface{})["default"]; exists {
			t.Error("expected allOf[0].default to be recursively removed")
		}
	})

	t.Run("handles items array, patternProperties, definitions, $defs, conditionals", func(t *testing.T) {
		params := map[string]interface{}{
			"items":             []interface{}{map[string]interface{}{"default": "a"}},
			"patternProperties": map[string]interface{}{"^x": map[string]interface{}{"default": "b"}},
			"definitions":       map[string]interface{}{"Foo": map[string]interface{}{"default": "c"}},
			"$defs":             map[string]interface{}{"Bar": map[string]interface{}{"default": "d"}},
			"if":                map[string]interface{}{"default": "e"},
		}
		cleaned := cleanFunctionParameters(params).(map[string]interface{})
		itemsArr := cleaned["items"].([]interface{})
		if _, exists := itemsArr[0].(map[string]interface{})["default"]; exists {
			t.Error("expected items array element default to be removed")
		}
		pp := cleaned["patternProperties"].(map[string]interface{})
		if _, exists := pp["^x"].(map[string]interface{})["default"]; exists {
			t.Error("expected patternProperties nested default to be removed")
		}
		defs := cleaned["definitions"].(map[string]interface{})
		if _, exists := defs["Foo"].(map[string]interface{})["default"]; exists {
			t.Error("expected definitions nested default to be removed")
		}
		dollarDefs := cleaned["$defs"].(map[string]interface{})
		if _, exists := dollarDefs["Bar"].(map[string]interface{})["default"]; exists {
			t.Error("expected $defs nested default to be removed")
		}
		ifClause := cleaned["if"].(map[string]interface{})
		if _, exists := ifClause["default"]; exists {
			t.Error("expected if-clause nested default to be removed")
		}
	})

	t.Run("array input recurses per element", func(t *testing.T) {
		input := []interface{}{
			map[string]interface{}{"default": "x"},
			"plain string",
		}
		cleaned := cleanFunctionParameters(input).([]interface{})
		if _, exists := cleaned[0].(map[string]interface{})["default"]; exists {
			t.Error("expected array element default to be removed")
		}
		if cleaned[1] != "plain string" {
			t.Errorf("cleaned[1] = %v", cleaned[1])
		}
	})
}

func TestRemoveAdditionalPropertiesWithDepth(t *testing.T) {
	t.Run("non-map passthrough", func(t *testing.T) {
		if got := removeAdditionalPropertiesWithDepth("x", 0); got != "x" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("empty map passthrough", func(t *testing.T) {
		empty := map[string]interface{}{}
		if got := removeAdditionalPropertiesWithDepth(empty, 0); len(got.(map[string]interface{})) != 0 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("depth limit stops recursion", func(t *testing.T) {
		schema := map[string]interface{}{"type": "object", "title": "should-survive-if-depth-capped"}
		got := removeAdditionalPropertiesWithDepth(schema, 5)
		gotMap := got.(map[string]interface{})
		if gotMap["title"] != "should-survive-if-depth-capped" {
			t.Error("expected schema to be returned unmodified once depth limit reached")
		}
	})

	t.Run("non object/array type returned unmodified aside from title/$schema strip", func(t *testing.T) {
		schema := map[string]interface{}{"type": "string", "title": "Foo", "$schema": "x"}
		got := removeAdditionalPropertiesWithDepth(schema, 0).(map[string]interface{})
		if _, exists := got["title"]; exists {
			t.Error("expected title removed even for non object/array type")
		}
		if got["type"] != "string" {
			t.Error("expected type=string preserved")
		}
	})

	t.Run("object type strips additionalProperties and recurses into properties/allOf", func(t *testing.T) {
		schema := map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"nested": map[string]interface{}{"type": "object", "additionalProperties": true, "title": "nested-title"},
			},
			"allOf": []interface{}{
				map[string]interface{}{"type": "object", "additionalProperties": true},
			},
		}
		got := removeAdditionalPropertiesWithDepth(schema, 0).(map[string]interface{})
		if _, exists := got["additionalProperties"]; exists {
			t.Error("expected additionalProperties removed at top level")
		}
		nested := got["properties"].(map[string]interface{})["nested"].(map[string]interface{})
		if _, exists := nested["additionalProperties"]; exists {
			t.Error("expected additionalProperties removed in nested properties")
		}
		allOfEntry := got["allOf"].([]interface{})[0].(map[string]interface{})
		if _, exists := allOfEntry["additionalProperties"]; exists {
			t.Error("expected additionalProperties removed in allOf entries")
		}
	})

	t.Run("array type recurses into items", func(t *testing.T) {
		schema := map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "object", "additionalProperties": true},
		}
		got := removeAdditionalPropertiesWithDepth(schema, 0).(map[string]interface{})
		items := got["items"].(map[string]interface{})
		if _, exists := items["additionalProperties"]; exists {
			t.Error("expected additionalProperties removed within array items")
		}
	})
}

// ---------------------------------------------------------------------------
// unescapeString / unescapeMapOrSlice
// ---------------------------------------------------------------------------

func TestUnescapeString(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"no escapes", "plain text", "plain text", false},
		{"quote escape", `a\"b`, `a"b`, false},
		{"backslash escape", `a\\b`, `a\b`, false},
		{"forward slash escape", `a\/b`, `a/b`, false},
		{"backspace escape", `a\bb`, "a\bb", false},
		{"formfeed escape", `a\fb`, "a\fb", false},
		{"newline escape", `a\nb`, "a\nb", false},
		{"carriage return escape", `a\rb`, "a\rb", false},
		{"tab escape", `a\tb`, "a\tb", false},
		{"single quote escape", `a\'b`, `a'b`, false},
		{"unknown escape kept literal", `a\xb`, `a\xb`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := unescapeString(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("unescapeString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnescapeMapOrSlice(t *testing.T) {
	t.Run("map values recursively unescaped", func(t *testing.T) {
		input := map[string]interface{}{"a": `x\ny`}
		got := unescapeMapOrSlice(input).(map[string]interface{})
		if got["a"] != "x\ny" {
			t.Errorf("got[a] = %q", got["a"])
		}
	})

	t.Run("slice values recursively unescaped", func(t *testing.T) {
		input := []interface{}{`a\tb`, `c`}
		got := unescapeMapOrSlice(input).([]interface{})
		if got[0] != "a\tb" || got[1] != "c" {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("non string/map/slice passthrough", func(t *testing.T) {
		if got := unescapeMapOrSlice(42); got != 42 {
			t.Errorf("got = %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// getResponseToolCall
// ---------------------------------------------------------------------------

func TestGetResponseToolCall(t *testing.T) {
	t.Run("map arguments marshalled and unescaped", func(t *testing.T) {
		part := &dto.GeminiPart{FunctionCall: &dto.FunctionCall{
			FunctionName: "get_weather",
			Arguments:    map[string]interface{}{"city": `ny\nc`},
		}}
		call := getResponseToolCall(part)
		if call == nil {
			t.Fatal("expected non-nil ToolCallResponse")
		}
		if call.Function.Name != "get_weather" {
			t.Errorf("Function.Name = %q", call.Function.Name)
		}
		if !strings.HasPrefix(call.ID, "call_") {
			t.Errorf("ID = %q, want call_ prefix", call.ID)
		}
		if call.Type != "function" {
			t.Errorf("Type = %q, want function", call.Type)
		}
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			t.Fatalf("failed to unmarshal Arguments: %v", err)
		}
		if args["city"] != "ny\nc" {
			t.Errorf("args[city] = %q, want unescaped newline", args["city"])
		}
	})

	t.Run("non-map arguments marshalled directly", func(t *testing.T) {
		part := &dto.GeminiPart{FunctionCall: &dto.FunctionCall{
			FunctionName: "noop",
			Arguments:    "raw-string-args",
		}}
		call := getResponseToolCall(part)
		if call == nil {
			t.Fatal("expected non-nil ToolCallResponse")
		}
		if call.Function.Arguments != `"raw-string-args"` {
			t.Errorf("Arguments = %q", call.Function.Arguments)
		}
	})
}

// ---------------------------------------------------------------------------
// responseGeminiChat2OpenAI
// ---------------------------------------------------------------------------

func TestResponseGeminiChat2OpenAI(t *testing.T) {
	c, _ := newGeminiTestContext()

	stop := "STOP"
	maxTokens := "MAX_TOKENS"
	other := "SAFETY"

	t.Run("text content with STOP finish reason", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{
				{
					Index:        0,
					FinishReason: &stop,
					Content:      dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hello"}, {Text: "\n"}}},
				},
			},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if len(got.Choices) != 1 {
			t.Fatalf("len(Choices) = %d", len(got.Choices))
		}
		if got.Choices[0].FinishReason != "stop" {
			t.Errorf("FinishReason = %q", got.Choices[0].FinishReason)
		}
		if got.Choices[0].StringContent() != "hello" {
			t.Errorf("content = %q, want hello (blank lines filtered)", got.Choices[0].StringContent())
		}
	})

	t.Run("max tokens finish reason mapped to length", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &maxTokens, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if got.Choices[0].FinishReason != "length" {
			t.Errorf("FinishReason = %q, want length", got.Choices[0].FinishReason)
		}
	})

	t.Run("unrecognized finish reason mapped to content_filter", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &other, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if got.Choices[0].FinishReason != "content_filter" {
			t.Errorf("FinishReason = %q, want content_filter", got.Choices[0].FinishReason)
		}
	})

	t.Run("function call sets tool_calls finish reason", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &stop,
				Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
					{FunctionCall: &dto.FunctionCall{FunctionName: "foo", Arguments: map[string]interface{}{}}},
				}},
			}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if got.Choices[0].FinishReason != "tool_calls" {
			t.Errorf("FinishReason = %q, want tool_calls", got.Choices[0].FinishReason)
		}
		if got.Choices[0].ToolCalls == nil {
			t.Error("expected ToolCalls to be set")
		}
	})

	t.Run("thought part sets ReasoningContent", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &stop,
				Content:      dto.GeminiChatContent{Parts: []dto.GeminiPart{{Thought: true, Text: "thinking..."}}},
			}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if got.Choices[0].ReasoningContent != "thinking..." {
			t.Errorf("ReasoningContent = %q", got.Choices[0].ReasoningContent)
		}
	})

	t.Run("inline image data rendered as markdown", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &stop,
				Content:      dto.GeminiChatContent{Parts: []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AAA"}}}},
			}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if !strings.Contains(got.Choices[0].StringContent(), "![image](data:image/png;base64,AAA)") {
			t.Errorf("content = %q", got.Choices[0].StringContent())
		}
	})

	t.Run("inline non-image media rendered as media link", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &stop,
				Content:      dto.GeminiChatContent{Parts: []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "BBB"}}}},
			}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		if !strings.Contains(got.Choices[0].StringContent(), "[media](data:audio/wav;base64,BBB)") {
			t.Errorf("content = %q", got.Choices[0].StringContent())
		}
	})

	t.Run("executable code and result rendered as code blocks", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &stop,
				Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
					{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "python", Code: "print(1)"}},
					{CodeExecutionResult: &dto.GeminiPartCodeExecutionResult{Output: "1"}},
				}},
			}},
		}
		got := responseGeminiChat2OpenAI(c, resp)
		content := got.Choices[0].StringContent()
		if !strings.Contains(content, "```python\nprint(1)\n```") {
			t.Errorf("content missing code block: %q", content)
		}
		if !strings.Contains(content, "```output\n1\n```") {
			t.Errorf("content missing output block: %q", content)
		}
	})
}

// ---------------------------------------------------------------------------
// streamResponseGeminiChat2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseGeminiChat2OpenAI(t *testing.T) {
	stop := "STOP"
	maxTokens := "MAX_TOKENS"

	t.Run("STOP finish reason reports isStop and clears FinishReason", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
		}
		got, isStop := streamResponseGeminiChat2OpenAI(resp)
		if !isStop {
			t.Error("expected isStop = true")
		}
		if got.Choices[0].FinishReason != nil {
			t.Errorf("FinishReason = %v, want nil after STOP special-case", got.Choices[0].FinishReason)
		}
	})

	t.Run("MAX_TOKENS mapped to length and isStop false", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &maxTokens, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
		}
		got, isStop := streamResponseGeminiChat2OpenAI(resp)
		if isStop {
			t.Error("expected isStop = false for MAX_TOKENS")
		}
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "length" {
			t.Errorf("FinishReason = %v, want length", got.Choices[0].FinishReason)
		}
	})

	t.Run("function call sets tool_calls finish reason and delta", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "foo", Arguments: map[string]interface{}{}}},
			}}}},
		}
		got, _ := streamResponseGeminiChat2OpenAI(resp)
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "tool_calls" {
			t.Errorf("FinishReason = %v, want tool_calls", got.Choices[0].FinishReason)
		}
		if len(got.Choices[0].Delta.ToolCalls) != 1 {
			t.Fatalf("len(ToolCalls) = %d, want 1", len(got.Choices[0].Delta.ToolCalls))
		}
	})

	t.Run("thought part sets reasoning content on delta", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Thought: true, Text: "musing"}}}}},
		}
		got, _ := streamResponseGeminiChat2OpenAI(resp)
		if got.Choices[0].Delta.GetReasoningContent() != "musing" {
			t.Errorf("ReasoningContent = %q", got.Choices[0].Delta.GetReasoningContent())
		}
	})

	t.Run("inline image data rendered in delta content", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AAA"}},
			}}}},
		}
		got, _ := streamResponseGeminiChat2OpenAI(resp)
		if !strings.Contains(got.Choices[0].Delta.GetContentString(), "![image]") {
			t.Errorf("content = %q", got.Choices[0].Delta.GetContentString())
		}
	})

	t.Run("executable code rendered in delta content", func(t *testing.T) {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "go", Code: "fmt.Println(1)"}},
				{CodeExecutionResult: &dto.GeminiPartCodeExecutionResult{Output: "1"}},
			}}}},
		}
		got, _ := streamResponseGeminiChat2OpenAI(resp)
		content := got.Choices[0].Delta.GetContentString()
		if !strings.Contains(content, "```go\nfmt.Println(1)\n```") {
			t.Errorf("content missing code block: %q", content)
		}
		if !strings.Contains(content, "```output\n1\n```") {
			t.Errorf("content missing output block: %q", content)
		}
	})
}

// ---------------------------------------------------------------------------
// Handlers operating on a fake http.Response body (hermetic, no live network)
// ---------------------------------------------------------------------------

func fakeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

func TestGeminiTextGenerationHandler(t *testing.T) {
	c, w := newGeminiTestContext()
	body, _ := json.Marshal(dto.GeminiChatResponse{
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
	})
	resp := fakeResponse(string(body))
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiTextGenerationHandler(c, &relaycommon.RelayInfo{}, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", usage)
	}
	if w.Body.Len() == 0 {
		t.Error("expected response body to be copied to writer")
	}
}

func TestGeminiTextGenerationHandler_BadJSON(t *testing.T) {
	c, _ := newGeminiTestContext()
	resp := fakeResponse("not json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := GeminiTextGenerationHandler(c, &relaycommon.RelayInfo{}, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestNativeGeminiEmbeddingHandler(t *testing.T) {
	t.Run("batch embedding mode", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiBatchEmbeddingResponse{Embeddings: []*dto.ContentEmbedding{{Values: []float64{0.1, 0.2}}}})
		info := &relaycommon.RelayInfo{IsGeminiBatchEmbedding: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"}}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := NativeGeminiEmbeddingHandler(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
	})

	t.Run("single embedding mode", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiEmbeddingResponse{Embedding: dto.ContentEmbedding{Values: []float64{0.3}}})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"}}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := NativeGeminiEmbeddingHandler(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
	})

	t.Run("bad JSON in batch mode", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		info := &relaycommon.RelayInfo{IsGeminiBatchEmbedding: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		resp := fakeResponse("not json")
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := NativeGeminiEmbeddingHandler(c, resp, info)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON body")
		}
	})
}

func TestGeminiEmbeddingHandler(t *testing.T) {
	c, w := newGeminiTestContext()
	body, _ := json.Marshal(dto.GeminiBatchEmbeddingResponse{Embeddings: []*dto.ContentEmbedding{
		{Values: []float64{1, 2}},
		{Values: []float64{3, 4}},
	}})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "text-embedding-004"}}
	resp := fakeResponse(string(body))
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	var openAIResp dto.OpenAIEmbeddingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &openAIResp); err != nil {
		t.Fatalf("failed to unmarshal written response: %v", err)
	}
	if len(openAIResp.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(openAIResp.Data))
	}
	if openAIResp.Data[0].Index != 0 || openAIResp.Data[1].Index != 1 {
		t.Errorf("indexes = %d, %d", openAIResp.Data[0].Index, openAIResp.Data[1].Index)
	}
}

func TestGeminiEmbeddingHandler_BadJSON(t *testing.T) {
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := fakeResponse("not json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := GeminiEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestGeminiImageHandler(t *testing.T) {
	c, w := newGeminiTestContext()
	body, _ := json.Marshal(dto.GeminiImageResponse{Predictions: []dto.GeminiImagePrediction{
		{BytesBase64Encoded: "AAA"},
		{BytesBase64Encoded: "BBB", RaiFilteredReason: "unsafe"}, // filtered out
	}})
	resp := fakeResponse(string(body))
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiImageHandler(c, &relaycommon.RelayInfo{}, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 258 || usage.TotalTokens != 258 {
		t.Errorf("usage = %+v, want single non-filtered image billed at 258 tokens", usage)
	}
	var imgResp dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &imgResp); err != nil {
		t.Fatalf("failed to unmarshal written response: %v", err)
	}
	if len(imgResp.Data) != 1 || imgResp.Data[0].B64Json != "AAA" {
		t.Errorf("Data = %+v, want single unfiltered image", imgResp.Data)
	}
}

func TestGeminiImageHandler_NoImagesReturnsError(t *testing.T) {
	c, _ := newGeminiTestContext()
	body, _ := json.Marshal(dto.GeminiImageResponse{Predictions: nil})
	resp := fakeResponse(string(body))
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := GeminiImageHandler(c, &relaycommon.RelayInfo{}, resp)
	if apiErr == nil || !strings.Contains(apiErr.Error(), "no images generated") {
		t.Fatalf("apiErr = %v", apiErr)
	}
}

func TestGeminiImageHandler_BadJSON(t *testing.T) {
	c, _ := newGeminiTestContext()
	resp := fakeResponse("not json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := GeminiImageHandler(c, &relaycommon.RelayInfo{}, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestGeminiChatHandler(t *testing.T) {
	stop := "STOP"

	t.Run("valid response with RelayFormatOpenAI", func(t *testing.T) {
		c, w := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiChatResponse{
			Candidates:    []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
			UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 3, TotalTokenCount: 8},
		})
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatOpenAI,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := GeminiChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.CompletionTokens != 5 {
			t.Errorf("CompletionTokens = %d, want 5 (8-3)", usage.CompletionTokens)
		}
		if w.Body.Len() == 0 {
			t.Error("expected written response body")
		}
	})

	t.Run("valid response with RelayFormatClaude", func(t *testing.T) {
		c, w := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiChatResponse{
			Candidates:    []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
			UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 1, TotalTokenCount: 2},
		})
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatClaude,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := GeminiChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		var claudeResp dto.ClaudeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &claudeResp); err != nil {
			t.Fatalf("expected written body to be a Claude-format response: %v", err)
		}
	})

	t.Run("valid response with RelayFormatGemini passes body through unchanged", func(t *testing.T) {
		c, w := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiChatResponse{
			Candidates:    []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
			UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 1, TotalTokenCount: 2},
		})
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatGemini,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := GeminiChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		if w.Body.Len() == 0 {
			t.Error("expected the original Gemini-format body to be copied to the writer")
		}
	})

	t.Run("empty candidates with block reason", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		blockReason := "SAFETY"
		body, _ := json.Marshal(dto.GeminiChatResponse{
			PromptFeedback: &dto.GeminiChatPromptFeedback{BlockReason: &blockReason},
		})
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiChatHandler(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, resp)
		if apiErr == nil || !strings.Contains(apiErr.Error(), "request blocked by Gemini API") {
			t.Fatalf("apiErr = %v", apiErr)
		}
	})

	t.Run("empty candidates without block reason", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiChatResponse{})
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiChatHandler(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, resp)
		if apiErr == nil || !strings.Contains(apiErr.Error(), "empty response from Gemini API") {
			t.Fatalf("apiErr = %v", apiErr)
		}
	})

	t.Run("bad JSON body", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		resp := fakeResponse("not json")
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiChatHandler(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON body")
		}
	})
}

// ---------------------------------------------------------------------------
// GeminiTTSHandler
// ---------------------------------------------------------------------------

func TestGeminiTTSHandler(t *testing.T) {
	t.Run("upstream error status", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		resp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(bytes.NewReader([]byte("boom")))}
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiTTSHandler(c, &relaycommon.RelayInfo{}, resp)
		if apiErr == nil || !strings.Contains(apiErr.Error(), "gemini TTS upstream error") {
			t.Fatalf("apiErr = %v", apiErr)
		}
	})

	t.Run("bad JSON body", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		resp := fakeResponse("not json")
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiTTSHandler(c, &relaycommon.RelayInfo{}, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON body")
		}
	})

	t.Run("gemini error object surfaced", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body := `{"error":{"message":"quota exceeded","code":429}}`
		resp := fakeResponse(body)
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiTTSHandler(c, &relaycommon.RelayInfo{}, resp)
		if apiErr == nil || !strings.Contains(apiErr.Error(), "quota exceeded") {
			t.Fatalf("apiErr = %v", apiErr)
		}
	})

	t.Run("no audio in response", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body := `{"candidates":[{"content":{"parts":[{}]},"finishReason":"OTHER"}]}`
		resp := fakeResponse(body)
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := GeminiTTSHandler(c, &relaycommon.RelayInfo{}, resp)
		if apiErr == nil || !strings.Contains(apiErr.Error(), "no audio") {
			t.Fatalf("apiErr = %v", apiErr)
		}
	})

	t.Run("audio decoded and written", func(t *testing.T) {
		c, w := newGeminiTestContext()
		body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"aGVsbG8="}}]},"finishReason":"STOP"}]}`
		resp := fakeResponse(body)
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := GeminiTTSHandler(c, &relaycommon.RelayInfo{}, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		if w.Body.String() != "hello" {
			t.Errorf("written body = %q, want %q", w.Body.String(), "hello")
		}
		if w.Header().Get("Content-Type") != "audio/wav" {
			t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
		}
	})
}

func TestMapVoiceToGemini(t *testing.T) {
	tests := []struct{ in, want string }{
		{"alloy", "Kore"},
		{"echo", "Charon"},
		{"fable", "Fenrir"},
		{"onyx", "Orus"},
		{"nova", "Aoede"},
		{"shimmer", "Zephyr"},
		{"AlreadyGeminiVoice", "AlreadyGeminiVoice"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := mapVoiceToGemini(tt.in); got != tt.want {
				t.Errorf("mapVoiceToGemini(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsMostlyCJK(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"pure CJK", "你好世界你好世界", true},
		{"pure ASCII", "hello world", false},
		{"mixed mostly CJK", "你好世界你好世界ab", true},
		{"mixed mostly ASCII", "hello 你", false},
		{"empty string", "", false},
		{"whitespace only", "   ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMostlyCJK(tt.text); got != tt.want {
				t.Errorf("isMostlyCJK(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestConvertAudioRequestToGemini(t *testing.T) {
	t.Run("CJK text gets ASCII prefix", func(t *testing.T) {
		reader, err := ConvertAudioRequestToGemini(dto.AudioRequest{Input: "你好世界你好", Voice: "alloy"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := io.ReadAll(reader)
		var req geminiTTSRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("failed to unmarshal request: %v", err)
		}
		if !strings.HasPrefix(req.Contents[0].Parts[0].Text, "Hello. ") {
			t.Errorf("Text = %q, want Hello. prefix for CJK text", req.Contents[0].Parts[0].Text)
		}
		if req.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
			t.Errorf("VoiceName = %q, want Kore", req.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName)
		}
	})

	t.Run("ASCII text unchanged", func(t *testing.T) {
		reader, err := ConvertAudioRequestToGemini(dto.AudioRequest{Input: "hello there", Voice: "nova"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := io.ReadAll(reader)
		var req geminiTTSRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("failed to unmarshal request: %v", err)
		}
		if req.Contents[0].Parts[0].Text != "hello there" {
			t.Errorf("Text = %q, want unchanged", req.Contents[0].Parts[0].Text)
		}
	})
}

// ---------------------------------------------------------------------------
// GeminiChatStreamHandler / GeminiTextGenerationStreamHandler (SSE, hermetic)
// ---------------------------------------------------------------------------

func TestGeminiChatStreamHandler(t *testing.T) {
	withStreamingTimeout(t)
	c, w := newGeminiTestContext()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}

	chunk1, _ := json.Marshal(dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hel"}}}}},
	})
	stop := "STOP"
	chunk2, _ := json.Marshal(dto.GeminiChatResponse{
		Candidates:    []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "lo"}}}}},
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 2, TotalTokenCount: 6},
	})

	stream := "data: " + string(chunk1) + "\n\n" + "data: " + string(chunk2) + "\n\n"
	resp := fakeResponse(stream)
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 2 {
		t.Errorf("PromptTokens = %d, want 2", usage.PromptTokens)
	}
	if !strings.Contains(w.Body.String(), "hel") || !strings.Contains(w.Body.String(), "lo") {
		t.Errorf("written body missing streamed text: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("written body missing [DONE] terminator: %q", w.Body.String())
	}
}

func TestGeminiChatStreamHandler_ToolCallInFirstChunk(t *testing.T) {
	withStreamingTimeout(t)
	c, w := newGeminiTestContext()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}

	stop := "STOP"
	chunk, _ := json.Marshal(dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			FinishReason: &stop,
			Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "get_weather", Arguments: map[string]interface{}{"city": "nyc"}}},
			}},
		}},
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 3, TotalTokenCount: 10},
	})
	stream := "data: " + string(chunk) + "\n\n"
	resp := fakeResponse(stream)
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 3 {
		t.Errorf("PromptTokens = %d, want 3", usage.PromptTokens)
	}
	if !strings.Contains(w.Body.String(), "get_weather") {
		t.Errorf("written body missing tool call name: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("written body missing [DONE] terminator: %q", w.Body.String())
	}
}

func TestGeminiChatStreamHandler_MalformedChunkPropagatesError(t *testing.T) {
	withStreamingTimeout(t)
	c, _ := newGeminiTestContext()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}
	stream := "data: not-json-at-all\n\n"
	resp := fakeResponse(stream)
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := GeminiChatStreamHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error from malformed SSE JSON chunk")
	}
}

func TestGeminiTextGenerationStreamHandler(t *testing.T) {
	withStreamingTimeout(t)
	c, w := newGeminiTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	chunk, _ := json.Marshal(dto.GeminiChatResponse{
		Candidates:    []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "raw passthrough"}}}}},
		UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 1, TotalTokenCount: 4},
	})
	stream := "data: " + string(chunk) + "\n\n"
	resp := fakeResponse(stream)
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := GeminiTextGenerationStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 1 {
		t.Errorf("PromptTokens = %d, want 1", usage.PromptTokens)
	}
	if !strings.Contains(w.Body.String(), "raw passthrough") {
		t.Errorf("written body = %q, want raw event data passthrough", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatch branches (still hermetic: fake resp body only)
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("native embedContent path", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiEmbeddingResponse{Embedding: dto.ContentEmbedding{Values: []float64{1}}})
		info := &relaycommon.RelayInfo{
			RelayMode:      constant.RelayModeGemini,
			RequestURLPath: "/v1beta/models/embedding-001:embedContent",
			ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("imagen path", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiImageResponse{Predictions: []dto.GeminiImagePrediction{{BytesBase64Encoded: "AAA"}}})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-002"}}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("audio speech path", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"aGk="}}]},"finishReason":"STOP"}]}`
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-tts"}}
		resp := fakeResponse(body)
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("embedding compat path", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiBatchEmbeddingResponse{Embeddings: []*dto.ContentEmbedding{{Values: []float64{1}}}})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "text-embedding-004"}}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("openai-compat non-stream chat path", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		stop := "STOP"
		body, _ := json.Marshal(dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}},
		})
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatOpenAI,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("openai-compat stream chat path", func(t *testing.T) {
		withStreamingTimeout(t)
		c, _ := newGeminiTestContext()
		chunk, _ := json.Marshal(dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}}}})
		stream := "data: " + string(chunk) + "\n\n"
		info := &relaycommon.RelayInfo{
			IsStream:    true,
			RelayFormat: types.RelayFormatOpenAI,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(stream)
		defer func() { _ = resp.Body.Close() }()
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("native gemini generateContent path routes to GeminiTextGenerationHandler", func(t *testing.T) {
		c, _ := newGeminiTestContext()
		body, _ := json.Marshal(dto.GeminiChatResponse{
			UsageMetadata: dto.GeminiUsageMetadata{PromptTokenCount: 5, TotalTokenCount: 9},
		})
		info := &relaycommon.RelayInfo{
			RelayMode:      constant.RelayModeGemini,
			RequestURLPath: "/v1beta/models/gemini-2.0-flash:generateContent",
			ChannelMeta:    &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		}
		resp := fakeResponse(string(body))
		defer func() { _ = resp.Body.Close() }()
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
	})
}
