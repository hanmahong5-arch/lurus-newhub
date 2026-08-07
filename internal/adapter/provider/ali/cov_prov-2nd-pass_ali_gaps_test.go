package ali

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	envconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func init() {
	// helper.StreamScannerHandler builds a time.NewTicker(StreamingTimeout*time.Second);
	// this must be positive or the ticker construction panics. Only populated by
	// full-server bootstrap (common.InitEnv), which package tests don't invoke.
	if envconstant.StreamingTimeout <= 0 {
		envconstant.StreamingTimeout = 60
	}
}

func prov_2nd_pass_ali_sseBody(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

// prov_2nd_pass_allowPrivateIP flips the SSRF fetch-setting guard so that
// httptest.Server (bound to 127.0.0.1) can be reached from this package's
// DoRequest / GetImageFromUrl tests, mirroring the pattern already used in
// the volcengine cov_ tests. Restored via t.Cleanup.
func prov_2nd_pass_allowPrivateIP(t *testing.T) {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow, prevPorts := fs.AllowPrivateIp, fs.AllowedPorts
	fs.AllowPrivateIp = true
	fs.AllowedPorts = nil // empty = all ports allowed; httptest.Server binds an ephemeral port
	prevMaxMB := envconstant.MaxFileDownloadMB
	envconstant.MaxFileDownloadMB = 10
	t.Cleanup(func() {
		s := system_setting.GetFetchSetting()
		s.AllowPrivateIp = prevAllow
		s.AllowedPorts = prevPorts
		envconstant.MaxFileDownloadMB = prevMaxMB
	})
}

// ---------------------------------------------------------------------------
// Adaptor.DoRequest: end-to-end REST call construction (URL + auth header)
// against a real upstream server. A wrong wiring here means the whole ali
// adaptor silently fails to reach dashscope in production.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_DoRequest_HitsUpstreamWithAuth(t *testing.T) {
	prov_2nd_pass_allowPrivateIP(t)

	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[]}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "sk-ali-key"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"qwen-turbo"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		t.Fatalf("resp type = %T, want *http.Response", resp)
	}
	defer httpResp.Body.Close()

	if gotPath != "/compatible-mode/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /compatible-mode/v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-ali-key" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-ali-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertImageRequest: dispatch across generations/edits x sync/async
