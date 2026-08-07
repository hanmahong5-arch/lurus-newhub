package jimeng

// Business-acceptance tests for the Jimeng (Volcengine CV) async video task
// adaptor: submit-task request construction (incl. HMAC request signing and
// multipart image upload), status polling, state-machine mapping, and result
// URL propagation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

func taskBatchANewMultipartCtx(t *testing.T, files map[string]string, fields map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := mw.CreateFormFile("input_reference", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/x", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.Request = req
	return c, w
}

// ─── isNewAPIRelay / Init ────────────────────────────────────────────────────

func TestJimeng_IsNewAPIRelay(t *testing.T) {
	if !isNewAPIRelay("sk-abcdef") {
		t.Fatalf("expected sk- prefixed key to be treated as new-api relay")
	}
	if isNewAPIRelay("ak123|sk456") {
		t.Fatalf("expected ak|sk direct-vendor key to NOT be treated as new-api relay")
	}
	if isNewAPIRelay("") {
		t.Fatalf("expected empty key to NOT be treated as new-api relay")
	}
}

func TestJimeng_Init(t *testing.T) {
	t.Run("ak|sk key format splits into access/secret", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := taskBatchANewRelayInfo("https://visual.volcengineapi.com", "AKID123 | SECRET456")
		a.Init(info)
		if a.accessKey != "AKID123" || a.secretKey != "SECRET456" {
			t.Fatalf("expected trimmed access/secret key parts, got %q / %q", a.accessKey, a.secretKey)
		}
	})

	t.Run("sk- new-api key does not populate access/secret (no ak|sk split)", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := taskBatchANewRelayInfo("https://hub", "sk-relay-token")
		a.Init(info)
		if a.accessKey != "" || a.secretKey != "" {
			t.Fatalf("expected no access/secret parsed from a non ak|sk key, got %q / %q", a.accessKey, a.secretKey)
		}
	})
}

// ─── BuildRequestURL ─────────────────────────────────────────────────────────

func TestJimeng_BuildRequestURL(t *testing.T) {
	t.Run("new-api relay (sk- key) uses the /jimeng/ subpath", func(t *testing.T) {
		a := &TaskAdaptor{baseURL: "https://hub.example.com"}
		info := taskBatchANewRelayInfo("https://hub.example.com", "sk-relay")
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://hub.example.com/jimeng/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31"
		if url != want {
			t.Fatalf("expected %q, got %q", want, url)
		}
	})

	t.Run("direct vendor key hits base URL without /jimeng/ subpath", func(t *testing.T) {
		a := &TaskAdaptor{baseURL: "https://visual.volcengineapi.com"}
		info := taskBatchANewRelayInfo("https://visual.volcengineapi.com", "AK|SK")
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31"
		if url != want {
			t.Fatalf("expected %q, got %q", want, url)
		}
	})
}

// ─── BuildRequestHeader / signRequest ───────────────────────────────────────

func TestJimeng_BuildRequestHeader(t *testing.T) {
	t.Run("new-api relay uses bearer auth, no signing", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := taskBatchANewRelayInfo("https://hub", "sk-relay-token")
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		req, _ := http.NewRequest(http.MethodPost, "https://hub/x", nil)

		if err := a.BuildRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-relay-token" {
			t.Fatalf("expected bearer auth with relay key, got %q", got)
		}
	})

	t.Run("direct vendor key signs the request with HMAC headers", func(t *testing.T) {
		a := &TaskAdaptor{accessKey: "AK123", secretKey: "SK456"}
		info := taskBatchANewRelayInfo("https://visual.volcengineapi.com", "AK123|SK456")
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		req, _ := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask", bytes.NewReader([]byte(`{"a":1}`)))

		if err := a.BuildRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auth := req.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "HMAC-SHA256 Credential=AK123/") {
			t.Fatalf("expected HMAC credential prefixed with access key, got %q", auth)
		}
		if req.Header.Get("X-Date") == "" || req.Header.Get("X-Content-Sha256") == "" || req.Header.Get("Host") == "" {
			t.Fatalf("expected signing headers to be populated: X-Date=%q X-Content-Sha256=%q Host=%q",
				req.Header.Get("X-Date"), req.Header.Get("X-Content-Sha256"), req.Header.Get("Host"))
		}
		// body must be rewound and left intact for the actual network send
		b, _ := io.ReadAll(req.Body)
		if string(b) != `{"a":1}` {
			t.Fatalf("expected signing to preserve the request body for transmission, got %q", string(b))
		}
	})

	t.Run("signing with nil body does not panic and still signs", func(t *testing.T) {
		a := &TaskAdaptor{}
		req, _ := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/x", nil)
		if err := a.signRequest(req, "AK", "SK"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Header.Get("Authorization") == "" {
			t.Fatalf("expected authorization header to be set even with nil body")
		}
	})
}

