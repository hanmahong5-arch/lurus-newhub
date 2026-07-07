package jina

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

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		wantURL string
		wantErr string
	}{
		{
			name: "rerank mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeRerank,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
			},
			wantURL: "https://api.jina.ai/v1/rerank",
		},
		{
			name: "embeddings mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
			},
			wantURL: "https://api.jina.ai/v1/embeddings",
		},
		{
			name: "unsupported relay mode returns error",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
			},
			wantErr: "invalid relay mode",
		},
		{
			name:    "zero-value relay info falls into error branch",
			info:    &relaycommon.RelayInfo{},
			wantErr: "invalid relay mode",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if got != "" {
					t.Errorf("got = %q, want empty string on error", got)
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

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "jina-test-key"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := header.Get("Authorization"); got != "Bearer jina-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer jina-test-key")
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.GeneralOpenAIRequest{Model: "jina-clip-v1"}

	got, err := a.ConvertOpenAIRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected the same pointer to be returned")
	}
	if gotReq.Model != "jina-clip-v1" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "jina-clip-v1")
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.RerankRequest{Model: "jina-reranker-v2-base-multilingual"}

	got, err := a.ConvertRerankRequest(nil, constant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.RerankRequest)
	if !ok {
		t.Fatalf("expected dto.RerankRequest, got %T", got)
	}
	if gotReq.Model != "jina-reranker-v2-base-multilingual" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "jina-reranker-v2-base-multilingual")
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "jina-clip-v1", EncodingFormat: "base64"}

	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest, got %T", got)
	}
	if gotReq.EncodingFormat != "" {
		t.Errorf("EncodingFormat = %q, want empty string (must be stripped)", gotReq.EncodingFormat)
	}
	if gotReq.Model != "jina-clip-v1" {
		t.Errorf("Model = %q, want %q (rest of the request must be preserved)", gotReq.Model, "jina-clip-v1")
	}
}

// TestDoResponseUnknownRelayModeReturnsNil covers the fallthrough branch of
// DoResponse where RelayMode is neither rerank nor embeddings: both usage and
// err stay at their zero values without touching resp at all (no I/O needed).
func TestDoResponseUnknownRelayModeReturnsNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}

	usage, err := a.DoResponse(c, nil, info)
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// TestDoResponseEmbeddingsBadBodyIsError exercises the embeddings branch of
// DoResponse purely in-memory: an http.Response with a malformed JSON body
// (no network involved) must surface as a non-nil *types.NewAPIError.
func TestDoResponseEmbeddingsBadBodyIsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
		Header:     http.Header{},
	}

	usage, err := a.DoResponse(c, resp, info)
	if u, ok := usage.(*dto.Usage); !ok || u != nil {
		t.Errorf("usage = %#v, want a nil *dto.Usage on malformed body", usage)
	}
	if err == nil {
		t.Fatal("expected non-nil error for malformed embeddings response body")
	}
}

// TestDoResponseRerankBadBodyIsError exercises the rerank branch of
// DoResponse in-memory: a malformed JSON body must produce a bad-response
// error without any network I/O.
func TestDoResponseRerankBadBodyIsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
		Header:     http.Header{},
	}

	usage, err := a.DoResponse(c, resp, info)
	if u, ok := usage.(*dto.Usage); !ok || u != nil {
		t.Errorf("usage = %#v, want a nil *dto.Usage on malformed body", usage)
	}
	if err == nil {
		t.Fatal("expected non-nil error for malformed rerank response body")
	}
	if err.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusInternalServerError)
	}
	if err.Unwrap() == nil {
		// sanity: Unwrap must expose the underlying decode error so
		// errors.Is/As chains keep working through NewAPIError.
		t.Errorf("Unwrap() returned nil, want the wrapped decode error")
	}
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	wantModels := []string{"jina-clip-v1", "jina-reranker-v2-base-multilingual", "jina-reranker-m0"}
	gotModels := a.GetModelList()
	if len(gotModels) != len(wantModels) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(gotModels), len(wantModels))
	}
	for i, m := range wantModels {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}

	if got := a.GetChannelName(); got != "jina" {
		t.Errorf("GetChannelName() = %q, want %q", got, "jina")
	}
}
