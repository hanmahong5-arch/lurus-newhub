package hailuo

// Business-acceptance tests for the Hailuo (MiniMax) async video task
// adaptor: submit-task request construction, status polling / state-machine
// mapping, result URL retrieval (2-hop: task status -> file retrieval), and
// failure-reason propagation.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func taskBatchANewRelayInfo(baseURL, apiKey string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    5,
			ChannelBaseUrl: baseURL,
			ApiKey:         apiKey,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

func taskBatchANoBodyLimit(t *testing.T) {
	t.Helper()
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })
}

func taskBatchAAllowLoopbackHTTP(t *testing.T) {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

// ─── Init / URL / Header / Body ─────────────────────────────────────────────

func TestHailuo_Init(t *testing.T) {
	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo("https://api.minimaxi.com", "hailuo-secret")
	a.Init(info)
	if a.baseURL != "https://api.minimaxi.com" || a.apiKey != "hailuo-secret" {
		t.Fatalf("expected baseURL/apiKey captured, got %q / %q", a.baseURL, a.apiKey)
	}
}

func TestHailuo_BuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.minimaxi.com"}
	url, err := a.BuildRequestURL(&relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.minimaxi.com" + TextToVideoEndpoint
	if url != want {
		t.Fatalf("expected %q, got %q", want, url)
	}
}

func TestHailuo_BuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "hailuo-secret"}
	req, _ := http.NewRequest(http.MethodPost, "https://api.minimaxi.com/x", nil)
	if err := a.BuildRequestHeader(nil, req, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer hailuo-secret" {
		t.Fatalf("expected bearer auth with api key, got %q", got)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("expected json content type")
	}
}

func TestHailuo_BuildRequestBody(t *testing.T) {
	t.Run("missing task_request is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for missing task_request")
		}
	})

	t.Run("wrong context type is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", 123)
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for wrong context type")
		}
	})

	t.Run("valid request builds a video generation payload", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "T2V-01", Prompt: "a robot dancing", Duration: 6})
		r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := io.ReadAll(r)
		var got VideoRequest
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if got.Model != "T2V-01" || got.Prompt != "a robot dancing" || got.Duration == nil || *got.Duration != 6 {
			t.Fatalf("expected model/prompt/duration preserved, got %+v", got)
		}
	})
}

// ─── convertToRequestPayload / parseResolutionFromSize ──────────────────────

func TestHailuo_ConvertToRequestPayload(t *testing.T) {
	t.Run("explicit duration wins, default 6s otherwise", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "T2V-01", Duration: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Duration == nil || *p.Duration != 10 {
			t.Fatalf("expected explicit duration 10, got %v", p.Duration)
		}

		pDefault, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "T2V-01"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pDefault.Duration == nil || *pDefault.Duration != DefaultDuration {
			t.Fatalf("expected default duration %d, got %v", DefaultDuration, pDefault.Duration)
		}
	})

	t.Run("known model uses its default resolution when size omitted", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "MiniMax-Hailuo-2.3"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Resolution != Resolution768P {
			t.Fatalf("expected model default resolution 768P, got %q", p.Resolution)
		}
	})

	t.Run("unknown model falls back to package default resolution", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "totally-unknown-model"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Resolution != DefaultResolution {
			t.Fatalf("expected package default resolution for unknown model, got %q", p.Resolution)
		}
	})

	t.Run("size substring maps to explicit resolution, overriding the model default", func(t *testing.T) {
		cases := []struct {
			size string
			want string
		}{
			{"1920x1080", Resolution1080P},
			{"1366x768", Resolution768P},
			{"1280x720", Resolution720P},
			{"640x512", Resolution512P},
		}
		for _, tc := range cases {
			a := &TaskAdaptor{}
			p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "MiniMax-Hailuo-02", Size: tc.size})
			if err != nil {
				t.Fatalf("size %q: unexpected error: %v", tc.size, err)
			}
			if p.Resolution != tc.want {
				t.Fatalf("size %q: expected resolution %q, got %q", tc.size, tc.want, p.Resolution)
			}
		}
	})

	t.Run("size with no recognizable resolution substring falls back to model default", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "MiniMax-Hailuo-2.3", Size: "banana"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Resolution != Resolution768P {
			t.Fatalf("expected fallback to model default resolution, got %q", p.Resolution)
		}
	})

	t.Run("metadata merges onto the built payload (e.g. resolution override)", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model:    "T2V-01",
			Metadata: map[string]interface{}{"resolution": "1080P"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Resolution != "1080P" {
			t.Fatalf("expected metadata resolution override to merge, got %q", p.Resolution)
		}
	})
}

