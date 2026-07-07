package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// relay_utils.go
// ---------------------------------------------------------------------------

func TestGetFullRequestURL(t *testing.T) {
	cases := []struct {
		name        string
		baseURL     string
		requestURL  string
		channelType int
		want        string
	}{
		{
			name:        "plain concat",
			baseURL:     "https://api.example.com",
			requestURL:  "/v1/chat/completions",
			channelType: constant.ChannelTypeOpenAI,
			want:        "https://api.example.com/v1/chat/completions",
		},
		{
			name:        "cloudflare openai trims /v1",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/openai",
			requestURL:  "/v1/chat/completions",
			channelType: constant.ChannelTypeOpenAI,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/openai/chat/completions",
		},
		{
			name:        "cloudflare azure trims /openai/deployments",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/azure-openai",
			requestURL:  "/openai/deployments/gpt-4/chat/completions",
			channelType: constant.ChannelTypeAzure,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/azure-openai/gpt-4/chat/completions",
		},
		{
			name:        "cloudflare unmatched channel type keeps concat",
			baseURL:     "https://gateway.ai.cloudflare.com/acct/gw/anthropic",
			requestURL:  "/v1/messages",
			channelType: constant.ChannelTypeVertexAi,
			want:        "https://gateway.ai.cloudflare.com/acct/gw/anthropic/v1/messages",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetFullRequestURL(tc.baseURL, tc.requestURL, tc.channelType)
			if got != tc.want {
				t.Errorf("GetFullRequestURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetAPIVersion(t *testing.T) {
	t.Run("query param wins", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/?api-version=2024-05-01", nil)
		if got := GetAPIVersion(c); got != "2024-05-01" {
			t.Errorf("GetAPIVersion() = %q, want %q", got, "2024-05-01")
		}
	})

	t.Run("falls back to context api_version", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set("api_version", "2023-01-01")
		if got := GetAPIVersion(c); got != "2023-01-01" {
			t.Errorf("GetAPIVersion() = %q, want %q", got, "2023-01-01")
		}
	})

	t.Run("empty when neither present", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if got := GetAPIVersion(c); got != "" {
			t.Errorf("GetAPIVersion() = %q, want empty", got)
		}
	})
}

func TestCreateTaskError(t *testing.T) {
	err := createTaskError(errString("boom"), "some_code", http.StatusBadGateway, true)
	if err.Code != "some_code" || err.Message != "boom" || err.StatusCode != http.StatusBadGateway || !err.LocalError {
		t.Errorf("unexpected TaskError: %+v", err)
	}
	if err.Error == nil || err.Error.Error() != "boom" {
		t.Errorf("expected wrapped error preserved, got %v", err.Error)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestStoreAndGetTaskRequest(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		req := TaskSubmitReq{Prompt: "hello"}
		storeTaskRequest(c, info, "text_generate", req)

		if info.Action != "text_generate" {
			t.Errorf("Action = %q, want text_generate", info.Action)
		}
		got, err := GetTaskRequest(c)
		if err != nil {
			t.Fatalf("GetTaskRequest error: %v", err)
		}
		if got.Prompt != "hello" {
			t.Errorf("got.Prompt = %q, want hello", got.Prompt)
		}
	})

	t.Run("missing in context", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		_, err := GetTaskRequest(c)
		if err == nil {
			t.Fatal("expected error for missing task_request")
		}
	})

	t.Run("wrong type in context", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", 42)
		_, err := GetTaskRequest(c)
		if err == nil {
			t.Fatal("expected error for invalid task request type")
		}
	})
}

func TestValidatePrompt(t *testing.T) {
	if err := validatePrompt("a prompt"); err != nil {
		t.Errorf("expected nil error for non-empty prompt, got %+v", err)
	}
	if err := validatePrompt(""); err == nil {
		t.Fatal("expected error for empty prompt")
	} else if err.Code != "invalid_request" || err.StatusCode != http.StatusBadRequest {
		t.Errorf("unexpected error: %+v", err)
	}
	if err := validatePrompt("   \n\t  "); err == nil {
		t.Fatal("expected error for whitespace-only prompt")
	}
}

func TestIsKnownTaskField(t *testing.T) {
	known := []string{"prompt", "model", "mode", "image", "images", "size", "duration", "input_reference"}
	for _, f := range known {
		if !isKnownTaskField(f) {
			t.Errorf("expected %q to be known", f)
		}
	}
	if isKnownTaskField("some_custom_field") {
		t.Error("expected unknown custom field to be reported as not known")
	}
}

func buildMultipartRequest(t *testing.T, fields map[string]string, images []string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}
	for _, img := range images {
		if err := mw.WriteField("images", img); err != nil {
			t.Fatalf("WriteField(images): %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestValidateMultipartTaskRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = buildMultipartRequest(t, map[string]string{
		"prompt":     "draw a cat",
		"model":      "sora-2",
		"mode":       "fast",
		"image":      "img-ref",
		"size":       "720x1280",
		"seconds":    "8",
		"custom_int": "42",
		"custom_flt": "3.14",
		"custom_str": "hello",
	}, []string{"i1.png", "i2.png"})

	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	req, err := validateMultipartTaskRequest(c, info, "some_action")
	if err != nil {
		t.Fatalf("validateMultipartTaskRequest error: %v", err)
	}

	if req.Prompt != "draw a cat" || req.Model != "sora-2" || req.Mode != "fast" ||
		req.Image != "img-ref" || req.Size != "720x1280" || req.Duration != 8 {
		t.Errorf("unexpected parsed req: %+v", req)
	}
	if len(req.Images) != 2 || req.Images[0] != "i1.png" || req.Images[1] != "i2.png" {
		t.Errorf("unexpected images: %+v", req.Images)
	}
	if v, ok := req.Metadata["custom_int"].(int); !ok || v != 42 {
		t.Errorf("custom_int metadata = %#v, want int 42", req.Metadata["custom_int"])
	}
	if v, ok := req.Metadata["custom_flt"].(float64); !ok || v != 3.14 {
		t.Errorf("custom_flt metadata = %#v, want float64 3.14", req.Metadata["custom_flt"])
	}
	if v, ok := req.Metadata["custom_str"].(string); !ok || v != "hello" {
		t.Errorf("custom_str metadata = %#v, want string hello", req.Metadata["custom_str"])
	}
	// known fields (per isKnownTaskField) must never leak into Metadata. Note
	// "seconds" is intentionally NOT in isKnownTaskField's list, so it is
	// expected to land in Metadata instead (asserted separately below).
	for _, known := range []string{"prompt", "model", "mode", "image", "size"} {
		if _, exists := req.Metadata[known]; exists {
			t.Errorf("known field %q should not be present in Metadata", known)
		}
	}
	if v, ok := req.Metadata["seconds"].(int); !ok || v != 8 {
		t.Errorf(`"seconds" is not in isKnownTaskField's list, so it should land in Metadata as int 8; got %#v`, req.Metadata["seconds"])
	}
}

