package kling

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Body fixtures built below are tiny in-memory JSON blobs; disable the
	// size cap so UnmarshalBodyReusable doesn't reject them (production sets
	// this from env/config, which is 0 by default in a bare test binary).
	pkgconstant.MaxRequestBodyMB = -1
}

func newGinCtx(t *testing.T, method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	return c, w
}

// ---------------------------------------------------------------------
// GetModelList / GetChannelName / Init
// ---------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}

	want := []string{"kling-v1", "kling-v1-6", "kling-v2-master"}
	got := a.GetModelList()
	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}

	if name := a.GetChannelName(); name != "kling" {
		t.Errorf("GetChannelName() = %q, want %q", name, "kling")
	}
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: 42,
			ChannelBaseUrl: "https://api.klingai.com",
			ApiKey:         "ak|sk",
		},
	}
	a.Init(info)
	if a.ChannelType != 42 {
		t.Errorf("ChannelType = %d, want 42", a.ChannelType)
	}
	if a.baseURL != "https://api.klingai.com" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "https://api.klingai.com")
	}
	if a.apiKey != "ak|sk" {
		t.Errorf("apiKey = %q, want %q", a.apiKey, "ak|sk")
	}
}

// ---------------------------------------------------------------------
// isNewAPIRelay
// ---------------------------------------------------------------------

