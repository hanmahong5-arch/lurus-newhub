package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// wireOpenAIUpstream builds a gin.Context + RelayInfo wired to an OpenAI-format
// upstream test server. The channel is an OpenAI channel so InitChannelMeta
// resolves ApiType=OpenAI and GetAdaptor returns the real openai adaptor — the
// request is genuinely converted, serialized, and dispatched over HTTP.
func wireOpenAIUpstream(t *testing.T, srvURL, requestURLPath string, relayMode int, model string, request dto.Request) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srvURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)

	info := &relaycommon.RelayInfo{
		Request:         request,
		OriginModelName: model,
		RelayMode:       relayMode,
		RequestURLPath:  requestURLPath,
		StartTime:       time.Now(),
	}
	return c, info
}

// errBody is a minimal OpenAI-style error payload the RelayErrorHandler parses.
const upstreamErrBody = `{"error":{"message":"upstream boom","type":"invalid_request_error","code":"bad"}}`

func newErr500Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(upstreamErrBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestHandlers_UpstreamErrorClassified drives each relay helper through a real
// (converted + dispatched) request against a 500 upstream and asserts the
// upstream error is surfaced as a non-nil NewAPIError rather than a panic or a
// hollow success. This exercises request conversion, marshalling, DoRequest and
// the non-2xx error-classification branch for each provider format.
func TestHandlers_UpstreamErrorClassified(t *testing.T) {
	app.InitHttpClient()

	textReq := &dto.GeneralOpenAIRequest{
		Model:    "gpt-4o-mini",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	claudeReq := &dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 16,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: []byte(`[{"type":"text","text":"hi"}]`)}},
	}
	geminiReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{Text: "hi"}}}},
	}
	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	imgReq := &dto.ImageRequest{Model: "dall-e-3", Prompt: "a cat", N: 1}
	embReq := &dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	rerankReq := &dto.RerankRequest{Model: "rerank-1", Query: "q", Documents: []any{"d1", "d2"}}

	tests := []struct {
		name  string
		path  string
		mode  int
		model string
		req   dto.Request
		call  func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{"Text", "/v1/chat/completions", relayconstant.RelayModeChatCompletions, "gpt-4o-mini", textReq, TextHelper},
		{"Claude", "/v1/chat/completions", relayconstant.RelayModeChatCompletions, "claude-3-5-sonnet", claudeReq, ClaudeHelper},
		{"Gemini", "/v1/chat/completions", relayconstant.RelayModeChatCompletions, "gemini-2.5-flash", geminiReq, GeminiHelper},
		{"Responses", "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq, ResponsesHelper},
		{"Image", "/v1/images/generations", relayconstant.RelayModeImagesGenerations, "dall-e-3", imgReq, ImageHelper},
		{"Embedding", "/v1/embeddings", relayconstant.RelayModeEmbeddings, "text-embedding-3-small", embReq, EmbeddingHelper},
		{"Rerank", "/v1/rerank", relayconstant.RelayModeRerank, "rerank-1", rerankReq, RerankHelper},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newErr500Server(t)
			c, info := wireOpenAIUpstream(t, srv.URL, tc.path, tc.mode, tc.model, tc.req)
			err := tc.call(c, info)
			if err == nil {
				t.Fatalf("%s: expected upstream error, got nil", tc.name)
			}
		})
	}
}