func TestValidateMultipartTaskRequestFormError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Not a valid multipart body -> c.MultipartForm() must fail.
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not multipart"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=missing")

	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	if _, err := validateMultipartTaskRequest(c, info, "action"); err == nil {
		t.Fatal("expected error for malformed multipart body")
	}
}

func newJSONContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestValidateMultipartDirect(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		c := newJSONContext(t, "{not json")
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateMultipartDirect(c, info)
		if err == nil || err.Code != "invalid_json" {
			t.Fatalf("expected invalid_json error, got %+v", err)
		}
	})

	t.Run("missing model", func(t *testing.T) {
		c := newJSONContext(t, `{"prompt":"a cat"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateMultipartDirect(c, info)
		if err == nil || err.Code != "missing_model" {
			t.Fatalf("expected missing_model error, got %+v", err)
		}
	})

	t.Run("missing prompt", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"sora-2","prompt":"  "}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateMultipartDirect(c, info)
		if err == nil || err.Code != "invalid_request" {
			t.Fatalf("expected invalid_request error, got %+v", err)
		}
	})

	t.Run("basic non-sora model with image sets generate action", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-image-1","prompt":"a cat","input_reference":"ref.png"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateMultipartDirect(c, info); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
	})

	t.Run("basic model without image sets text-generate action", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-image-1","prompt":"a cat"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateMultipartDirect(c, info); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if info.Action != constant.TaskActionTextGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionTextGenerate)
		}
	})

	t.Run("sora-2 defaults size and seconds", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"sora-2","prompt":"a cat"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateMultipartDirect(c, info); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if info.PriceData.OtherRatios["seconds"] != 4 {
			t.Errorf("seconds ratio = %v, want 4 (default)", info.PriceData.OtherRatios["seconds"])
		}
		if info.PriceData.OtherRatios["size"] != 1 {
			t.Errorf("size ratio = %v, want 1 for default 720x1280", info.PriceData.OtherRatios["size"])
		}
	})

	t.Run("sora-2 invalid size errors", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"sora-2","prompt":"a cat","size":"1792x1024"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateMultipartDirect(c, info)
		if err == nil || err.Code != "invalid_size" {
			t.Fatalf("expected invalid_size error, got %+v", err)
		}
	})

	t.Run("sora-2-pro wide size gets 1.666667 ratio", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"sora-2-pro","prompt":"a cat","size":"1792x1024","seconds":"12"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateMultipartDirect(c, info); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if info.PriceData.OtherRatios["size"] != 1.666667 {
			t.Errorf("size ratio = %v, want 1.666667", info.PriceData.OtherRatios["size"])
		}
		if info.PriceData.OtherRatios["seconds"] != 12 {
			t.Errorf("seconds ratio = %v, want 12", info.PriceData.OtherRatios["seconds"])
		}
	})

	t.Run("sora-2-pro invalid size errors", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"sora-2-pro","prompt":"a cat","size":"999x999"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateMultipartDirect(c, info)
		if err == nil || err.Code != "invalid_size" {
			t.Fatalf("expected invalid_size error, got %+v", err)
		}
	})

	t.Run("duration falls back when seconds string missing", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-image-1","prompt":"a cat","duration":15}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateMultipartDirect(c, info); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		// non-sora model doesn't populate OtherRatios, but no error means duration path was exercised.
	})
}

func TestValidateBasicTaskRequest(t *testing.T) {
	t.Run("json branch valid", func(t *testing.T) {
		c := newJSONContext(t, `{"prompt":"a cat","model":"gpt-image-1"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateBasicTaskRequest(c, info, "text_generate"); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		got, err := GetTaskRequest(c)
		if err != nil {
			t.Fatalf("GetTaskRequest error: %v", err)
		}
		if got.Prompt != "a cat" || info.Action != "text_generate" {
			t.Errorf("unexpected stored request: %+v action=%q", got, info.Action)
		}
	})

	t.Run("json branch single image fallback", func(t *testing.T) {
		c := newJSONContext(t, `{"prompt":"a cat","model":"gpt-image-1","image":"only.png"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateBasicTaskRequest(c, info, "generate"); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		got, _ := GetTaskRequest(c)
		if len(got.Images) != 1 || got.Images[0] != "only.png" {
			t.Errorf("expected single image fallback into Images, got %+v", got.Images)
		}
	})

	t.Run("json branch invalid json", func(t *testing.T) {
		c := newJSONContext(t, "{bad")
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateBasicTaskRequest(c, info, "action")
		if err == nil || err.Code != "invalid_request" {
			t.Fatalf("expected invalid_request error, got %+v", err)
		}
	})

	t.Run("json branch empty prompt", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-image-1"}`)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateBasicTaskRequest(c, info, "action")
		if err == nil || err.Code != "invalid_request" {
			t.Fatalf("expected invalid_request for missing prompt, got %+v", err)
		}
	})

	t.Run("multipart branch valid", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = buildMultipartRequest(t, map[string]string{
			"prompt": "a cat",
			"model":  "gpt-image-1",
		}, nil)
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		if err := ValidateBasicTaskRequest(c, info, "generate"); err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		got, _ := GetTaskRequest(c)
		if got.Prompt != "a cat" {
			t.Errorf("unexpected multipart-parsed prompt: %q", got.Prompt)
		}
	})

	t.Run("multipart branch malformed errors invalid_multipart_form", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("garbage"))
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		err := ValidateBasicTaskRequest(c, info, "action")
		if err == nil || err.Code != "invalid_multipart_form" {
			t.Fatalf("expected invalid_multipart_form error, got %+v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// relay_info.go
// ---------------------------------------------------------------------------

func TestRelayInfoToStringNil(t *testing.T) {
	var info *RelayInfo
	if got := info.ToString(); got != "RelayInfo<nil>" {
		t.Errorf("ToString() on nil = %q", got)
	}
}

func TestRelayInfoToStringMinimal(t *testing.T) {
	now := time.Now()
	info := &RelayInfo{
		RelayFormat:    types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeChatCompletions,
		IsStream:       true,
		RequestURLPath: "/v1/chat/completions",
		StartTime:      now,
		FirstResponseTime: now.Add(50 * time.Millisecond),
	}
	got := info.ToString()
	if !strings.Contains(got, "RelayFormat: openai") {
		t.Errorf("expected RelayFormat in output, got %q", got)
	}
	if !strings.Contains(got, "LatencyMs: 50") {
		t.Errorf("expected computed latency in output, got %q", got)
	}
	if strings.Contains(got, "ChannelMeta{") {
		t.Errorf("did not expect ChannelMeta section without ChannelMeta set: %q", got)
	}
	if strings.Contains(got, "Realtime{") {
		t.Errorf("did not expect Realtime section without audio fields: %q", got)
	}
}

func TestRelayInfoToStringWithChannelMetaAndExtras(t *testing.T) {
	info := &RelayInfo{
		StartTime:         time.Now(),
		FirstResponseTime: time.Now(),
		AudioUsage:        true,
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		ReasoningEffort:   "high",
		ChannelMeta: &ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelId:      7,
			ChannelBaseUrl: "https://api.openai.com",
			ApiKey:         "sk-should-be-masked",
		},
		PriceData: types.PriceData{UsePrice: true, ModelRatio: 1},
		ResponsesUsageInfo: &ResponsesUsageInfo{
			BuiltInTools: map[string]*BuildInToolInfo{
				"web_search_preview": {ToolName: "web_search_preview", CallCount: 3},
				"file_search":        nil,
			},
		},
	}
	got := info.ToString()
	if !strings.Contains(got, "Realtime{") {
		t.Errorf("expected Realtime section: %q", got)
	}
	if !strings.Contains(got, "ReasoningEffort: \"high\"") {
		t.Errorf("expected ReasoningEffort section: %q", got)
	}
	if !strings.Contains(got, "ChannelMeta{") || strings.Contains(got, "sk-should-be-masked") {
		t.Errorf("expected masked ChannelMeta section, got %q", got)
	}
	if !strings.Contains(got, "PriceData{") {
		t.Errorf("expected PriceData section when UsePrice=true: %q", got)
	}
	if !strings.Contains(got, "web_search_preview: calls=3") {
		t.Errorf("expected populated tool call count: %q", got)
	}
	if !strings.Contains(got, "file_search: calls=0") {
		t.Errorf("expected nil tool entry to render calls=0: %q", got)
	}
}

func TestFailTaskInfo(t *testing.T) {
	ti := FailTaskInfo("upstream exploded")
	if ti.Status != "FAILURE" || ti.Reason != "upstream exploded" {
		t.Errorf("unexpected TaskInfo: %+v", ti)
	}
}

func TestRemoveDisabledFields(t *testing.T) {
	base := `{"model":"gpt-4","service_tier":"scale","store":true,"safety_identifier":"u123"}`

	t.Run("defaults strip service_tier and safety_identifier, keep store", func(t *testing.T) {
		out, err := RemoveDisabledFields([]byte(base), dto.ChannelOtherSettings{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		s := string(out)
		if strings.Contains(s, "service_tier") {
			t.Errorf("service_tier should be removed by default: %s", s)
		}
		if strings.Contains(s, "safety_identifier") {
			t.Errorf("safety_identifier should be removed by default: %s", s)
		}
		if !strings.Contains(s, `"store":true`) {
			t.Errorf("store should be kept by default: %s", s)
		}
	})

	t.Run("AllowServiceTier keeps service_tier", func(t *testing.T) {
		out, err := RemoveDisabledFields([]byte(base), dto.ChannelOtherSettings{AllowServiceTier: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), `"service_tier":"scale"`) {
			t.Errorf("expected service_tier kept: %s", out)
		}
	})

	t.Run("DisableStore removes store", func(t *testing.T) {
		out, err := RemoveDisabledFields([]byte(base), dto.ChannelOtherSettings{DisableStore: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(string(out), "store") {
			t.Errorf("expected store removed: %s", out)
		}
	})

	t.Run("AllowSafetyIdentifier keeps safety_identifier", func(t *testing.T) {
		out, err := RemoveDisabledFields([]byte(base), dto.ChannelOtherSettings{AllowSafetyIdentifier: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), `"safety_identifier":"u123"`) {
			t.Errorf("expected safety_identifier kept: %s", out)
		}
	})

	t.Run("invalid JSON passes through unchanged with nil error", func(t *testing.T) {
		out, err := RemoveDisabledFields([]byte("{not json"), dto.ChannelOtherSettings{})
		if err != nil {
			t.Fatalf("expected nil error on unmarshal failure, got %v", err)
		}
		if string(out) != "{not json" {
			t.Errorf("expected passthrough of original bytes, got %q", out)
		}
	})
}

func TestRemoveGeminiDisabledFields(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	origEnabled := settings.RemoveFunctionResponseIdEnabled
	t.Cleanup(func() { settings.RemoveFunctionResponseIdEnabled = origEnabled })

	t.Run("removes camelCase functionResponse.id when enabled", func(t *testing.T) {
		settings.RemoveFunctionResponseIdEnabled = true
		in := `{"contents":[{"parts":[{"functionResponse":{"id":"abc","name":"x"}}]}]}`
		out, err := RemoveGeminiDisabledFields([]byte(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(string(out), `"id"`) {
			t.Errorf("expected id removed: %s", out)
		}
		if !strings.Contains(string(out), `"name":"x"`) {
			t.Errorf("expected sibling field kept: %s", out)
		}
	})

	t.Run("removes snake_case function_response.id when enabled", func(t *testing.T) {
		settings.RemoveFunctionResponseIdEnabled = true
		in := `{"contents":[{"parts":[{"function_response":{"id":"abc","name":"y"}}]}]}`
		out, err := RemoveGeminiDisabledFields([]byte(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(string(out), `"id"`) {
			t.Errorf("expected id removed: %s", out)
		}
	})

	t.Run("no-op when disabled", func(t *testing.T) {
		settings.RemoveFunctionResponseIdEnabled = false
		in := `{"contents":[{"parts":[{"functionResponse":{"id":"abc"}}]}]}`
		out, err := RemoveGeminiDisabledFields([]byte(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != in {
			t.Errorf("expected passthrough when disabled, got %s", out)
		}
	})

	t.Run("invalid JSON passes through unchanged with nil error", func(t *testing.T) {
		settings.RemoveFunctionResponseIdEnabled = true
		out, err := RemoveGeminiDisabledFields([]byte("{not json"))
		if err != nil {
			t.Fatalf("expected nil error on unmarshal failure, got %v", err)
		}
		if string(out) != "{not json" {
			t.Errorf("expected passthrough of original bytes, got %q", out)
		}
	})
}

func TestSetGetEstimatePromptTokens(t *testing.T) {
	info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	if got := info.GetEstimatePromptTokens(); got != 0 {
		t.Errorf("expected zero-value default, got %d", got)
	}
	info.SetEstimatePromptTokens(123)
	if got := info.GetEstimatePromptTokens(); got != 123 {
		t.Errorf("GetEstimatePromptTokens() = %d, want 123", got)
	}
}

func TestSetFirstResponseTimeAndHasSendResponse(t *testing.T) {
	// start is set slightly in the past to guarantee the later time.Now() call
	// inside SetFirstResponseTime() is strictly after it, regardless of clock
	// resolution.
	start := time.Now().Add(-time.Millisecond)
	info := &RelayInfo{
		StartTime:         start,
		FirstResponseTime: start.Add(-time.Second),
		isFirstResponse:   true,
	}
	if info.HasSendResponse() {
		t.Fatal("HasSendResponse() should be false before first response recorded")
	}
	info.SetFirstResponseTime()
	if !info.FirstResponseTime.After(start) {
		t.Errorf("FirstResponseTime should be updated to now, got %v (start %v)", info.FirstResponseTime, start)
	}
	if !info.HasSendResponse() {
		t.Fatal("HasSendResponse() should be true after first response recorded")
	}

	recorded := info.FirstResponseTime
	// Second call must be a no-op because isFirstResponse flips to false.
	info.SetFirstResponseTime()
	if !info.FirstResponseTime.Equal(recorded) {
		t.Errorf("second SetFirstResponseTime() call should be a no-op, got %v want %v", info.FirstResponseTime, recorded)
	}
}

func TestTaskSubmitReqGetPromptHasImage(t *testing.T) {
	req := &TaskSubmitReq{Prompt: "p"}
	if req.GetPrompt() != "p" {
		t.Errorf("GetPrompt() = %q, want p", req.GetPrompt())
	}
	if req.HasImage() {
		t.Error("expected HasImage() false with no images")
	}
	req.Images = []string{"a.png"}
	if !req.HasImage() {
		t.Error("expected HasImage() true with images present")
	}
}

func TestTaskSubmitReqUnmarshalJSON(t *testing.T) {
	t.Run("plain metadata object", func(t *testing.T) {
		var req TaskSubmitReq
		in := `{"prompt":"p","model":"m","metadata":{"k":"v"}}`
		if err := req.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Prompt != "p" || req.Model != "m" {
			t.Errorf("unexpected base fields: %+v", req)
		}
		if req.Metadata["k"] != "v" {
			t.Errorf("expected metadata k=v, got %+v", req.Metadata)
		}
	})

	t.Run("double-encoded metadata string", func(t *testing.T) {
		var req TaskSubmitReq
		// metadata is itself a JSON-encoded string containing an object.
		in := `{"prompt":"p","metadata":"{\"k2\":\"v2\"}"}`
		if err := req.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Metadata["k2"] != "v2" {
			t.Errorf("expected decoded nested metadata, got %+v", req.Metadata)
		}
	})

	t.Run("no metadata field", func(t *testing.T) {
		var req TaskSubmitReq
		in := `{"prompt":"p"}`
		if err := req.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Metadata != nil {
			t.Errorf("expected nil metadata, got %+v", req.Metadata)
		}
	})

	t.Run("invalid top level json errors", func(t *testing.T) {
		var req TaskSubmitReq
		if err := req.UnmarshalJSON([]byte("{not json")); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestTaskSubmitReqUnmarshalMetadata(t *testing.T) {
	type target struct {
		K string `json:"k"`
	}

	t.Run("with metadata", func(t *testing.T) {
		req := &TaskSubmitReq{Metadata: map[string]interface{}{"k": "v"}}
		var tgt target
		if err := req.UnmarshalMetadata(&tgt); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tgt.K != "v" {
			t.Errorf("expected decoded target, got %+v", tgt)
		}
	})

	t.Run("nil metadata is a no-op", func(t *testing.T) {
		req := &TaskSubmitReq{}
		var tgt target
		if err := req.UnmarshalMetadata(&tgt); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tgt.K != "" {
			t.Errorf("expected untouched target, got %+v", tgt)
		}
	})
}

// ---------------------------------------------------------------------------
// GenRelayInfo* family (gin-context-driven construction)
// ---------------------------------------------------------------------------

func newRelayGinContext(t *testing.T, method, path string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, path, nil)
	return c
}

func TestGenBaseRelayInfoBasicsAndPlaygroundPrefix(t *testing.T) {
	c := newRelayGinContext(t, http.MethodPost, "/pg/chat/completions")
	c.Set(string(constant.ContextKeyUserId), 55)
	c.Set(string(constant.ContextKeyUserGroup), "default")
	// Leave token group empty on purpose to exercise the fallback-to-user-group branch.

	info := GenRelayInfoOpenAI(c, nil)
	if info.RelayFormat != types.RelayFormatOpenAI {
		t.Errorf("RelayFormat = %q, want openai", info.RelayFormat)
	}
	if info.UserId != 55 {
		t.Errorf("UserId = %d, want 55", info.UserId)
	}
	if info.TokenGroup != "default" {
		t.Errorf("TokenGroup should fall back to UserGroup, got %q", info.TokenGroup)
	}
	if !info.IsPlayground {
		t.Error("expected IsPlayground true for /pg prefixed path")
	}
	if info.RequestURLPath != "/v1/chat/completions" {
		t.Errorf("RequestURLPath = %q, want /v1/chat/completions after /pg rewrite", info.RequestURLPath)
	}
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		t.Errorf("RelayMode = %d, want RelayModeChatCompletions", info.RelayMode)
	}
}

func TestGenBaseRelayInfoRelayModeFallbackToContext(t *testing.T) {
	c := newRelayGinContext(t, http.MethodPost, "/some/unrecognized/path")
	c.Set("relay_mode", relayconstant.RelayModeEmbeddings)

	info := GenRelayInfoOpenAI(c, nil)
	if info.RelayMode != relayconstant.RelayModeEmbeddings {
		t.Errorf("RelayMode = %d, want fallback RelayModeEmbeddings from gin context", info.RelayMode)
	}
}

func TestGenRelayInfoVariants(t *testing.T) {
	t.Run("Claude sets format and beta flag", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/messages?beta=true")
		info := GenRelayInfoClaude(c, nil)
		if info.RelayFormat != types.RelayFormatClaude {
			t.Errorf("RelayFormat = %q, want claude", info.RelayFormat)
		}
		if !info.IsClaudeBetaQuery {
			t.Error("expected IsClaudeBetaQuery true when beta=true query present")
		}
		if info.ClaudeConvertInfo == nil || info.ClaudeConvertInfo.LastMessagesType != LastMessageTypeNone {
			t.Errorf("expected initialized ClaudeConvertInfo, got %+v", info.ClaudeConvertInfo)
		}
	})

	t.Run("Claude without beta query", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/messages")
		info := GenRelayInfoClaude(c, nil)
		if info.IsClaudeBetaQuery {
			t.Error("expected IsClaudeBetaQuery false without beta query")
		}
	})

	t.Run("Rerank captures documents and ReturnDocuments", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/rerank")
		yes := true
		req := &dto.RerankRequest{Documents: []any{"a", "b"}, ReturnDocuments: &yes}
		info := GenRelayInfoRerank(c, req)
		if info.RelayFormat != types.RelayFormatRerank {
			t.Errorf("RelayFormat = %q, want rerank", info.RelayFormat)
		}
		if info.RelayMode != relayconstant.RelayModeRerank {
			t.Errorf("RelayMode = %d, want RelayModeRerank", info.RelayMode)
		}
		if len(info.RerankerInfo.Documents) != 2 || !info.RerankerInfo.ReturnDocuments {
			t.Errorf("unexpected RerankerInfo: %+v", info.RerankerInfo)
		}
	})

	t.Run("OpenAIAudio sets format", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/audio/speech")
		info := GenRelayInfoOpenAIAudio(c, nil)
		if info.RelayFormat != types.RelayFormatOpenAIAudio {
			t.Errorf("RelayFormat = %q, want openai_audio", info.RelayFormat)
		}
	})

	t.Run("Embedding sets format", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/embeddings")
		info := GenRelayInfoEmbedding(c, nil)
		if info.RelayFormat != types.RelayFormatEmbedding {
			t.Errorf("RelayFormat = %q, want embedding", info.RelayFormat)
		}
	})

	t.Run("Gemini sets format and disables usage", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1beta/models/gemini-pro:generateContent")
		info := GenRelayInfoGemini(c, nil)
		if info.RelayFormat != types.RelayFormatGemini {
			t.Errorf("RelayFormat = %q, want gemini", info.RelayFormat)
		}
		if info.ShouldIncludeUsage {
			t.Error("expected ShouldIncludeUsage false for gemini format")
		}
	})

	t.Run("Image sets format", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/images/generations")
		info := GenRelayInfoImage(c, nil)
		if info.RelayFormat != types.RelayFormatOpenAIImage {
			t.Errorf("RelayFormat = %q, want openai_image", info.RelayFormat)
		}
	})

	t.Run("Ws sets format and audio defaults", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodGet, "/v1/realtime")
		info := GenRelayInfoWs(c, nil)
		if info.RelayFormat != types.RelayFormatOpenAIRealtime {
			t.Errorf("RelayFormat = %q, want openai_realtime", info.RelayFormat)
		}
		if info.InputAudioFormat != "pcm16" || info.OutputAudioFormat != "pcm16" {
			t.Errorf("unexpected audio formats: in=%q out=%q", info.InputAudioFormat, info.OutputAudioFormat)
		}
		if !info.IsFirstRequest {
			t.Error("expected IsFirstRequest true")
		}
	})

	t.Run("Responses collects built-in tools and default context size", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/responses")
		req := &dto.OpenAIResponsesRequest{
			Tools: []byte(`[{"type":"web_search_preview"}]`),
		}
		info := GenRelayInfoResponses(c, req)
		if info.RelayFormat != types.RelayFormatOpenAIResponses {
			t.Errorf("RelayFormat = %q, want openai_responses", info.RelayFormat)
		}
		if info.RelayMode != relayconstant.RelayModeResponses {
			t.Errorf("RelayMode = %d, want RelayModeResponses", info.RelayMode)
		}
		tool, ok := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]
		if !ok {
			t.Fatalf("expected web_search_preview tool tracked, got %+v", info.ResponsesUsageInfo.BuiltInTools)
		}
		if tool.SearchContextSize != "medium" {
			t.Errorf("SearchContextSize = %q, want default medium", tool.SearchContextSize)
		}
	})

	t.Run("Responses with explicit search_context_size", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/responses")
		req := &dto.OpenAIResponsesRequest{
			Tools: []byte(`[{"type":"web_search_preview","search_context_size":"large"}]`),
		}
		info := GenRelayInfoResponses(c, req)
		tool := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]
		if tool.SearchContextSize != "large" {
			t.Errorf("SearchContextSize = %q, want large", tool.SearchContextSize)
		}
	})

	t.Run("Responses without tools leaves empty map", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/responses")
		req := &dto.OpenAIResponsesRequest{}
		info := GenRelayInfoResponses(c, req)
		if len(info.ResponsesUsageInfo.BuiltInTools) != 0 {
			t.Errorf("expected empty BuiltInTools, got %+v", info.ResponsesUsageInfo.BuiltInTools)
		}
	})
}

