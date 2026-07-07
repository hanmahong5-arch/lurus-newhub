package perplexity

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, w
}

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "perplexity" {
		t.Errorf("GetChannelName() = %q, want %q", got, "perplexity")
	}
	if got := a.GetChannelName(); got != ChannelName {
		t.Errorf("GetChannelName() = %q, want ChannelName constant %q", got, ChannelName)
	}
}

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	want := []string{
		"llama-3-sonar-small-32k-chat", "llama-3-sonar-small-32k-online", "llama-3-sonar-large-32k-chat", "llama-3-sonar-large-32k-online", "llama-3-8b-instruct", "llama-3-70b-instruct", "mixtral-8x7b-instruct",
		"sonar", "sonar-pro", "sonar-reasoning",
	}
	got := a.GetModelList()
	if len(got) != len(want) {
		t.Fatalf("GetModelList() len = %d, want %d", len(got), len(want))
	}
	for i, m := range want {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
}

func TestAdaptor_GetRequestURL(t *testing.T) {
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "standard base url",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.perplexity.ai"}},
			want: "https://api.perplexity.ai/chat/completions",
		},
		{
			name: "empty base url",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""}},
			want: "/chat/completions",
		},
		{
			name: "base url with trailing slash preserved as-is",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com/"}},
			want: "https://custom.example.com//chat/completions",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("GetRequestURL() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdaptor_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{})
	a.Init(nil)
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	c, _ := newTestContext()
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/plain")

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-key-123"}}
	h := make(http.Header)

	a := &Adaptor{}
	err := a.SetupRequestHeader(c, &h, info)
	if err != nil {
		t.Fatalf("SetupRequestHeader() unexpected error: %v", err)
	}

	if got := h.Get("Authorization"); got != "Bearer test-key-123" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer test-key-123")
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header = %q, want %q", got, "application/json")
	}
	if got := h.Get("Accept"); got != "text/plain" {
		t.Errorf("Accept header = %q, want %q", got, "text/plain")
	}
}

func TestAdaptor_SetupRequestHeader_StreamDefaultsAccept(t *testing.T) {
	c, _ := newTestContext()
	// no Content-Type/Accept set on the incoming request

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}, IsStream: true}
	h := make(http.Header)

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &h, info); err != nil {
		t.Fatalf("SetupRequestHeader() unexpected error: %v", err)
	}

	if got := h.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept header = %q, want %q (stream default)", got, "text/event-stream")
	}
	if got := h.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer k")
	}
}

func TestAdaptor_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
	if result != nil {
		t.Errorf("ConvertOpenAIRequest(nil) result = %v, want nil", result)
	}
	if err == nil || err.Error() != "request is nil" {
		t.Errorf("ConvertOpenAIRequest(nil) err = %v, want %q", err, "request is nil")
	}
}

func TestAdaptor_ConvertOpenAIRequest_TopPClamping(t *testing.T) {
	tests := []struct {
		name     string
		topP     float64
		wantTopP float64
	}{
		{name: "top_p equal to 1 clamped to 0.99", topP: 1, wantTopP: 0.99},
		{name: "top_p above 1 clamped to 0.99", topP: 1.5, wantTopP: 0.99},
		{name: "top_p below 1 left untouched", topP: 0.5, wantTopP: 0.5},
		{name: "top_p zero left untouched", topP: 0, wantTopP: 0},
	}

	c, _ := newTestContext()
	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &dto.GeneralOpenAIRequest{
				Model: "sonar",
				TopP:  tt.topP,
			}
			result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
			if err != nil {
				t.Fatalf("ConvertOpenAIRequest() unexpected error: %v", err)
			}
			converted, ok := result.(*dto.GeneralOpenAIRequest)
			if !ok {
				t.Fatalf("ConvertOpenAIRequest() result type = %T, want *dto.GeneralOpenAIRequest", result)
			}
			if converted.TopP != tt.wantTopP {
				t.Errorf("converted.TopP = %v, want %v", converted.TopP, tt.wantTopP)
			}
			// The clamping mutates the caller's request in place too since
			// ConvertOpenAIRequest dereferences and re-uses the same TopP field.
			if req.TopP != tt.wantTopP {
				t.Errorf("original req.TopP = %v, want %v (mutated in place)", req.TopP, tt.wantTopP)
			}
		})
	}
}

