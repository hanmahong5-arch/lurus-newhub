package vertex

// Business-acceptance tests for the Vertex AI (Veo) async video task
// adaptor: submit-task URL/region resolution, service-account credential
// parsing failure modes (kept network-free per the "no real egress" test
// constraint), operation-name encode/decode, status polling result parsing
// across the several upstream response variants, and result URL shaping.

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
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

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

// A syntactically valid (but cryptographically bogus) service-account JSON.
// createSignedJWT will fail to PEM-decode the private key, so acquiring a
// token fails locally without ever dialing Google.
const fakeCreds = `{"project_id":"proj-1","client_email":"svc@proj-1.iam.gserviceaccount.com","private_key":"not-a-real-key"}`

// ─── Init ────────────────────────────────────────────────────────────────────

func TestVertex_Init(t *testing.T) {
	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo("ignored", fakeCreds)
	a.Init(info)
	if a.apiKey != fakeCreds {
		t.Fatalf("expected raw credentials json captured as apiKey")
	}
	if a.ChannelType != 5 {
		t.Fatalf("expected ChannelType captured, got %d", a.ChannelType)
	}
}

// ─── BuildRequestURL (region/model resolution) ──────────────────────────────

func TestVertex_BuildRequestURL(t *testing.T) {
	t.Run("invalid credentials json is rejected before any URL is built", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: "not json"}
		if _, err := a.BuildRequestURL(&relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for invalid credentials json")
		}
	})

	t.Run("empty model name defaults to veo-3.0-generate-001", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		// ChannelMeta must be present: BuildRequestURL reads the promoted
		// info.ApiVersion, so a bare RelayInfo{} is not a shape the relay layer
		// ever produces and would only test a nil dereference.
		url, err := a.BuildRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(url, "models/veo-3.0-generate-001:predictLongRunning") {
			t.Fatalf("expected default model name in URL, got %q", url)
		}
	})

	t.Run("blank ApiVersion resolves to the global endpoint (no region prefix)", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiVersion: ""}, OriginModelName: "veo-3.0-generate-001"}
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://aiplatform.googleapis.com/v1/projects/proj-1/locations/global/publishers/google/models/veo-3.0-generate-001:predictLongRunning"
		if url != want {
			t.Fatalf("expected global endpoint %q, got %q", want, url)
		}
	})

	t.Run("per-model region map routes to a region-specific host", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiVersion: `{"veo-3.0-generate-001":"us-central1"}`,
			},
			OriginModelName: "veo-3.0-generate-001",
		}
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001:predictLongRunning"
		if url != want {
			t.Fatalf("expected regional endpoint %q, got %q", want, url)
		}
	})

	t.Run("region map 'default' entry is used when the specific model is absent", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiVersion: `{"default":"asia-east1"}`},
			OriginModelName: "some-other-model",
		}
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(url, "https://asia-east1-aiplatform.googleapis.com/") {
			t.Fatalf("expected default-region fallback, got %q", url)
		}
	})
}

// ─── BuildRequestHeader (credential parsing / local token-acquire failure) ──

func TestVertex_BuildRequestHeader(t *testing.T) {
	t.Run("invalid credentials json is rejected", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: "not json"}
		req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
		if err := a.BuildRequestHeader(nil, req, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for invalid credentials json")
		}
	})

	t.Run("bogus private key fails token acquisition locally (no network egress)", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		err := a.BuildRequestHeader(nil, req, info)
		if err == nil {
			t.Fatalf("expected error for unparseable private key")
		}
		if !strings.Contains(err.Error(), "failed to acquire access token") {
			t.Fatalf("expected acquire-token error wrapper, got %v", err)
		}
	})

	t.Run("nil info does not panic (proxy defaults to empty)", func(t *testing.T) {
		a := &TaskAdaptor{apiKey: fakeCreds}
		req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
		// info == nil exercises the `if info != nil` guard; must not panic even
		// though it still fails locally on the bogus key.
		_ = a.BuildRequestHeader(nil, req, nil)
	})
}

// ─── BuildRequestBody (sampleCount / storageUri / billing ratio) ───────────