// ─── DoResponse (submit-task response handling) ─────────────────────────────

func TestHailuo_DoResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    bool
		errCode    string
		wantTaskID string
	}{
		{
			name:       "status_code 0 is success, extracts task id",
			body:       `{"task_id":"hailuo-1","base_resp":{"status_code":0,"status_msg":"success"}}`,
			wantTaskID: "hailuo-1",
		},
		{
			name:    "non-zero status_code surfaces as task error",
			body:    `{"task_id":"","base_resp":{"status_code":1008,"status_msg":"insufficient balance"}}`,
			wantErr: true,
			errCode: "1008",
		},
		{
			name:    "malformed json does not panic",
			body:    `{not json`,
			wantErr: true,
			errCode: "unmarshal_response_body_failed",
		},
		{
			name:    "empty body does not panic",
			body:    ``,
			wantErr: true,
			errCode: "unmarshal_response_body_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(tt.body))}
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
				t.Fatalf("expected raw response body returned for persistence")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 echoed to client, got %d", w.Code)
			}
		})
	}
}

// ─── FetchTask (polling) ─────────────────────────────────────────────────────

func TestHailuo_FetchTask(t *testing.T) {
	t.Run("missing task_id is rejected before any network call", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.FetchTask("https://x", "k", map[string]any{}, ""); err == nil {
			t.Fatalf("expected error for missing task_id")
		}
	})

	t.Run("polls the query endpoint with task_id in the URL and bearer auth", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		var gotPath, gotQuery, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"task_id":"t1","status":"Success","base_resp":{"status_code":0}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{}
		resp, err := a.FetchTask(srv.URL, "poll-key", map[string]any{"task_id": "t1"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if gotPath != QueryTaskEndpoint {
			t.Fatalf("expected polling path %q, got %q", QueryTaskEndpoint, gotPath)
		}
		if gotQuery != "task_id=t1" {
			t.Fatalf("expected task_id query param, got %q", gotQuery)
		}
		if gotAuth != "Bearer poll-key" {
			t.Fatalf("expected bearer auth, got %q", gotAuth)
		}
	})
}

// ─── ParseTaskResult (status machine mapping) ───────────────────────────────