func TestIsNewAPIRelay(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"sk-abc123", true},
		{"ak|sk", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isNewAPIRelay(c.key); got != c.want {
			t.Errorf("isNewAPIRelay(%q) = %t, want %t", c.key, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	cases := []struct {
		name   string
		action string
		apiKey string
		want   string
	}{
		{"generate_plain_key", pkgconstant.TaskActionGenerate, "ak|sk", "https://base/v1/videos/image2video"},
		{"other_plain_key", "somethingElse", "ak|sk", "https://base/v1/videos/text2video"},
		{"generate_newapi_key", pkgconstant.TaskActionGenerate, "sk-abc", "https://base/kling/v1/videos/image2video"},
		{"other_newapi_key", "somethingElse", "sk-abc", "https://base/kling/v1/videos/text2video"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &TaskAdaptor{baseURL: "https://base"}
			info := &relaycommon.RelayInfo{
				ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: tc.apiKey},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: tc.action},
			}
			got, err := a.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("BuildRequestURL() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("BuildRequestURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------

func TestBuildRequestHeader_InvalidKeyFormat(t *testing.T) {
	a := &TaskAdaptor{apiKey: "no-pipe-here"}
	req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
	err := a.BuildRequestHeader(nil, req, nil)
	if err == nil {
		t.Fatal("expected error for invalid api key format")
	}
	if !strings.Contains(err.Error(), "failed to create JWT token") {
		t.Errorf("error = %v, want to contain %q", err, "failed to create JWT token")
	}
}

func TestBuildRequestHeader_NewAPIRelay(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-passthrough"}
	req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
	if err := a.BuildRequestHeader(nil, req, nil); err != nil {
		t.Fatalf("BuildRequestHeader() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-passthrough" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-passthrough")
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := req.Header.Get("User-Agent"); got != "kling-sdk/1.0" {
		t.Errorf("User-Agent = %q, want kling-sdk/1.0", got)
	}
}

func TestBuildRequestHeader_JWTSigned(t *testing.T) {
	a := &TaskAdaptor{apiKey: "myaccess|mysecret"}
	req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
	if err := a.BuildRequestHeader(nil, req, nil); err != nil {
		t.Fatalf("BuildRequestHeader() error = %v", err)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("Authorization = %q, want Bearer prefix", auth)
	}
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, &claims, func(tok *jwt.Token) (interface{}, error) {
		return []byte("mysecret"), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("token did not verify with expected secret: err=%v valid=%v", err, parsed.Valid)
	}
	if iss, _ := claims["iss"].(string); iss != "myaccess" {
		t.Errorf("iss claim = %q, want %q", iss, "myaccess")
	}
}

// ---------------------------------------------------------------------
// createJWTTokenWithKey
// ---------------------------------------------------------------------

func TestCreateJWTTokenWithKey(t *testing.T) {
	a := &TaskAdaptor{}

	if tok, err := a.createJWTTokenWithKey("sk-passthrough"); err != nil || tok != "sk-passthrough" {
		t.Errorf("newapi key: token=%q err=%v, want %q,nil", tok, err, "sk-passthrough")
	}

	if _, err := a.createJWTTokenWithKey("no-pipe"); err == nil {
		t.Error("expected error for key without pipe separator")
	} else if !strings.Contains(err.Error(), "invalid api_key, required format is accessKey|secretKey") {
		t.Errorf("error = %v, want format hint", err)
	}

	if _, err := a.createJWTTokenWithKey("a|b|c"); err == nil {
		t.Error("expected error for key with too many parts")
	}

	tok, err := a.createJWTTokenWithKey(" ak |  sk  ")
	if err != nil {
		t.Fatalf("createJWTTokenWithKey() error = %v", err)
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tok, &claims, func(t *jwt.Token) (interface{}, error) {
		return []byte("sk"), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("token invalid: err=%v valid=%v", err, parsed.Valid)
	}
	if iss, _ := claims["iss"].(string); iss != "ak" {
		t.Errorf("iss = %q, want %q (trimmed)", iss, "ak")
	}
	exp, _ := claims["exp"].(float64)
	nbf, _ := claims["nbf"].(float64)
	now := float64(time.Now().Unix())
	if exp-now < 1795 || exp-now > 1805 {
		t.Errorf("exp offset = %v, want ~1800", exp-now)
	}
	if now-nbf < 4 || now-nbf > 6 {
		t.Errorf("nbf offset = %v, want ~5", now-nbf)
	}
}

// ---------------------------------------------------------------------
// convertToRequestPayload / getAspectRatio / defaultString / defaultInt
// ---------------------------------------------------------------------

func TestGetAspectRatio(t *testing.T) {
	a := &TaskAdaptor{}
	cases := map[string]string{
		"1024x1024": "1:1",
		"512x512":   "1:1",
		"1280x720":  "16:9",
		"1920x1080": "16:9",
		"720x1280":  "9:16",
		"1080x1920": "9:16",
		"weird":     "1:1",
		"":          "1:1",
	}
	for size, want := range cases {
		if got := a.getAspectRatio(size); got != want {
			t.Errorf("getAspectRatio(%q) = %q, want %q", size, got, want)
		}
	}
}

func TestDefaultStringAndDefaultInt(t *testing.T) {
	if got := defaultString("  ", "fallback"); got != "fallback" {
		t.Errorf("defaultString(blank) = %q, want fallback", got)
	}
	if got := defaultString("value", "fallback"); got != "value" {
		t.Errorf("defaultString(value) = %q, want value", got)
	}
	if got := defaultInt(0, 5); got != 5 {
		t.Errorf("defaultInt(0) = %d, want 5", got)
	}
	if got := defaultInt(9, 5); got != 9 {
		t.Errorf("defaultInt(9) = %d, want 9", got)
	}
}

func TestConvertToRequestPayload_Defaults(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Image:  "img-data",
		Size:   "1280x720",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if payload.Mode != "std" {
		t.Errorf("Mode = %q, want std", payload.Mode)
	}
	if payload.Duration != "5" {
		t.Errorf("Duration = %q, want 5", payload.Duration)
	}
	if payload.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want 16:9", payload.AspectRatio)
	}
	if payload.ModelName != "kling-v1" {
		t.Errorf("ModelName = %q, want kling-v1 (default fallback)", payload.ModelName)
	}
	if payload.Model != "" {
		t.Errorf("Model = %q, want empty (req.Model was empty)", payload.Model)
	}
	if payload.CfgScale != 0.5 {
		t.Errorf("CfgScale = %v, want 0.5", payload.CfgScale)
	}
	if payload.Image != "img-data" {
		t.Errorf("Image = %q, want img-data", payload.Image)
	}
}

func TestConvertToRequestPayload_ExplicitModelAndDuration(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:   "a dog",
		Model:    "kling-v2-master",
		Mode:     "pro",
		Duration: 10,
		Size:     "720x1280",
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if payload.ModelName != "kling-v2-master" || payload.Model != "kling-v2-master" {
		t.Errorf("ModelName/Model = %q/%q, want kling-v2-master both", payload.ModelName, payload.Model)
	}
	if payload.Mode != "pro" {
		t.Errorf("Mode = %q, want pro", payload.Mode)
	}
	if payload.Duration != "10" {
		t.Errorf("Duration = %q, want 10", payload.Duration)
	}
	if payload.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want 9:16", payload.AspectRatio)
	}
}

func TestConvertToRequestPayload_MetadataOverride(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "override test",
		Metadata: map[string]interface{}{
			"negative_prompt":  "blurry",
			"callback_url":     "https://cb.example.com",
			"external_task_id": "ext-1",
			"static_mask":      "mask-data",
		},
	}
	payload, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if payload.NegativePrompt != "blurry" {
		t.Errorf("NegativePrompt = %q, want blurry", payload.NegativePrompt)
	}
	if payload.CallbackUrl != "https://cb.example.com" {
		t.Errorf("CallbackUrl = %q, want https://cb.example.com", payload.CallbackUrl)
	}
	if payload.ExternalTaskId != "ext-1" {
		t.Errorf("ExternalTaskId = %q, want ext-1", payload.ExternalTaskId)
	}
	if payload.StaticMask != "mask-data" {
		t.Errorf("StaticMask = %q, want mask-data", payload.StaticMask)
	}
}

// ---------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------

func TestValidateRequestAndSetAction_MissingPrompt(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx(t, http.MethodPost, "/", []byte(`{"prompt":""}`))
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("expected TaskError for empty prompt")
	}
	if taskErr.Message != "prompt is required" {
		t.Errorf("Message = %q, want %q", taskErr.Message, "prompt is required")
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
	}
}

func TestValidateRequestAndSetAction_Valid(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx(t, http.MethodPost, "/", []byte(`{"prompt":"a scene"}`))
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("unexpected TaskError: %+v", taskErr)
	}
	if info.Action != pkgconstant.TaskActionGenerate {
		t.Errorf("info.Action = %q, want %q", info.Action, pkgconstant.TaskActionGenerate)
	}
	v, ok := c.Get("task_request")
	if !ok {
		t.Fatal("expected task_request set in context")
	}
	tr := v.(relaycommon.TaskSubmitReq)
	if tr.Prompt != "a scene" {
		t.Errorf("stored prompt = %q, want %q", tr.Prompt, "a scene")
	}
}

// ---------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------

func TestBuildRequestBody_MissingTaskRequest(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	_, err := a.BuildRequestBody(c, info)
	if err == nil || !strings.Contains(err.Error(), "request not found in context") {
		t.Fatalf("err = %v, want 'request not found in context'", err)
	}
}

func TestBuildRequestBody_TextOnlySetsTextGenerateAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx(t, http.MethodPost, "/", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "text only, no image"})
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}

	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	if got := c.GetString("action"); got != pkgconstant.TaskActionTextGenerate {
		t.Errorf("action = %q, want %q", got, pkgconstant.TaskActionTextGenerate)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}
	var got requestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal produced body: %v", err)
	}
	if got.Prompt != "text only, no image" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "text only, no image")
	}
}

