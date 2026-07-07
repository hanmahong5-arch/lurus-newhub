package vertex

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------- Init ----------

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    5,
			ChannelBaseUrl: "https://example.com",
			ApiKey:         `{"project_id":"p"}`,
		},
	}
	a.Init(info)
	if a.ChannelType != 5 {
		t.Fatalf("expected ChannelType=5, got %d", a.ChannelType)
	}
	if a.baseURL != "https://example.com" {
		t.Fatalf("expected baseURL set, got %q", a.baseURL)
	}
	if a.apiKey != `{"project_id":"p"}` {
		t.Fatalf("expected apiKey set, got %q", a.apiKey)
	}
}

// ---------- ValidateRequestAndSetAction ----------

func TestValidateRequestAndSetAction_MissingPrompt(t *testing.T) {
	prevMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1
	defer func() { constant.MaxRequestBodyMB = prevMax }()

	a := &TaskAdaptor{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := strings.NewReader(`{"prompt":""}`)
	req := httptest.NewRequest("POST", "http://example.com", body)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	taskErr := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
	if taskErr == nil {
		t.Fatalf("expected task error for empty prompt")
	}
	if taskErr.Code != "invalid_request" {
		t.Fatalf("expected invalid_request code, got %q", taskErr.Code)
	}
}

func TestValidateRequestAndSetAction_Valid(t *testing.T) {
	// GetRequestBody enforces constant.MaxRequestBodyMB (0 by default in this
	// hermetic test binary, which would reject any body); raise it just for
	// this test and restore afterward.
	prevMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1
	defer func() { constant.MaxRequestBodyMB = prevMax }()

	a := &TaskAdaptor{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := strings.NewReader(`{"prompt":"a dog running"}`)
	req := httptest.NewRequest("POST", "http://example.com", body)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	taskErr := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	v, ok := c.Get("task_request")
	if !ok {
		t.Fatalf("expected task_request to be stored in context")
	}
	stored, ok := v.(relaycommon.TaskSubmitReq)
	if !ok || stored.Prompt != "a dog running" {
		t.Fatalf("unexpected stored task request: %+v", v)
	}
}

// ---------- BuildRequestURL ----------

func TestBuildRequestURL_GlobalRegion(t *testing.T) {
	a := &TaskAdaptor{apiKey: `{"project_id":"proj-1"}`}
	info := &relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001", ChannelMeta: &relaycommon.ChannelMeta{ApiVersion: ""}}
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/projects/proj-1/locations/global/publishers/google/models/veo-3.0-generate-001:predictLongRunning"
	if url != want {
		t.Fatalf("got %q want %q", url, want)
	}
}

func TestBuildRequestURL_DefaultModelName(t *testing.T) {
	a := &TaskAdaptor{apiKey: `{"project_id":"proj-2"}`}
	info := &relaycommon.RelayInfo{OriginModelName: "", ChannelMeta: &relaycommon.ChannelMeta{ApiVersion: ""}}
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(url, "models/veo-3.0-generate-001:predictLongRunning") {
		t.Fatalf("expected default model in url, got %q", url)
	}
}

func TestBuildRequestURL_RegionSpecific(t *testing.T) {
	a := &TaskAdaptor{apiKey: `{"project_id":"proj-3"}`}
	// ApiVersion as JSON map routes model to a specific region.
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiVersion: `{"veo-3.0-generate-001":"us-central1"}`},
	}
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj-3/locations/us-central1/publishers/google/models/veo-3.0-generate-001:predictLongRunning"
	if url != want {
		t.Fatalf("got %q want %q", url, want)
	}
}

func TestBuildRequestURL_BadCredentials(t *testing.T) {
	a := &TaskAdaptor{apiKey: `not-json`}
	_, err := a.BuildRequestURL(&relaycommon.RelayInfo{})
	if err == nil || !strings.Contains(err.Error(), "failed to decode credentials") {
		t.Fatalf("expected decode credentials error, got %v", err)
	}
}

// ---------- BuildRequestHeader ----------

func TestBuildRequestHeader_BadCredentials(t *testing.T) {
	a := &TaskAdaptor{apiKey: `{bad json`}
	req := httptest.NewRequest("POST", "http://example.com", nil)
	err := a.BuildRequestHeader(nil, req, &relaycommon.RelayInfo{})
	if err == nil || !strings.Contains(err.Error(), "failed to decode credentials") {
		t.Fatalf("expected decode credentials error, got %v", err)
	}
}

