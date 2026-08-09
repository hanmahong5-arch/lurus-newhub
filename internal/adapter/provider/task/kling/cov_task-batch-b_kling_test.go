package kling

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// task_batch_b_newGinContext builds a minimal gin.Context wired to a
// ResponseRecorder so handler code under test can write JSON responses
// and we can assert on the recorded business payload.
func task_batch_b_newGinContext(method, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/v1/videos", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

// task_batch_b_allowLoopbackHTTP initializes the shared relay HTTP client and
// temporarily disables the private-IP SSRF dial guard so httptest.Server
// loopback stubs used by FetchTask tests are reachable. Returns a restore func.
func task_batch_b_allowLoopbackHTTP() func() {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	return func() { system_setting.GetFetchSetting().AllowPrivateIp = prev }
}

// ---------------------------------------------------------------------------
// getAspectRatio
// ---------------------------------------------------------------------------

func TestTaskAdaptor_GetAspectRatio(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		size string
		want string
	}{
		{"1024x1024", "1:1"},
		{"512x512", "1:1"},
		{"1280x720", "16:9"},
		{"1920x1080", "16:9"},
		{"720x1280", "9:16"},
		{"1080x1920", "9:16"},
		{"unknown-size", "1:1"},
		{"", "1:1"},
	}
	for _, tt := range cases {
		t.Run(tt.size, func(t *testing.T) {
			if got := a.getAspectRatio(tt.size); got != tt.want {
				t.Errorf("getAspectRatio(%q) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// defaultString / defaultInt
// ---------------------------------------------------------------------------

func TestDefaultString(t *testing.T) {
	if got := defaultString("  ", "std"); got != "std" {
		t.Errorf("whitespace-only should fall back to default, got %q", got)
	}
	if got := defaultString("pro", "std"); got != "pro" {
		t.Errorf("non-empty value should be preserved, got %q", got)
	}
}

func TestDefaultInt(t *testing.T) {
	if got := defaultInt(0, 5); got != 5 {
		t.Errorf("zero should fall back to default, got %d", got)
	}
	if got := defaultInt(-1, 5); got != -1 {
		t.Errorf("negative value is not treated as unset, got %d", got)
	}
	if got := defaultInt(10, 5); got != 10 {
		t.Errorf("explicit value should be preserved, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// isNewAPIRelay
// ---------------------------------------------------------------------------

func TestIsNewAPIRelay(t *testing.T) {
	if !isNewAPIRelay("sk-abc123") {
		t.Error("sk- prefixed key should be classified as a new-api relay key")
	}
	if isNewAPIRelay("accessKey|secretKey") {
		t.Error("kling native access|secret key must not be classified as new-api relay")
	}
}

// ---------------------------------------------------------------------------
// convertToRequestPayload
// ---------------------------------------------------------------------------

func TestConvertToRequestPayload_Defaults(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Size:   "1280x720",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Mode != "std" {
		t.Errorf("Mode default = %q, want std", payload.Mode)
	}
	if payload.Duration != "5" {
		t.Errorf("Duration default = %q, want 5", payload.Duration)
	}
	if payload.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want 16:9 for 1280x720", payload.AspectRatio)
	}
	if payload.ModelName != "kling-v1" {
		t.Errorf("ModelName default = %q, want kling-v1", payload.ModelName)
	}
}

func TestConvertToRequestPayload_MetadataOverridesDefaults(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "a dog",
		Model:  "kling-v2-master",
		Metadata: map[string]any{
			"cfg_scale": 0.9,
			"mode":      "pro",
		},
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.CfgScale != 0.9 {
		t.Errorf("metadata must override CfgScale default, got %v", payload.CfgScale)
	}
	if payload.Mode != "pro" {
		t.Errorf("metadata must override Mode default, got %v", payload.Mode)
	}
	if payload.ModelName != "kling-v2-master" {
		t.Errorf("explicit model should be preserved, got %q", payload.ModelName)
	}
}

// ---------------------------------------------------------------------------
// createJWTTokenWithKey
// ---------------------------------------------------------------------------

func TestCreateJWTTokenWithKey_NewAPIRelayReturnsKeyVerbatim(t *testing.T) {
	a := &TaskAdaptor{}
	tok, err := a.createJWTTokenWithKey("sk-passthrough")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-passthrough" {
		t.Errorf("sk- key must be returned unchanged (used as bearer directly), got %q", tok)
	}
}

func TestCreateJWTTokenWithKey_InvalidFormat(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.createJWTTokenWithKey("no-pipe-here"); err == nil {
		t.Fatal("expected error for api_key missing 'access|secret' separator")
	}
}

func TestCreateJWTTokenWithKey_ValidFormatProducesParsableJWT(t *testing.T) {
	a := &TaskAdaptor{}
	tok, err := a.createJWTTokenWithKey("myAccess|mySecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(token *jwt.Token) (interface{}, error) {
		return []byte("mySecret"), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("token should be verifiable with the secret half of the key: err=%v valid=%v", err, parsed != nil && parsed.Valid)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("expected MapClaims")
	}
	if claims["iss"] != "myAccess" {
		t.Errorf("iss claim = %v, want myAccess (the access-key half)", claims["iss"])
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	cases := []struct {
		name   string
		action string
		apiKey string
		want   string
	}{
		{"generate native key", constant.TaskActionGenerate, "ak|sk", "https://api.kling.com/v1/videos/image2video"},
		{"textGenerate native key", constant.TaskActionTextGenerate, "ak|sk", "https://api.kling.com/v1/videos/text2video"},
		{"generate new-api key", constant.TaskActionGenerate, "sk-abc", "https://api.kling.com/kling/v1/videos/image2video"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{apiKey: tt.apiKey, baseURL: "https://api.kling.com"}
			info := &relaycommon.RelayInfo{
				TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: tt.action},
				ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: tt.apiKey},
			}
			got, err := a.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("BuildRequestURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader_ValidKey(t *testing.T) {
	a := &TaskAdaptor{apiKey: "myAccess|mySecret"}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type not set correctly: %q", req.Header.Get("Content-Type"))
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) < 10 {
		t.Errorf("Authorization header malformed: %q", auth)
	}
}

func TestBuildRequestHeader_InvalidKeyPropagatesError(t *testing.T) {
	a := &TaskAdaptor{apiKey: "malformed-key-no-pipe"}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatal("expected error when JWT signing fails due to malformed api key")
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error when task_request is absent from gin context")
	}
}

func TestBuildRequestBody_NoImageSwitchesToTextGenerate(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hello"})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action := c.GetString("action"); action != constant.TaskActionTextGenerate {
		t.Errorf("expected action to be switched to textGenerate when no image is present, got %q", action)
	}
	data, _ := io.ReadAll(reader)
	var payload requestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if payload.Prompt != "hello" {
		t.Errorf("prompt not carried through to request body: %q", payload.Prompt)
	}
}

func TestBuildRequestBody_WithImageKeepsAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hello", Image: "data:image/png;base64,xyz"})
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action := c.GetString("action"); action != "" {
		t.Errorf("action should not be overridden when an image is present, got %q", action)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	body := `{"code":0,"message":"ok","data":{"task_id":"task-123","task_status":"submitted"}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v1"}
	taskID, data, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "task-123" {
		t.Errorf("taskID = %q, want task-123", taskID)
	}
	if len(data) == 0 {
		t.Error("expected raw response body to be returned for later persistence")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected HTTP 200 written to client, got %d", w.Code)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if ov.ID != "task-123" || ov.Model != "kling-v1" {
		t.Errorf("client response has wrong id/model: %+v", ov)
	}
}

func TestDoResponse_UpstreamErrorCode(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	body := `{"code":1001,"message":"invalid params"}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when upstream code != 0")
	}
	if !strings.Contains(taskErr.Message, "invalid params") {
		t.Errorf("upstream failure reason should be propagated, got %q", taskErr.Message)
	}
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{not json"))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for malformed upstream JSON, must not panic")
	}
}

func TestDoResponse_EmptyBody(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for empty upstream body")
	}
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_MissingTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp2, err := a.FetchTask("https://x", "sk-key", map[string]any{"action": constant.TaskActionGenerate}, "")
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when task_id missing from body")
	}
}

func TestFetchTask_MissingAction(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp1, err := a.FetchTask("https://x", "sk-key", map[string]any{"task_id": "abc"}, "")
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when action missing from body")
	}
}

func TestFetchTask_BuildsCorrectURLAndAuth(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath, gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"t1","task_status":"succeed"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	resp, err := a.FetchTask(srv.URL, "sk-relay-key", map[string]any{
		"task_id": "t1",
		"action":  constant.TaskActionGenerate,
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/kling/v1/videos/image2video/t1" {
		t.Errorf("path = %q, want new-api relay-prefixed image2video path with task id", gotPath)
	}
	if gotAuth != "Bearer sk-relay-key" {
		t.Errorf("Authorization = %q, sk- keys should pass through as bearer token verbatim", gotAuth)
	}
	if gotUA != "kling-sdk/1.0" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestFetchTask_TextGeneratePath(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	bcResp0, err := a.FetchTask(srv.URL, "ak|sk", map[string]any{
		"task_id": "t9",
		"action":  constant.TaskActionTextGenerate,
	}, "")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v1/videos/text2video/t9" {
		t.Errorf("path = %q, want native text2video path (native key, no relay prefix)", gotPath)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("model list must not be empty")
	}
	found := false
	for _, m := range models {
		if m == "kling-v1" {
			found = true
		}
	}
	if !found {
		t.Error("expected kling-v1 to be part of the advertised model list")
	}
	if a.GetChannelName() != "kling" {
		t.Errorf("GetChannelName() = %q, want kling", a.GetChannelName())
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_StatusMapping(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		status string
		want   repo.TaskStatus
	}{
		{"submitted", repo.TaskStatusSubmitted},
		{"processing", repo.TaskStatusInProgress},
		{"succeed", repo.TaskStatusSuccess},
		{"failed", repo.TaskStatusFailure},
	}
	for _, tt := range cases {
		t.Run(tt.status, func(t *testing.T) {
			body := `{"code":0,"data":{"task_id":"t1","task_status":"` + tt.status + `","task_status_msg":"reason"}}`
			ti, err := a.ParseTaskResult([]byte(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ti.Status != string(tt.want) {
				t.Errorf("Status = %q, want %q", ti.Status, tt.want)
			}
			if ti.Reason != "reason" {
				t.Errorf("Reason not propagated: %q", ti.Reason)
			}
		})
	}
}

func TestParseTaskResult_UnknownStatusErrors(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.ParseTaskResult([]byte(`{"data":{"task_status":"bogus_enum_value"}}`))
	if err == nil {
		t.Fatal("expected error for unrecognized upstream task status enum")
	}
}

func TestParseTaskResult_ExtractsFirstVideoURL(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"data":{"task_status":"succeed","task_result":{"videos":[{"url":"https://cdn/v1.mp4"},{"url":"https://cdn/v2.mp4"}]}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Url != "https://cdn/v1.mp4" {
		t.Errorf("Url = %q, want first video url", ti.Url)
	}
}

func TestParseTaskResult_EmptyVideosNoURL(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"data":{"task_status":"succeed","task_result":{"videos":[]}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Url != "" {
		t.Errorf("Url should stay empty for empty video array, got %q", ti.Url)
	}
}

func TestParseTaskResult_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON, must not panic")
	}
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_SuccessWithVideo(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "t1",
		Status: repo.TaskStatusSuccess,
		Data:   []byte(`{"code":0,"data":{"task_result":{"videos":[{"url":"https://cdn/x.mp4","duration":"5.0"}]},"created_at":100,"updated_at":200}}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(data, &ov); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if ov.Status != "completed" {
		t.Errorf("Status = %q, want completed for TaskStatusSuccess", ov.Status)
	}
	if ov.Metadata["url"] != "https://cdn/x.mp4" {
		t.Errorf("expected video url metadata to be set, got %+v", ov.Metadata)
	}
	if ov.Seconds != "5.0" {
		t.Errorf("Seconds = %q, want 5.0", ov.Seconds)
	}
	if ov.CreatedAt != 100 || ov.CompletedAt != 200 {
		t.Errorf("timestamps not copied through: created=%d completed=%d", ov.CreatedAt, ov.CompletedAt)
	}
}

func TestConvertToOpenAIVideo_ErrorPropagated(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "t2",
		Status: repo.TaskStatusFailure,
		Data:   []byte(`{"code":500,"message":"upstream exploded"}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(data, &ov); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if ov.Error == nil || ov.Error.Message != "upstream exploded" {
		t.Errorf("expected upstream failure reason surfaced in Error field, got %+v", ov.Error)
	}
	if ov.Status != "failed" {
		t.Errorf("Status = %q, want failed", ov.Status)
	}
}

func TestConvertToOpenAIVideo_MalformedTaskData(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "t3", Data: []byte("not json at all")}
	if _, err := a.ConvertToOpenAIVideo(task); err == nil {
		t.Fatal("expected error for malformed stored task data, must not panic")
	}
}
