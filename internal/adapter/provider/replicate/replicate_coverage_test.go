package replicate

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestGinContext(t *testing.T, method, path string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, body)
	return c, w
}

// ---------------------------------------------------------------------
// GetModelList / GetChannelName / Init
// ---------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	got := a.GetModelList()
	if len(got) != 1 || got[0] != "black-forest-labs/flux-1.1-pro" {
		t.Errorf("GetModelList() = %v, want [black-forest-labs/flux-1.1-pro]", got)
	}

	if name := a.GetChannelName(); name != "replicate" {
		t.Errorf("GetChannelName() = %q, want %q", name, "replicate")
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op and must not panic, even with nil info.
	a.Init(nil)
}

// ---------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	t.Run("nil info", func(t *testing.T) {
		a := &Adaptor{}
		url, err := a.GetRequestURL(nil)
		if err == nil || err.Error() != "replicate adaptor: relay info is nil" {
			t.Fatalf("err = %v, want nil-info error", err)
		}
		if url != "" {
			t.Errorf("url = %q, want empty", url)
		}
	})

	t.Run("empty base url and empty path uses default base", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.replicate.com" {
			t.Errorf("url = %q, want default base url", url)
		}
		if info.ChannelBaseUrl != "https://api.replicate.com" {
			t.Errorf("info.ChannelBaseUrl not populated with default, got %q", info.ChannelBaseUrl)
		}
	})

	t.Run("custom base url with empty path returns base url as-is", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://custom.example.com" {
			t.Errorf("url = %q, want custom base url", url)
		}
	})

	t.Run("default base url with request path concatenates", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			ChannelMeta:    &relaycommon.ChannelMeta{},
			RequestURLPath: "/v1/models/black-forest-labs/flux-1.1-pro/predictions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.replicate.com/v1/models/black-forest-labs/flux-1.1-pro/predictions"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("custom base url with request path concatenates", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			RequestURLPath: "/v1/predictions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://custom.example.com/v1/predictions" {
			t.Errorf("url = %q, want concatenated custom url", url)
		}
	})
}

// ---------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	t.Run("nil info errors", func(t *testing.T) {
		a := &Adaptor{}
		header := http.Header{}
		err := a.SetupRequestHeader(nil, &header, nil)
		if err == nil || err.Error() != "replicate adaptor: relay info is nil" {
			t.Fatalf("err = %v, want nil-info error", err)
		}
	})

	t.Run("empty api key errors", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		header := http.Header{}
		err := a.SetupRequestHeader(c, &header, info)
		if err == nil || err.Error() != "replicate adaptor: api key is required" {
			t.Fatalf("err = %v, want api-key-required error", err)
		}
	})

	t.Run("sets auth, prefer, and default content-type/accept", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		if got := header.Get("Prefer"); got != "wait" {
			t.Errorf("Prefer = %q, want wait", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
	})

	t.Run("preserves incoming content-type/accept from the client request", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		c.Request.Header.Set("Accept", "text/event-stream")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}}
		header := http.Header{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Content-Type"); got != "multipart/form-data; boundary=x" {
			t.Errorf("Content-Type = %q, want preserved value", got)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want preserved value", got)
		}
	})
}

// ---------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------

func TestConvertImageRequest_NilInfo(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	_, err := a.ConvertImageRequest(c, nil, dto.ImageRequest{Prompt: "x"})
	if err == nil || err.Error() != "replicate adaptor: relay info is nil" {
		t.Fatalf("err = %v, want nil-info error", err)
	}
}

func TestConvertImageRequest_MissingPrompt(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "   "})
	if err == nil || err.Error() != "replicate adaptor: prompt is required" {
		t.Fatalf("err = %v, want prompt-required error", err)
	}
}

func TestConvertImageRequest_PromptFallsBackToPostForm(t *testing.T) {
	a := &Adaptor{}
	form := url.Values{"prompt": {"from form"}}
	c, _ := newTestGinContext(t, http.MethodPost, "/", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, ok := req.(map[string]any)
	if !ok {
		t.Fatalf("req type = %T, want map[string]any", req)
	}
	input, ok := body["input"].(map[string]any)
	if !ok {
		t.Fatalf("input type = %T, want map[string]any", body["input"])
	}
	if input["prompt"] != "from form" {
		t.Errorf("prompt = %v, want %q", input["prompt"], "from form")
	}
}

func TestConvertImageRequest_ModelNamePrecedence(t *testing.T) {
	t.Run("uses info.UpstreamModelName first", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "vendor/upstream-model"}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", Model: "request-model"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "vendor/upstream-model" {
			t.Errorf("UpstreamModelName = %q, want vendor/upstream-model", info.UpstreamModelName)
		}
		wantPath := "/v1/models/vendor/upstream-model/predictions"
		if info.RequestURLPath != wantPath {
			t.Errorf("RequestURLPath = %q, want %q", info.RequestURLPath, wantPath)
		}
	})

	t.Run("falls back to request.Model", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", Model: "request-model"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "request-model" {
			t.Errorf("UpstreamModelName = %q, want request-model", info.UpstreamModelName)
		}
	})

	t.Run("falls back to default flux model", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != ModelFlux11Pro {
			t.Errorf("UpstreamModelName = %q, want %q", info.UpstreamModelName, ModelFlux11Pro)
		}
	})
}