func TestGenRelayInfoDispatcher(t *testing.T) {
	c := newRelayGinContext(t, http.MethodPost, "/v1/chat/completions")

	cases := []struct {
		name       string
		format     types.RelayFormat
		req        dto.Request
		wantFormat types.RelayFormat
		wantErr    bool
	}{
		{"openai", types.RelayFormatOpenAI, nil, types.RelayFormatOpenAI, false},
		{"openai audio", types.RelayFormatOpenAIAudio, nil, types.RelayFormatOpenAIAudio, false},
		{"openai image", types.RelayFormatOpenAIImage, nil, types.RelayFormatOpenAIImage, false},
		{"claude", types.RelayFormatClaude, nil, types.RelayFormatClaude, false},
		{"gemini", types.RelayFormatGemini, nil, types.RelayFormatGemini, false},
		{"embedding", types.RelayFormatEmbedding, nil, types.RelayFormatEmbedding, false},
		{"rerank ok", types.RelayFormatRerank, &dto.RerankRequest{}, types.RelayFormatRerank, false},
		{"rerank wrong type", types.RelayFormatRerank, nil, "", true},
		{"responses ok", types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{}, types.RelayFormatOpenAIResponses, false},
		{"responses wrong type", types.RelayFormatOpenAIResponses, nil, "", true},
		{"task", types.RelayFormatTask, nil, "", false},
		{"mj proxy", types.RelayFormatMjProxy, nil, "", false},
		{"invalid format", types.RelayFormat("nope"), nil, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := GenRelayInfo(c, tc.format, tc.req, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantFormat != "" && info.RelayFormat != tc.wantFormat {
				t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, tc.wantFormat)
			}
		})
	}

	t.Run("realtime dispatches to Ws variant", func(t *testing.T) {
		info, err := GenRelayInfo(c, types.RelayFormatOpenAIRealtime, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.RelayFormat != types.RelayFormatOpenAIRealtime {
			t.Errorf("RelayFormat = %q, want openai_realtime", info.RelayFormat)
		}
	})
}

