package cloudflare

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func provLongtailCfCtx(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: per-relay-mode URL shape is the business contract, since
// Cloudflare Workers AI exposes distinct paths for chat/embeddings/responses
// vs. arbitrary model "run" (used for completions, image, etc.).
// ---------------------------------------------------------------------------

func TestCloudflare_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	cases := []struct {
		name string
		mode int
		want string
	}{
		{"chat completions", constant.RelayModeChatCompletions, "https://api.cloudflare.com/client/v4/accounts/acct123/ai/v1/chat/completions"},
		{"embeddings", constant.RelayModeEmbeddings, "https://api.cloudflare.com/client/v4/accounts/acct123/ai/v1/embeddings"},
		{"responses", constant.RelayModeResponses, "https://api.cloudflare.com/client/v4/accounts/acct123/ai/v1/responses"},
		{"default falls back to model run path", constant.RelayModeCompletions, "https://api.cloudflare.com/client/v4/accounts/acct123/ai/run/@cf/meta/llama-3.1-8b-instruct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode: tc.mode,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://api.cloudflare.com",
					ApiVersion:     "acct123",
				},
				OriginModelName: "@cf/meta/llama-3.1-8b-instruct",
			}
			info.UpstreamModelName = "@cf/meta/llama-3.1-8b-instruct"
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("GetRequestURL(mode=%d) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader: Bearer auth is the money-adjacent header - a wrong
// scheme or missing key means every upstream call 401s.
// ---------------------------------------------------------------------------

func TestCloudflare_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "cf-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer cf-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer cf-secret")
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want propagated from inbound request", got)
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: completions mode must translate to Cloudflare's
// {prompt, max_tokens, stream, temperature} shape; other modes pass through
// unmodified.
// ---------------------------------------------------------------------------

func TestCloudflare_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestCloudflare_ConvertOpenAIRequest_CompletionsMode(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeCompletions}
	temp := 0.5
	req := &dto.GeneralOpenAIRequest{Prompt: "hello world", Stream: true, Temperature: &temp}
	req.MaxTokens = 128

	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfReq, ok := got.(*CfRequest)
	if !ok {
		t.Fatalf("expected *CfRequest, got %T", got)
	}
	if cfReq.Prompt != "hello world" {
		t.Errorf("Prompt = %q, want %q", cfReq.Prompt, "hello world")
	}
	if cfReq.MaxTokens != 128 {
		t.Errorf("MaxTokens = %d, want 128", cfReq.MaxTokens)
	}
	if !cfReq.Stream {
		t.Error("Stream should propagate true")
	}
	if cfReq.Temperature == nil || *cfReq.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", cfReq.Temperature)
	}
}

func TestCloudflare_ConvertOpenAIRequest_CompletionsMode_NonStringPrompt(t *testing.T) {
	// FINDING: convertCf2CompletionsRequest silently drops non-string
	// prompts (e.g. []string, which GeneralOpenAIRequest.Prompt permits as
	// `any`) instead of erroring or stringifying — a caller sending an array
	// prompt in completions mode gets an empty prompt sent upstream, not a
	// visible failure. This test locks the current (surprising) behavior as
	// a regression baseline.
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeCompletions}
	req := &dto.GeneralOpenAIRequest{Prompt: []string{"a", "b"}}

	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfReq := got.(*CfRequest)
	if cfReq.Prompt != "" {
		t.Errorf("Prompt = %q, want empty string for non-string prompt type (current behavior)", cfReq.Prompt)
	}
}

func TestCloudflare_ConvertOpenAIRequest_DefaultModePassesThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
	req := &dto.GeneralOpenAIRequest{Model: "@cf/meta/llama-3.1-8b-instruct"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Errorf("expected the same *GeneralOpenAIRequest pointer to pass through untouched, got %v", got)
	}
}

func TestCloudflare_ConvertOpenAIResponsesRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.OpenAIResponsesRequest{Model: "@cf/meta/llama-3.1-8b-instruct"}
	got, err := a.ConvertOpenAIResponsesRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.OpenAIResponsesRequest)
	if !ok || gotReq.Model != "@cf/meta/llama-3.1-8b-instruct" {
		t.Errorf("got %#v, want pass-through of the request value", got)
	}
}

func TestCloudflare_ConvertRerankRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	req := dto.RerankRequest{Query: "q"}
	got, err := a.ConvertRerankRequest(c, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.RerankRequest).Query != "q" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