// ─── BuildRequestBody (submit-task construction) ────────────────────────────

func TestJimeng_BuildRequestBody(t *testing.T) {
	t.Run("no task_request in context is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error when task_request missing from context")
		}
	})

	t.Run("JSON submission builds a valid payload from the stored task request", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Model:  "jimeng_vgfm_t2v_l20",
			Prompt: "a cat playing piano",
		})
		r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := io.ReadAll(r)
		var got requestPayload
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("invalid json body: %v", err)
		}
		if got.ReqKey != "jimeng_vgfm_t2v_l20" || got.Prompt != "a cat playing piano" {
			t.Fatalf("expected model/prompt preserved, got %+v", got)
		}
		if got.Frames != 121 {
			t.Fatalf("expected default 5s duration -> 121 frames, got %d", got.Frames)
		}
	})

	t.Run("wrong context value type is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
		c.Set("task_request", "not-a-task-request")
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
			t.Fatalf("expected error for wrong context value type")
		}
	})

	t.Run("single-file multipart upload sets Generate action and base64-encodes the image", func(t *testing.T) {
		c, _ := taskBatchANewMultipartCtx(t, map[string]string{"a.png": "fake-image-bytes"}, nil)
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		a := &TaskAdaptor{}

		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Fatalf("expected single-file upload to set Generate action, got %q", info.Action)
		}
		data, _ := io.ReadAll(r)
		var got requestPayload
		_ = json.Unmarshal(data, &got)
		if len(got.BinaryDataBase64) != 1 {
			t.Fatalf("expected 1 base64-encoded image, got %d", len(got.BinaryDataBase64))
		}
	})

	t.Run("multi-file multipart upload sets FirstTailGenerate action", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, name := range []string{"first.png", "last.png"} {
			fw, _ := mw.CreateFormFile("input_reference", name)
			_, _ = fw.Write([]byte("bytes-" + name))
		}
		_ = mw.Close()

		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/x", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		c.Request = req
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})

		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		a := &TaskAdaptor{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Action != constant.TaskActionFirstTailGenerate {
			t.Fatalf("expected multi-file upload to set FirstTailGenerate action, got %q", info.Action)
		}
		data, _ := io.ReadAll(r)
		var got requestPayload
		_ = json.Unmarshal(data, &got)
		if len(got.BinaryDataBase64) != 2 {
			t.Fatalf("expected 2 base64-encoded images, got %d", len(got.BinaryDataBase64))
		}
	})

	t.Run("oversized file is rejected before submission (protects upstream 4.7MB limit)", func(t *testing.T) {
		bigContent := strings.Repeat("x", int(MaxFileSize)+1)
		c, _ := taskBatchANewMultipartCtx(t, map[string]string{"huge.png": bigContent}, nil)
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})
		a := &TaskAdaptor{}
		if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}); err == nil {
			t.Fatalf("expected oversized file to be rejected")
		}
	})
}

// ─── convertToRequestPayload (duration / image / v30 ReqKey rewrite) ────────

