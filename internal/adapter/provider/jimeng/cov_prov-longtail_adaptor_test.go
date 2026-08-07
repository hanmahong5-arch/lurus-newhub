package jimeng

import (
	"encoding/json"
	"io"
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

func newJimengBody(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

func provLongtailJimengCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: business contract is a fixed action/version query string
// appended to the channel base URL, regardless of trailing slash or path.
// ---------------------------------------------------------------------------

func TestJimeng_GetRequestURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"no trailing slash", "https://visual.volcengineapi.com", "https://visual.volcengineapi.com/?Action=CVProcess&Version=2022-08-31"},
		{"trailing slash", "https://visual.volcengineapi.com/", "https://visual.volcengineapi.com//?Action=CVProcess&Version=2022-08-31"},
		{"with path", "https://proxy.example.com/jimeng", "https://proxy.example.com/jimeng/?Action=CVProcess&Version=2022-08-31"},
		{"empty base url", "", "/?Action=CVProcess&Version=2022-08-31"},
	}
	a := &Adaptor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tc.baseURL}}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("GetRequestURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: nil guard + pass-through business contract
// ---------------------------------------------------------------------------

func TestJimeng_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.ConvertOpenAIRequest(c, info, nil)
	if err == nil {
		t.Fatal("expected error for nil request, got nil")
	}
	if got != nil {
		t.Errorf("expected nil result on error, got %v", got)
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error = %q, want mention of nil", err.Error())
	}
}

func TestJimeng_ConvertOpenAIRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "jimeng_high_aes_general_v21_L"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected pass-through of the same pointer, got a different value")
	}
}

// ---------------------------------------------------------------------------
// ConvertImageRequest: default response_format handling + extra_fields override
// ---------------------------------------------------------------------------

func TestJimeng_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("default response format returns url flag", func(t *testing.T) {
		c, _ := provLongtailJimengCtx()
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{Model: "jimeng_high_aes_general_v21_L", Prompt: "a cat"}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload, ok := got.(imageRequestPayload)
		if !ok {
			t.Fatalf("expected imageRequestPayload, got %T", got)
		}
		if payload.ReqKey != "jimeng_high_aes_general_v21_L" {
			t.Errorf("ReqKey = %q, want model name propagated", payload.ReqKey)
		}
		if payload.Prompt != "a cat" {
			t.Errorf("Prompt = %q, want %q", payload.Prompt, "a cat")
		}
		if !payload.ReturnURL {
			t.Error("ReturnURL should default to true when response_format is empty")
		}
	})

	t.Run("explicit url response format", func(t *testing.T) {
		c, _ := provLongtailJimengCtx()
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{Model: "m", Prompt: "p", ResponseFormat: "url"}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if !payload.ReturnURL {
			t.Error("ReturnURL should be true for response_format=url")
		}
	})

	t.Run("b64_json response format does not force ReturnURL", func(t *testing.T) {
		c, _ := provLongtailJimengCtx()
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{Model: "m", Prompt: "p", ResponseFormat: "b64_json"}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if payload.ReturnURL {
			t.Error("ReturnURL should stay false for response_format=b64_json (business: caller wants base64, not a URL)")
		}
	})

	t.Run("extra_fields overrides width/height and can flip return url", func(t *testing.T) {
		c, _ := provLongtailJimengCtx()
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:       "m",
			Prompt:      "p",
			ExtraFields: json.RawMessage(`{"width":768,"height":512,"return_url":false,"use_sr":true}`),
		}
		got, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload := got.(imageRequestPayload)
		if payload.Width != 768 || payload.Height != 512 {
			t.Errorf("Width/Height = %d/%d, want 768/512 from extra_fields", payload.Width, payload.Height)
		}
		if payload.ReturnURL {
			t.Error("extra_fields explicitly set return_url=false, must override the response_format default")
		}
		if !payload.UseSR {
			t.Error("extra_fields use_sr=true must be applied")
		}
		// Prompt/ReqKey set before unmarshal must survive since extra_fields doesn't mention them
		if payload.Prompt != "p" || payload.ReqKey != "m" {
			t.Errorf("base fields clobbered: prompt=%q reqKey=%q", payload.Prompt, payload.ReqKey)
		}
	})

	t.Run("malformed extra_fields JSON returns error", func(t *testing.T) {
		c, _ := provLongtailJimengCtx()
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:       "m",
			Prompt:      "p",
			ExtraFields: json.RawMessage(`{not-json`),
		}
		_, err := a.ConvertImageRequest(c, info, req)
		if err == nil {
			t.Fatal("expected error for malformed extra_fields JSON")
		}
		if !strings.Contains(err.Error(), "unmarshal") {
			t.Errorf("error = %q, want mention of unmarshal failure", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Not-implemented surfaces: business contract is a stable, explicit error
// rather than a panic or nil-nil silent pass, for every unsupported capability.
// ---------------------------------------------------------------------------

func TestJimeng_UnimplementedSurfaces(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	if _, err := a.ConvertGeminiRequest(c, info, nil); err == nil {
		t.Error("ConvertGeminiRequest should error (not implemented)")
	}
	if _, err := a.ConvertClaudeRequest(c, info, nil); err == nil {
		t.Error("ConvertClaudeRequest should error (not implemented)")
	}
	if err := a.SetupRequestHeader(c, &http.Header{}, info); err == nil {
		t.Error("SetupRequestHeader should error (not implemented; signing happens in DoRequest instead)")
	}
	if _, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err == nil {
		t.Error("ConvertRerankRequest should error (not implemented)")
	}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest should error (not implemented)")
	}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest should error (not implemented)")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest should error (not implemented)")
	}

	// Init must be a safe no-op.
	a.Init(info)
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName: static catalog surface used by the channel
// picker; a wrong/empty answer here silently removes jimeng from routing.
// ---------------------------------------------------------------------------

func TestJimeng_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "jimeng" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "jimeng")
	}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "jimeng_high_aes_general_v21_L" {
		t.Errorf("GetModelList() = %v, want [jimeng_high_aes_general_v21_L]", models)
	}
}