func TestBuildRequestHeader_TokenAcquisitionFails(t *testing.T) {
	// Valid JSON credentials but an unparsable private key -> AcquireAccessToken
	// fails hermetically (no network dial reached).
	a := &TaskAdaptor{apiKey: `{"project_id":"proj-1","client_email":"a@b.com","private_key":""}`}
	req := httptest.NewRequest("POST", "http://example.com", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	err := a.BuildRequestHeader(nil, req, info)
	if err == nil || !strings.Contains(err.Error(), "failed to acquire access token") {
		t.Fatalf("expected acquire access token error, got %v", err)
	}
}

// ---------- BuildRequestBody ----------

func newGinContextWithTaskRequest(v any) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if v != nil {
		c.Set("task_request", v)
	}
	return c
}

func TestBuildRequestBody_MissingTaskRequest(t *testing.T) {
	a := &TaskAdaptor{}
	c := newGinContextWithTaskRequest(nil)
	_, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil || err.Error() != "request not found in context" {
		t.Fatalf("expected 'request not found in context', got %v", err)
	}
}

func TestBuildRequestBody_DefaultSampleCount(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{Prompt: "a cat playing piano"}
	c := newGinContextWithTaskRequest(req)
	info := &relaycommon.RelayInfo{}
	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload requestPayload
	if derr := json.NewDecoder(r).Decode(&payload); derr != nil {
		t.Fatalf("decode failed: %v", derr)
	}
	if len(payload.Instances) != 1 || payload.Instances[0]["prompt"] != "a cat playing piano" {
		t.Fatalf("unexpected instances: %+v", payload.Instances)
	}
	if payload.Parameters["sampleCount"].(float64) != 1 {
		t.Fatalf("expected default sampleCount=1, got %v", payload.Parameters["sampleCount"])
	}
	if info.PriceData.OtherRatios["sampleCount"] != 1 {
		t.Fatalf("expected OtherRatios sampleCount=1, got %v", info.PriceData.OtherRatios["sampleCount"])
	}
}

func TestBuildRequestBody_MetadataIntSampleCountAndStorageUri(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Prompt: "sunset",
		Metadata: map[string]interface{}{
			"sampleCount": 3,
			"storageUri":  "gs://bucket/path",
		},
	}
	c := newGinContextWithTaskRequest(req)
	info := &relaycommon.RelayInfo{}
	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload requestPayload
	if derr := json.NewDecoder(r).Decode(&payload); derr != nil {
		t.Fatalf("decode failed: %v", derr)
	}
	if payload.Parameters["sampleCount"].(float64) != 3 {
		t.Fatalf("expected sampleCount=3, got %v", payload.Parameters["sampleCount"])
	}
	if payload.Parameters["storageUri"] != "gs://bucket/path" {
		t.Fatalf("expected storageUri passthrough, got %v", payload.Parameters["storageUri"])
	}
	if info.PriceData.OtherRatios["sampleCount"] != 3 {
		t.Fatalf("expected OtherRatios sampleCount=3, got %v", info.PriceData.OtherRatios["sampleCount"])
	}
}

func TestBuildRequestBody_MetadataFloatSampleCount(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Prompt:   "sunrise",
		Metadata: map[string]interface{}{"sampleCount": float64(2)},
	}
	c := newGinContextWithTaskRequest(req)
	info := &relaycommon.RelayInfo{}
	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload requestPayload
	if derr := json.NewDecoder(r).Decode(&payload); derr != nil {
		t.Fatalf("decode failed: %v", derr)
	}
	if payload.Parameters["sampleCount"].(float64) != 2 {
		t.Fatalf("expected sampleCount=2, got %v", payload.Parameters["sampleCount"])
	}
}

func TestBuildRequestBody_SampleCountZeroErrors(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Prompt:   "invalid",
		Metadata: map[string]interface{}{"sampleCount": 0},
	}
	c := newGinContextWithTaskRequest(req)
	_, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil || err.Error() != "sampleCount must be greater than 0" {
		t.Fatalf("expected sampleCount error, got %v", err)
	}
}

// ---------- DoResponse ----------

func TestDoResponse_UnmarshalFails(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := httptest.NewRecorder().Result()
	resp.Body = readCloser("not json")
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected task error for bad json")
	}
}

func TestDoResponse_MissingName(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := httptest.NewRecorder().Result()
	resp.Body = readCloser(`{"name":""}`)
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected task error for missing operation name")
	}
}

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	name := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
	resp := httptest.NewRecorder().Result()
	body, _ := json.Marshal(submitResponse{Name: name})
	resp.Body = readCloser(string(body))
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %v", taskErr)
	}
	if taskID == "" {
		t.Fatalf("expected non-empty taskID")
	}
	decoded, err := decodeLocalTaskID(taskID)
	if err != nil || decoded != name {
		t.Fatalf("expected round-trip decode of %q, got %q err=%v", name, decoded, err)
	}
	if string(taskData) != string(body) {
		t.Fatalf("expected taskData to equal raw response body")
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200 response written, got %d", rec.Code)
	}
}