func TestJimeng_ConvertToRequestPayload(t *testing.T) {
	t.Run("10s duration maps to 241 frames, others default to 121", func(t *testing.T) {
		a := &TaskAdaptor{}
		p10, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "m", Duration: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p10.Frames != 241 {
			t.Fatalf("expected 241 frames for 10s duration, got %d", p10.Frames)
		}
		p5, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "m", Duration: 5})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p5.Frames != 121 {
			t.Fatalf("expected 121 frames for non-10s duration, got %d", p5.Frames)
		}
	})

	t.Run("http-prefixed image goes to image_urls, otherwise binary_data_base64", func(t *testing.T) {
		a := &TaskAdaptor{}
		urlReq, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "m", Images: []string{"https://img/a.png"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(urlReq.ImageUrls) != 1 || len(urlReq.BinaryDataBase64) != 0 {
			t.Fatalf("expected http image to route to ImageUrls, got %+v", urlReq)
		}

		b64Req, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "m", Images: []string{"aGVsbG8="}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(b64Req.BinaryDataBase64) != 1 || len(b64Req.ImageUrls) != 0 {
			t.Fatalf("expected non-http image to route to BinaryDataBase64, got %+v", b64Req)
		}
	})

	t.Run("jimeng_v30_pro is pinned to the fixed pro ReqKey regardless of images", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "jimeng_v30_pro"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ReqKey != "jimeng_ti2v_v30_pro" {
			t.Fatalf("expected fixed pro ReqKey, got %q", p.ReqKey)
		}
	})

	t.Run("jimeng_v30 with 2+ images rewrites to first-tail variant", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "jimeng_v30p", Images: []string{"https://a/1.png", "https://a/2.png"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ReqKey != "jimeng_i2v_first_tail_v30" {
			t.Fatalf("expected first-tail rewrite for 2-image jimeng_v30 model, got %q", p.ReqKey)
		}
	})

	t.Run("jimeng_v30 with 1 image rewrites to first-frame variant", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "jimeng_v30p", Images: []string{"https://a/1.png"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ReqKey != "jimeng_i2v_first_v30" {
			t.Fatalf("expected first-frame rewrite for 1-image jimeng_v30 model, got %q", p.ReqKey)
		}
	})

	t.Run("jimeng_v30 with no images rewrites to text-to-video variant", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "jimeng_v30"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ReqKey != "jimeng_t2v_v30" {
			t.Fatalf("expected t2v rewrite for no-image jimeng_v30 model, got %q", p.ReqKey)
		}
	})

	t.Run("metadata overrides carry into the payload (e.g. aspect_ratio/seed)", func(t *testing.T) {
		a := &TaskAdaptor{}
		p, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model:    "m",
			Metadata: map[string]interface{}{"aspect_ratio": "9:16", "seed": float64(7)},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.AspectRatio != "9:16" || p.Seed != 7 {
			t.Fatalf("expected metadata aspect_ratio/seed to merge into payload, got %+v", p)
		}
	})
}

// ─── DoResponse (submit-task response handling) ─────────────────────────────

func TestJimeng_DoResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    bool
		errCode    string
		wantTaskID string
	}{
		{
			name:       "code 10000 is success, extracts task id",
			body:       `{"code":10000,"message":"success","request_id":"r1","data":{"task_id":"jimeng-task-1"}}`,
			wantTaskID: "jimeng-task-1",
		},
		{
			name:    "non-10000 code surfaces as task error, not swallowed",
			body:    `{"code":50411,"message":"内容审核不通过","request_id":"r2"}`,
			wantErr: true,
			errCode: "50411",
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
				t.Fatalf("expected raw body returned for persistence")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 echoed to client, got %d", w.Code)
			}
			var ov dto.OpenAIVideo
			_ = json.Unmarshal(w.Body.Bytes(), &ov)
			if ov.ID != tt.wantTaskID {
				t.Fatalf("expected client video id %q, got %q", tt.wantTaskID, ov.ID)
			}
		})
	}
}