func TestInitChannelMeta(t *testing.T) {
	t.Run("basic channel with stream support and settings", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/v1/chat/completions")
		c.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeOpenAI)
		c.Set(string(constant.ContextKeyChannelId), 9)
		c.Set(string(constant.ContextKeyChannelIsMultiKey), true)
		c.Set(string(constant.ContextKeyChannelMultiKeyIndex), 2)
		c.Set(string(constant.ContextKeyChannelBaseUrl), "https://api.openai.com")
		c.Set(string(constant.ContextKeyChannelKey), "sk-test")
		c.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
		c.Set(string(constant.ContextKeyChannelSetting), dto.ChannelSettings{SystemPrompt: "be nice"})
		c.Set(string(constant.ContextKeyChannelOtherSetting), dto.ChannelOtherSettings{AllowServiceTier: true})

		info := &RelayInfo{OriginModelName: "gpt-4o-original", Request: &dto.RerankRequest{}}
		info.InitChannelMeta(c)

		if info.ChannelMeta == nil {
			t.Fatal("expected ChannelMeta to be populated")
		}
		if info.ChannelType != constant.ChannelTypeOpenAI || info.ChannelId != 9 || !info.ChannelIsMultiKey || info.ChannelMultiKeyIndex != 2 {
			t.Errorf("unexpected channel identity fields: %+v", info.ChannelMeta)
		}
		if info.ChannelBaseUrl != "https://api.openai.com" || info.ApiKey != "sk-test" {
			t.Errorf("unexpected channel connection fields: %+v", info.ChannelMeta)
		}
		if info.UpstreamModelName != "gpt-4o" {
			t.Errorf("UpstreamModelName = %q, want gpt-4o", info.UpstreamModelName)
		}
		if !info.SupportStreamOptions {
			t.Error("expected SupportStreamOptions true for OpenAI channel type")
		}
		if info.ChannelSetting.SystemPrompt != "be nice" {
			t.Errorf("ChannelSetting not carried through: %+v", info.ChannelSetting)
		}
		if !info.ChannelOtherSettings.AllowServiceTier {
			t.Errorf("ChannelOtherSettings not carried through: %+v", info.ChannelOtherSettings)
		}
		// Request.SetModelName should have been invoked with OriginModelName.
		if req, ok := info.Request.(*dto.RerankRequest); !ok || req.Model != "gpt-4o-original" {
			t.Errorf("expected Request.SetModelName called with OriginModelName, got %+v", info.Request)
		}
	})

	t.Run("Azure channel resolves api version from query", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/?api-version=2024-06-01", nil)
		c.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeAzure)

		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		info.InitChannelMeta(c)
		if info.ApiVersion != "2024-06-01" {
			t.Errorf("ApiVersion = %q, want 2024-06-01", info.ApiVersion)
		}
	})

	t.Run("VertexAI channel resolves api version from region", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/")
		c.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeVertexAi)
		c.Set("region", "us-central1")

		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		info.InitChannelMeta(c)
		if info.ApiVersion != "us-central1" {
			t.Errorf("ApiVersion = %q, want us-central1 (region)", info.ApiVersion)
		}
	})

	t.Run("nil Request does not panic and leaves ChannelMeta populated", func(t *testing.T) {
		c := newRelayGinContext(t, http.MethodPost, "/")
		info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
		info.InitChannelMeta(c)
		if info.ChannelMeta == nil {
			t.Fatal("expected ChannelMeta populated even with nil Request")
		}
	})
}