// x old-wan/wan/other x form/json. Wrong dispatch here routes billed image
// traffic to the wrong upstream shape (silent 400 or worse, wrong billing).
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("generations sync model marks adaptor sync and returns wrapped-message shape", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations, OriginModelName: "qwen-image"}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "qwen-image", Prompt: "a cat"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !a.IsSyncImageModel {
			t.Error("expected IsSyncImageModel=true for sync image generation model")
		}
		req, ok := got.(*AliImageRequest)
		if !ok {
			t.Fatalf("result type = %T, want *AliImageRequest", got)
		}
		if _, ok := req.Input.(AliImageInput); !ok {
			t.Fatalf("Input type = %T, want AliImageInput (sync message-wrapped shape)", req.Input)
		}
	})

	t.Run("generations async model does not flip adaptor to sync", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations, OriginModelName: "wanx-v1"}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "wanx-v1", Prompt: "a dog"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.IsSyncImageModel {
			t.Error("expected IsSyncImageModel to stay false for an async image generation model")
		}
	})

	t.Run("edits old-wan model routes to wanx-specific edit builder", func(t *testing.T) {
		a := &Adaptor{}
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "recolor"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, OriginModelName: "wanx-edit"}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "wanx-edit", Prompt: "recolor"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		req := got.(*AliImageRequest)
		if _, ok := req.Input.(WanImageInput); !ok {
			t.Fatalf("Input type = %T, want WanImageInput (old-wan edit path)", req.Input)
		}
	})

	t.Run("edits sync-wan model (wan2.6) flips IsSyncImageModel back to false", func(t *testing.T) {
		a := &Adaptor{IsSyncImageModel: true}
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, OriginModelName: "wan2.6-edit"}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "wan2.6-edit", Prompt: "x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.IsSyncImageModel {
			t.Error("wan2.6 edit model must clear IsSyncImageModel (it is async despite being a 'sync image model' family)")
		}
	})

	t.Run("edits sync non-wan model (z-image) keeps IsSyncImageModel true", func(t *testing.T) {
		a := &Adaptor{}
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "image", prov_ali_repl_vertex_pngBytes())
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, OriginModelName: "z-image-edit"}
		_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "z-image-edit", Prompt: "x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !a.IsSyncImageModel {
			t.Error("non-wan sync image family edit should set IsSyncImageModel=true")
		}
	})

	t.Run("edits with JSON body (non-multipart) routes to async/sync request builder, not the form parser", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, OriginModelName: "qwen-image-edit"}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "qwen-image-edit", Prompt: "edit via url"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		req := got.(*AliImageRequest)
		if _, ok := req.Input.(AliImageInput); !ok {
			t.Fatalf("Input type = %T, want AliImageInput (JSON body path)", req.Input)
		}
	})

	t.Run("unsupported relay mode returns an explicit error, not a nil result", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings}
		got, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "x"})
		if err == nil {
			t.Fatal("expected error for unsupported relay mode, got nil")
		}
		if got != nil {
			t.Errorf("result should be nil on error, got %+v", got)
		}
		if !strings.Contains(err.Error(), "unsupported image relay mode") {
			t.Errorf("error = %v, want 'unsupported image relay mode'", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest: thin wiring to the package-level converter;
// must forward the query/documents/model untouched.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_Adaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	req := dto.RerankRequest{Model: "gte-rerank-v2", Query: "hello", Documents: []any{"a", "b", "c"}, TopN: 5}

	got, err := a.ConvertRerankRequest(c, constant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq, ok := got.(*AliRerankRequest)
	if !ok {
		t.Fatalf("result type = %T, want *AliRerankRequest", got)
	}
	if aliReq.Model != "gte-rerank-v2" || aliReq.Input.Query != "hello" || len(aliReq.Input.Documents) != 3 {
		t.Errorf("AliRerankRequest = %+v, want model/query/documents forwarded from input", aliReq)
	}
	if aliReq.Parameters.TopN == nil || *aliReq.Parameters.TopN != 5 {
		t.Errorf("TopN = %v, want 5", aliReq.Parameters.TopN)
	}
}

// ---------------------------------------------------------------------------
// PromptExtendValue: nil-vs-set pointer branch not exercised by the existing
// oaiImage2AliImageRequest table (those always set PromptExtend explicitly
// when checking the z-image surcharge). A z-image model whose caller never
// set prompt_extend must NOT get billed the 2x surcharge.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_PromptExtendValue_UnsetPointerDefaultsFalse(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{Model: "z-image-turbo", Prompt: "x"} // Extra is nil -> Parameters.PromptExtend stays nil
	_, err := oaiImage2AliImageRequest(info, req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, billed := info.PriceData.OtherRatios["prompt_extend"]; billed {
		t.Error("z-image model with unset prompt_extend must not be billed the 2x surcharge")
	}
}

// ---------------------------------------------------------------------------
// updateTask: single-shot GET against the ali task-status endpoint. Business-
// critical because asyncTaskWait polls this in a loop and a wrong Authorization
// header or a swallowed transport error would silently strand async image
// generations without ever completing (customer pays, never gets an image).
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_UpdateTask(t *testing.T) {
	prov_2nd_pass_allowPrivateIP(t)

	t.Run("success returns parsed task status and raw body", func(t *testing.T) {
		var gotPath, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"output":{"task_id":"t1","task_status":"SUCCEEDED"}}`))
		}))
		defer srv.Close()

		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "sk-poll"}}
		resp, err, body := updateTask(info, "t1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Output.TaskStatus != "SUCCEEDED" {
			t.Errorf("TaskStatus = %q, want SUCCEEDED", resp.Output.TaskStatus)
		}
		if len(body) == 0 {
			t.Error("expected non-empty raw response body")
		}
		if gotPath != "/api/v1/tasks/t1" {
			t.Errorf("polled path = %q, want /api/v1/tasks/t1", gotPath)
		}
		if gotAuth != "Bearer sk-poll" {
			t.Errorf("polled Authorization = %q, want Bearer sk-poll", gotAuth)
		}
	})

	t.Run("malformed JSON body returns an error, not a zero-value silently accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()

		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "k"}}
		_, err, _ := updateTask(info, "t2")
		if err == nil {
			t.Fatal("expected an error for malformed JSON task response, got nil")
		}
	})

	t.Run("transport failure (unreachable host) surfaces as an error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:1", ApiKey: "k"}}
		_, err, _ := updateTask(info, "t3")
		if err == nil {
			t.Fatal("expected a transport error for an unreachable host, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// asyncTaskWait: the polling loop backing async image generation. Only
// terminal-on-first-poll cases are exercised here to keep runtime bounded
// (waitSeconds is a hardcoded 10s per retry, not injectable) — the initial
// 5s sleep is unavoidable but each sub-test still finishes well under 30s.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_AsyncTaskWait_TerminatesOnFirstTerminalStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the mandatory 5s poll delay in -short mode")
	}

	tests := []struct {
		name       string
		taskStatus string
	}{
		{"empty status short-circuits immediately", ""},
		{"SUCCEEDED is terminal on first poll", "SUCCEEDED"},
		{"FAILED is terminal on first poll", "FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"output":{"task_id":"tt","task_status":"` + tt.taskStatus + `","message":"m"}}`))
			}))
			defer srv.Close()
			prov_2nd_pass_allowPrivateIP(t)

			c, _ := prov_ali_repl_vertex_newGinContext(t)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "k"}}

			start := time.Now()
			resp, body, err := asyncTaskWait(c, info, "tt")
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if elapsed < 5*time.Second {
				t.Errorf("elapsed = %v, want >= 5s (asyncTaskWait always sleeps once before its first poll)", elapsed)
			}
			if elapsed > 12*time.Second {
				t.Errorf("elapsed = %v, want a single-poll terminal return well under the 10s retry interval", elapsed)
			}
			if tt.taskStatus == "" {
				if resp.Output.TaskStatus != "" {
					t.Errorf("TaskStatus = %q, want empty (the empty-status short-circuit returns a fresh zero-value response)", resp.Output.TaskStatus)
				}
				if body == nil {
					t.Error("expected the raw polled body to be captured even on the empty-status branch")
				}
			} else if resp.Output.TaskStatus != tt.taskStatus {
				t.Errorf("TaskStatus = %q, want %q", resp.Output.TaskStatus, tt.taskStatus)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ChoicesToOpenAIImageDate / ResultToOpenAIImageDate: the b64_json response
// format branch (GetImageFromUrl round-trip) is the uncovered seam — getting
// it wrong means customers who ask for base64 images get an empty payload or
// a raw upstream URL leaks through unconverted.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_ChoicesToOpenAIImageDate_B64JsonFetchesAndSkipsOnFailure(t *testing.T) {
	prov_2nd_pass_allowPrivateIP(t)
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(prov_ali_repl_vertex_pngBytes())
	}))
	defer imgSrv.Close()

	t.Run("http image url with b64_json format is downloaded and base64-encoded", func(t *testing.T) {
		out := &AliOutput{Choices: []struct {
			FinishReason string `json:"finish_reason,omitempty"`
			Message      struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			} `json:"message,omitempty"`
		}{
			{Message: struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			}{Content: []AliMediaContent{{Image: imgSrv.URL + "/img.png"}}}},
		}}
		got := out.ChoicesToOpenAIImageDate(c, "b64_json")
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].Url != imgSrv.URL+"/img.png" {
			t.Errorf("Url = %q, want the original image URL preserved alongside the b64 payload", got[0].Url)
		}
		if got[0].B64Json == "" {
			t.Error("B64Json is empty, want the downloaded image base64-encoded")
		}
	})

	t.Run("unreachable http image url is skipped, not fatal", func(t *testing.T) {
		out := &AliOutput{Choices: []struct {
			FinishReason string `json:"finish_reason,omitempty"`
			Message      struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			} `json:"message,omitempty"`
		}{
			{Message: struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			}{Content: []AliMediaContent{{Image: "http://127.0.0.1:1/dead.png"}}}},
		}}
		got := out.ChoicesToOpenAIImageDate(c, "b64_json")
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1 (empty ImageData entry for the choice, download failure just skips the image field)", len(got))
		}
		if got[0].Url != "" || got[0].B64Json != "" {
			t.Errorf("got[0] = %+v, want both Url and B64Json empty when download fails", got[0])
		}
	})

	t.Run("text-only content maps to RevisedPrompt", func(t *testing.T) {
		out := &AliOutput{Choices: []struct {
			FinishReason string `json:"finish_reason,omitempty"`
			Message      struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			} `json:"message,omitempty"`
		}{
			{Message: struct {
				Role             string            `json:"role,omitempty"`
				Content          []AliMediaContent `json:"content,omitempty"`
				ReasoningContent string            `json:"reasoning_content,omitempty"`
			}{Content: []AliMediaContent{{Text: "a revised prompt"}}}},
		}}
		got := out.ChoicesToOpenAIImageDate(c, "url")
		if len(got) != 1 || got[0].RevisedPrompt != "a revised prompt" {
			t.Errorf("got = %+v, want RevisedPrompt = %q", got, "a revised prompt")
		}
	})
}