// ---------------------------------------------------------------------------
// DoRequest: signing failure surfaces as an error (bad api key format), and
// the request method/URL used for signing must match the inbound request.
// ---------------------------------------------------------------------------

func TestJimeng_DoRequest_InvalidApiKeyFormat(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com", ApiKey: "no-pipe-separator"}}
	_, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for malformed api key (must be ak|sk)")
	}
	if !strings.Contains(err.Error(), "setup request header failed") {
		t.Errorf("error = %q, want wrapped 'setup request header failed'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// DoResponse: dispatch by RelayMode/IsStream. We only assert the image
// generation branch end-to-end here (the stream/non-stream OpenAI branches
// belong to the openai package); this locks the routing decision itself.
// ---------------------------------------------------------------------------

func TestJimeng_DoResponse_ImageGeneration_Success(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Unix(1700000000, 0),
	}
	body := `{"code":10000,"message":"success","data":{"image_urls":["https://x/img.png"]},"status":10000}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}
	resp.Body = newJimengBody(body)

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("expected non-nil usage envelope on success")
	}
	if w.Code != 200 {
		t.Errorf("recorder status = %d, want 200", w.Code)
	}
	var out dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].Url != "https://x/img.png" {
		t.Errorf("Data = %+v, want single entry with url https://x/img.png", out.Data)
	}
	if out.Created != 1700000000 {
		t.Errorf("Created = %d, want 1700000000 (from info.StartTime)", out.Created)
	}
}

func TestJimeng_DoResponse_ImageGeneration_UpstreamError(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	body := `{"code":50411,"message":"content policy violation","status":50411}`
	resp := &http.Response{StatusCode: 400, Body: newJimengBody(body)}

	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for non-10000 upstream code")
	}
	oaiErr := apiErr.ToOpenAIError()
	if oaiErr.Message != "content policy violation" {
		t.Errorf("message = %q, want upstream message propagated", oaiErr.Message)
	}
	if oaiErr.Code != "50411" {
		t.Errorf("code = %v, want %q (stringified upstream code)", oaiErr.Code, "50411")
	}
}

func TestJimeng_DoResponse_ImageGeneration_MalformedJSON(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailJimengCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	resp := &http.Response{StatusCode: 200, Body: newJimengBody("not json{{{")}

	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream JSON body")
	}
}