// ---------- GetModelList / GetChannelName ----------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "veo-3.0-generate-001" {
		t.Fatalf("unexpected model list: %v", models)
	}
	if a.GetChannelName() != "vertex" {
		t.Fatalf("unexpected channel name: %s", a.GetChannelName())
	}
}

// ---------- FetchTask ----------

func TestFetchTask_InvalidTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("", "", map[string]any{}, "")
	if err == nil || err.Error() != "invalid task_id" {
		t.Fatalf("expected invalid task_id error, got %v", err)
	}
}

func TestFetchTask_DecodeFails(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("", "", map[string]any{"task_id": "not-base64-!!!"}, "")
	if err == nil || !strings.Contains(err.Error(), "decode task_id failed") {
		t.Fatalf("expected decode task_id failed error, got %v", err)
	}
}

func TestFetchTask_CannotExtractProjectOrModel(t *testing.T) {
	a := &TaskAdaptor{}
	localID := encodeLocalTaskID("operations/op-without-project-or-model")
	_, err := a.FetchTask("", "", map[string]any{"task_id": localID}, "")
	if err == nil || err.Error() != "cannot extract project/model from operation name" {
		t.Fatalf("expected extraction error, got %v", err)
	}
}

func TestFetchTask_BadCredentials(t *testing.T) {
	a := &TaskAdaptor{}
	name := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
	localID := encodeLocalTaskID(name)
	_, err := a.FetchTask("", "not-json", map[string]any{"task_id": localID}, "")
	if err == nil || !strings.Contains(err.Error(), "failed to decode credentials") {
		t.Fatalf("expected decode credentials error, got %v", err)
	}
}

func TestFetchTask_GlobalRegionDefaultAndTokenAcquisitionFails(t *testing.T) {
	a := &TaskAdaptor{}
	// No "locations/xxx/" segment present -> region falls back to "us-central1"
	// (see FetchTask default), but here we also validate the "global" branch by
	// including a global location segment.
	name := "projects/proj-1/locations/global/publishers/google/models/veo-3.0-generate-001/operations/op-1"
	localID := encodeLocalTaskID(name)
	key := `{"project_id":"proj-1","client_email":"a@b.com","private_key":""}`
	_, err := a.FetchTask("", key, map[string]any{"task_id": localID}, "")
	if err == nil || !strings.Contains(err.Error(), "failed to acquire access token") {
		t.Fatalf("expected acquire access token error, got %v", err)
	}
}

// ---------- ParseTaskResult ----------

func TestParseTaskResult_UnmarshalFails(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.ParseTaskResult([]byte("not json"))
	if err == nil || !strings.Contains(err.Error(), "unmarshal operation response failed") {
		t.Fatalf("expected unmarshal error, got %v", err)
	}
}

func TestParseTaskResult_ErrorBranch(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"error":{"message":"quota exceeded"}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != repo.TaskStatusFailure || ti.Reason != "quota exceeded" || ti.Progress != "100%" {
		t.Fatalf("unexpected task info: %+v", ti)
	}
}

func TestParseTaskResult_InProgress(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":false}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != repo.TaskStatusInProgress || ti.Progress != "50%" {
		t.Fatalf("unexpected task info: %+v", ti)
	}
}

func TestParseTaskResult_SuccessNoContent(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != repo.TaskStatusSuccess || ti.Progress != "100%" || ti.Url != "" {
		t.Fatalf("unexpected task info: %+v", ti)
	}
}

func TestParseTaskResult_VideosWithMimeType(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"videos":[{"mimeType":"video/mp4","bytesBase64Encoded":"QUJD"}]}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/mp4;base64,QUJD"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_VideosMissingMimeTypeUsesEncoding(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"videos":[{"encoding":"webm","bytesBase64Encoded":"QUJD"}]}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/webm;base64,QUJD"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_VideosMissingMimeAndEncodingDefaultsMp4(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"videos":[{"bytesBase64Encoded":"QUJD"}]}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/mp4;base64,QUJD"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_VideosEncodingContainsSlashUsedAsMime(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"videos":[{"encoding":"application/octet-stream","bytesBase64Encoded":"QUJD"}]}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:application/octet-stream;base64,QUJD"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_TopLevelBytesBase64Encoded(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"bytesBase64Encoded":"XYZ1"}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/mp4;base64,XYZ1"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_TopLevelBytesBase64EncodedWithSlashEncoding(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"bytesBase64Encoded":"XYZ1","encoding":"video/quicktime"}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/quicktime;base64,XYZ1"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

func TestParseTaskResult_VideoField(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"done":true,"response":{"video":"VIDDATA","encoding":"mov"}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:video/mov;base64,VIDDATA"
	if ti.Url != want {
		t.Fatalf("got %q want %q", ti.Url, want)
	}
}

// ---------- ConvertToOpenAIVideo ----------

