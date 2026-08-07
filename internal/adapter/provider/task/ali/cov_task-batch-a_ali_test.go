package ali

// Business-acceptance tests for the Ali (Tongyi Wanxiang) async video task
// adaptor: submit-task request construction, status polling / state-machine
// mapping, result URL propagation, and billing ratio derivation.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func taskBatchANewGinCtx(t *testing.T, method, path string, body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

// taskBatchANoBodyLimit lifts the request-body size guard, which defaults to
// 0MB (i.e. truncate everything) until production init runs. Restores the
// prior value on cleanup.
func taskBatchANoBodyLimit(t *testing.T) {
	t.Helper()
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })
}

// taskBatchAAllowLoopbackHTTP initializes the shared relay HTTP client and
// disables the private-IP SSRF dial guard so httptest.Server loopback stubs
// used by FetchTask/DoRequest tests are reachable. Restores on cleanup.
func taskBatchAAllowLoopbackHTTP(t *testing.T) {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func taskBatchANewRelayInfo(baseURL, apiKey string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    5,
			ChannelBaseUrl: baseURL,
			ApiKey:         apiKey,
			ApiVersion:     "",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

// ─── Init / URL / Header / Body ─────────────────────────────────────────────

func TestAli_Init(t *testing.T) {
	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo("https://dashscope.aliyuncs.com", "sk-ali-key")
	a.Init(info)

	if a.baseURL != "https://dashscope.aliyuncs.com" {
		t.Fatalf("expected baseURL to be captured from RelayInfo, got %q", a.baseURL)
	}
	if a.apiKey != "sk-ali-key" {
		t.Fatalf("expected apiKey to be captured, got %q", a.apiKey)
	}
	if a.ChannelType != 5 {
		t.Fatalf("expected ChannelType 5, got %d", a.ChannelType)
	}
}

func TestAli_BuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://dashscope.aliyuncs.com"}
	info := taskBatchANewRelayInfo("https://dashscope.aliyuncs.com", "sk-x")
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	if url != want {
		t.Fatalf("expected submit URL %q, got %q", want, url)
	}
}

func TestAli_BuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-secret-token"}
	c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
	req, _ := http.NewRequest(http.MethodPost, "https://dashscope.aliyuncs.com/x", nil)

	if err := a.BuildRequestHeader(c, req, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-secret-token" {
		t.Fatalf("expected Authorization bearer header with api key, got %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected json content type, got %q", got)
	}
	if got := req.Header.Get("X-DashScope-Async"); got != "enable" {
		t.Fatalf("ali async video tasks require X-DashScope-Async: enable, got %q", got)
	}
}

func TestAli_BuildRequestBody(t *testing.T) {
	a := &TaskAdaptor{
		aliReq: &AliVideoRequest{
			Model: "wan2.2-i2v-plus",
			Input: AliVideoInput{Prompt: "a cat"},
			Parameters: &AliVideoParameters{
				Resolution: "720P",
				Duration:   5,
			},
		},
	}
	r, err := a.BuildRequestBody(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	var got AliVideoRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("body must be valid json mirroring aliReq: %v", err)
	}
	if got.Model != "wan2.2-i2v-plus" || got.Input.Prompt != "a cat" || got.Parameters.Resolution != "720P" {
		t.Fatalf("built body does not preserve submitted fields: %+v", got)
	}
}

// ─── ValidateRequestAndSetAction ────────────────────────────────────────────

