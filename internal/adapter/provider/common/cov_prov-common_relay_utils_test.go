package common

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// ---- GetFullRequestURL ----

func TestGetFullRequestURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		requestURL  string
		channelType int
		want        string
	}{
		{
			name:        "plain concat",
			baseURL:     "https://api.openai.com",
			requestURL:  "/v1/chat/completions",
			channelType: constant.ChannelTypeOpenAI,
			want:        "https://api.openai.com/v1/chat/completions",
		},
		{
			name:        "cloudflare gateway + openai trims /v1 prefix",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/openai",
			requestURL:  "/v1/chat/completions",
			channelType: constant.ChannelTypeOpenAI,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/openai/chat/completions",
		},
		{
			name:        "cloudflare gateway + azure trims /openai/deployments prefix",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/azure-openai",
			requestURL:  "/openai/deployments/gpt-4/chat/completions",
			channelType: constant.ChannelTypeAzure,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/azure-openai/gpt-4/chat/completions",
		},
		{
			name:        "cloudflare gateway + unrelated channel type is untouched",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/anthropic",
			requestURL:  "/v1/messages",
			channelType: constant.ChannelTypeAnthropic,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/anthropic/v1/messages",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFullRequestURL(tt.baseURL, tt.requestURL, tt.channelType)
			if got != tt.want {
				t.Errorf("GetFullRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- GetAPIVersion ----

func TestGetAPIVersion(t *testing.T) {
	t.Run("query param wins", func(t *testing.T) {
		c := provCommonNewGinContext(t, "GET", "/x?api-version=2024-06-01")
		c.Set("api_version", "should-be-ignored")
		if got := GetAPIVersion(c); got != "2024-06-01" {
			t.Errorf("GetAPIVersion() = %q, want query value", got)
		}
	})
	t.Run("falls back to gin context value", func(t *testing.T) {
		c := provCommonNewGinContext(t, "GET", "/x")
		c.Set("api_version", "2023-01-01")
		if got := GetAPIVersion(c); got != "2023-01-01" {
			t.Errorf("GetAPIVersion() = %q, want context fallback", got)
		}
	})
	t.Run("empty when neither present", func(t *testing.T) {
		c := provCommonNewGinContext(t, "GET", "/x")
		if got := GetAPIVersion(c); got != "" {
			t.Errorf("GetAPIVersion() = %q, want empty", got)
		}
	})
}

// ---- createTaskError ----

func TestCreateTaskError(t *testing.T) {
	err := createTaskError(errors.New("bad request"), "invalid_request", 400, true)
	if err.Code != "invalid_request" {
		t.Errorf("Code = %q, want %q", err.Code, "invalid_request")
	}
	if err.Message != "bad request" {
		t.Errorf("Message = %q, want %q", err.Message, "bad request")
	}
	if err.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", err.StatusCode)
	}
	if !err.LocalError {
		t.Error("expected LocalError = true")
	}
}

// ---- storeTaskRequest / GetTaskRequest ----

func TestStoreAndGetTaskRequest_RoundTrip(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/x")
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	req := TaskSubmitReq{Prompt: "hello"}

	storeTaskRequest(c, info, constant.TaskActionGenerate, req)

	if info.Action != constant.TaskActionGenerate {
		t.Errorf("info.Action = %q, want %q", info.Action, constant.TaskActionGenerate)
	}

	got, err := GetTaskRequest(c)
	if err != nil {
		t.Fatalf("GetTaskRequest() error = %v", err)
	}
	if got.Prompt != "hello" {
		t.Errorf("GetTaskRequest().Prompt = %q, want %q", got.Prompt, "hello")
	}
}

func TestGetTaskRequest_MissingReturnsError(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/x")
	_, err := GetTaskRequest(c)
	if err == nil {
		t.Fatal("expected error when task_request was never stored")
	}
}

func TestGetTaskRequest_WrongTypeReturnsError(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/x")
	c.Set("task_request", "not-a-TaskSubmitReq")
	_, err := GetTaskRequest(c)
	if err == nil {
		t.Fatal("expected error for a task_request value of the wrong type")
	}
}

// ---- validatePrompt ----

func TestValidatePrompt(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		wantErr bool
	}{
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"valid", "draw a cat", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrompt(tt.prompt)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePrompt(%q) err = %v, wantErr %v", tt.prompt, err, tt.wantErr)
			}
			if err != nil && err.Code != "invalid_request" {
				t.Errorf("Code = %q, want invalid_request", err.Code)
			}
		})
	}
}

