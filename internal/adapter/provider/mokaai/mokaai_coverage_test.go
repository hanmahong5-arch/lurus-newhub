package mokaai

import (
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

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name       string
		baseUrl    string
		modelName  string
		wantSuffix string
	}{
		{"m3e-large model uses embeddings suffix", "https://aip.baidubce.com", "m3e-large", "https://aip.baidubce.com/embeddings"},
		{"m3e-base model uses embeddings suffix", "https://aip.baidubce.com", "m3e-base", "https://aip.baidubce.com/embeddings"},
		{"m3e prefix generic uses embeddings suffix", "https://aip.baidubce.com", "m3e-anything", "https://aip.baidubce.com/embeddings"},
		{"non m3e model uses chat suffix", "https://aip.baidubce.com", "ernie-bot", "https://aip.baidubce.com/chat/"},
		{"empty model name uses chat suffix", "https://aip.baidubce.com", "", "https://aip.baidubce.com/chat/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    tt.baseUrl,
					UpstreamModelName: tt.modelName,
				},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("GetRequestURL() unexpected error: %v", err)
			}
			if got != tt.wantSuffix {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantSuffix)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "application/json")

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "sk-test-key",
		},
	}
	req := &http.Header{}
	err := a.SetupRequestHeader(c, req, info)
	if err != nil {
		t.Fatalf("SetupRequestHeader() unexpected error: %v", err)
	}
	if got := req.Get("Authorization"); got != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-test-key")
	}
	if got := req.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type header = %q, want %q", got, "application/json")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	t.Run("nil request returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings}
		got, err := a.ConvertOpenAIRequest(c, info, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("err = %v, want %q", err, "request is nil")
		}
	})

	t.Run("embeddings mode converts request", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings}
		req := &dto.GeneralOpenAIRequest{
			Model: "m3e-large",
			Input: "hello world",
		}
		got, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		embReq, ok := got.(*dto.EmbeddingRequest)
		if !ok {
			t.Fatalf("expected *dto.EmbeddingRequest, got %T", got)
		}
		if embReq.Model != "m3e-large" {
			t.Errorf("Model = %q, want %q", embReq.Model, "m3e-large")
		}
		wantInput := []string{"hello world"}
		gotInput, ok := embReq.Input.([]string)
		if !ok || len(gotInput) != 1 || gotInput[0] != wantInput[0] {
			t.Errorf("Input = %#v, want %#v", embReq.Input, wantInput)
		}
	})

	t.Run("other mode returns not implemented error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
		req := &dto.GeneralOpenAIRequest{Model: "ernie-bot"}
		got, err := a.ConvertOpenAIRequest(c, info, req)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("err = %v, want %q", err, "not implemented")
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertEmbeddingRequest / ConvertRerankRequest (pass-through stubs)
// ---------------------------------------------------------------------------

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	req := dto.EmbeddingRequest{Model: "m3e-base", Input: []string{"a", "b"}}

	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest, got %T", got)
	}
	if gotReq.Model != "m3e-base" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "m3e-base")
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, constant.RelayModeRerank, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse (default branch only; embeddings covered by mokaEmbeddingHandler
// test below since it needs a real *http.Response and gin writer, no network)
// ---------------------------------------------------------------------------

func TestDoResponseDefaultMode(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}

	usage, err := a.DoResponse(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestDoResponseEmbeddingsMode(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"m3e-large","usage":{"prompt_tokens":5,"total_tokens":5}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, err := a.DoResponse(c, resp, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	dtoUsage, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if dtoUsage.PromptTokens != 5 || dtoUsage.TotalTokens != 5 {
		t.Errorf("usage = %+v, want prompt=5 total=5", dtoUsage)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	want := []string{"m3e-large", "m3e-base", "m3e-small"}
	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i, m := range want {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
}

func TestGetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "mokaai" {
		t.Errorf("GetChannelName() = %q, want %q", got, "mokaai")
	}
}

// ---------------------------------------------------------------------------
// embeddingRequestOpenAI2Moka
// ---------------------------------------------------------------------------

func TestEmbeddingRequestOpenAI2Moka(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{"string input", "hello", []string{"hello"}},
		{"[]string input", []string{"a", "b"}, []string{"a", "b"}},
		{"[]interface{} of strings", []interface{}{"x", "y"}, []string{"x", "y"}},
		{"[]interface{} with non-string skipped", []interface{}{"x", 42}, []string{"x"}},
		{"unsupported type yields nil", 12345, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := dto.GeneralOpenAIRequest{Model: "m3e-small", Input: tt.input}
			got := embeddingRequestOpenAI2Moka(req)
			if got.Model != "m3e-small" {
				t.Errorf("Model = %q, want %q", got.Model, "m3e-small")
			}
			gotInput, _ := got.Input.([]string)
			if len(gotInput) != len(tt.want) {
				t.Fatalf("Input = %#v, want %#v", got.Input, tt.want)
			}
			for i := range tt.want {
				if gotInput[i] != tt.want[i] {
					t.Errorf("Input[%d] = %q, want %q", i, gotInput[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// embeddingResponseMoka2OpenAI
// ---------------------------------------------------------------------------

func TestEmbeddingResponseMoka2OpenAI(t *testing.T) {
	src := &dto.EmbeddingResponse{
		Object: "list",
		Model:  "moka-source-model",
		Data: []dto.EmbeddingResponseItem{
			{Object: "embedding", Index: 0, Embedding: []float64{1, 2, 3}},
			{Object: "embedding", Index: 1, Embedding: []float64{4, 5, 6}},
		},
		Usage: dto.Usage{PromptTokens: 3, TotalTokens: 3},
	}
	got := embeddingResponseMoka2OpenAI(src)

	if got.Object != "list" {
		t.Errorf("Object = %q, want %q", got.Object, "list")
	}
	// Model is hardcoded to "baidu-embedding" regardless of source model name.
	if got.Model != "baidu-embedding" {
		t.Errorf("Model = %q, want %q", got.Model, "baidu-embedding")
	}
	if got.PromptTokens != 3 || got.TotalTokens != 3 {
		t.Errorf("Usage = %+v, want prompt=3 total=3", got.Usage)
	}
	if len(got.Data) != 2 {
		t.Fatalf("Data length = %d, want 2", len(got.Data))
	}
	if got.Data[0].Index != 0 || got.Data[1].Index != 1 {
		t.Errorf("Data indices = %d,%d, want 0,1", got.Data[0].Index, got.Data[1].Index)
	}
	if len(got.Data[0].Embedding) != 3 || got.Data[0].Embedding[0] != 1 {
		t.Errorf("Data[0].Embedding = %v, want [1 2 3]", got.Data[0].Embedding)
	}
}

func TestEmbeddingResponseMoka2OpenAIEmptyData(t *testing.T) {
	src := &dto.EmbeddingResponse{Object: "list", Data: []dto.EmbeddingResponseItem{}}
	got := embeddingResponseMoka2OpenAI(src)
	if len(got.Data) != 0 {
		t.Errorf("Data length = %d, want 0", len(got.Data))
	}
}

// ---------------------------------------------------------------------------
// mokaEmbeddingHandler error path: malformed JSON body
// ---------------------------------------------------------------------------

func TestMokaEmbeddingHandlerBadJSON(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not-json"))}

	usage, err := mokaEmbeddingHandler(c, info, resp)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if err == nil {
		t.Fatal("expected non-nil error for malformed JSON body")
	}
}