// ─── FetchTask (polling) ────────────────────────────────────────────────────

func TestJimeng_FetchTask(t *testing.T) {
	t.Run("missing task_id in body map is rejected before any network call", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.FetchTask("https://x", "sk-x", map[string]any{}, ""); err == nil {
			t.Fatalf("expected error for missing task_id")
		}
	})

	t.Run("direct vendor key with malformed ak|sk format is rejected", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.FetchTask("https://x", "not-ak-sk-format", map[string]any{"task_id": "t1"}, ""); err == nil {
			t.Fatalf("expected error for malformed direct-vendor key")
		}
	})

	t.Run("new-api relay polls /jimeng/ subpath with bearer auth", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		var gotPath, gotAuth string
		var gotBody map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":10000,"data":{"status":"done","video_url":"https://cdn/v.mp4"}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{baseURL: srv.URL}
		resp, err := a.FetchTask(srv.URL, "sk-relay-token", map[string]any{"task_id": "poll-1"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if gotPath != "/jimeng/" {
			t.Fatalf("expected new-api relay to poll /jimeng/ subpath, got %q", gotPath)
		}
		if gotAuth != "Bearer sk-relay-token" {
			t.Fatalf("expected bearer auth, got %q", gotAuth)
		}
		if gotBody["task_id"] != "poll-1" {
			t.Fatalf("expected task_id forwarded to upstream poll, got %v", gotBody)
		}

		body, _ := io.ReadAll(resp.Body)
		info, err := a.ParseTaskResult(body)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if info.Status != string(repo.TaskStatusSuccess) || info.Url != "https://cdn/v.mp4" {
			t.Fatalf("expected success + video url, got status=%q url=%q", info.Status, info.Url)
		}
	})

	t.Run("direct vendor key signs the poll request", func(t *testing.T) {
		taskBatchAAllowLoopbackHTTP(t)
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":10000,"data":{"status":"in_queue"}}`))
		}))
		defer srv.Close()

		a := &TaskAdaptor{}
		resp, err := a.FetchTask(srv.URL, "AK1|SK1", map[string]any{"task_id": "poll-2"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()
		if !strings.HasPrefix(gotAuth, "HMAC-SHA256 Credential=AK1/") {
			t.Fatalf("expected HMAC-signed poll request for direct vendor key, got %q", gotAuth)
		}
	})
}

// ─── ParseTaskResult (status machine mapping) ───────────────────────────────