func TestAli_ValidateRequestAndSetAction(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		errCode   string
		wantModel string
	}{
		{
			name:      "valid t2v request builds ali request and passes",
			body:      `{"prompt":"a dog running","model":"wanx2.1-i2v-turbo","input_reference":"https://img/a.png"}`,
			wantErr:   false,
			wantModel: "wanx2.1-i2v-turbo",
		},
		{
			name:    "malformed json body is rejected before hitting upstream",
			body:    `{"prompt": `,
			wantErr: true,
			errCode: "unmarshal_task_request_failed",
		},
		{
			name:    "t2v model with non-wildcard size is rejected (billing/size contract)",
			body:    `{"prompt":"p","model":"wanx2.1-t2v-turbo","size":"720p"}`,
			wantErr: true,
			errCode: "convert_to_ali_request_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskBatchANoBodyLimit(t)
			a := &TaskAdaptor{}
			info := taskBatchANewRelayInfo("https://dashscope.aliyuncs.com", "sk-x")
			c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(tt.body), "application/json")

			taskErr := a.ValidateRequestAndSetAction(c, info)
			if tt.wantErr {
				if taskErr == nil {
					t.Fatalf("expected a task error, got nil (aliReq=%+v)", a.aliReq)
				}
				if taskErr.Code != tt.errCode {
					t.Fatalf("expected error code %q, got %q (message=%s)", tt.errCode, taskErr.Code, taskErr.Message)
				}
				return
			}
			if taskErr != nil {
				t.Fatalf("expected success, got task error: %s / %s", taskErr.Code, taskErr.Message)
			}
			if a.aliReq == nil {
				t.Fatalf("expected adaptor to cache the converted ali request")
			}
			if a.aliReq.Model != tt.wantModel {
				t.Fatalf("expected model %q preserved, got %q", tt.wantModel, a.aliReq.Model)
			}
		})
	}
}

// ─── sizeToResolution / ProcessAliOtherRatios (billing) ─────────────────────

func TestAli_SizeToResolution(t *testing.T) {
	tests := []struct {
		size    string
		want    string
		wantErr bool
	}{
		{"832*480", "480P", false},
		{"1280*720", "720P", false},
		{"1920*1080", "1080P", false},
		{"1632*1248", "1080P", false},
		{"999*999", "", true},
	}
	for _, tt := range tests {
		got, err := sizeToResolution(tt.size)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("size %q: expected error for unsupported size", tt.size)
			}
			continue
		}
		if err != nil {
			t.Fatalf("size %q: unexpected error: %v", tt.size, err)
		}
		if got != tt.want {
			t.Fatalf("size %q: expected resolution %q, got %q", tt.size, tt.want, got)
		}
	}
}

