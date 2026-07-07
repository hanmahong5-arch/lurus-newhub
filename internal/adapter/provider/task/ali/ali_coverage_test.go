package ali

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
	constant.MaxRequestBodyMB = -1 // no limit, isolate tests from real body-size config
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    99,
			ChannelBaseUrl: "https://ali.example.com",
			ApiKey:         "sk-ali",
		},
	}
	a.Init(info)

	if a.ChannelType != 99 {
		t.Errorf("ChannelType = %d, want 99", a.ChannelType)
	}
	if a.baseURL != "https://ali.example.com" {
		t.Errorf("baseURL = %q, want https://ali.example.com", a.baseURL)
	}
	if a.apiKey != "sk-ali" {
		t.Errorf("apiKey = %q, want sk-ali", a.apiKey)
	}
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://ali.example.com"}
	url, err := a.BuildRequestURL(&relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://ali.example.com/api/v1/services/aigc/video-generation/video-synthesis"
	if url != want {
		t.Errorf("BuildRequestURL() = %q, want %q", url, want)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-abc"}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/", nil)

	err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-abc" {
		t.Errorf("Authorization = %q, want Bearer sk-abc", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("X-DashScope-Async"); got != "enable" {
		t.Errorf("X-DashScope-Async = %q, want enable", got)
	}
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	want := []string{
		"wan2.5-i2v-preview",
		"wan2.2-i2v-flash",
		"wan2.2-i2v-plus",
		"wanx2.1-i2v-plus",
		"wanx2.1-i2v-turbo",
	}
	if len(models) != len(want) {
		t.Fatalf("GetModelList() len = %d, want %d", len(models), len(want))
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, models[i], m)
		}
	}
	if got := a.GetChannelName(); got != "ali" {
		t.Errorf("GetChannelName() = %q, want ali", got)
	}
}