// TestEmbeddingHelper_HappyPath drives EmbeddingHelper against a 200 upstream
// returning a valid embedding+usage payload. It proves the full success path:
// convert → dispatch → DoResponse (OpenaiHandler) → postConsumeQuota, observed
// via the user's incremented request count.
func TestEmbeddingHelper_HappyPath(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "emb-user", Quota: 1_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"completion_tokens":0,"total_tokens":5}}`))
	}))
	defer srv.Close()

	embReq := &dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/embeddings", relayconstant.RelayModeEmbeddings, "text-embedding-3-small", embReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	err := EmbeddingHelper(c, info)
	if err != nil {
		t.Fatalf("EmbeddingHelper happy path returned error: %v", err.Error())
	}

	var refreshed repo.User
	if dbErr := repo.DB.First(&refreshed, u.Id).Error; dbErr != nil {
		t.Fatalf("reload user: %v", dbErr)
	}
	if refreshed.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1 (post-consume ran)", refreshed.RequestCount)
	}
}

// TestAudioHelper_UpstreamError drives the TTS path (ConvertAudioRequest →
// DoApiRequest) against a 500 upstream and asserts the error surfaces.
func TestAudioHelper_UpstreamError(t *testing.T) {
	app.InitHttpClient()
	srv := newErr500Server(t)

	audioReq := &dto.AudioRequest{Model: "tts-1", Input: "hello world", Voice: "alloy"}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/audio/speech", relayconstant.RelayModeAudioSpeech, "tts-1", audioReq)
	if err := AudioHelper(c, info); err == nil {
		t.Fatalf("expected upstream error from AudioHelper, got nil")
	}
}

// TestAudioHelper_TTSHappyPath drives the TTS success tail: DoResponse
// (OpenaiTTSHandler streams the audio bytes) → post-consume billing.
func TestAudioHelper_TTSHappyPath(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "tts-user", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ID3fake-mp3-bytes"))
	}))
	defer srv.Close()

	audioReq := &dto.AudioRequest{Model: "tts-1", Input: "hello world", Voice: "alloy"}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/audio/speech", relayconstant.RelayModeAudioSpeech, "tts-1", audioReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	if err := AudioHelper(c, info); err != nil {
		t.Fatalf("AudioHelper TTS happy path returned error: %v", err.Error())
	}
	var refreshed repo.User
	if dbErr := repo.DB.First(&refreshed, u.Id).Error; dbErr != nil {
		t.Fatalf("reload user: %v", dbErr)
	}
	if refreshed.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1 (post-consume ran)", refreshed.RequestCount)
	}
}

// TestGeminiEmbeddingHandler_UpstreamError drives GeminiEmbeddingHandler (single
// embedContent) through body parse → convert → dispatch against a 500 upstream.
func TestGeminiEmbeddingHandler_UpstreamError(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()
	srv := newErr500Server(t)

	body := []byte(`{"model":"text-embedding-004","content":{"parts":[{"text":"hello"}]}}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/text-embedding-004:embedContent", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "text-embedding-004")

	info := &relaycommon.RelayInfo{OriginModelName: "text-embedding-004", RequestURLPath: "/v1/embeddings", StartTime: time.Now()}
	if err := GeminiEmbeddingHandler(c, info); err == nil {
		t.Fatalf("expected upstream error from GeminiEmbeddingHandler, got nil")
	}
}