func TestAli_ProcessAliOtherRatios(t *testing.T) {
	tests := []struct {
		name       string
		req        *AliVideoRequest
		wantKey    string
		wantVal    float64
		wantErr    bool
		wantNoKeys bool
	}{
		{
			name: "wan2.5-t2v-preview 1080P uses discount ratio via size lookup",
			req: &AliVideoRequest{
				Model:      "wan2.5-t2v-preview",
				Parameters: &AliVideoParameters{Size: "1920*1080"},
			},
			wantKey: "resolution-1080P",
			wantVal: 1.0 / 0.3,
		},
		{
			name: "wan2.2-i2v-plus 480P via resolution field (lower-cased normalized)",
			req: &AliVideoRequest{
				Model:      "wan2.2-i2v-plus",
				Parameters: &AliVideoParameters{Resolution: "480p"},
			},
			wantKey: "resolution-480P",
			wantVal: 1,
		},
		{
			name: "unknown model yields no billing ratio override",
			req: &AliVideoRequest{
				Model:      "wanx2.1-i2v-turbo",
				Parameters: &AliVideoParameters{Resolution: "720P"},
			},
			wantNoKeys: true,
		},
		{
			name: "invalid size propagates sizeToResolution error",
			req: &AliVideoRequest{
				Model:      "wan2.2-t2v-plus",
				Parameters: &AliVideoParameters{Size: "1*1"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProcessAliOtherRatios(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ratios=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNoKeys {
				if len(got) != 0 {
					t.Fatalf("expected no other-ratio override, got %v", got)
				}
				return
			}
			v, ok := got[tt.wantKey]
			if !ok {
				t.Fatalf("expected key %q in ratios %v", tt.wantKey, got)
			}
			if v != tt.wantVal {
				t.Fatalf("expected ratio %v for %q, got %v", tt.wantVal, tt.wantKey, v)
			}
		})
	}
}

// ─── convertToAliRequest (submit-task construction, billing side effects) ──

func TestAli_ConvertToAliRequest(t *testing.T) {
	t.Run("t2v with plain size (no *) is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		_, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wanx2.1-t2v-turbo", Prompt: "p", Size: "720p",
		})
		if err == nil {
			t.Fatalf("expected error for non-wildcard size on t2v model")
		}
	})

	t.Run("explicit wildcard size is passed through as Size not Resolution", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wanx2.1-t2v-turbo", Prompt: "p", Size: "1280*720",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Size != "1280*720" || req.Parameters.Resolution != "" {
			t.Fatalf("expected Size=1280*720 Resolution=empty, got Size=%q Resolution=%q", req.Parameters.Size, req.Parameters.Resolution)
		}
	})

	t.Run("plain resolution string is uppercased and suffixed with P", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p", Size: "720p",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Resolution != "720P" {
			t.Fatalf("expected normalized resolution 720P, got %q", req.Parameters.Resolution)
		}
	})

	t.Run("default resolution per model family when size omitted", func(t *testing.T) {
		cases := []struct {
			model    string
			wantSize string
			wantRes  string
		}{
			{"wan2.5-t2v-preview", "1920*1080", ""},
			{"wan2.2-t2v-plus", "1920*1080", ""},
			{"wanx2.1-t2v-turbo", "1280*720", ""},
			{"wan2.6-i2v", "", "1080P"},
			{"wan2.5-i2v-preview", "", "1080P"},
			{"wan2.2-i2v-flash", "", "720P"},
			{"wan2.2-i2v-plus", "", "1080P"},
			{"wanx2.1-i2v-turbo", "", "720P"},
		}
		for _, tc := range cases {
			a := &TaskAdaptor{}
			info := &relaycommon.RelayInfo{}
			req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{Model: tc.model, Prompt: "p"})
			if err != nil {
				t.Fatalf("model %s: unexpected error: %v", tc.model, err)
			}
			if req.Parameters.Size != tc.wantSize {
				t.Fatalf("model %s: expected default size %q, got %q", tc.model, tc.wantSize, req.Parameters.Size)
			}
			if req.Parameters.Resolution != tc.wantRes {
				t.Fatalf("model %s: expected default resolution %q, got %q", tc.model, tc.wantRes, req.Parameters.Resolution)
			}
		}
	})

	t.Run("duration precedence: explicit Duration wins over Seconds and default", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p", Duration: 8, Seconds: "3",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Duration != 8 {
			t.Fatalf("expected explicit Duration=8 to win, got %d", req.Parameters.Duration)
		}
		if info.PriceData.OtherRatios["seconds"] != 8 {
			t.Fatalf("expected billed seconds ratio to reflect duration, got %v", info.PriceData.OtherRatios["seconds"])
		}
	})

	t.Run("Seconds string used when Duration is zero", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p", Seconds: "7",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Duration != 7 {
			t.Fatalf("expected Duration=7 from Seconds field, got %d", req.Parameters.Duration)
		}
	})

	t.Run("non-numeric Seconds is rejected instead of silently defaulting", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		_, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p", Seconds: "not-a-number",
		})
		if err == nil {
			t.Fatalf("expected error for malformed seconds value")
		}
	})

	t.Run("no Duration/Seconds falls back to 5s default", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{Model: "wan2.2-i2v-plus", Prompt: "p"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Duration != 5 {
			t.Fatalf("expected default duration 5, got %d", req.Parameters.Duration)
		}
	})

	// FINDING: convertToAliRequest unmarshals req.Metadata straight onto the
	// top-level AliVideoRequest (json tags "model"/"input"/"parameters"), not
	// onto the documented flat AliMetadata shape (which is declared but never
	// referenced anywhere in the codebase). A caller passing the advertised
	// flat keys like {"seed": 42} to override the random seed is silently
	// ignored -- no error, no effect. Only a nested {"parameters": {...}}
	// metadata payload actually merges. This test locks in the current
	// (surprising) behavior as a regression baseline; it is not corrected
	// here per the "don't change tested code" constraint.
	t.Run("FINDING: flat metadata key (per documented AliMetadata shape) is silently dropped", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p",
			Metadata: map[string]interface{}{"seed": float64(42)},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Seed != 0 {
			t.Fatalf("FINDING regressed: flat metadata seed now merges (got %d); AliMetadata dead-code claim needs re-check", req.Parameters.Seed)
		}
	})

	t.Run("metadata nested under the real 'parameters' json key does merge", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p",
			Metadata: map[string]interface{}{"parameters": map[string]interface{}{"seed": float64(42)}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Parameters.Seed != 42 {
			t.Fatalf("expected nested parameters.seed metadata to merge, got %d", req.Parameters.Seed)
		}
	})

	t.Run("metadata attempting to change model is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		_, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p",
			Metadata: map[string]interface{}{"model": "some-other-model"},
		})
		if err == nil {
			t.Fatalf("expected error: model must not be changeable via metadata (billing bypass risk)")
		}
	})

	t.Run("metadata with a type-mismatched top-level field fails to unmarshal", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{}
		_, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
			Model: "wan2.2-i2v-plus", Prompt: "p",
			Metadata: map[string]interface{}{"model": float64(123)},
		})
		if err == nil {
			t.Fatalf("expected unmarshal error for type-mismatched metadata field (model must be string)")
		}
	})
}