func TestConvertImageRequest_SizeMapping(t *testing.T) {
	tests := []struct {
		name       string
		size       string
		wantAspect string
		wantWidth  any
		wantHeight any
	}{
		{"square", "1024x1024", "1:1", nil, nil},
		{"landscape 16:9", "1792x1024", "16:9", nil, nil},
		{"custom fallback", "1000x333", "custom", 992, 320},
		{"invalid size ignored", "not-a-size", "", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", Size: tt.size})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			input := req.(map[string]any)["input"].(map[string]any)
			if tt.wantAspect == "" {
				if _, ok := input["aspect_ratio"]; ok {
					t.Errorf("aspect_ratio present for invalid size: %v", input["aspect_ratio"])
				}
				return
			}
			if input["aspect_ratio"] != tt.wantAspect {
				t.Errorf("aspect_ratio = %v, want %v", input["aspect_ratio"], tt.wantAspect)
			}
			if tt.wantWidth != nil {
				if input["width"] != tt.wantWidth {
					t.Errorf("width = %v, want %v", input["width"], tt.wantWidth)
				}
				if input["height"] != tt.wantHeight {
					t.Errorf("height = %v, want %v", input["height"], tt.wantHeight)
				}
			}
		})
	}
}

func TestConvertImageRequest_OutputFormat(t *testing.T) {
	t.Run("valid output format applied", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		raw, _ := json.Marshal("png")
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", OutputFormat: raw})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if input["output_format"] != "png" {
			t.Errorf("output_format = %v, want png", input["output_format"])
		}
	})

	t.Run("invalid output format json is silently ignored", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", OutputFormat: json.RawMessage(`{not valid`)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if _, ok := input["output_format"]; ok {
			t.Errorf("output_format should be absent for malformed JSON, got %v", input["output_format"])
		}
	})
}

func TestConvertImageRequest_NAndQuality(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", N: 3, Quality: "HD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := req.(map[string]any)["input"].(map[string]any)
	if input["num_outputs"] != 3 {
		t.Errorf("num_outputs = %v, want 3", input["num_outputs"])
	}
	if input["prompt_upsampling"] != true {
		t.Errorf("prompt_upsampling = %v, want true", input["prompt_upsampling"])
	}
}

func TestConvertImageRequest_QualityHighAlsoUpsamples(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", Quality: "high"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	input := req.(map[string]any)["input"].(map[string]any)
	if input["prompt_upsampling"] != true {
		t.Errorf("prompt_upsampling = %v, want true", input["prompt_upsampling"])
	}
}

func TestConvertImageRequest_ImagesEditsWithoutFileErrors(t *testing.T) {
	a := &Adaptor{}
	// A well-formed but empty multipart body: uploadFileFromForm parses it
	// successfully, finds zero files, and returns ("", nil) -- purely
	// hermetic (no network call is reached).
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("other", "value")
	_ = w.Close()
	c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayMode: relayconstant.RelayModeImagesEdits}
	_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p"})
	if err == nil || err.Error() != "replicate adaptor: image file is required for edits" {
		t.Fatalf("err = %v, want image-required-for-edits error", err)
	}
}

func TestConvertImageRequest_ImagesEditsMalformedMultipart(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", bytes.NewBufferString("garbage"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=doesnotmatch")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayMode: relayconstant.RelayModeImagesEdits}
	_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected error for malformed multipart body")
	}
}

func TestConvertImageRequest_ExtraFields(t *testing.T) {
	t.Run("valid extra_fields merged", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", ExtraFields: json.RawMessage(`{"seed":42}`)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if input["seed"] != float64(42) {
			t.Errorf("seed = %v, want 42", input["seed"])
		}
	})

	t.Run("invalid extra_fields errors", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "p", ExtraFields: json.RawMessage(`not json`)})
		if err == nil || !strings.Contains(err.Error(), "failed to decode extra_fields") {
			t.Fatalf("err = %v, want decode extra_fields error", err)
		}
	})
}