func TestAdaptor_ConvertOpenAIRequest_FieldMapping(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}

	name := "bob"
	req := &dto.GeneralOpenAIRequest{
		Model:                  "sonar-pro",
		Stream:                 true,
		Messages:               []dto.Message{{Role: "user", Content: "hello", Name: &name}},
		Temperature:            nil,
		TopP:                   0.3,
		MaxTokens:              111,
		FrequencyPenalty:       0.1,
		PresencePenalty:        0.2,
		SearchDomainFilter:     []byte(`["example.com"]`),
		SearchRecencyFilter:    "week",
		ReturnImages:           true,
		ReturnRelatedQuestions: true,
		SearchMode:             "academic",
	}

	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() unexpected error: %v", err)
	}
	got, ok := result.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest() result type = %T, want *dto.GeneralOpenAIRequest", result)
	}

	if got.Model != "sonar-pro" {
		t.Errorf("Model = %q, want %q", got.Model, "sonar-pro")
	}
	if !got.Stream {
		t.Error("Stream = false, want true")
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" || got.Messages[0].Content != "hello" {
		t.Errorf("Messages = %+v, want single user/hello message", got.Messages)
	}
	if got.MaxTokens != 111 {
		t.Errorf("MaxTokens = %d, want 111 (from GetMaxTokens fallback to MaxTokens)", got.MaxTokens)
	}
	if got.FrequencyPenalty != 0.1 {
		t.Errorf("FrequencyPenalty = %v, want 0.1", got.FrequencyPenalty)
	}
	if got.PresencePenalty != 0.2 {
		t.Errorf("PresencePenalty = %v, want 0.2", got.PresencePenalty)
	}
	if string(got.SearchDomainFilter) != `["example.com"]` {
		t.Errorf("SearchDomainFilter = %s, want %s", got.SearchDomainFilter, `["example.com"]`)
	}
	if got.SearchRecencyFilter != "week" {
		t.Errorf("SearchRecencyFilter = %q, want %q", got.SearchRecencyFilter, "week")
	}
	if !got.ReturnImages {
		t.Error("ReturnImages = false, want true")
	}
	if !got.ReturnRelatedQuestions {
		t.Error("ReturnRelatedQuestions = false, want true")
	}
	if got.SearchMode != "academic" {
		t.Errorf("SearchMode = %q, want %q", got.SearchMode, "academic")
	}
	// TopP below 1 is not clamped, verifying the requestOpenAI2Perplexity path
	// preserves whatever ConvertOpenAIRequest set before conversion.
	if got.TopP != 0.3 {
		t.Errorf("TopP = %v, want %v", got.TopP, 0.3)
	}
}

func TestAdaptor_ConvertOpenAIRequest_MaxCompletionTokensPreferred(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}
	req := &dto.GeneralOpenAIRequest{
		Model:               "sonar",
		MaxTokens:           50,
		MaxCompletionTokens: 200,
	}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() unexpected error: %v", err)
	}
	got := result.(*dto.GeneralOpenAIRequest)
	if got.MaxTokens != 200 {
		t.Errorf("MaxTokens = %d, want 200 (MaxCompletionTokens takes precedence)", got.MaxTokens)
	}
}

func TestAdaptor_NotImplementedStubs(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{}

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		result, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
		if result != nil {
			t.Errorf("result = %v, want nil", result)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		result, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		if result != nil {
			t.Errorf("result = %v, want nil", result)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		result, err := a.ConvertImageRequest(c, info, dto.ImageRequest{})
		if result != nil {
			t.Errorf("result = %v, want nil", result)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		result, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
		if result != nil {
			t.Errorf("result = %v, want nil", result)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		result, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
		if result != nil {
			t.Errorf("result = %v, want nil", result)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})
}

func TestAdaptor_ConvertRerankRequest_ReturnsNilNil(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}
	result, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if result != nil {
		t.Errorf("ConvertRerankRequest() result = %v, want nil", result)
	}
	if err != nil {
		t.Errorf("ConvertRerankRequest() err = %v, want nil", err)
	}
}

func TestAdaptor_ConvertClaudeRequest_DelegatesToOpenAI(t *testing.T) {
	c, _ := newTestContext()
	a := &Adaptor{}
	// A nil/empty ClaudeRequest routed through the embedded openai.Adaptor
	// should not panic; we only assert it returns without panicking and
	// produces *some* deterministic (result, err) pair reachable hermetically.
	result, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, &dto.ClaudeRequest{
		Model:     "sonar",
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("ConvertClaudeRequest() unexpected error: %v", err)
	}
	if result == nil {
		t.Error("ConvertClaudeRequest() result = nil, want non-nil converted request")
	}
}