func TestSizeToResolution(t *testing.T) {
	tests := []struct {
		size    string
		want    string
		wantErr bool
	}{
		{size: "832*480", want: "480P"},
		{size: "480*832", want: "480P"},
		{size: "624*624", want: "480P"},
		{size: "1280*720", want: "720P"},
		{size: "832*1088", want: "720P"},
		{size: "1920*1080", want: "1080P"},
		{size: "1248*1632", want: "1080P"},
		{size: "999*999", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got, err := sizeToResolution(tt.size)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for size %q", tt.size)
				}
				if err.Error() != "invalid size: "+tt.size {
					t.Errorf("err = %q, want %q", err.Error(), "invalid size: "+tt.size)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("sizeToResolution(%q) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestProcessAliOtherRatios(t *testing.T) {
	tests := []struct {
		name    string
		req     *AliVideoRequest
		want    map[string]float64
		wantErr bool
	}{
		{
			name: "size resolves to 480P with matching model ratio",
			req: &AliVideoRequest{
				Model:      "wan2.5-t2v-preview",
				Parameters: &AliVideoParameters{Size: "832*480"},
			},
			want: map[string]float64{"resolution-480P": 1},
		},
		{
			name: "resolution field lowercase gets uppercased and suffixed",
			req: &AliVideoRequest{
				Model:      "wan2.6-i2v",
				Parameters: &AliVideoParameters{Resolution: "720"},
			},
			want: map[string]float64{"resolution-720P": 1},
		},
		{
			name: "resolution field already has P suffix",
			req: &AliVideoRequest{
				Model:      "wan2.6-i2v",
				Parameters: &AliVideoParameters{Resolution: "1080p"},
			},
			want: map[string]float64{"resolution-1080P": 1 / 0.6},
		},
		{
			name: "model not in ratio table yields empty map",
			req: &AliVideoRequest{
				Model:      "unknown-model",
				Parameters: &AliVideoParameters{Resolution: "720P"},
			},
			want: map[string]float64{},
		},
		{
			name: "resolution present in model table but not matched",
			req: &AliVideoRequest{
				Model:      "wan2.2-i2v-flash",
				Parameters: &AliVideoParameters{Resolution: "1080P"},
			},
			want: map[string]float64{},
		},
		{
			name: "invalid size propagates error",
			req: &AliVideoRequest{
				Model:      "wan2.5-t2v-preview",
				Parameters: &AliVideoParameters{Size: "bad-size"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProcessAliOtherRatios(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("got[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{}
}

func TestConvertToAliRequest(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("t2v model requires * size", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-t2v-plus", Size: "720p"}
		_, err := a.convertToAliRequest(info, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		want := "invalid size: 720p, example: 1920*1080"
		if err.Error() != want {
			t.Errorf("err = %q, want %q", err.Error(), want)
		}
	})

	t.Run("explicit * size sets Parameters.Size", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-t2v-plus", Prompt: "p", Size: "832*480"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Size != "832*480" {
			t.Errorf("Size = %q, want 832*480", out.Parameters.Size)
		}
		if out.Parameters.Resolution != "" {
			t.Errorf("Resolution = %q, want empty", out.Parameters.Resolution)
		}
	})

	t.Run("non t2v size without * becomes resolution", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-plus", Prompt: "p", Size: "720p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "720P" {
			t.Errorf("Resolution = %q, want 720P", out.Parameters.Resolution)
		}
		if out.Parameters.Size != "" {
			t.Errorf("Size = %q, want empty", out.Parameters.Size)
		}
	})

	t.Run("non t2v size already suffixed P is untouched", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-plus", Prompt: "p", Size: "480P"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "480P" {
			t.Errorf("Resolution = %q, want 480P", out.Parameters.Resolution)
		}
	})

	t.Run("default size for wan2.5 t2v model", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.5-t2v-preview", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Size != "1920*1080" {
			t.Errorf("Size = %q, want 1920*1080", out.Parameters.Size)
		}
	})

	t.Run("default size for wan2.2 t2v model", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-t2v-plus", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Size != "1920*1080" {
			t.Errorf("Size = %q, want 1920*1080", out.Parameters.Size)
		}
	})

	t.Run("default size for other t2v model", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "other-t2v-model", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Size != "1280*720" {
			t.Errorf("Size = %q, want 1280*720", out.Parameters.Size)
		}
	})

	t.Run("default resolution wan2.6", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.6-i2v", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "1080P" {
			t.Errorf("Resolution = %q, want 1080P", out.Parameters.Resolution)
		}
	})

	t.Run("default resolution wan2.5", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.5-i2v-preview", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "1080P" {
			t.Errorf("Resolution = %q, want 1080P", out.Parameters.Resolution)
		}
	})

	t.Run("default resolution wan2.2-i2v-flash", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-flash", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "720P" {
			t.Errorf("Resolution = %q, want 720P", out.Parameters.Resolution)
		}
	})

	t.Run("default resolution wan2.2-i2v-plus", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-plus", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "1080P" {
			t.Errorf("Resolution = %q, want 1080P", out.Parameters.Resolution)
		}
	})

	t.Run("default resolution fallback", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-turbo", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Resolution != "720P" {
			t.Errorf("Resolution = %q, want 720P", out.Parameters.Resolution)
		}
	})

	t.Run("duration from req.Duration takes precedence", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-turbo", Prompt: "p", Duration: 8, Seconds: "3"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Duration != 8 {
			t.Errorf("Duration = %d, want 8", out.Parameters.Duration)
		}
	})

	t.Run("duration from seconds string", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-turbo", Prompt: "p", Seconds: "6"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Duration != 6 {
			t.Errorf("Duration = %d, want 6", out.Parameters.Duration)
		}
	})

	t.Run("invalid seconds string errors", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-turbo", Prompt: "p", Seconds: "not-a-number"}
		_, err := a.convertToAliRequest(info, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "convert seconds to int failed") {
			t.Errorf("err = %q, want to contain 'convert seconds to int failed'", err.Error())
		}
	})

	t.Run("default duration when neither set", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-turbo", Prompt: "p"}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Parameters.Duration != 5 {
			t.Errorf("Duration = %d, want 5", out.Parameters.Duration)
		}
	})

	t.Run("metadata merges extra fields", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{
			Model:  "wanx2.1-i2v-turbo",
			Prompt: "p",
			Metadata: map[string]interface{}{
				"model": "wanx2.1-i2v-turbo",
				"input": map[string]interface{}{
					"negative_prompt": "no blur",
				},
			},
		}
		out, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Input.NegativePrompt != "no blur" {
			t.Errorf("NegativePrompt = %q, want 'no blur'", out.Input.NegativePrompt)
		}
		// json.Unmarshal merges into the existing struct field-by-field, so
		// fields absent from the metadata JSON (like prompt) are untouched.
		if out.Input.Prompt != "p" {
			t.Errorf("Input.Prompt = %q, want 'p' (unchanged by metadata merge)", out.Input.Prompt)
		}
	})

	t.Run("metadata that changes model errors", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{
			Model:  "wanx2.1-i2v-turbo",
			Prompt: "p",
			Metadata: map[string]interface{}{
				"model": "some-other-model",
			},
		}
		_, err := a.convertToAliRequest(info, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "can't change model with metadata" {
			t.Errorf("err = %q, want \"can't change model with metadata\"", err.Error())
		}
	})

	t.Run("price data other ratios set from duration and resolution", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-flash", Prompt: "p", Duration: 4}
		_, err := a.convertToAliRequest(info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.PriceData.OtherRatios["seconds"] != 4 {
			t.Errorf("OtherRatios[seconds] = %v, want 4", info.PriceData.OtherRatios["seconds"])
		}
		if info.PriceData.OtherRatios["resolution-720P"] != 2 {
			t.Errorf("OtherRatios[resolution-720P] = %v, want 2", info.PriceData.OtherRatios["resolution-720P"])
		}
	})

	t.Run("invalid ratio size propagates error", func(t *testing.T) {
		info := newInfo()
		req := relaycommon.TaskSubmitReq{Model: "wan2.2-t2v-plus", Prompt: "p", Size: "1*1"}
		_, err := a.convertToAliRequest(info, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid size: 1*1") {
			t.Errorf("err = %q, want to contain 'invalid size: 1*1'", err.Error())
		}
	})
}