func TestConvertImageRequest_ExtraMap(t *testing.T) {
	t.Run("input key merges nested map", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "p",
			Extra:  map[string]json.RawMessage{"input": json.RawMessage(`{"guidance":7.5}`)},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if input["guidance"] != 7.5 {
			t.Errorf("guidance = %v, want 7.5", input["guidance"])
		}
	})

	t.Run("input key with bad json errors", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "p",
			Extra:  map[string]json.RawMessage{"input": json.RawMessage(`nope`)},
		})
		if err == nil || !strings.Contains(err.Error(), "failed to decode extra input") {
			t.Fatalf("err = %v, want decode extra input error", err)
		}
	})

	t.Run("nil raw value is skipped", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "p",
			Extra:  map[string]json.RawMessage{"skip_me": nil},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if _, ok := input["skip_me"]; ok {
			t.Errorf("skip_me should not be present, got %v", input["skip_me"])
		}
	})

	t.Run("other key merges as scalar", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "p",
			Extra:  map[string]json.RawMessage{"custom_flag": json.RawMessage(`true`)},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input := req.(map[string]any)["input"].(map[string]any)
		if input["custom_flag"] != true {
			t.Errorf("custom_flag = %v, want true", input["custom_flag"])
		}
	})

	t.Run("other key with bad json errors", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{
			Prompt: "p",
			Extra:  map[string]json.RawMessage{"bad_key": json.RawMessage(`nope`)},
		})
		if err == nil || !strings.Contains(err.Error(), "failed to decode extra field bad_key") {
			t.Fatalf("err = %v, want decode extra field error", err)
		}
	})
}

// ---------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------

func TestDoResponse_NilResponse(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	_, apiErr := a.DoResponse(c, nil, &relaycommon.RelayInfo{})
	if apiErr == nil {
		t.Fatal("expected error for nil response")
	}
	if apiErr.GetErrorCode() != "bad_response" {
		t.Errorf("errorCode = %v, want bad_response", apiErr.GetErrorCode())
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error                { return nil }

func TestDoResponse_BodyReadError(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: errReader{}}
	_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr == nil {
		t.Fatal("expected error for unreadable body")
	}
	if apiErr.GetErrorCode() != "read_response_body_failed" {
		t.Errorf("errorCode = %v, want read_response_body_failed", apiErr.GetErrorCode())
	}
}

func TestDoResponse_InvalidJSON(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString("not json"))}
	_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr == nil {
		t.Fatal("expected error for invalid json")
	}
	if apiErr.GetErrorCode() != "bad_response_body" {
		t.Errorf("errorCode = %v, want bad_response_body", apiErr.GetErrorCode())
	}
}

func TestDoResponse_PredictionError(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{"message wins", `{"error":{"message":"m","detail":"d","code":"c"}}`, "m"},
		{"detail when message empty", `{"error":{"detail":"d","code":"c"}}`, "d"},
		{"code when message and detail empty", `{"error":{"code":"c"}}`, "c"},
		{"fallback when all empty", `{"error":{}}`, "replicate adaptor: prediction error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
			resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(tt.payload))}
			_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
			if apiErr == nil || apiErr.Error() != tt.wantMsg {
				t.Fatalf("err = %v, want %q", apiErr, tt.wantMsg)
			}
		})
	}
}

func TestDoResponse_NonSucceededStatus(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(`{"status":"processing"}`))}
	_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr == nil || apiErr.Error() != `replicate adaptor: prediction status "processing"` {
		t.Fatalf("err = %v, want prediction status error", apiErr)
	}
}