// ─── DoResponse (submit-task response handling) ─────────────────────────────

func TestAli_DoResponse(t *testing.T) {
	tests := []struct {
		name        string
		respBody    string
		statusCode  int
		wantErr     bool
		errCode     string
		wantTaskID  string
	}{
		{
			name:       "successful submission extracts task id and echoes 200 to caller",
			respBody:   `{"output":{"task_id":"ali-task-1","task_status":"PENDING"},"request_id":"r1"}`,
			statusCode: http.StatusOK,
			wantTaskID: "ali-task-1",
		},
		{
			name:       "upstream business error code surfaces as task error, not silently swallowed",
			respBody:   `{"code":"InvalidParameter","message":"bad size","request_id":"r2"}`,
			statusCode: http.StatusBadRequest,
			wantErr:    true,
			errCode:    "ali_api_error",
		},
		{
			name:       "200 OK with missing task_id must not be treated as success",
			respBody:   `{"output":{"task_status":"PENDING"},"request_id":"r3"}`,
			statusCode: http.StatusOK,
			wantErr:    true,
			errCode:    "invalid_response",
		},
		{
			name:       "malformed json body does not panic and surfaces a parse error",
			respBody:   `{not json`,
			statusCode: http.StatusOK,
			wantErr:    true,
			errCode:    "unmarshal_response_body_failed",
		},
		{
			name:       "empty body does not panic",
			respBody:   ``,
			statusCode: http.StatusOK,
			wantErr:    true,
			errCode:    "unmarshal_response_body_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.respBody)),
			}
			taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
			if tt.wantErr {
				if taskErr == nil {
					t.Fatalf("expected task error, got taskID=%q", taskID)
				}
				if taskErr.Code != tt.errCode {
					t.Fatalf("expected error code %q, got %q", tt.errCode, taskErr.Code)
				}
				return
			}
			if taskErr != nil {
				t.Fatalf("unexpected task error: %s / %s", taskErr.Code, taskErr.Message)
			}
			if taskID != tt.wantTaskID {
				t.Fatalf("expected task id %q, got %q", tt.wantTaskID, taskID)
			}
			if taskData == nil {
				t.Fatalf("expected raw response body to be returned for later persistence")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("expected client to receive 200 with normalized OpenAI video payload, got %d", w.Code)
			}
			var ov dto.OpenAIVideo
			if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
				t.Fatalf("expected client body to be valid OpenAIVideo json: %v", err)
			}
			if ov.ID != tt.wantTaskID {
				t.Fatalf("expected client-facing video id %q, got %q", tt.wantTaskID, ov.ID)
			}
		})
	}
}

// ─── FetchTask (polling) ────────────────────────────────────────────────────