func TestHailuo_ParseTaskResult(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantStatus   string
		wantProgress string
		wantReason   string
	}{
		{
			name:         "Preparing maps to in_progress at 30%",
			body:         `{"status":"Preparing","base_resp":{"status_code":0}}`,
			wantStatus:   string(repo.TaskStatusInProgress),
			wantProgress: "30%",
		},
		{
			name:         "Queueing maps to in_progress at 30%",
			body:         `{"status":"Queueing","base_resp":{"status_code":0}}`,
			wantStatus:   string(repo.TaskStatusInProgress),
			wantProgress: "30%",
		},
		{
			name:         "Processing maps to in_progress at 50%",
			body:         `{"status":"Processing","base_resp":{"status_code":0}}`,
			wantStatus:   string(repo.TaskStatusInProgress),
			wantProgress: "50%",
		},
		{
			name:       "Fail maps to failure with default reason when base_resp has none",
			body:       `{"status":"Fail","base_resp":{"status_code":0}}`,
			wantStatus: string(repo.TaskStatusFailure),
			wantReason: "task failed",
		},
		// FINDING: ParseTaskResult sets Status=Failure when base_resp.status_code
		// is non-zero, but the subsequent unconditional switch on resTask.Status
		// re-overwrites Status for "Preparing"/"Queueing"/"Processing"/"Success"
		// (only the "Fail" case agrees with the failure branch). So an upstream
		// error (e.g. rate-limited, auth failed) reported while the task is
		// still nominally "Preparing" is silently downgraded to in_progress and
		// the failure reason is dropped from Status (Reason string itself is
		// still populated, but pollers keying off Status will not see FAILURE).
		// Locking in the actual current behavior below as a regression baseline.
		{
			name:         "non-zero base_resp status_code with a non-terminal status is masked back to in_progress",
			body:         `{"status":"Preparing","base_resp":{"status_code":1002,"status_msg":"rate limited"}}`,
			wantStatus:   string(repo.TaskStatusInProgress),
			wantProgress: "30%",
			wantReason:   "rate limited",
		},
		{
			name:         "unknown status value defaults to in_progress rather than crashing",
			body:         `{"status":"SomeNewUpstreamState","base_resp":{"status_code":0}}`,
			wantStatus:   string(repo.TaskStatusInProgress),
			wantProgress: "30%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{} // no apiKey/baseURL: Success branch would try buildVideoURL, tested separately
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Status != tt.wantStatus {
				t.Fatalf("expected status %q, got %q", tt.wantStatus, info.Status)
			}
			if tt.wantProgress != "" && info.Progress != tt.wantProgress {
				t.Fatalf("expected progress %q, got %q", tt.wantProgress, info.Progress)
			}
			if tt.wantReason != "" && info.Reason != tt.wantReason {
				t.Fatalf("expected reason %q, got %q", tt.wantReason, info.Reason)
			}
		})
	}

	t.Run("Success status without configured apiKey/baseURL returns success with empty url (no crash, no network)", func(t *testing.T) {
		a := &TaskAdaptor{}
		info, err := a.ParseTaskResult([]byte(`{"status":"Success","file_id":"f1","base_resp":{"status_code":0}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != string(repo.TaskStatusSuccess) {
			t.Fatalf("expected success status, got %q", info.Status)
		}
		if info.Url != "" {
			t.Fatalf("expected empty url when adaptor has no credentials configured, got %q", info.Url)
		}
	})

	t.Run("Success status resolves the download url via the file-retrieve endpoint", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		var gotPath, gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"file":{"download_url":"https://cdn/final.mp4"},"base_resp":{"status_code":0}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
		info, err := a.ParseTaskResult([]byte(`{"status":"Success","task_id":"t9","file_id":"f9","base_resp":{"status_code":0}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "https://cdn/final.mp4" {
			t.Fatalf("expected resolved download url, got %q", info.Url)
		}
		if gotPath != "/v1/files/retrieve" || gotQuery != "file_id=f9" {
			t.Fatalf("expected file-retrieve call with file_id, got path=%q query=%q", gotPath, gotQuery)
		}
	})

	t.Run("malformed json does not panic", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.ParseTaskResult([]byte(`not json`)); err == nil {
			t.Fatalf("expected error for malformed json")
		}
	})
}

// ─── buildVideoURL edge cases (called from ParseTaskResult on success) ─────

func TestHailuo_BuildVideoURL(t *testing.T) {
	t.Run("missing credentials short-circuits without a network call", func(t *testing.T) {
		a := &TaskAdaptor{}
		if got := a.buildVideoURL("t", "f"); got != "" {
			t.Fatalf("expected empty url without credentials, got %q", got)
		}
	})

	t.Run("file-retrieve upstream error response yields empty url, not a crash", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"auth failed"}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
		if got := a.buildVideoURL("t", "f"); got != "" {
			t.Fatalf("expected empty url when file-retrieve reports an error status, got %q", got)
		}
	})

	t.Run("malformed file-retrieve response yields empty url, not a crash", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
		if got := a.buildVideoURL("t", "f"); got != "" {
			t.Fatalf("expected empty url for malformed upstream response, got %q", got)
		}
	})
}