// ---------------------------------------------------------------------------
// override.go gap-fill: paths not exercised by override_test.go
// ---------------------------------------------------------------------------

func TestApplyParamOverrideLegacyPath(t *testing.T) {
	// No "operations" key -> falls back to the legacy direct-merge path.
	input := []byte(`{"model":"gpt-4","temperature":0.7}`)
	override := map[string]interface{}{
		"temperature": 0.2,
		"max_tokens":  1024,
	}

	out, err := ApplyParamOverride(input, override, nil)
	if err != nil {
		t.Fatalf("ApplyParamOverride returned error: %v", err)
	}
	assertJSONEqual(t, `{"model":"gpt-4","temperature":0.2,"max_tokens":1024}`, string(out))
}

func TestApplyParamOverrideLegacyPathInvalidJSON(t *testing.T) {
	_, err := ApplyParamOverride([]byte("{not json"), map[string]interface{}{"a": 1}, nil)
	if err == nil {
		t.Fatal("expected error unmarshalling invalid legacy input JSON")
	}
}

func TestApplyParamOverrideEmptyOverride(t *testing.T) {
	input := []byte(`{"model":"gpt-4"}`)
	out, err := ApplyParamOverride(input, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(input) {
		t.Errorf("expected passthrough for empty override, got %s", out)
	}
}

func TestApplyParamOverrideFullEqualityCondition(t *testing.T) {
	// "full" mode exercises compareEqual for matching and non-matching values,
	// plus type-mismatch and null-vs-null branches.
	cases := []struct {
		name  string
		json  string
		value interface{}
		want  bool
	}{
		{"string equal", `{"model":"gpt-4"}`, "gpt-4", true},
		{"string not equal", `{"model":"gpt-4"}`, "gpt-5", false},
		{"number equal", `{"n":5}`, 5.0, true},
		{"number not equal", `{"n":5}`, 6.0, false},
		{"bool equal", `{"b":true}`, true, true},
		{"bool not equal", `{"b":true}`, false, false},
		{"null equal", `{"n":null}`, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			override := map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{
						"path":  "temperature",
						"mode":  "set",
						"value": 1,
						"conditions": []interface{}{
							map[string]interface{}{
								"path":  gjsonPathFor(tc.json),
								"mode":  "full",
								"value": tc.value,
							},
						},
					},
				},
			}
			out, err := ApplyParamOverride([]byte(tc.json), override, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotSet := strings.Contains(string(out), `"temperature":1`)
			if gotSet != tc.want {
				t.Errorf("condition result = %v, want %v (out=%s)", gotSet, tc.want, out)
			}
		})
	}
}