func TestBuildRequestBody_WithImageDoesNotSetTextAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx(t, http.MethodPost, "/", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "img prompt", Image: "b64data"})
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}

	if _, err := a.BuildRequestBody(c, info); err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	if got := c.GetString("action"); got != "" {
		t.Errorf("action = %q, want empty (image present)", got)
	}
}

// ---------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------

func makeRespBody(t *testing.T, v any) *http.Response {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return &http.Response{Body: io.NopCloser(bytes.NewReader(b))}
}

func TestDoResponse_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("not json")))}
	c, _ := newGinCtx(t, http.MethodPost, "/", nil)
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected TaskError for invalid JSON")
	}
	if taskErr.Code != "unmarshal_response_failed" {
		t.Errorf("Code = %q, want unmarshal_response_failed", taskErr.Code)
	}
}

func TestDoResponse_UpstreamErrorCode(t *testing.T) {
	a := &TaskAdaptor{}
	resp := makeRespBody(t, map[string]any{"code": 1001, "message": "boom"})
	c, _ := newGinCtx(t, http.MethodPost, "/", nil)
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected TaskError for non-zero code")
	}
	if taskErr.Code != "task_failed" {
		t.Errorf("Code = %q, want task_failed", taskErr.Code)
	}
	if taskErr.Message != "boom" {
		t.Errorf("Message = %q, want boom", taskErr.Message)
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
	}
}

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	resp := makeRespBody(t, map[string]any{
		"code": 0,
		"data": map[string]any{"task_id": "task-123", "task_status": "submitted"},
	})
	c, w := newGinCtx(t, http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v1"}
	taskID, body, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected TaskError: %+v", taskErr)
	}
	if taskID != "task-123" {
		t.Errorf("taskID = %q, want task-123", taskID)
	}
	if len(body) == 0 {
		t.Error("expected non-empty raw response body returned")
	}
	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want %d", w.Code, http.StatusOK)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal written JSON response: %v", err)
	}
	if ov.ID != "task-123" || ov.TaskID != "task-123" {
		t.Errorf("ID/TaskID = %q/%q, want task-123 both", ov.ID, ov.TaskID)
	}
	if ov.Model != "kling-v1" {
		t.Errorf("Model = %q, want kling-v1", ov.Model)
	}
}