func TestDoResponse_EmptyOutput(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"nil output", `{"status":"succeeded","output":null}`},
		{"empty array", `{"status":"succeeded","output":[]}`},
		{"array of non-strings", `{"status":"succeeded","output":[1,2,3]}`},
		{"array of only blank strings", `{"status":"succeeded","output":["", "   "]}`},
		{"unsupported type", `{"status":"succeeded","output":{"foo":"bar"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
			resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(tt.payload))}
			_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
			if apiErr == nil || apiErr.Error() != "replicate adaptor: empty prediction output" {
				t.Fatalf("err = %v, want empty-prediction-output error", apiErr)
			}
		})
	}
}

func TestDoResponse_SuccessStringOutput(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(`{"status":"succeeded","output":"https://cdn.example.com/a.png"}`))}
	usage, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if _, ok := usage.(*dto.Usage); !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Url != "https://cdn.example.com/a.png" {
		t.Errorf("body.Data = %+v, want single url entry", body.Data)
	}
}

func TestDoResponse_SuccessArrayOutputMixedTypes(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		`{"status":"succeeded","output":["https://cdn.example.com/a.png",123,"https://cdn.example.com/b.png"]}`,
	))}
	_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	var body dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("len(body.Data) = %d, want 2", len(body.Data))
	}
	if body.Data[0].Url != "https://cdn.example.com/a.png" || body.Data[1].Url != "https://cdn.example.com/b.png" {
		t.Errorf("body.Data = %+v, want two url entries", body.Data)
	}
}

// TestDoResponse_Base64ConversionFailure exercises the wantsBase64 branch
// (info.Request is an *dto.ImageRequest with ResponseFormat "b64_json") via
// a URL scheme that app.GetImageFromUrl's SSRF protection rejects
// synchronously -- so no live network I/O occurs, but the wantsBase64 /
// downloadImagesToBase64-error code path is still exercised.
func TestDoResponse_Base64ConversionFailure(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{Request: &dto.ImageRequest{ResponseFormat: "b64_json"}}
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(`{"status":"succeeded","output":"ftp://example.com/a.png"}`))}
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil || apiErr.GetErrorCode() != "bad_response" {
		t.Fatalf("apiErr = %v, want bad_response error", apiErr)
	}
}

func TestDoResponse_EmptyStatusStillSucceeds(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(`{"output":"https://cdn.example.com/a.png"}`))}
	_, apiErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if apiErr != nil {
		t.Fatalf("unexpected error for empty status field: %v", apiErr)
	}
}

// ---------------------------------------------------------------------
// DoRequest (thin wrapper around provider.DoApiRequest; the nil-info guard
// short-circuits via a.GetRequestURL before any network I/O is attempted --
// the success path performs a live HTTP round-trip and is not hermetically
// reachable here.)
// ---------------------------------------------------------------------

func TestDoRequest_NilInfoShortCircuitsBeforeNetwork(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	_, err := a.DoRequest(c, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "get request url failed") {
		t.Fatalf("err = %v, want get-request-url-failed error", err)
	}
}

// ---------------------------------------------------------------------
// unimplemented Convert* stubs
// ---------------------------------------------------------------------

func TestUnimplementedConverters(t *testing.T) {
	a := &Adaptor{}

	if _, err := a.ConvertOpenAIRequest(nil, nil, nil); err == nil || err.Error() != "replicate adaptor: ConvertOpenAIRequest is not implemented" {
		t.Errorf("ConvertOpenAIRequest err = %v", err)
	}
	if _, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{}); err == nil || err.Error() != "replicate adaptor: ConvertRerankRequest is not implemented" {
		t.Errorf("ConvertRerankRequest err = %v", err)
	}
	if _, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{}); err == nil || err.Error() != "replicate adaptor: ConvertEmbeddingRequest is not implemented" {
		t.Errorf("ConvertEmbeddingRequest err = %v", err)
	}
	if _, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{}); err == nil || err.Error() != "replicate adaptor: ConvertAudioRequest is not implemented" {
		t.Errorf("ConvertAudioRequest err = %v", err)
	}
	if _, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{}); err == nil || err.Error() != "replicate adaptor: ConvertOpenAIResponsesRequest is not implemented" {
		t.Errorf("ConvertOpenAIResponsesRequest err = %v", err)
	}
	if _, err := a.ConvertClaudeRequest(nil, nil, nil); err == nil || err.Error() != "replicate adaptor: ConvertClaudeRequest is not implemented" {
		t.Errorf("ConvertClaudeRequest err = %v", err)
	}
	if _, err := a.ConvertGeminiRequest(nil, nil, nil); err == nil || err.Error() != "replicate adaptor: ConvertGeminiRequest is not implemented" {
		t.Errorf("ConvertGeminiRequest err = %v", err)
	}
}

// ---------------------------------------------------------------------
// uploadFileFromForm (hermetic branches only; the "file present" branch
// performs a live HTTP upload to the channel base URL and is therefore not
// exercised here.)
// ---------------------------------------------------------------------

func TestUploadFileFromForm_NilInfo(t *testing.T) {
	c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
	_, err := uploadFileFromForm(c, nil)
	if err == nil || err.Error() != "replicate adaptor: relay info is nil" {
		t.Fatalf("err = %v, want nil-info error", err)
	}
}

func TestUploadFileFromForm_MalformedMultipart(t *testing.T) {
	c, _ := newTestGinContext(t, http.MethodPost, "/", bytes.NewBufferString("garbage"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=doesnotmatch")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := uploadFileFromForm(c, info)
	if err == nil || !strings.Contains(err.Error(), "parse multipart form failed") {
		t.Fatalf("err = %v, want parse-multipart-form error", err)
	}
}

func TestUploadFileFromForm_NoFilesReturnsEmpty(t *testing.T) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("other", "value")
	_ = w.Close()
	c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	url, err := uploadFileFromForm(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("url = %q, want empty", url)
	}
}

func TestUploadFileFromForm_DefaultFieldCandidates(t *testing.T) {
	// No explicit fieldCandidates supplied: exercises the branch that falls
	// back to the default {"image", "image[]", "image_prompt"} list, but
	// with an unrelated field present so no file is ever picked and the
	// function returns before reaching the network.
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("unrelated", "value")
	_ = w.Close()
	c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	url, err := uploadFileFromForm(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("url = %q, want empty", url)
	}
}

// ---------------------------------------------------------------------
// downloadImagesToBase64 (hermetic branch: all-blank URLs never dial out)
// ---------------------------------------------------------------------

func TestDownloadImagesToBase64_AllBlankURLsSkipNetwork(t *testing.T) {
	results, err := downloadImagesToBase64([]string{"", "   "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %v, want empty slice", results)
	}
}

// TestDownloadImagesToBase64_RejectedSchemeErrorsWithoutNetwork exercises the
// error branch of downloadImagesToBase64 via app.GetImageFromUrl's SSRF
// protection, which rejects unsupported URL schemes synchronously (before
// any DNS lookup or socket dial) -- so this stays hermetic.
func TestDownloadImagesToBase64_RejectedSchemeErrorsWithoutNetwork(t *testing.T) {
	_, err := downloadImagesToBase64([]string{"ftp://example.com/a.png"})
	if err == nil || !strings.Contains(err.Error(), "failed to download image from") {
		t.Fatalf("err = %v, want failed-to-download error", err)
	}
}

// ---------------------------------------------------------------------
// mapOpenAISizeToFlux / reduceRatio / gcd / normalizeFluxDimension
// ---------------------------------------------------------------------

func TestMapOpenAISizeToFlux(t *testing.T) {
	tests := []struct {
		size       string
		wantAspect string
		wantW      int
		wantH      int
		wantOK     bool
	}{
		{"1024x1024", "1:1", 0, 0, true},
		{"1792x1024", "16:9", 0, 0, true},
		{"1024x1792", "9:16", 0, 0, true},
		{"1536x1024", "3:2", 0, 0, true},
		{"1024x1536", "2:3", 0, 0, true},
		{"800x1000", "4:5", 0, 0, true},
		{"1000x800", "5:4", 0, 0, true},
		{"1000x333", "custom", 992, 320, true},
		{"badformat", "", 0, 0, false},
		{"axb", "", 0, 0, false},
		{"0x100", "", 0, 0, false},
		{"100x0", "", 0, 0, false},
		{"-100x100", "", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			aspect, w, h, ok := mapOpenAISizeToFlux(tt.size)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if aspect != tt.wantAspect {
				t.Errorf("aspect = %q, want %q", aspect, tt.wantAspect)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("w,h = %d,%d want %d,%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestReduceRatio(t *testing.T) {
	tests := []struct {
		w, h   int
		wantW  int
		wantH  int
	}{
		{8, 4, 2, 1},
		{0, 0, 0, 0},
		{5, 0, 1, 0},
	}
	for _, tt := range tests {
		w, h := reduceRatio(tt.w, tt.h)
		if w != tt.wantW || h != tt.wantH {
			t.Errorf("reduceRatio(%d,%d) = %d,%d want %d,%d", tt.w, tt.h, w, h, tt.wantW, tt.wantH)
		}
	}
}

func TestGCD(t *testing.T) {
	tests := []struct{ a, b, want int }{
		{8, 4, 4},
		{-8, 4, 4},
		{0, 5, 5},
		{5, 0, 5},
		{-4, 0, 4}, // b==0 immediately, exercises the a<0 -> return -a branch
	}
	for _, tt := range tests {
		if got := gcd(tt.a, tt.b); got != tt.want {
			t.Errorf("gcd(%d,%d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNormalizeFluxDimension(t *testing.T) {
	tests := []struct {
		value int
		want  int
	}{
		{100, 256},   // below min clamps up
		{2000, 1440}, // above max clamps down
		{500, 512},   // rounds up to nearest step (remainder 20 >= 16)
		{520, 512},   // rounds down to nearest step (remainder 8 < 16)
		{512, 512},   // already aligned
	}
	for _, tt := range tests {
		if got := normalizeFluxDimension(tt.value); got != tt.want {
			t.Errorf("normalizeFluxDimension(%d) = %d, want %d", tt.value, got, tt.want)
		}
	}
}