func newJSONRequestContext(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

// newValidateInfo builds a RelayInfo with the TaskRelayInfo embed
// initialized, since ValidateMultipartDirect writes to info.Action which is
// promoted from the (otherwise nil) *TaskRelayInfo pointer.
func newValidateInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
}

func TestValidateRequestAndSetAction(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("invalid json body", func(t *testing.T) {
		c, _ := newJSONRequestContext(t, "{not-json")
		info := newValidateInfo()
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "unmarshal_task_request_failed" {
			t.Errorf("Code = %q, want unmarshal_task_request_failed", taskErr.Code)
		}
	})

	t.Run("convert failure surfaces as convert_to_ali_request_failed", func(t *testing.T) {
		c, _ := newJSONRequestContext(t, `{"model":"wan2.2-t2v-plus","prompt":"p","size":"720p"}`)
		info := newValidateInfo()
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "convert_to_ali_request_failed" {
			t.Errorf("Code = %q, want convert_to_ali_request_failed", taskErr.Code)
		}
	})

	t.Run("valid request sets aliReq and passes validation", func(t *testing.T) {
		c, _ := newJSONRequestContext(t, `{"model":"wan2.5-i2v-preview","prompt":"a cat"}`)
		info := newValidateInfo()
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr != nil {
			t.Fatalf("unexpected taskErr: %+v", taskErr)
		}
		if a.aliReq == nil {
			t.Fatal("expected aliReq to be set")
		}
		if a.aliReq.Model != "wan2.5-i2v-preview" {
			t.Errorf("aliReq.Model = %q, want wan2.5-i2v-preview", a.aliReq.Model)
		}
		if a.aliReq.Input.Prompt != "a cat" {
			t.Errorf("aliReq.Input.Prompt = %q, want 'a cat'", a.aliReq.Input.Prompt)
		}
	})

	t.Run("missing prompt fails ValidateMultipartDirect", func(t *testing.T) {
		c, _ := newJSONRequestContext(t, `{"model":"wan2.5-i2v-preview"}`)
		info := newValidateInfo()
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected taskErr for missing prompt, got nil")
		}
		if taskErr.Code != "invalid_request" {
			t.Errorf("Code = %q, want invalid_request", taskErr.Code)
		}
	})
}