// gjsonPathFor extracts the sole top-level key name from a tiny single-field
// JSON object literal, e.g. `{"model":"gpt-4"}` -> "model".
func gjsonPathFor(jsonObj string) string {
	start := strings.Index(jsonObj, `"`) + 1
	end := strings.Index(jsonObj[start:], `"`)
	return jsonObj[start : start+end]
}

func TestApplyParamOverrideFullTypeMismatchErrors(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path":  "temperature",
				"mode":  "set",
				"value": 1,
				"conditions": []interface{}{
					map[string]interface{}{
						"path":  "model",
						"mode":  "full",
						"value": 42, // number vs. string model field -> type mismatch
					},
				},
			},
		},
	}
	_, err := ApplyParamOverride([]byte(`{"model":"gpt-4","temperature":0.5}`), override, nil)
	if err == nil {
		t.Fatal("expected error for compareEqual type mismatch")
	}
}

func TestApplyParamOverrideNumericConditionOperators(t *testing.T) {
	cases := []struct {
		mode string
		val  interface{}
		want bool
	}{
		{"gte", 5.0, true},
		{"gte", 6.0, false},
		{"lt", 6.0, true},
		{"lt", 5.0, false},
		{"lte", 5.0, true},
		{"lte", 4.0, false},
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("%s_case_%d", tc.mode, i), func(t *testing.T) {
			override := map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{
						"path":  "flag",
						"mode":  "set",
						"value": true,
						"conditions": []interface{}{
							map[string]interface{}{
								"path":  "n",
								"mode":  tc.mode,
								"value": tc.val,
							},
						},
					},
				},
			}
			out, err := ApplyParamOverride([]byte(`{"n":5}`), override, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := strings.Contains(string(out), `"flag":true`)
			if got != tc.want {
				t.Errorf("%s %v: condition result = %v, want %v (out=%s)", tc.mode, tc.val, got, tc.want, out)
			}
		})
	}
}