func TestProv2ndPass_Ali_ResultToOpenAIImageDate_B64JsonAndPassthrough(t *testing.T) {
	prov_2nd_pass_allowPrivateIP(t)
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(prov_ali_repl_vertex_pngBytes())
	}))
	defer imgSrv.Close()

	t.Run("b64_json format downloads the result URL", func(t *testing.T) {
		out := &AliOutput{Results: []TaskResult{{Url: imgSrv.URL + "/r.png"}}}
		got := out.ResultToOpenAIImageDate(c, "b64_json")
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].B64Json == "" {
			t.Error("B64Json is empty, want the downloaded result base64-encoded")
		}
	})

	t.Run("download failure for one result skips it but does not drop the whole response", func(t *testing.T) {
		out := &AliOutput{Results: []TaskResult{
			{Url: "http://127.0.0.1:1/dead.png"},
			{Url: imgSrv.URL + "/ok.png"},
		}}
		got := out.ResultToOpenAIImageDate(c, "b64_json")
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1 (the failing result must be skipped, not zero-valued or fatal)", len(got))
		}
		if got[0].Url != imgSrv.URL+"/ok.png" {
			t.Errorf("surviving result Url = %q, want the reachable one", got[0].Url)
		}
	})

	t.Run("non-b64_json format uses the pre-supplied B64Image field verbatim, no network call", func(t *testing.T) {
		out := &AliOutput{Results: []TaskResult{{Url: "https://cdn/x.png", B64Image: "already-encoded"}}}
		got := out.ResultToOpenAIImageDate(c, "url")
		if len(got) != 1 || got[0].B64Json != "already-encoded" {
			t.Errorf("got = %+v, want B64Json = %q passed through unchanged", got, "already-encoded")
		}
		if got[0].RevisedPrompt != "" {
			t.Errorf("RevisedPrompt = %q, want empty (ali results never carry a revised prompt)", got[0].RevisedPrompt)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatch: remaining branches (claude non-stream, claude
// stream, images relay-mode, default openai fallthrough). Wrong wiring here
// silently routes a whole request class through the wrong usage-extraction
// path, which is a direct billing correctness bug.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Ali_DoResponse_ClaudeNonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi from claude route"}],"stop_reason":"end_turn","model":"qwen-max","usage":{"input_tokens":4,"output_tokens":2}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "qwen-max"},
	}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok || u.PromptTokens != 4 || u.CompletionTokens != 2 {
		t.Errorf("usage = %+v (ok=%v), want PromptTokens=4 CompletionTokens=2 from claude-format response", usage, ok)
	}
	if !strings.Contains(w.Body.String(), "hi from claude route") {
		t.Errorf("expected claude-format response body forwarded, got %s", w.Body.String())
	}
}

func TestProv2ndPass_Ali_DoResponse_ClaudeStream(t *testing.T) {
	a := &Adaptor{}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := prov_2nd_pass_ali_sseBody(
		`{"type":"message_start","message":{"id":"m1","model":"qwen-max","usage":{"input_tokens":2,"output_tokens":0}}}`,
		`{"type":"message_stop"}`,
	)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "qwen-max"},
	}

	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if w.Body.Len() == 0 {
		t.Error("expected the claude-format stream route to forward at least one chunk")
	}
}