func TestBuildRequestBody(t *testing.T) {
	a := &TaskAdaptor{
		aliReq: &AliVideoRequest{
			Model: "wan2.5-i2v-preview",
			Input: AliVideoInput{Prompt: "hello"},
		},
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := io.ReadAll(body)
	if readErr != nil {
		t.Fatalf("read body error: %v", readErr)
	}
	var got AliVideoRequest
	if jsonErr := json.Unmarshal(data, &got); jsonErr != nil {
		t.Fatalf("unmarshal error: %v", jsonErr)
	}
	if got.Model != "wan2.5-i2v-preview" || got.Input.Prompt != "hello" {
		t.Errorf("got = %+v, want Model=wan2.5-i2v-preview Input.Prompt=hello", got)
	}
}

func newBodyResponse(body string) io.ReadCloser {
	return io.NopCloser(bytes.NewBufferString(body))
}

func TestDoResponse(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("valid response with task id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = newBodyResponse(`{"output":{"task_id":"task-1","task_status":"PENDING"},"request_id":"r1"}`)

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{OriginModelName: "wan2.5-i2v-preview"})
		if taskErr != nil {
			t.Fatalf("unexpected taskErr: %+v", taskErr)
		}
		if taskID != "task-1" {
			t.Errorf("taskID = %q, want task-1", taskID)
		}
		if len(taskData) == 0 {
			t.Error("expected non-empty taskData")
		}
		if w.Code != 200 {
			t.Errorf("response code = %d, want 200", w.Code)
		}
	})

	t.Run("invalid json body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = newBodyResponse(`not-json`)

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "unmarshal_response_body_failed" {
			t.Errorf("Code = %q, want unmarshal_response_body_failed", taskErr.Code)
		}
		if taskID != "" || taskData != nil {
			t.Errorf("taskID=%q taskData=%v, want empty/nil", taskID, taskData)
		}
	})

	t.Run("api error code", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{Code: 400}
		httpResp := resp.Result()
		httpResp.Body = newBodyResponse(`{"code":"InvalidParameter","message":"bad param"}`)

		taskID, _, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "ali_api_error" {
			t.Errorf("Code = %q, want ali_api_error", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
	})

	t.Run("empty task id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = newBodyResponse(`{"output":{"task_id":""}}`)

		taskID, _, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "invalid_response" {
			t.Errorf("Code = %q, want invalid_response", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
	})
}

func TestFetchTask(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("missing task_id", func(t *testing.T) {
		resp, err := a.FetchTask("https://ali.example.com", "sk-key", map[string]any{}, "")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("task_id wrong type", func(t *testing.T) {
		resp, err := a.FetchTask("https://ali.example.com", "sk-key", map[string]any{"task_id": 123}, "")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("invalid proxy causes client creation failure", func(t *testing.T) {
		resp, err := a.FetchTask("https://ali.example.com", "sk-key", map[string]any{"task_id": "t1"}, "://bad-proxy")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error for bad proxy, got nil")
		}
		if !strings.Contains(err.Error(), "new proxy http client failed") {
			t.Errorf("err = %q, want to contain 'new proxy http client failed'", err.Error())
		}
	})
}

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	tests := []struct {
		name       string
		body       string
		wantStatus repo.TaskStatus
		wantURL    string
		wantReason string
	}{
		{name: "pending", body: `{"output":{"task_status":"PENDING"}}`, wantStatus: repo.TaskStatusQueued},
		{name: "running", body: `{"output":{"task_status":"RUNNING"}}`, wantStatus: repo.TaskStatusInProgress},
		{
			name:       "succeeded with video url",
			body:       `{"output":{"task_status":"SUCCEEDED","video_url":"https://video.mp4"}}`,
			wantStatus: repo.TaskStatusSuccess,
			wantURL:    "https://video.mp4",
		},
		{
			name:       "failed with top-level message",
			body:       `{"output":{"task_status":"FAILED"},"message":"top level failure"}`,
			wantStatus: repo.TaskStatusFailure,
			wantReason: "top level failure",
		},
		{
			name:       "failed with output message only",
			body:       `{"output":{"task_status":"FAILED","code":"E1","message":"output failure"}}`,
			wantStatus: repo.TaskStatusFailure,
			wantReason: "task failed, code: E1 , message: output failure",
		},
		{
			name:       "failed with neither message",
			body:       `{"output":{"task_status":"FAILED"}}`,
			wantStatus: repo.TaskStatusFailure,
			wantReason: "task failed",
		},
		{name: "canceled", body: `{"output":{"task_status":"CANCELED"}}`, wantStatus: repo.TaskStatusFailure, wantReason: "task failed"},
		{name: "unknown status literal", body: `{"output":{"task_status":"UNKNOWN"}}`, wantStatus: repo.TaskStatusFailure, wantReason: "task failed"},
		{name: "unrecognized status defaults to queued", body: `{"output":{"task_status":"WEIRD"}}`, wantStatus: repo.TaskStatusQueued},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Code != 0 {
				t.Errorf("Code = %d, want 0", info.Code)
			}
			if info.Status != string(tt.wantStatus) {
				t.Errorf("Status = %q, want %q", info.Status, tt.wantStatus)
			}
			if info.Url != tt.wantURL {
				t.Errorf("Url = %q, want %q", info.Url, tt.wantURL)
			}
			if info.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", info.Reason, tt.wantReason)
			}
		})
	}

	t.Run("invalid json", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`not-json`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if info != nil {
			t.Errorf("info = %+v, want nil", info)
		}
	})
}