// ─── ConvertToOpenAIVideo (poll-endpoint client response shaping) ──────────

func TestHailuo_ConvertToOpenAIVideo(t *testing.T) {
	t.Run("success: no error, base client fields copied from the task row", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{
			TaskID:     "t1",
			Status:     repo.TaskStatusSuccess,
			Progress:   "100%",
			FailReason: "https://cdn/x.mp4", // ToOpenAIVideo() surfaces this as metadata.url
		}
		task.SetData(QueryTaskResponse{Status: "Success", BaseResp: BaseResp{StatusCode: StatusSuccess}})

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
			t.Fatalf("expected metadata url sourced from task.FailReason, got %v", ov.Metadata)
		}
	})

	t.Run("non-success stored status_code surfaces as OpenAIVideoError", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t2", Status: repo.TaskStatusFailure}
		task.SetData(QueryTaskResponse{Status: "Fail", BaseResp: BaseResp{StatusCode: StatusNoBalance, StatusMsg: "insufficient balance"}})

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Error == nil || ov.Error.Code != strconv.Itoa(StatusNoBalance) || ov.Error.Message != "insufficient balance" {
			t.Fatalf("expected error code/message propagated, got %+v", ov.Error)
		}
	})

	t.Run("malformed stored task data does not panic, returns error", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t3"}
		task.Data = []byte(`not json`)
		if _, err := a.ConvertToOpenAIVideo(task); err == nil {
			t.Fatalf("expected error for malformed stored task data")
		}
	})
}

// ─── GetModelList / GetChannelName / dead-code helpers ─────────────────────

func TestHailuo_GetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != ChannelName {
		t.Fatalf("expected channel name %q, got %q", ChannelName, a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatalf("expected non-empty model list")
	}
}

func TestHailuo_ContainsHelpers(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Fatalf("expected contains to find present element")
	}
	if contains([]string{"a", "b"}, "c") {
		t.Fatalf("expected contains to reject absent element")
	}
	if !containsInt([]int{1, 2, 3}, 2) {
		t.Fatalf("expected containsInt to find present element")
	}
	if containsInt([]int{1, 2, 3}, 9) {
		t.Fatalf("expected containsInt to reject absent element")
	}
}

// ─── GetModelConfig (per-model capability table) ────────────────────────────

func TestHailuo_GetModelConfig(t *testing.T) {
	t.Run("known model returns its declared config", func(t *testing.T) {
		cfg := GetModelConfig("T2V-01-Director")
		if cfg.DefaultResolution != Resolution768P || !cfg.HasPromptOptimizer || cfg.HasFastPretreatment {
			t.Fatalf("unexpected config for known model: %+v", cfg)
		}
	})

	t.Run("unknown model falls back to generic default config, echoing the model name", func(t *testing.T) {
		cfg := GetModelConfig("does-not-exist")
		if cfg.Name != "does-not-exist" || cfg.DefaultResolution != DefaultResolution {
			t.Fatalf("unexpected fallback config: %+v", cfg)
		}
	})
}

// ─── End-to-end submit round trip via a local httptest upstream ────────────

func TestHailuo_DoRequest_RoundTrip(t *testing.T) {
	taskBatchANoBodyLimit(t)
	taskBatchAAllowLoopbackHTTP(t)

	var gotAuth string
	var gotBody VideoRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task_id":"submitted-42","base_resp":{"status_code":0,"status_msg":"success"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo(srv.URL, "e2e-key")
	a.Init(info)

	c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(`{"prompt":"a lake","model":"T2V-01"}`), "application/json")
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
	if taskID != "submitted-42" {
		t.Fatalf("expected upstream task id surfaced, got %q", taskID)
	}
	if gotAuth != "Bearer e2e-key" {
		t.Fatalf("expected bearer auth to reach upstream, got %q", gotAuth)
	}
	if gotBody.Model != "T2V-01" || gotBody.Prompt != "a lake" {
		t.Fatalf("expected model/prompt to reach upstream, got %+v", gotBody)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 to caller, got %d", w.Code)
	}
}