func TestConvertToOpenAIVideo_DecodeFailsFallsBackToDefaultModel(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID:    "not-base64-!!!",
		Status:    repo.TaskStatusInProgress,
		Progress:  "42%",
		CreatedAt: 100,
		UpdatedAt: 200,
	}
	b, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var v dto.OpenAIVideo
	if uerr := json.Unmarshal(b, &v); uerr != nil {
		t.Fatalf("unmarshal failed: %v", uerr)
	}
	if v.Model != "veo-3.0-generate-001" {
		t.Fatalf("expected default model fallback, got %q", v.Model)
	}
	if v.Status != "in_progress" {
		t.Fatalf("expected in_progress status, got %q", v.Status)
	}
	if v.Progress != 42 {
		t.Fatalf("expected progress 42, got %d", v.Progress)
	}
}

func TestConvertToOpenAIVideo_SuccessWithModelAndURLMetadata(t *testing.T) {
	a := &TaskAdaptor{}
	name := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
	task := &repo.Task{
		TaskID:     encodeLocalTaskID(name),
		Status:     repo.TaskStatusSuccess,
		Progress:   "100%",
		CreatedAt:  111,
		UpdatedAt:  222,
		FailReason: "data:video/mp4;base64,QUJD",
	}
	b, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var v dto.OpenAIVideo
	if uerr := json.Unmarshal(b, &v); uerr != nil {
		t.Fatalf("unmarshal failed: %v", uerr)
	}
	if v.ID != task.TaskID {
		t.Fatalf("expected ID to equal task.TaskID")
	}
	if v.Model != "veo-3.0-generate-001" {
		t.Fatalf("expected model extracted from operation name, got %q", v.Model)
	}
	if v.Status != "completed" {
		t.Fatalf("expected completed status, got %q", v.Status)
	}
	if v.CreatedAt != 111 || v.CompletedAt != 222 {
		t.Fatalf("unexpected timestamps: %+v", v)
	}
	if v.Metadata["url"] != "data:video/mp4;base64,QUJD" {
		t.Fatalf("expected url metadata set, got %+v", v.Metadata)
	}
}

func TestConvertToOpenAIVideo_NoURLMetadataWhenFailReasonNotDataURI(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID:     encodeLocalTaskID("projects/p/locations/us-central1/publishers/google/models/m1/operations/op-1"),
		Status:     repo.TaskStatusFailure,
		FailReason: "some upstream error",
	}
	b, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var v dto.OpenAIVideo
	if uerr := json.Unmarshal(b, &v); uerr != nil {
		t.Fatalf("unmarshal failed: %v", uerr)
	}
	if v.Status != "failed" {
		t.Fatalf("expected failed status, got %q", v.Status)
	}
	if _, ok := v.Metadata["url"]; ok {
		t.Fatalf("expected no url metadata, got %+v", v.Metadata)
	}
}

// ---------- helpers: encode/decode task id, region/model/project extraction ----------

func TestEncodeDecodeLocalTaskIDRoundTrip(t *testing.T) {
	name := "projects/p1/locations/us-central1/publishers/google/models/m1/operations/op-1"
	encoded := encodeLocalTaskID(name)
	decoded, err := decodeLocalTaskID(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != name {
		t.Fatalf("got %q want %q", decoded, name)
	}
}

func TestDecodeLocalTaskID_InvalidBase64(t *testing.T) {
	_, err := decodeLocalTaskID("not valid base64!!!")
	if err == nil {
		t.Fatalf("expected error for invalid base64 input")
	}
}

func TestExtractRegionFromOperationName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"present", "projects/p/locations/us-central1/publishers/google/models/m/operations/op", "us-central1"},
		{"global", "projects/p/locations/global/publishers/google/models/m/operations/op", "global"},
		{"absent", "no-locations-segment-here", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRegionFromOperationName(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExtractModelFromOperationName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"regex-match", "projects/p/locations/us-central1/publishers/google/models/veo-3.0/operations/op-1", "veo-3.0"},
		{"fallback-index-path-multi-segment-model", "projects/p/models/sub/dir/operations/op-1", "sub/dir"},
		{"models-present-no-operations-suffix", "projects/p/models/onlymodel", ""},
		{"absent", "no-models-segment", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractModelFromOperationName(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExtractProjectFromOperationName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"present", "projects/my-proj/locations/us-central1/publishers/google/models/m/operations/op", "my-proj"},
		{"absent", "no-projects-segment", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractProjectFromOperationName(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// readCloser wraps a string body as an io.ReadCloser for constructing
// *http.Response bodies in tests without any network I/O.
type stringReadCloser struct {
	*strings.Reader
}

func (stringReadCloser) Close() error { return nil }

func readCloser(s string) stringReadCloser {
	return stringReadCloser{strings.NewReader(s)}
}