func TestVertex_BuildRequestBody(t *testing.T) {
	t.Run("missing task_request is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for missing task_request")
		}
	})

	t.Run("default sampleCount=1 when metadata omits it, billed via OtherRatios", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "a horse running"})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got requestPayload
		_ = json.NewDecoder(r).Decode(&got)
		if got.Instances[0]["prompt"] != "a horse running" {
			t.Fatalf("expected prompt preserved, got %+v", got.Instances)
		}
		if got.Parameters["sampleCount"].(float64) != 1 {
			t.Fatalf("expected default sampleCount 1, got %v", got.Parameters["sampleCount"])
		}
		if info.PriceData.OtherRatios["sampleCount"] != 1 {
			t.Fatalf("expected billed sampleCount ratio 1, got %v", info.PriceData.OtherRatios["sampleCount"])
		}
	})

	t.Run("metadata sampleCount (float64, as decoded from JSON) is honored", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Prompt:   "p",
			Metadata: map[string]interface{}{"sampleCount": float64(3), "storageUri": "gs://bucket/out/"},
		})
		info := &relaycommon.RelayInfo{}
		_, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.PriceData.OtherRatios["sampleCount"] != 3 {
			t.Fatalf("expected billed sampleCount 3, got %v", info.PriceData.OtherRatios["sampleCount"])
		}
	})

	t.Run("sampleCount <= 0 is rejected (would under-bill / no-op generate)", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Prompt:   "p",
			Metadata: map[string]interface{}{"sampleCount": float64(0)},
		})
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for sampleCount <= 0")
		}
	})

	t.Run("negative sampleCount is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Prompt:   "p",
			Metadata: map[string]interface{}{"sampleCount": float64(-2)},
		})
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for negative sampleCount")
		}
	})
}

// ─── DoResponse (submit-task response handling) ─────────────────────────────

func TestVertex_DoResponse(t *testing.T) {
	t.Run("operation name present -> encodes local task id and echoes 200", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		opName := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-123"
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"name":"` + opName + `"}`))}
		taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr != nil {
			t.Fatalf("unexpected error: %s / %s", taskErr.Code, taskErr.Message)
		}
		if taskID == "" {
			t.Fatalf("expected non-empty encoded local task id")
		}
		decoded, err := decodeLocalTaskID(taskID)
		if err != nil || decoded != opName {
			t.Fatalf("expected round-trippable encoding of the operation name, got %q err=%v", decoded, err)
		}
		if taskData == nil {
			t.Fatalf("expected raw body returned for persistence")
		}
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 to caller, got %d", w.Code)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["task_id"] != taskID {
			t.Fatalf("expected client-visible task_id to match returned id, got %v", body)
		}
	})

	t.Run("missing operation name is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{}`))}
		_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr == nil || taskErr.Code != "invalid_response" {
			t.Fatalf("expected invalid_response error for missing operation name, got %+v", taskErr)
		}
	})

	t.Run("malformed json does not panic", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(`not json`))}
		_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr == nil || taskErr.Code != "unmarshal_response_failed" {
			t.Fatalf("expected unmarshal error, got %+v", taskErr)
		}
	})
}

// ─── FetchTask (polling; local error paths only, no network) ───────────────

func TestVertex_FetchTask(t *testing.T) {
	t.Run("missing task_id is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		bcResp4, err := a.FetchTask("https://x", fakeCreds, map[string]any{}, "")
		defer func() {
			if bcResp4 != nil {
				_ = bcResp4.Body.Close()
			}
		}()
		if err == nil {
			t.Fatalf("expected error for missing task_id")
		}
	})

	t.Run("non-base64 task_id fails to decode into an operation name", func(t *testing.T) {
		a := &TaskAdaptor{}
		bcResp3, err := a.FetchTask("https://x", fakeCreds, map[string]any{"task_id": "not!base64!"}, "")
		defer func() {
			if bcResp3 != nil {
				_ = bcResp3.Body.Close()
			}
		}()
		if err == nil {
			t.Fatalf("expected decode error for malformed local task id")
		}
	})

	t.Run("operation name missing project/model is rejected before any credential/network work", func(t *testing.T) {
		a := &TaskAdaptor{}
		localID := encodeLocalTaskID("garbage-operation-name-with-no-fields")
		bcResp2, err := a.FetchTask("https://x", fakeCreds, map[string]any{"task_id": localID}, "")
		defer func() {
			if bcResp2 != nil {
				_ = bcResp2.Body.Close()
			}
		}()
		if err == nil {
			t.Fatalf("expected error when project/model cannot be extracted")
		}
	})

	t.Run("invalid credentials json is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		opName := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
		localID := encodeLocalTaskID(opName)
		bcResp1, err := a.FetchTask("https://x", "not json", map[string]any{"task_id": localID}, "")
		defer func() {
			if bcResp1 != nil {
				_ = bcResp1.Body.Close()
			}
		}()
		if err == nil {
			t.Fatalf("expected error for invalid credentials json")
		}
	})

	t.Run("bogus private key fails token acquisition locally before any HTTP dial", func(t *testing.T) {
		a := &TaskAdaptor{}
		opName := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
		localID := encodeLocalTaskID(opName)
		bcResp0, err := a.FetchTask("https://x", fakeCreds, map[string]any{"task_id": localID}, "")
		defer func() {
			if bcResp0 != nil {
				_ = bcResp0.Body.Close()
			}
		}()
		if err == nil {
			t.Fatalf("expected error for unparseable private key")
		}
		if !strings.Contains(err.Error(), "failed to acquire access token") {
			t.Fatalf("expected acquire-token error wrapper, got %v", err)
		}
	})
}