func TestAli_FetchTask(t *testing.T) {
	t.Run("missing task_id in body map is rejected before any network call", func(t *testing.T) {
		a := &TaskAdaptor{}
		_, err := a.FetchTask("https://dashscope.aliyuncs.com", "sk-x", map[string]any{}, "")
		if err == nil {
			t.Fatalf("expected error for missing task_id")
		}
	})

	t.Run("polls the correct upstream URL with bearer auth and surfaces response", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		var gotPath, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"output":{"task_id":"t1","task_status":"SUCCEEDED","video_url":"https://cdn/v.mp4"}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{}
		resp, err := a.FetchTask(srv.URL, "sk-poll-key", map[string]any{"task_id": "t1"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if gotPath != "/api/v1/tasks/t1" {
			t.Fatalf("expected polling path /api/v1/tasks/t1, got %q", gotPath)
		}
		if gotAuth != "Bearer sk-poll-key" {
			t.Fatalf("expected bearer auth header with the channel key, got %q", gotAuth)
		}

		body, _ := io.ReadAll(resp.Body)
		info, err := a.ParseTaskResult(body)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if info.Status != string(repo.TaskStatusSuccess) || info.Url != "https://cdn/v.mp4" {
			t.Fatalf("expected success status with video url propagated, got status=%q url=%q", info.Status, info.Url)
		}
	})
}

// ─── ParseTaskResult (status machine mapping) ───────────────────────────────

func TestAli_ParseTaskResult(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{
			name:       "PENDING maps to queued",
			body:       `{"output":{"task_status":"PENDING"}}`,
			wantStatus: string(repo.TaskStatusQueued),
		},
		{
			name:       "RUNNING maps to in_progress",
			body:       `{"output":{"task_status":"RUNNING"}}`,
			wantStatus: string(repo.TaskStatusInProgress),
		},
		{
			name:       "SUCCEEDED maps to success and propagates video url directly (no proxy needed)",
			body:       `{"output":{"task_status":"SUCCEEDED","video_url":"https://cdn/out.mp4"}}`,
			wantStatus: string(repo.TaskStatusSuccess),
			wantURL:    "https://cdn/out.mp4",
		},
		{
			name:       "FAILED maps to failure with top-level message preferred",
			body:       `{"message":"quota exceeded","output":{"task_status":"FAILED","code":"Sub","message":"ignored"}}`,
			wantStatus: string(repo.TaskStatusFailure),
			wantReason: "quota exceeded",
		},
		{
			name:       "FAILED with only output-level message builds composite reason",
			body:       `{"output":{"task_status":"FAILED","code":"E1","message":"bad prompt"}}`,
			wantStatus: string(repo.TaskStatusFailure),
			wantReason: "task failed, code: E1 , message: bad prompt",
		},
		{
			name:       "FAILED with no message at all falls back to generic reason",
			body:       `{"output":{"task_status":"FAILED"}}`,
			wantStatus: string(repo.TaskStatusFailure),
			wantReason: "task failed",
		},
		{
			name:       "CANCELED maps to failure",
			body:       `{"output":{"task_status":"CANCELED"}}`,
			wantStatus: string(repo.TaskStatusFailure),
		},
		{
			name:       "unknown enum value defaults to queued rather than crashing",
			body:       `{"output":{"task_status":"SOME_NEW_UPSTREAM_STATE"}}`,
			wantStatus: string(repo.TaskStatusQueued),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Status != tt.wantStatus {
				t.Fatalf("expected status %q, got %q", tt.wantStatus, info.Status)
			}
			if tt.wantURL != "" && info.Url != tt.wantURL {
				t.Fatalf("expected url %q, got %q", tt.wantURL, info.Url)
			}
			if tt.wantReason != "" && info.Reason != tt.wantReason {
				t.Fatalf("expected reason %q, got %q", tt.wantReason, info.Reason)
			}
		})
	}

	t.Run("malformed json does not panic", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.ParseTaskResult([]byte(`not json`)); err == nil {
			t.Fatalf("expected error for malformed json")
		}
	})

	t.Run("empty body does not panic", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.ParseTaskResult([]byte(``)); err == nil {
			t.Fatalf("expected error for empty body")
		}
	})
}

// ─── ConvertToOpenAIVideo (poll-endpoint client response shaping) ──────────