// ---------------------------------------------------------------------
// FetchTask - guard branches only (no network I/O)
// ---------------------------------------------------------------------

func TestFetchTask_MissingTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://base", "ak|sk", map[string]any{"action": pkgconstant.TaskActionGenerate}, "")
	if err == nil || err.Error() != "invalid task_id" {
		t.Errorf("err = %v, want 'invalid task_id'", err)
	}
}

func TestFetchTask_MissingAction(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://base", "ak|sk", map[string]any{"task_id": "t1"}, "")
	if err == nil || err.Error() != "invalid action" {
		t.Errorf("err = %v, want 'invalid action'", err)
	}
}

// TestFetchTask_BadRequestURL drives FetchTask through path/url construction
// (both action branches, both key relay modes) up to http.NewRequest, which
// fails on a control character in the URL before any network I/O occurs.
func TestFetchTask_BadRequestURL(t *testing.T) {
	cases := []struct {
		name   string
		action string
		apiKey string
	}{
		{"generate_plain_key", pkgconstant.TaskActionGenerate, "ak|sk"},
		{"other_newapi_key", "somethingElse", "sk-abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			_, err := a.FetchTask("https://bad\nhost", tc.apiKey, map[string]any{
				"task_id": "t1",
				"action":  tc.action,
			}, "")
			if err == nil {
				t.Fatal("expected error building request from malformed URL")
			}
		})
	}
}

// TestFetchTask_ProxyClientError drives FetchTask all the way through JWT
// header construction and stops at app.GetHttpClientWithProxy's url.Parse
// failure — this happens before client.Do, so no network I/O occurs.
func TestFetchTask_ProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://base", "myaccess|mysecret", map[string]any{
		"task_id": "t1",
		"action":  pkgconstant.TaskActionGenerate,
	}, "http://bad\nproxy")
	if err == nil || !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Errorf("err = %v, want to contain 'new proxy http client failed'", err)
	}
}

