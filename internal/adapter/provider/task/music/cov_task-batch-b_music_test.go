package music

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func task_batch_b_newGinContext(method, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/v1/audio/music", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

func task_batch_b_allowLoopbackHTTP() func() {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	return func() { system_setting.GetFetchSetting().AllowPrivateIp = prev }
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_MissingPromptRejected(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	if err := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestValidateRequestAndSetAction_ValidSetsMusicAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"upbeat pop","style":"pop"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != "MUSIC" {
		t.Errorf("Action = %q, want MUSIC", info.Action)
	}
	if _, ok := c.Get("music_request"); !ok {
		t.Fatal("music_request should be stored in context for BuildRequestBody to consume")
	}
}

func TestValidateRequestAndSetAction_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{bad`, "application/json")
	if err := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL / BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://music.example"}}
	got, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://music.example/suno/submit/music" {
		t.Errorf("BuildRequestURL = %q", got)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "music-key"}}
	if err := a.BuildRequestHeader(nil, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer music-key" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error when music_request missing from context")
	}
}

func TestBuildRequestBody_StylePrependedToPrompt(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("music_request", &dto.MusicSubmitReq{Prompt: "sad song", Style: "lofi", Model: "suno-v4", Instrumental: true})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var sunoReq dto.SunoSubmitReq
	if err := json.Unmarshal(data, &sunoReq); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if sunoReq.GptDescriptionPrompt != "[Style: lofi] sad song" {
		t.Errorf("prompt = %q, want style-prefixed prompt", sunoReq.GptDescriptionPrompt)
	}
	if sunoReq.Mv != "chirp-v4" {
		t.Errorf("Mv = %q, want chirp-v4 for suno-v4 model", sunoReq.Mv)
	}
	if !sunoReq.MakeInstrumental {
		t.Error("MakeInstrumental should carry through from Instrumental=true")
	}
}

func TestBuildRequestBody_ModelMapping(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"suno-v4", "chirp-v4"},
		{"suno-v3.5", "chirp-v3-5"},
		{"unknown-model", "chirp-v3-0"},
		{"", "chirp-v3-0"},
	}
	for _, tt := range cases {
		t.Run(tt.model, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
			c.Set("music_request", &dto.MusicSubmitReq{Prompt: "x", Model: tt.model})
			reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			data, _ := io.ReadAll(reader)
			var sunoReq dto.SunoSubmitReq
			_ = json.Unmarshal(data, &sunoReq)
			if sunoReq.Mv != tt.want {
				t.Errorf("model %q -> Mv = %q, want %q", tt.model, sunoReq.Mv, tt.want)
			}
		})
	}
}

func TestBuildRequestBody_NoStyleLeavesPromptUnprefixed(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("music_request", &dto.MusicSubmitReq{Prompt: "plain prompt"})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var sunoReq dto.SunoSubmitReq
	_ = json.Unmarshal(data, &sunoReq)
	if sunoReq.GptDescriptionPrompt != "plain prompt" {
		t.Errorf("prompt = %q, want unmodified when no style set", sunoReq.GptDescriptionPrompt)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_SuccessReturnsStandardizedPayload(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":"success","data":"music-task-1"}`))}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "music-task-1" {
		t.Errorf("taskID = %q, want music-task-1", taskID)
	}
	if data != nil {
		t.Errorf("music DoResponse does not persist raw data, want nil, got %v", data)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("client body not valid JSON: %v", err)
	}
	if out["task_id"] != "music-task-1" || out["status"] != "queued" {
		t.Errorf("client-facing payload wrong: %+v", out)
	}
}

func TestDoResponse_UpstreamFailure(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":"error","message":"bad request"}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for non-success upstream code")
	}
	if !strings.Contains(taskErr.Message, "bad request") {
		t.Errorf("failure reason not propagated: %q", taskErr.Message)
	}
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{bad"))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error, must not panic")
	}
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_PostsToSunoFetch(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"code":"success"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	resp, err := a.FetchTask(srv.URL, "music-key", map[string]any{"ids": []string{"t1"}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if gotPath != "/suno/fetch" {
		t.Errorf("path = %q, want /suno/fetch", gotPath)
	}
	if gotAuth != "Bearer music-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName / Init
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "music" {
		t.Errorf("GetChannelName() = %q, want music", a.GetChannelName())
	}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "suno-v4" {
			found = true
		}
	}
	if !found {
		t.Error("expected suno-v4 in model list")
	}
}

func TestInit_SetsChannelType(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 7}})
	if a.ChannelType != 7 {
		t.Errorf("ChannelType = %d, want 7", a.ChannelType)
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_ExtractsStatusProgressAndURL(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"code":"success","data":{"status":"in_progress","progress":"40%","data":{"audio_url":"https://cdn/x.mp3"}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", ti.Status)
	}
	if ti.Progress != "40%" {
		t.Errorf("Progress = %q, want 40%%", ti.Progress)
	}
	if ti.Url != "https://cdn/x.mp3" {
		t.Errorf("Url = %q, want extracted audio_url", ti.Url)
	}
}

func TestParseTaskResult_NonMapDataReturnsEmptyInfo(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"code":"success","data":"just-a-string"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != "" || ti.Url != "" {
		t.Errorf("expected empty TaskInfo when data is not a map, got %+v", ti)
	}
}

func TestParseTaskResult_EmptyAudioURLIgnored(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"data":{"status":"processing","data":{"audio_url":""}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Url != "" {
		t.Errorf("Url should stay empty when audio_url is empty string, got %q", ti.Url)
	}
}

func TestParseTaskResult_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Fatal("expected error, must not panic")
	}
}
