package jimeng

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		baseUrl string
		want    string
	}{
		{"plain base url", "https://visual.volcengineapi.com", "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31"},
		{"empty base url", "", "/?Action=CVProcess&Version=2022-08-31"},
		{"base url with trailing slash", "https://visual.volcengineapi.com/", "https://visual.volcengineapi.com//?Action=CVProcess&Version=2022-08-31"},
	}
	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tt.baseUrl}}
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
// SetupRequestHeader / GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestSetupRequestHeader_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	header := http.Header{}
	err := a.SetupRequestHeader(c, &header, &relaycommon.RelayInfo{})
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestGetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != 1 || got[0] != "jimeng_high_aes_general_v21_L" {
		t.Errorf("GetModelList() = %v, want [jimeng_high_aes_general_v21_L]", got)
	}
}

func TestGetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "jimeng" {
		t.Errorf("GetChannelName() = %q, want %q", got, "jimeng")
	}
}

func TestConstants(t *testing.T) {
	if ChannelName != "jimeng" {
		t.Errorf("ChannelName = %q, want jimeng", ChannelName)
	}
	if len(ModelList) != 1 || ModelList[0] != "jimeng_high_aes_general_v21_L" {
		t.Errorf("ModelList = %v", ModelList)
	}
}

// ---------------------------------------------------------------------------
// Convert* stubs
// ---------------------------------------------------------------------------

func TestConvertGeminiRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertGeminiRequest(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertClaudeRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertClaudeRequest(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertRerankRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertEmbeddingRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertAudioRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	if got != nil {
		t.Errorf("expected nil reader, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertOpenAIResponsesRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestInit_NoOp(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic and must not mutate info.
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.com"}}
	a.Init(info)
	if info.ChannelBaseUrl != "https://example.com" {
		t.Errorf("Init unexpectedly mutated info: %+v", info)
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("expected 'request is nil' error, got %v", err)
		}
	})

	t.Run("non-nil request returned as-is", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "jimeng_high_aes_general_v21_L"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq != req {
			t.Errorf("expected same pointer returned, got different: %p vs %p", gotReq, req)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("default response format defaults to url", func(t *testing.T) {
		req := dto.ImageRequest{Model: "jimeng_high_aes_general_v21_L", Prompt: "a cat"}
		got, err := a.ConvertImageRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload, ok := got.(imageRequestPayload)
		if !ok {
			t.Fatalf("expected imageRequestPayload, got %T", got)
		}
		if payload.ReqKey != "jimeng_high_aes_general_v21_L" || payload.Prompt != "a cat" {
			t.Errorf("unexpected payload fields: %+v", payload)
		}
		if !payload.ReturnURL {
			t.Errorf("expected ReturnURL=true for empty response_format, got false")
		}
	})

	t.Run("explicit url response format", func(t *testing.T) {
		req := dto.ImageRequest{Model: "m", Prompt: "p", ResponseFormat: "url"}
		got, err := a.ConvertImageRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if !payload.ReturnURL {
			t.Errorf("expected ReturnURL=true for response_format=url")
		}
	})

	t.Run("non-url response format leaves ReturnURL false", func(t *testing.T) {
		req := dto.ImageRequest{Model: "m", Prompt: "p", ResponseFormat: "b64_json"}
		got, err := a.ConvertImageRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if payload.ReturnURL {
			t.Errorf("expected ReturnURL=false for response_format=b64_json")
		}
	})

	t.Run("extra fields override payload", func(t *testing.T) {
		req := dto.ImageRequest{
			Model:       "m",
			Prompt:      "p",
			ExtraFields: json.RawMessage(`{"width":768,"height":768,"use_sr":true,"seed":42}`),
		}
		got, err := a.ConvertImageRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if payload.Width != 768 || payload.Height != 768 || !payload.UseSR || payload.Seed != 42 {
			t.Errorf("extra fields not applied: %+v", payload)
		}
		// Prompt/ReqKey should still be preserved from the base payload since
		// extra fields don't override them.
		if payload.ReqKey != "m" || payload.Prompt != "p" {
			t.Errorf("base fields lost after extra field merge: %+v", payload)
		}
	})

	t.Run("invalid extra fields returns error", func(t *testing.T) {
		req := dto.ImageRequest{
			Model:       "m",
			Prompt:      "p",
			ExtraFields: json.RawMessage(`{not valid json`),
		}
		got, err := a.ConvertImageRequest(nil, nil, req)
		if got != nil {
			t.Errorf("expected nil result on error, got %v", got)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to unmarshal extra fields") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest — hermetic early-return branches only (no live network)
// ---------------------------------------------------------------------------

func TestDoRequest_GetRequestURLPropagatesButSignFailsOnBadApiKey(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://visual.volcengineapi.com",
			ApiKey:         "not-a-valid-key-without-pipe",
		},
	}
	got, err := a.DoRequest(c, info, strings.NewReader(""))
	if got != nil {
		t.Errorf("expected nil result on Sign failure, got %v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("expected 'setup request header failed' wrapping error, got %v", err)
	}
}

func TestDoRequest_InvalidMethodFailsAtNewRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	// A control character in the method makes http.NewRequest fail before
	// Sign / the network are ever reached.
	c.Request.Method = "BAD METHOD\x00"
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com"}}
	got, err := a.DoRequest(c, info, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "new request failed") {
		t.Fatalf("expected 'new request failed' wrapping error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Sign / SetPayloadHash / getPayloadHash
// ---------------------------------------------------------------------------

func TestSign_InvalidApiKeyFormat(t *testing.T) {
	c, _ := newGinContext()
	req := httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess", nil)
	err := Sign(c, req, "no-pipe-here")
	if err == nil || err.Error() != "invalid api key format for jimeng: expected 'ak|sk'" {
		t.Fatalf("expected invalid api key format error, got %v", err)
	}
}

func TestSign_InvalidApiKeyFormat_TooManyParts(t *testing.T) {
	c, _ := newGinContext()
	req := httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess", nil)
	err := Sign(c, req, "a|b|c")
	if err == nil || err.Error() != "invalid api key format for jimeng: expected 'ak|sk'" {
		t.Fatalf("expected invalid api key format error, got %v", err)
	}
}

func TestSign_ValidKeySetsAuthorizationHeader(t *testing.T) {
	c, _ := newGinContext()
	req := httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31", strings.NewReader(`{"prompt":"x"}`))
	err := Sign(c, req, " ak123 | sk456 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "HMAC-SHA256 Credential=ak123/") {
		t.Errorf("Authorization header missing expected credential prefix: %q", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-content-sha256;x-date") {
		t.Errorf("Authorization header missing expected signed headers: %q", auth)
	}
	if req.Header.Get("Host") == "" {
		t.Errorf("expected Host header to be set")
	}
	if req.Header.Get("X-Date") == "" {
		t.Errorf("expected X-Date header to be set")
	}
	if req.Header.Get("X-Content-Sha256") == "" {
		t.Errorf("expected X-Content-Sha256 header to be set")
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected default Content-Type application/json, got %q", req.Header.Get("Content-Type"))
	}
	// Body must be rewound and still readable after Sign consumed it.
	body := req.Body
	if body == nil {
		t.Fatalf("expected non-nil rewound body")
	}
}

func TestSign_PreservesExistingContentType(t *testing.T) {
	c, _ := newGinContext()
	req := httptest.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVProcess", nil)
	req.Header.Set("Content-Type", "text/plain")
	err := Sign(c, req, "ak|sk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("expected Content-Type to be preserved, got %q", req.Header.Get("Content-Type"))
	}
}

func TestSetPayloadHash_And_GetPayloadHash(t *testing.T) {
	c, _ := newGinContext()
	type sample struct {
		Foo string `json:"foo"`
	}
	err := SetPayloadHash(c, sample{Foo: "bar"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hash := getPayloadHash(c)
	if hash == "" {
		t.Errorf("expected non-empty payload hash")
	}
	// SHA256 hex digest is always 64 chars.
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex sha256 digest, got %d chars: %q", len(hash), hash)
	}
}

func TestGetPayloadHash_UnsetReturnsEmpty(t *testing.T) {
	c, _ := newGinContext()
	if got := getPayloadHash(c); got != "" {
		t.Errorf("expected empty string for unset payload hash, got %q", got)
	}
}

func TestSetPayloadHash_MarshalErrorOnUnsupportedType(t *testing.T) {
	c, _ := newGinContext()
	// channels are not JSON-marshalable and cause json.Marshal to fail.
	err := SetPayloadHash(c, make(chan int))
	if err == nil {
		t.Fatalf("expected marshal error for unsupported type, got nil")
	}
}

// ---------------------------------------------------------------------------
// responseJimeng2OpenAIImage
// ---------------------------------------------------------------------------

func TestResponseJimeng2OpenAIImage(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	info := &relaycommon.RelayInfo{StartTime: start}
	resp := &ImageResponse{}
	resp.Data.BinaryDataBase64 = []string{"base64data1"}
	resp.Data.ImageUrls = []string{"https://img.example.com/a.png"}

	got := responseJimeng2OpenAIImage(nil, resp, info)
	if got.Created != start.Unix() {
		t.Errorf("Created = %d, want %d", got.Created, start.Unix())
	}
	if len(got.Data) != 2 {
		t.Fatalf("expected 2 data entries (1 base64 + 1 url), got %d", len(got.Data))
	}
	if got.Data[0].B64Json != "base64data1" {
		t.Errorf("expected first entry to be base64 data, got %+v", got.Data[0])
	}
	if got.Data[1].Url != "https://img.example.com/a.png" {
		t.Errorf("expected second entry to be image url, got %+v", got.Data[1])
	}
}

func TestResponseJimeng2OpenAIImage_Empty(t *testing.T) {
	info := &relaycommon.RelayInfo{StartTime: time.Unix(1000, 0)}
	resp := &ImageResponse{}
	got := responseJimeng2OpenAIImage(nil, resp, info)
	if len(got.Data) != 0 {
		t.Errorf("expected empty Data slice, got %v", got.Data)
	}
}

// ---------------------------------------------------------------------------
// jimengImageHandler
// ---------------------------------------------------------------------------

func TestJimengImageHandler_BadJSON(t *testing.T) {
	c, _ := newGinContext()
	resp := httptest.NewRecorder().Result()
	resp.Body = readCloserFrom("not json at all")
	usage, apiErr := jimengImageHandler(c, resp, &relaycommon.RelayInfo{})
	if usage != nil {
		t.Errorf("expected nil usage on bad json, got %v", usage)
	}
	if apiErr == nil {
		t.Fatalf("expected error for invalid json body")
	}
}

func TestJimengImageHandler_ErrorCode(t *testing.T) {
	c, _ := newGinContext()
	body := `{"code":50500,"message":"internal error"}`
	resp := httptest.NewRecorder().Result()
	resp.StatusCode = http.StatusInternalServerError
	resp.Body = readCloserFrom(body)
	usage, apiErr := jimengImageHandler(c, resp, &relaycommon.RelayInfo{})
	if usage != nil {
		t.Errorf("expected nil usage on error response, got %v", usage)
	}
	if apiErr == nil {
		t.Fatalf("expected error for non-10000 code")
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
}

func TestJimengImageHandler_Success(t *testing.T) {
	c, w := newGinContext()
	body := `{"code":10000,"message":"success","data":{"image_urls":["https://img.example.com/x.png"]}}`
	resp := httptest.NewRecorder().Result()
	resp.StatusCode = http.StatusOK
	resp.Body = readCloserFrom(body)
	info := &relaycommon.RelayInfo{StartTime: time.Now()}
	usage, apiErr := jimengImageHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage on success")
	}
	if w.Code != http.StatusOK {
		t.Errorf("response writer status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "img.example.com") {
		t.Errorf("response body missing expected image url: %s", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", w.Header().Get("Content-Type"))
	}
}

func readCloserFrom(s string) *closerReader {
	return &closerReader{Reader: strings.NewReader(s)}
}

type closerReader struct {
	*strings.Reader
}

func (c *closerReader) Close() error { return nil }

// ---------------------------------------------------------------------------
// DoResponse — hermetic error path exercised indirectly via RelayMode routing
// ---------------------------------------------------------------------------

func TestDoResponse_ImagesGenerationsRoutesToJimengHandler(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	body := `{"code":10000,"message":"success","data":{"image_urls":["https://img.example.com/y.png"]}}`
	resp := httptest.NewRecorder().Result()
	resp.StatusCode = http.StatusOK
	resp.Body = readCloserFrom(body)
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Now(),
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage for images-generations path")
	}
}