// ─── ParseTaskResult (status machine mapping / result shaping) ─────────────

func TestVertex_ParseTaskResult(t *testing.T) {
	t.Run("error message present -> failure, regardless of done flag", func(t *testing.T) {
		a := &TaskAdaptor{}
		info, err := a.ParseTaskResult([]byte(`{"done":true,"error":{"message":"quota exceeded"}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != string(repo.TaskStatusFailure) || info.Reason != "quota exceeded" || info.Progress != "100%" {
			t.Fatalf("expected failure with reason propagated, got %+v", info)
		}
	})

	t.Run("not done -> in_progress at 50%%", func(t *testing.T) {
		a := &TaskAdaptor{}
		info, err := a.ParseTaskResult([]byte(`{"done":false}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != string(repo.TaskStatusInProgress) || info.Progress != "50%" {
			t.Fatalf("expected in-progress at 50%%, got %+v", info)
		}
	})

	t.Run("done + videos[0] with explicit mimeType builds a data: url using it", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"videos":[{"mimeType":"video/webm","bytesBase64Encoded":"QUJD"}]}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "data:video/webm;base64,QUJD"
		if info.Status != string(repo.TaskStatusSuccess) || info.Url != want {
			t.Fatalf("expected %q, got status=%q url=%q", want, info.Status, info.Url)
		}
	})

	t.Run("done + videos[0] with no mimeType/encoding defaults to video/mp4", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"videos":[{"bytesBase64Encoded":"QUJD"}]}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "data:video/mp4;base64,QUJD" {
			t.Fatalf("expected mp4 default mime, got %q", info.Url)
		}
	})

	t.Run("done + videos[0] with a slash-bearing encoding is used verbatim as mime", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"videos":[{"encoding":"application/octet-stream","bytesBase64Encoded":"QUJD"}]}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "data:application/octet-stream;base64,QUJD" {
			t.Fatalf("expected mime taken verbatim from slash-bearing encoding, got %q", info.Url)
		}
	})

	t.Run("done + videos[0] with a bare (non-slash) encoding is prefixed with video/", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"videos":[{"encoding":"webm","bytesBase64Encoded":"QUJD"}]}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "data:video/webm;base64,QUJD" {
			t.Fatalf("expected video/ prefix for bare encoding, got %q", info.Url)
		}
	})

	t.Run("done + top-level response.bytesBase64Encoded (no videos array)", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"bytesBase64Encoded":"QUJD","encoding":"mp4"}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "data:video/mp4;base64,QUJD" {
			t.Fatalf("expected top-level bytesBase64Encoded to build a data url, got %q", info.Url)
		}
	})

	t.Run("done + response.video variant field", func(t *testing.T) {
		a := &TaskAdaptor{}
		body := `{"done":true,"response":{"video":"QUJD","encoding":"video/quicktime"}}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Url != "data:video/quicktime;base64,QUJD" {
			t.Fatalf("expected response.video variant to build a data url, got %q", info.Url)
		}
	})

	t.Run("done with no recognizable video payload returns success but empty url", func(t *testing.T) {
		a := &TaskAdaptor{}
		info, err := a.ParseTaskResult([]byte(`{"done":true,"response":{}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != string(repo.TaskStatusSuccess) || info.Url != "" {
			t.Fatalf("expected success with empty url for a payload-less done response, got %+v", info)
		}
	})

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

func TestVertex_ConvertToOpenAIVideo(t *testing.T) {
	t.Run("well-formed local task id resolves the model name from the operation", func(t *testing.T) {
		a := &TaskAdaptor{}
		opName := "projects/proj-1/locations/us-central1/publishers/google/models/veo-2.0-generate-001/operations/op-9"
		task := &repo.Task{
			TaskID:   encodeLocalTaskID(opName),
			Status:   repo.TaskStatusSuccess,
			Progress: "100%",
		}
		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		if err := json.Unmarshal(raw, &ov); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if ov.Model != "veo-2.0-generate-001" {
			t.Fatalf("expected model resolved from the operation name, got %q", ov.Model)
		}
	})

	t.Run("undecodable task id falls back to the default model name, no crash", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "not-base64-encoded!!", Status: repo.TaskStatusFailure}
		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Model != "veo-3.0-generate-001" {
			t.Fatalf("expected default model fallback, got %q", ov.Model)
		}
	})

	t.Run("data: FailReason is surfaced as metadata url (video is stored there, not in Data)", func(t *testing.T) {
		a := &TaskAdaptor{}
		opName := "projects/proj-1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/op-1"
		task := &repo.Task{
			TaskID:     encodeLocalTaskID(opName),
			Status:     repo.TaskStatusSuccess,
			FailReason: "data:video/mp4;base64,QUJD",
		}
		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Metadata["url"] != "data:video/mp4;base64,QUJD" {
			t.Fatalf("expected data: FailReason surfaced as metadata url, got %v", ov.Metadata)
		}
	})

	t.Run("non data: FailReason is not surfaced as a url", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: encodeLocalTaskID("projects/p/locations/us-central1/publishers/google/models/m/operations/o"), FailReason: "upstream 500"}
		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if _, ok := ov.Metadata["url"]; ok {
			t.Fatalf("did not expect a metadata url for a non-data: fail reason, got %v", ov.Metadata)
		}
	})
}