// TestGeminiEmbeddingHandler_BatchUpstreamError covers the batch branch (path
// ends with batchEmbedContents) which parses a GeminiBatchEmbeddingRequest.
func TestGeminiEmbeddingHandler_BatchUpstreamError(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()
	srv := newErr500Server(t)

	body := []byte(`{"requests":[{"model":"text-embedding-004","content":{"parts":[{"text":"a"}]}}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/text-embedding-004:batchEmbedContents", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "text-embedding-004")

	info := &relaycommon.RelayInfo{OriginModelName: "text-embedding-004", RequestURLPath: "/v1/embeddings", StartTime: time.Now()}
	if err := GeminiEmbeddingHandler(c, info); err == nil {
		t.Fatalf("expected upstream error from batch GeminiEmbeddingHandler, got nil")
	}
	if !info.IsGeminiBatchEmbedding {
		t.Errorf("expected IsGeminiBatchEmbedding=true for batch path")
	}
}

// TestGeminiEmbeddingHandler_ParamOverride_SkipsLurusKeys is the lock for L5
// repair finding #17/#40/#46: GeminiEmbeddingHandler used to merge
// info.ParamOverride into the outbound body with a bespoke loop that never
// skipped __lurus_-prefixed internal control keys. As of this fix, the other
// non-test call sites in this package (claude/compatible/embedding/gemini
// (x2)/image/rerank/responses — `grep -rn 'ApplyParamOverride(' internal/app/relay
// | grep -v _test`) route through relaycommon.ApplyParamOverride instead;
// this file adds none of the structural enumeration those sites would need
// to stay true automatically, so re-grep before trusting this list again.
// Setting __lurus_force_http1 on a Gemini channel would leak that key into the
// request Google actually receives. This drives GeminiEmbeddingHandler
// end-to-end against a real httptest server that captures the raw request
// body, so a regression here (reverting to the direct-merge loop) turns
// this red rather than being caught only by a lower-level override.go test
// that this handler does not even call into.
func TestGeminiEmbeddingHandler_ParamOverride_SkipsLurusKeys(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(upstreamErrBody))
	}))
	defer srv.Close()

	body := []byte(`{"model":"text-embedding-004","content":{"parts":[{"text":"hello"}]}}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/text-embedding-004:embedContent", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "text-embedding-004")
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{
		"__lurus_force_http1":  true,
		"outputDimensionality": float64(256),
	})

	info := &relaycommon.RelayInfo{OriginModelName: "text-embedding-004", RequestURLPath: "/v1/embeddings", StartTime: time.Now()}
	if err := GeminiEmbeddingHandler(c, info); err == nil {
		t.Fatalf("expected upstream error from GeminiEmbeddingHandler, got nil")
	}

	if capturedBody == nil {
		t.Fatal("upstream never received a request body")
	}
	if strings.Contains(string(capturedBody), "__lurus_force_http1") {
		t.Errorf("__lurus_force_http1 leaked into the upstream request body: %s", capturedBody)
	}
	if !strings.Contains(string(capturedBody), "outputDimensionality") {
		t.Errorf("a real override key must still apply: %s", capturedBody)
	}
}

const chatCompletionBody = `{"id":"c1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`
const imageGenBody = `{"created":1,"data":[{"url":"https://cdn/img.png"}],"usage":{"prompt_tokens":5,"total_tokens":5}}`
const responsesBody = `{"id":"r1","object":"response","model":"gpt-4o-mini","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`

// TestHandlers_HappyPath drives each relay helper end-to-end against a 200
// upstream returning a valid provider payload, proving the success tail
// (DoResponse parse + write + post-consume billing) runs. The observable
// contract is the seeded user's request count being incremented exactly once.
func TestHandlers_HappyPath(t *testing.T) {
	textReq := &dto.GeneralOpenAIRequest{Model: "gpt-4o-mini", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	geminiReq := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{Text: "hi"}}}}}
	imgReq := &dto.ImageRequest{Model: "dall-e-3", Prompt: "a cat", N: 1}
	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}

	tests := []struct {
		name  string
		path  string
		mode  int
		model string
		body  string
		req   dto.Request
		call  func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{"Text", "/v1/chat/completions", relayconstant.RelayModeChatCompletions, "gpt-4o-mini", chatCompletionBody, textReq, TextHelper},
		{"Gemini", "/v1/chat/completions", relayconstant.RelayModeChatCompletions, "gemini", chatCompletionBody, geminiReq, GeminiHelper},
		{"Image", "/v1/images/generations", relayconstant.RelayModeImagesGenerations, "dall-e-3", imageGenBody, imgReq, ImageHelper},
		{"Responses", "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", responsesBody, respReq, ResponsesHelper},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := setupRelayDB(t)
			defer cleanup()
			app.InitHttpClient()

			u := &repo.User{Username: "happy-" + tc.name, Quota: 10_000_000}
			if err := repo.DB.Create(u).Error; err != nil {
				t.Fatalf("seed user: %v", err)
			}

			body := tc.body
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c, info := wireOpenAIUpstream(t, srv.URL, tc.path, tc.mode, tc.model, tc.req)
			c.Set("token_name", "tkn")
			info.UserId = u.Id

			err := tc.call(c, info)
			if err != nil {
				t.Fatalf("%s happy path returned error: %v", tc.name, err.Error())
			}

			var refreshed repo.User
			if dbErr := repo.DB.First(&refreshed, u.Id).Error; dbErr != nil {
				t.Fatalf("reload user: %v", dbErr)
			}
			if refreshed.RequestCount != 1 {
				t.Errorf("%s: RequestCount = %d, want 1", tc.name, refreshed.RequestCount)
			}
		})
	}
}