func TestApplyParamOverrideNumericConditionNonNumberErrors(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path":  "flag",
				"mode":  "set",
				"value": true,
				"conditions": []interface{}{
					map[string]interface{}{
						"path":  "model",
						"mode":  "gt",
						"value": 5,
					},
				},
			},
		},
	}
	_, err := ApplyParamOverride([]byte(`{"model":"gpt-4"}`), override, nil)
	if err == nil {
		t.Fatal("expected error for numeric comparison against a non-number field")
	}
}

func TestApplyParamOverrideUnsupportedComparisonModeErrors(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path":  "flag",
				"mode":  "set",
				"value": true,
				"conditions": []interface{}{
					map[string]interface{}{
						"path":  "model",
						"mode":  "bogus_mode",
						"value": "x",
					},
				},
			},
		},
	}
	_, err := ApplyParamOverride([]byte(`{"model":"gpt-4"}`), override, nil)
	if err == nil {
		t.Fatal("expected error for unsupported comparison mode")
	}
}

func TestApplyParamOverrideUnknownOperationModeErrors(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "model",
				"mode": "not_a_real_mode",
			},
		},
	}
	_, err := ApplyParamOverride([]byte(`{"model":"gpt-4"}`), override, nil)
	if err == nil {
		t.Fatal("expected error for unknown operation mode")
	}
}