func TestProv2ndPass_Ali_DoResponse_ImagesGenerations_DispatchesToImageHandler(t *testing.T) {
	a := &Adaptor{IsSyncImageModel: true}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	c.Set("response_format", "url")
	body := `{"output":{"results":[{"url":"https://img/routed.png"}]}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations, StartTime: prov_ali_repl_vertex_fixedStartTime}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage from the images-generations dispatch")
	}
	if !strings.Contains(w.Body.String(), "routed.png") {
		t.Errorf("expected image-generation response routed to aliImageHandler, got %s", w.Body.String())
	}
}

func TestProv2ndPass_Ali_DoResponse_ImagesEdits_DispatchesToImageHandler(t *testing.T) {
	a := &Adaptor{IsSyncImageModel: true}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	c.Set("response_format", "url")
	body := `{"output":{"results":[{"url":"https://img/edited.png"}]}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, StartTime: prov_ali_repl_vertex_fixedStartTime}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage from the images-edits dispatch")
	}
	if !strings.Contains(w.Body.String(), "edited.png") {
		t.Errorf("expected image-edits response routed to aliImageHandler, got %s", w.Body.String())
	}
}

func TestProv2ndPass_Ali_DoResponse_Default_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `{"id":"chatcmpl-1","object":"chat.completion","model":"qwen-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":6,"completion_tokens":3,"total_tokens":9}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok || u.PromptTokens != 6 || u.CompletionTokens != 3 || u.TotalTokens != 9 {
		t.Errorf("usage = %+v (ok=%v), want PromptTokens=6 CompletionTokens=3 TotalTokens=9 (billed quantity from openai fallback path)", usage, ok)
	}
	if !strings.Contains(w.Body.String(), `"hi"`) {
		t.Errorf("expected the openai-format body forwarded, got %s", w.Body.String())
	}
}