// ---- isKnownTaskField ----

func TestIsKnownTaskField(t *testing.T) {
	known := []string{"prompt", "model", "mode", "image", "images", "size", "duration", "input_reference"}
	for _, f := range known {
		if !isKnownTaskField(f) {
			t.Errorf("isKnownTaskField(%q) = false, want true", f)
		}
	}
	unknown := []string{"seed", "callback_url", ""}
	for _, f := range unknown {
		if isKnownTaskField(f) {
			t.Errorf("isKnownTaskField(%q) = true, want false", f)
		}
	}
}

// ---- validateMultipartTaskRequest ----

func provCommonMultipartRequest(t *testing.T, fields map[string]string) *gin.Context {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%q): %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/x", &buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	return c
}

func TestValidateMultipartTaskRequest_ParsesKnownAndMetadataFields(t *testing.T) {
	c := provCommonMultipartRequest(t, map[string]string{
		"prompt":     "a dog",
		"model":      "sora-2",
		"mode":       "fast",
		"image":      "http://x/1.png",
		"size":       "720x1280",
		"seconds":    "8",
		"custom_int": "5",
		"custom_flt": "1.5",
		"custom_str": "hello",
	})
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	req, err := validateMultipartTaskRequest(c, info, constant.TaskActionGenerate)
	if err != nil {
		t.Fatalf("validateMultipartTaskRequest() error = %v", err)
	}
	if req.Prompt != "a dog" || req.Model != "sora-2" || req.Mode != "fast" || req.Size != "720x1280" {
		t.Errorf("req = %+v, want known fields populated", req)
	}
	if req.Duration != 8 {
		t.Errorf("Duration = %d, want 8 (parsed from 'seconds')", req.Duration)
	}
	if int(req.Metadata["custom_int"].(int)) != 5 {
		t.Errorf("Metadata[custom_int] = %v, want int 5", req.Metadata["custom_int"])
	}
	if req.Metadata["custom_flt"].(float64) != 1.5 {
		t.Errorf("Metadata[custom_flt] = %v, want float64 1.5", req.Metadata["custom_flt"])
	}
	if req.Metadata["custom_str"].(string) != "hello" {
		t.Errorf("Metadata[custom_str] = %v, want string hello", req.Metadata["custom_str"])
	}
	// Known fields must never leak into Metadata.
	if _, ok := req.Metadata["prompt"]; ok {
		t.Error("known field 'prompt' leaked into Metadata")
	}
}

// ---- ValidateMultipartDirect ----

func provCommonJSONContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	// UnmarshalBodyReusable enforces constant.MaxRequestBodyMB, which defaults
	// to its zero value outside of normal server boot (env-driven init);
	// without raising it every non-empty JSON body is rejected as "too large".
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestValidateMultipartDirect_MissingModelErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{"prompt":"hi"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err == nil || err.Code != "missing_model" {
		t.Fatalf("err = %+v, want missing_model", err)
	}
}

func TestValidateMultipartDirect_MissingPromptErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"gpt-image-1"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err == nil || err.Code != "invalid_request" {
		t.Fatalf("err = %+v, want invalid_request for missing prompt", err)
	}
}