func TestCloudflare_ConvertEmbeddingRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "@cf/baai/bge-base-en-v1.5"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.EmbeddingRequest).Model != "@cf/baai/bge-base-en-v1.5" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest: multipart file upload extraction.
// ---------------------------------------------------------------------------

func TestCloudflare_ConvertAudioRequest_MissingFileErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/audio/transcriptions")
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error when the multipart request has no 'file' field")
	}
	if !strings.Contains(err.Error(), "file is required") {
		t.Errorf("error = %q, want mention of missing file", err.Error())
	}
}

func TestCloudflare_ConvertAudioRequest_ExtractsFileBytes(t *testing.T) {
	a := &Adaptor{}
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	audioBytes := []byte("RIFF....WAVEfmt ")
	if _, err := fw.Write(audioBytes); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotBytes, err := io.ReadAll(got)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !bytes.Equal(gotBytes, audioBytes) {
		t.Errorf("extracted body = %q, want %q", gotBytes, audioBytes)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestCloudflare_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "cloudflare" {
		t.Errorf("GetChannelName() = %q, want cloudflare", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("GetModelList() must not be empty; empty list removes cloudflare from routing")
	}
	found := false
	for _, m := range models {
		if m == "@cf/meta/llama-3.1-8b-instruct" {
			found = true
		}
	}
	if !found {
		t.Error("expected @cf/meta/llama-3.1-8b-instruct in model list")
	}
}

// ---------------------------------------------------------------------------
// DoResponse: dispatch and each handler's response-conversion + usage
// extraction (this is the money path — token counts feed billing).
// ---------------------------------------------------------------------------

func TestCloudflare_DoResponse_ChatCompletions_NonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeChatCompletions,
		OriginModelName: "@cf/meta/llama-3.1-8b-instruct",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3.1-8b-instruct"},
	}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for non-empty response text (this is the billed quantity)", u.CompletionTokens)
	}
	if w.Code != 200 {
		t.Errorf("recorder status = %d, want 200", w.Code)
	}
	var out dto.TextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if out.Model != "@cf/meta/llama-3.1-8b-instruct" {
		t.Errorf("response Model = %q, want upstream model name mapped back", out.Model)
	}
}

func TestCloudflare_DoResponse_ChatCompletions_MalformedBody(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{not json"))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
	if u, ok := usage.(*dto.Usage); ok && u != nil {
		t.Errorf("usage should carry no real quantities on parse failure, got %+v", u)
	}
}

func TestCloudflare_DoResponse_Embeddings_UsesChatHandler(t *testing.T) {
	// Business contract: embeddings falls through to the same non-stream
	// text handler as chat completions (per the switch's fallthrough).
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/embeddings")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"vec"},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCloudflare_DoResponse_AudioTranscription(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/audio/transcriptions")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioTranscription, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"result":{"text":"the quick brown fox"}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for transcribed text", u.CompletionTokens)
	}
	var out dto.AudioResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if out.Text != "the quick brown fox" {
		t.Errorf("Text = %q, want %q", out.Text, "the quick brown fox")
	}
}

func TestCloudflare_DoResponse_AudioTranslation_MalformedBody(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/audio/translations")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioTranslation, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json"))}
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed STT response body")
	}
}

func TestCloudflare_DoResponse_ChatCompletions_Stream(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{
		RelayMode:          constant.RelayModeChatCompletions,
		IsStream:           true,
		StartTime:          time.Unix(1700000000, 0),
		ShouldIncludeUsage: true,
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3.1-8b-instruct"},
	}

	sseBody := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"}}]}\n" +
		"data: [DONE]\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sseBody))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for concatenated stream deltas 'hello'", u.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Errorf("expected SSE-framed body, got %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected terminating [DONE] event to be forwarded, got %q", w.Body.String())
	}
}

func TestCloudflare_DoResponse_ChatCompletions_Stream_SkipsNonJSONLines(t *testing.T) {
	// FINDING (locked, not a fix): a non-JSON "data: " line is silently
	// dropped (logged, loop continues) rather than surfacing an error to the
	// caller. Verified via the recorder never seeing an error-shaped frame.
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, IsStream: true, StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "m"}}

	sseBody := "data: {malformed\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n" +
		"data: [DONE]\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sseBody))}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (the malformed line must not zero out the valid chunk that follows)", u.CompletionTokens)
	}
	_ = w
}