// ---------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}

	cases := []struct {
		status string
		want   string
	}{
		{"submitted", string(repo.TaskStatusSubmitted)},
		{"processing", string(repo.TaskStatusInProgress)},
		{"succeed", string(repo.TaskStatusSuccess)},
		{"failed", string(repo.TaskStatusFailure)},
	}
	for _, c := range cases {
		body, _ := json.Marshal(map[string]any{
			"code": 0,
			"data": map[string]any{
				"task_id":         "tid",
				"task_status":     c.status,
				"task_status_msg": "msg-" + c.status,
			},
		})
		ti, err := a.ParseTaskResult(body)
		if err != nil {
			t.Fatalf("status %q: unexpected error %v", c.status, err)
		}
		if ti.Status != c.want {
			t.Errorf("status %q: Status = %v, want %v", c.status, ti.Status, c.want)
		}
		if ti.TaskID != "tid" {
			t.Errorf("status %q: TaskID = %q, want tid", c.status, ti.TaskID)
		}
		if ti.Reason != "msg-"+c.status {
			t.Errorf("status %q: Reason = %q, want %q", c.status, ti.Reason, "msg-"+c.status)
		}
	}

	unknownBody, _ := json.Marshal(map[string]any{
		"data": map[string]any{"task_status": "weird"},
	})
	if _, err := a.ParseTaskResult(unknownBody); err == nil {
		t.Error("expected error for unknown task status")
	} else if !strings.Contains(err.Error(), "unknown task status: weird") {
		t.Errorf("err = %v, want to mention weird status", err)
	}

	withVideo, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"task_status": "succeed",
			"task_result": map[string]any{
				"videos": []map[string]any{{"url": "https://cdn/vid.mp4"}},
			},
		},
	})
	ti, err := a.ParseTaskResult(withVideo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Url != "https://cdn/vid.mp4" {
		t.Errorf("Url = %q, want https://cdn/vid.mp4", ti.Url)
	}
}

// ---------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------

func TestConvertToOpenAIVideo_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{Data: []byte("not json")}
	if _, err := a.ConvertToOpenAIVideo(task); err == nil {
		t.Fatal("expected error for invalid task data JSON")
	}
}

func TestConvertToOpenAIVideo_SuccessWithVideoAndError(t *testing.T) {
	a := &TaskAdaptor{}
	raw, _ := json.Marshal(map[string]any{
		"code":    500,
		"message": "upstream failure",
		"data": map[string]any{
			"created_at": int64(1000),
			"updated_at": int64(2000),
			"task_result": map[string]any{
				"videos": []map[string]any{{"url": "https://cdn/x.mp4", "duration": "5"}},
			},
		},
	})
	task := &repo.Task{
		TaskID:   "tid-1",
		Status:   repo.TaskStatusSuccess,
		Progress: "42%",
		Data:     raw,
	}
	out, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(out, &ov); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ov.ID != "tid-1" {
		t.Errorf("ID = %q, want tid-1", ov.ID)
	}
	if ov.Status != dto.VideoStatusCompleted {
		t.Errorf("Status = %q, want %q", ov.Status, dto.VideoStatusCompleted)
	}
	if ov.Progress != 42 {
		t.Errorf("Progress = %d, want 42", ov.Progress)
	}
	if ov.CreatedAt != 1000 || ov.CompletedAt != 2000 {
		t.Errorf("CreatedAt/CompletedAt = %d/%d, want 1000/2000", ov.CreatedAt, ov.CompletedAt)
	}
	if ov.Seconds != "5" {
		t.Errorf("Seconds = %q, want 5", ov.Seconds)
	}
	if ov.Metadata["url"] != "https://cdn/x.mp4" {
		t.Errorf("Metadata[url] = %v, want https://cdn/x.mp4", ov.Metadata["url"])
	}
	if ov.Error == nil || ov.Error.Message != "upstream failure" || ov.Error.Code != "500" {
		t.Errorf("Error = %+v, want message=upstream failure code=500", ov.Error)
	}
}

func TestConvertToOpenAIVideo_NoVideoNoError(t *testing.T) {
	a := &TaskAdaptor{}
	raw, _ := json.Marshal(map[string]any{"code": 0})
	task := &repo.Task{
		TaskID:   "tid-2",
		Status:   repo.TaskStatusInProgress,
		Progress: "10%",
		Data:     raw,
	}
	out, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(out, &ov); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ov.Status != dto.VideoStatusInProgress {
		t.Errorf("Status = %q, want %q", ov.Status, dto.VideoStatusInProgress)
	}
	if ov.Error != nil {
		t.Errorf("Error = %+v, want nil", ov.Error)
	}
	if _, ok := ov.Metadata["url"]; ok {
		t.Error("Metadata[url] should be absent when there are no videos")
	}
}