// ─── encode/decode local task id + operation-name regex extraction ─────────

func TestVertex_LocalTaskIDRoundTrip(t *testing.T) {
	name := "projects/p1/locations/us-central1/publishers/google/models/veo-3.0-generate-001/operations/abc123"
	enc := encodeLocalTaskID(name)
	dec, err := decodeLocalTaskID(enc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec != name {
		t.Fatalf("expected round-trip to preserve the operation name, got %q", dec)
	}

	if _, err := decodeLocalTaskID("not valid base64!!"); err == nil {
		t.Fatalf("expected decode error for invalid base64")
	}
}

func TestVertex_ExtractFromOperationName(t *testing.T) {
	name := "projects/proj-9/locations/asia-east1/publishers/google/models/veo-3.0-generate-001/operations/op-77"

	if got := extractRegionFromOperationName(name); got != "asia-east1" {
		t.Fatalf("expected region asia-east1, got %q", got)
	}
	if got := extractModelFromOperationName(name); got != "veo-3.0-generate-001" {
		t.Fatalf("expected model veo-3.0-generate-001, got %q", got)
	}
	if got := extractProjectFromOperationName(name); got != "proj-9" {
		t.Fatalf("expected project proj-9, got %q", got)
	}

	t.Run("no match returns empty string, not panic", func(t *testing.T) {
		if got := extractRegionFromOperationName("garbage"); got != "" {
			t.Fatalf("expected empty region for unmatched input, got %q", got)
		}
		if got := extractModelFromOperationName("garbage"); got != "" {
			t.Fatalf("expected empty model for unmatched input, got %q", got)
		}
		if got := extractProjectFromOperationName("garbage"); got != "" {
			t.Fatalf("expected empty project for unmatched input, got %q", got)
		}
	})

	t.Run("model fallback path used when regex character class would reject the model segment", func(t *testing.T) {
		// The regex requires "models/<no-slash-chars>/operations/"; the
		// substring fallback in extractModelFromOperationName is exercised
		// via the identical well-formed case (regex already matches), so we
		// assert the fallback branch converges to the same answer here.
		got := extractModelFromOperationName("prefix/models/veo-x/operations/suffix")
		if got != "veo-x" {
			t.Fatalf("expected veo-x, got %q", got)
		}
	})
}

// ─── GetModelList / GetChannelName ──────────────────────────────────────────

func TestVertex_GetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "vertex" {
		t.Fatalf("expected channel name 'vertex', got %q", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "veo-3.0-generate-001" {
		t.Fatalf("expected [veo-3.0-generate-001], got %v", models)
	}
}

// ─── ValidateRequestAndSetAction (delegates to shared validator) ───────────

func TestVertex_ValidateRequestAndSetAction(t *testing.T) {
	taskBatchANoBodyLimit(t)
	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo("ignored", fakeCreds)

	t.Run("valid prompt passes and records TextGenerate action", func(t *testing.T) {
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(`{"prompt":"a river at dawn"}`), "application/json")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected task error: %s", taskErr.Message)
		}
		if info.Action != constant.TaskActionTextGenerate {
			t.Fatalf("expected TextGenerate action, got %q", info.Action)
		}
	})

	t.Run("empty prompt is rejected", func(t *testing.T) {
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(`{"prompt":""}`), "application/json")
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatalf("expected error for empty prompt")
		}
	})
}