func TestValidateMultipartDirect_Sora2DefaultsSizeAndSeconds(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"sora-2","prompt":"a scene"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if info.Action != constant.TaskActionTextGenerate {
		t.Errorf("Action = %q, want TaskActionTextGenerate (no image reference)", info.Action)
	}
	if info.PriceData.OtherRatios["seconds"] != 4 {
		t.Errorf("OtherRatios[seconds] = %v, want default 4", info.PriceData.OtherRatios["seconds"])
	}
	if info.PriceData.OtherRatios["size"] != 1 {
		t.Errorf("OtherRatios[size] = %v, want 1 for the default 720x1280 size", info.PriceData.OtherRatios["size"])
	}
}

func TestValidateMultipartDirect_Sora2InvalidSizeErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"sora-2","prompt":"a scene","size":"999x999"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err == nil || err.Code != "invalid_size" {
		t.Fatalf("err = %+v, want invalid_size", err)
	}
}

func TestValidateMultipartDirect_Sora2ProLargeSizeGetsHigherRatio(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"sora-2-pro","prompt":"a scene","size":"1792x1024"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if info.PriceData.OtherRatios["size"] != 1.666667 {
		t.Errorf("OtherRatios[size] = %v, want the widescreen 1.666667 multiplier", info.PriceData.OtherRatios["size"])
	}
}

func TestValidateMultipartDirect_Sora2ProInvalidSizeErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"sora-2-pro","prompt":"a scene","size":"999x999"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err == nil || err.Code != "invalid_size" {
		t.Fatalf("err = %+v, want invalid_size for sora-2-pro too", err)
	}
}

func TestValidateMultipartDirect_InputReferenceMarksGenerateAction(t *testing.T) {
	c := provCommonJSONContext(t, `{"model":"gpt-image-1","prompt":"remix this","input_reference":"http://x/ref.png"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if info.Action != constant.TaskActionGenerate {
		t.Errorf("Action = %q, want TaskActionGenerate when an input reference image is supplied", info.Action)
	}
}

func TestValidateMultipartDirect_MalformedJSONErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{not valid`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateMultipartDirect(c, info)
	if err == nil || err.Code != "invalid_json" {
		t.Fatalf("err = %+v, want invalid_json", err)
	}
}

// ---- ValidateBasicTaskRequest ----

func TestValidateBasicTaskRequest_JSONHappyPath(t *testing.T) {
	c := provCommonJSONContext(t, `{"prompt":"a fish","image":"http://x/a.png"}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	err := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if info.Action != constant.TaskActionGenerate {
		t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
	}
	stored, getErr := GetTaskRequest(c)
	if getErr != nil {
		t.Fatalf("GetTaskRequest() error = %v", getErr)
	}
	// Business rule: a single legacy 'image' field is folded into Images.
	if len(stored.Images) != 1 || stored.Images[0] != "http://x/a.png" {
		t.Errorf("stored.Images = %+v, want single-image fallback from legacy 'image' field", stored.Images)
	}
}

func TestValidateBasicTaskRequest_EmptyPromptErrors(t *testing.T) {
	c := provCommonJSONContext(t, `{"prompt":""}`)
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
	if err == nil || err.Code != "invalid_request" {
		t.Fatalf("err = %+v, want invalid_request for empty prompt", err)
	}
}

func TestValidateBasicTaskRequest_MultipartRoutesThroughFormParser(t *testing.T) {
	c := provCommonMultipartRequest(t, map[string]string{"prompt": "multipart prompt", "model": "m1"})
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	err := ValidateBasicTaskRequest(c, info, constant.TaskActionTextGenerate)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	stored, getErr := GetTaskRequest(c)
	if getErr != nil {
		t.Fatalf("GetTaskRequest() error = %v", getErr)
	}
	if stored.Prompt != "multipart prompt" {
		t.Errorf("stored.Prompt = %q, want %q", stored.Prompt, "multipart prompt")
	}
}

func TestValidateBasicTaskRequest_MultipartInvalidFormErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/x", strings.NewReader("not a real multipart body"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

	err := ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
	if err == nil || err.Code != "invalid_multipart_form" {
		t.Fatalf("err = %+v, want invalid_multipart_form", err)
	}
}