func TestAli_ConvertToOpenAIVideo(t *testing.T) {
	t.Run("success payload embeds video url in metadata and no error", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{
			TaskID:   "t1",
			Status:   repo.TaskStatusSuccess,
			Progress: "100%",
		}
		task.SetData(AliVideoResponse{Output: AliVideoOutput{TaskStatus: "SUCCEEDED", VideoURL: "https://cdn/x.mp4"}})

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		if err := json.Unmarshal(raw, &ov); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if ov.Error != nil {
			t.Fatalf("expected no error on success, got %+v", ov.Error)
		}
		if ov.Metadata["url"] != "https://cdn/x.mp4" {
			t.Fatalf("expected metadata url to carry the video url, got %v", ov.Metadata)
		}
	})

	t.Run("top-level error code surfaces in OpenAIVideoError", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t2", Status: repo.TaskStatusFailure}
		task.SetData(AliVideoResponse{Code: "Throttling", Message: "rate limited"})

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Error == nil || ov.Error.Code != "Throttling" || ov.Error.Message != "rate limited" {
			t.Fatalf("expected error code/message propagated to client, got %+v", ov.Error)
		}
	})

	t.Run("output-level error code used when top-level code absent", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t3", Status: repo.TaskStatusFailure}
		task.SetData(AliVideoResponse{Output: AliVideoOutput{TaskStatus: "FAILED", Code: "E2", Message: "nope"}})

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Error == nil || ov.Error.Code != "E2" {
			t.Fatalf("expected output-level error code propagated, got %+v", ov.Error)
		}
	})

	t.Run("malformed stored data does not panic, returns error", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t4"}
		task.Data = []byte(`not json`)
		if _, err := a.ConvertToOpenAIVideo(task); err == nil {
			t.Fatalf("expected error for malformed stored task data")
		}
	})
}

// ─── convertAliStatus / GetModelList / GetChannelName ──────────────────────

func TestAli_ConvertAliStatus(t *testing.T) {
	tests := map[string]string{
		"PENDING":   dto.VideoStatusQueued,
		"RUNNING":   dto.VideoStatusInProgress,
		"SUCCEEDED": dto.VideoStatusCompleted,
		"FAILED":    dto.VideoStatusFailed,
		"CANCELED":  dto.VideoStatusFailed,
		"UNKNOWN":   dto.VideoStatusFailed,
		"GARBAGE":   dto.VideoStatusUnknown,
	}
	for status, want := range tests {
		if got := convertAliStatus(status); got != want {
			t.Fatalf("status %q: expected client status %q, got %q", status, want, got)
		}
	}
}

func TestAli_GetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "ali" {
		t.Fatalf("expected channel name %q, got %q", "ali", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatalf("expected non-empty model list")
	}
	found := false
	for _, m := range models {
		if m == "wan2.2-i2v-plus" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected wan2.2-i2v-plus to be in advertised model list, got %v", models)
	}
}

// ─── End-to-end submit round trip via a local httptest upstream ────────────

func TestAli_DoRequest_RoundTrip(t *testing.T) {
	taskBatchANoBodyLimit(t)
	taskBatchAAllowLoopbackHTTP(t)
	var gotBody AliVideoRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"output":{"task_id":"submitted-1","task_status":"PENDING"},"request_id":"r"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo(srv.URL, "sk-e2e")
	a.Init(info)

	c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(`{"prompt":"a bird","model":"wan2.2-i2v-plus"}`), "application/json")
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("validate failed: %s", taskErr.Message)
	}

	body, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("build body failed: %v", err)
	}
	resp, err := a.DoRequest(c, info, body)
	if err != nil {
		t.Fatalf("do request failed: %v", err)
	}

	taskID, _, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("do response failed: %s", taskErr.Message)
	}
	if taskID != "submitted-1" {
		t.Fatalf("expected task id from upstream to be surfaced, got %q", taskID)
	}
	if gotBody.Model != "wan2.2-i2v-plus" || gotBody.Input.Prompt != "a bird" {
		t.Fatalf("expected upstream to receive submitted model/prompt, got %+v", gotBody)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 to caller, got %d", w.Code)
	}
}