func TestApplyParamOverrideOperationsNotAList(t *testing.T) {
	// "operations" present but not a []interface{} -> tryParseOperations fails
	// and ApplyParamOverride falls back to the legacy merge path, which
	// preserves the map verbatim.
	input := []byte(`{"model":"gpt-4"}`)
	override := map[string]interface{}{
		"operations": "not-a-list",
	}
	out, err := ApplyParamOverride(input, override, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONEqual(t, `{"model":"gpt-4","operations":"not-a-list"}`, string(out))
}

func TestApplyParamOverrideOperationEntryNotAMap(t *testing.T) {
	// tryParseOperations bails out (returns false) as soon as it hits a
	// non-map operation entry, so ApplyParamOverride falls back to the legacy
	// merge path, which just assigns the raw override value verbatim.
	override := map[string]interface{}{
		"operations": []interface{}{"not-a-map"},
	}
	out, err := ApplyParamOverride([]byte(`{"model":"gpt-4"}`), override, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	ops, ok := got["operations"].([]interface{})
	if !ok || len(ops) != 1 || ops[0] != "not-a-map" {
		t.Errorf("expected legacy path to keep the raw operations value, got %+v", got)
	}
}

func TestApplyParamOverrideMissingModeInOperation(t *testing.T) {
	// An operation object without "mode" makes tryParseOperations fail ->
	// legacy merge path is used instead of the structured operations path.
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "model",
			},
		},
	}
	out, err := ApplyParamOverride([]byte(`{"model":"gpt-4"}`), override, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if _, ok := got["operations"]; !ok {
		t.Errorf("expected legacy path to keep the raw operations key, got %s", out)
	}
}

func TestBuildParamOverrideContext(t *testing.T) {
	t.Run("nil info returns nil", func(t *testing.T) {
		if got := BuildParamOverrideContext(nil); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("nil ChannelMeta returns nil", func(t *testing.T) {
		info := &RelayInfo{OriginModelName: "gpt-4"}
		if got := BuildParamOverrideContext(info); got != nil {
			t.Errorf("expected nil when ChannelMeta is nil, got %+v", got)
		}
	})

	t.Run("upstream model name present", func(t *testing.T) {
		info := &RelayInfo{
			OriginModelName: "gpt-4",
			ChannelMeta:     &ChannelMeta{UpstreamModelName: "gpt-4-mapped"},
		}
		ctx := BuildParamOverrideContext(info)
		if ctx["model"] != "gpt-4-mapped" || ctx["upstream_model"] != "gpt-4-mapped" || ctx["original_model"] != "gpt-4" {
			t.Errorf("unexpected context: %+v", ctx)
		}
	})

	t.Run("falls back to original model name when no upstream mapping", func(t *testing.T) {
		info := &RelayInfo{
			OriginModelName: "gpt-4",
			ChannelMeta:     &ChannelMeta{},
		}
		ctx := BuildParamOverrideContext(info)
		if ctx["model"] != "gpt-4" || ctx["original_model"] != "gpt-4" {
			t.Errorf("unexpected context: %+v", ctx)
		}
		if _, exists := ctx["upstream_model"]; exists {
			t.Errorf("did not expect upstream_model without an UpstreamModelName, got %+v", ctx)
		}
	})

	t.Run("empty ChannelMeta and empty OriginModelName returns nil", func(t *testing.T) {
		info := &RelayInfo{ChannelMeta: &ChannelMeta{}}
		if got := BuildParamOverrideContext(info); got != nil {
			t.Errorf("expected nil for empty context, got %+v", got)
		}
	})
}