func TestConvertToOpenAIVideo(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("success without error fields", func(t *testing.T) {
		task := &repo.Task{
			TaskID:    "task-1",
			CreatedAt: 111,
			UpdatedAt: 222,
			Progress:  "50%",
			Data:      []byte(`{"output":{"task_id":"task-1","task_status":"RUNNING","video_url":""}}`),
		}
		task.Properties.OriginModelName = "wan2.5-i2v-preview"

		out, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video dto.OpenAIVideo
		if jsonErr := json.Unmarshal(out, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video.ID != "task-1" {
			t.Errorf("ID = %q, want task-1", video.ID)
		}
		if video.Status != dto.VideoStatusInProgress {
			t.Errorf("Status = %q, want %q", video.Status, dto.VideoStatusInProgress)
		}
		if video.Model != "wan2.5-i2v-preview" {
			t.Errorf("Model = %q, want wan2.5-i2v-preview", video.Model)
		}
		if video.Error != nil {
			t.Errorf("Error = %+v, want nil", video.Error)
		}
	})

	t.Run("top level code sets error", func(t *testing.T) {
		task := &repo.Task{
			TaskID: "task-2",
			Data:   []byte(`{"output":{"task_status":"FAILED"},"code":"E-TOP","message":"top failure"}`),
		}
		out, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video dto.OpenAIVideo
		if jsonErr := json.Unmarshal(out, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video.Error == nil {
			t.Fatal("expected Error, got nil")
		}
		if video.Error.Code != "E-TOP" || video.Error.Message != "top failure" {
			t.Errorf("Error = %+v, want Code=E-TOP Message='top failure'", video.Error)
		}
	})

	t.Run("output level code sets error when top level empty", func(t *testing.T) {
		task := &repo.Task{
			TaskID: "task-3",
			Data:   []byte(`{"output":{"task_status":"FAILED","code":"E-OUT","message":"output failure"}}`),
		}
		out, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video dto.OpenAIVideo
		if jsonErr := json.Unmarshal(out, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video.Error == nil {
			t.Fatal("expected Error, got nil")
		}
		if video.Error.Code != "E-OUT" || video.Error.Message != "output failure" {
			t.Errorf("Error = %+v, want Code=E-OUT Message='output failure'", video.Error)
		}
	})

	t.Run("invalid json errors", func(t *testing.T) {
		task := &repo.Task{Data: []byte(`not-json`)}
		out, err := a.ConvertToOpenAIVideo(task)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if out != nil {
			t.Errorf("out = %v, want nil", out)
		}
	})
}

func TestConvertAliStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"PENDING", dto.VideoStatusQueued},
		{"RUNNING", dto.VideoStatusInProgress},
		{"SUCCEEDED", dto.VideoStatusCompleted},
		{"FAILED", dto.VideoStatusFailed},
		{"CANCELED", dto.VideoStatusFailed},
		{"UNKNOWN", dto.VideoStatusFailed},
		{"anything-else", dto.VideoStatusUnknown},
	}
	for _, tt := range tests {
		if got := convertAliStatus(tt.in); got != tt.want {
			t.Errorf("convertAliStatus(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