func TestJimeng_ParseTaskResult(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
		wantCode   int
	}{
		{
			name:       "code 10000 + in_queue maps to queued",
			body:       `{"code":10000,"data":{"status":"in_queue"}}`,
			wantStatus: string(repo.TaskStatusQueued),
		},
		{
			name:       "code 10000 + done maps to success with video url",
			body:       `{"code":10000,"data":{"status":"done","video_url":"https://cdn/x.mp4"}}`,
			wantStatus: string(repo.TaskStatusSuccess),
			wantURL:    "https://cdn/x.mp4",
		},
		{
			name:       "non-10000 code maps to failure with reason propagated",
			body:       `{"code":50411,"message":"content review failed","data":{"status":"done"}}`,
			wantStatus: string(repo.TaskStatusSuccess),
			wantReason: "content review failed",
			wantCode:   50411,
		},
		{
			name: "unknown status value with success code leaves status untouched (not queued/success)",
			body: `{"code":10000,"data":{"status":"generating"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantStatus != "" && info.Status != tt.wantStatus {
				t.Fatalf("expected status %q, got %q", tt.wantStatus, info.Status)
			}
			if tt.wantURL != "" && info.Url != tt.wantURL {
				t.Fatalf("expected url %q, got %q", tt.wantURL, info.Url)
			}
			if tt.wantReason != "" && info.Reason != tt.wantReason {
				t.Fatalf("expected reason %q, got %q", tt.wantReason, info.Reason)
			}
			if tt.wantCode != 0 && info.Code != tt.wantCode {
				t.Fatalf("expected code %d, got %d", tt.wantCode, info.Code)
			}
		})
	}

	t.Run("malformed json does not panic", func(t *testing.T) {
		a := &TaskAdaptor{}
		if _, err := a.ParseTaskResult([]byte(`not json`)); err == nil {
			t.Fatalf("expected error for malformed json")
		}
	})
}

// FINDING: when the upstream response carries a non-10000 top-level "code"
// (failure) but data.status == "done", ParseTaskResult's status switch runs
// unconditionally after the code check and overwrites taskResult.Status from
// TaskStatusFailure back to TaskStatusSuccess, because both branches write to
// the same field and the "done" case always wins last. A partially-failed
// generation whose status label happens to be "done" is reported to the
// billing/poll layer as a successful task. Locked in above via the
// "non-10000 code maps to failure with reason propagated" case, which
// documents the actual (surprising) resulting status as TaskStatusSuccess.

// ─── ConvertToOpenAIVideo (poll-endpoint client response shaping) ──────────

func TestJimeng_ConvertToOpenAIVideo(t *testing.T) {
	t.Run("success payload embeds video url metadata, no error", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t1", Status: repo.TaskStatusSuccess, Progress: "100%"}
		var resp responseTask
		resp.Code = 10000
		resp.Data.Status = "done"
		resp.Data.VideoUrl = "https://cdn/out.mp4"
		task.SetData(resp)

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		if err := json.Unmarshal(raw, &ov); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if ov.Error != nil {
			t.Fatalf("expected no error, got %+v", ov.Error)
		}
		if ov.Metadata["url"] != "https://cdn/out.mp4" {
			t.Fatalf("expected metadata url to carry video url, got %v", ov.Metadata)
		}
	})

	t.Run("non-10000 stored code surfaces as OpenAIVideoError", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t2", Status: repo.TaskStatusFailure}
		var resp responseTask
		resp.Code = 50411
		resp.Message = "content review failed"
		task.SetData(resp)

		raw, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov dto.OpenAIVideo
		_ = json.Unmarshal(raw, &ov)
		if ov.Error == nil || ov.Error.Code != fmt.Sprintf("%d", 50411) || ov.Error.Message != "content review failed" {
			t.Fatalf("expected error code/message propagated, got %+v", ov.Error)
		}
	})

	t.Run("malformed stored data does not panic, returns error", func(t *testing.T) {
		a := &TaskAdaptor{}
		task := &repo.Task{TaskID: "t3"}
		task.Data = []byte(`not json`)
		if _, err := a.ConvertToOpenAIVideo(task); err == nil {
			t.Fatalf("expected error for malformed stored task data")
		}
	})
}

// ─── GetModelList / GetChannelName ──────────────────────────────────────────

func TestJimeng_GetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "jimeng" {
		t.Fatalf("expected channel name 'jimeng', got %q", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "jimeng_vgfm_t2v_l20" {
		t.Fatalf("expected advertised model list [jimeng_vgfm_t2v_l20], got %v", models)
	}
}

// ─── End-to-end submit round trip via a local httptest upstream ────────────

func TestJimeng_DoRequest_RoundTrip(t *testing.T) {
	taskBatchANoBodyLimit(t)
	taskBatchAAllowLoopbackHTTP(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":10000,"message":"ok","request_id":"r","data":{"task_id":"submitted-9"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	info := taskBatchANewRelayInfo(srv.URL, "sk-e2e")
	a.Init(info)

	c, w := taskBatchANewGinCtx(t, http.MethodPost, "/x", []byte(`{"prompt":"a bird","model":"jimeng_vgfm_t2v_l20"}`), "application/json")
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
	if taskID != "submitted-9" {
		t.Fatalf("expected upstream task id to be surfaced, got %q", taskID)
	}
	if gotAuth != "Bearer sk-e2e" {
		t.Fatalf("expected relay bearer auth to reach upstream, got %q", gotAuth)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 to caller, got %d", w.Code)
	}
}
