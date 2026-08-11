package ali

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// ---------------------------------------------------------------------------
// oaiImage2AliImageRequest: OpenAI-shaped image request -> ali request.
// This is where sync vs async model families diverge in payload shape; a
// mixed-up shape means the upstream silently 400s or (worse) accepts a
// malformed prompt and bills for a no-op.
// ---------------------------------------------------------------------------

func TestOaiImage2AliImageRequest(t *testing.T) {
	t.Run("sync model with no extra wraps prompt as message content", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{Model: "qwen-image", Prompt: "a red fox"}
		got, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input, ok := got.Input.(AliImageInput)
		if !ok {
			t.Fatalf("Input type = %T, want AliImageInput", got.Input)
		}
		if len(input.Messages) != 1 || input.Messages[0].Role != "user" {
			t.Fatalf("Messages = %+v, want single user message", input.Messages)
		}
		content, ok := input.Messages[0].Content.([]AliMediaContent)
		if !ok || len(content) != 1 || content[0].Text != "a red fox" {
			t.Errorf("Messages[0].Content = %+v, want prompt text preserved", input.Messages[0].Content)
		}
	})

	t.Run("async model with no extra uses flat prompt field", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{Model: "wanx-v1", Prompt: "a blue whale"}
		got, err := oaiImage2AliImageRequest(info, req, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		input, ok := got.Input.(AliImageInput)
		if !ok {
			t.Fatalf("Input type = %T, want AliImageInput", got.Input)
		}
		if input.Prompt != "a blue whale" {
			t.Errorf("Prompt = %q, want %q", input.Prompt, "a blue whale")
		}
		if len(input.Messages) != 0 {
			t.Errorf("async model should not populate Messages, got %+v", input.Messages)
		}
	})

	t.Run("extra parameters field with malformed JSON is rejected, not silently dropped", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:  "qwen-image",
			Prompt: "x",
			Extra:  map[string]json.RawMessage{"parameters": json.RawMessage(`{not json`)},
		}
		_, err := oaiImage2AliImageRequest(info, req, true)
		if err == nil {
			t.Fatal("expected error for malformed extra.parameters, got nil")
		}
		if !strings.Contains(err.Error(), "invalid parameters field") {
			t.Errorf("error = %v, want wrapped 'invalid parameters field'", err)
		}
	})

	t.Run("extra parameters field overrides derived size/n/watermark", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:  "qwen-image",
			Prompt: "x",
			Size:   "1024x1024", // would be ignored since extra.parameters takes precedence
			Extra:  map[string]json.RawMessage{"parameters": json.RawMessage(`{"size":"512*512","n":3}`)},
		}
		got, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Parameters.Size != "512*512" || got.Parameters.N != 3 {
			t.Errorf("Parameters = %+v, want size=512*512 n=3 from extra", got.Parameters)
		}
		if ratio := info.PriceData.OtherRatios["n"]; ratio != 3 {
			t.Errorf("PriceData OtherRatios[n] = %v, want 3 (billing multiplier for n images)", ratio)
		}
	})

	t.Run("no extra falls back to openai size/n/watermark with x replaced by *", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		watermark := true
		req := dto.ImageRequest{
			Model:     "qwen-image",
			Prompt:    "x",
			Size:      "1024x768",
			N:         2,
			Watermark: &watermark,
			Extra:     map[string]json.RawMessage{}, // present but empty -> triggers fallback branch
		}
		got, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Parameters.Size != "1024*768" {
			t.Errorf("Size = %q, want %q (x replaced with * for ali API)", got.Parameters.Size, "1024*768")
		}
		if got.Parameters.N != 2 {
			t.Errorf("N = %d, want 2", got.Parameters.N)
		}
		if got.Parameters.Watermark == nil || !*got.Parameters.Watermark {
			t.Errorf("Watermark = %v, want true", got.Parameters.Watermark)
		}
	})

	t.Run("extra input field overrides derived input shape", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:  "qwen-image",
			Prompt: "ignored",
			Extra: map[string]json.RawMessage{
				"input": json.RawMessage(`{"prompt":"from extra input","negative_prompt":"no cats"}`),
			},
		}
		got, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Input is a bare `any` field, so unmarshalling extra.input into it
		// yields a generic map rather than a typed AliImageInput -- it is
		// still marshalled to the same upstream JSON shape on send.
		input, ok := got.Input.(map[string]interface{})
		if !ok {
			t.Fatalf("Input type = %T, want map[string]interface{}", got.Input)
		}
		if input["prompt"] != "from extra input" || input["negative_prompt"] != "no cats" {
			t.Errorf("Input = %+v, want extra.input to take precedence over request.Prompt", input)
		}
	})

	t.Run("z-image with prompt_extend enabled doubles the price ratio", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:  "z-image-turbo",
			Prompt: "x",
			Extra:  map[string]json.RawMessage{"parameters": json.RawMessage(`{"prompt_extend":true}`)},
		}
		_, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ratio := info.PriceData.OtherRatios["prompt_extend"]; ratio != 2 {
			t.Errorf("PriceData OtherRatios[prompt_extend] = %v, want 2 (2x billing for prompt_extend on z-image)", ratio)
		}
	})

	t.Run("non z-image model with prompt_extend does not add the surcharge ratio", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.ImageRequest{
			Model:  "qwen-image",
			Prompt: "x",
			Extra:  map[string]json.RawMessage{"parameters": json.RawMessage(`{"prompt_extend":true}`)},
		}
		_, err := oaiImage2AliImageRequest(info, req, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := info.PriceData.OtherRatios["prompt_extend"]; ok {
			t.Errorf("non z-image model should not get the prompt_extend surcharge, ratios=%v", info.PriceData.OtherRatios)
		}
	})
}

// ---------------------------------------------------------------------------
// getImageBase64sFromForm / oaiFormEdit2AliImageEdit: multipart image-edit
// requests. Missing/misnamed fields must error clearly, not silently send an
// empty image edit that upstream would bill for nothing useful.
// ---------------------------------------------------------------------------

func TestGetImageBase64sFromForm(t *testing.T) {
	t.Run("standard image field extracted as base64 data URL", func(t *testing.T) {
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "edit it"}, "image", prov_ali_repl_vertex_pngBytes())
		got, err := getImageBase64sFromForm(c, "image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if !strings.HasPrefix(got[0], "data:image/png;base64,") {
			t.Errorf("got[0] = %q, want data URL with image/png mime prefix", got[0])
		}
	})

	t.Run("missing image field is required error", func(t *testing.T) {
		c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "edit it"}, "", nil)
		_, err := getImageBase64sFromForm(c, "image")
		if err == nil {
			t.Fatal("expected 'image is required' error, got nil")
		}
		if err.Error() != "image is required" {
			t.Errorf("error = %q, want %q", err.Error(), "image is required")
		}
	})
}

func TestOaiFormEdit2AliImageEdit(t *testing.T) {
	c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "make it blue"}, "image", prov_ali_repl_vertex_pngBytes())
	info := &relaycommon.RelayInfo{}
	watermark := false
	req := dto.ImageRequest{Model: "qwen-image-edit", Prompt: "make it blue", Watermark: &watermark}

	got, err := oaiFormEdit2AliImageEdit(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "qwen-image-edit" {
		t.Errorf("Model = %q, want %q", got.Model, "qwen-image-edit")
	}
	input, ok := got.Input.(AliImageInput)
	if !ok {
		t.Fatalf("Input type = %T, want AliImageInput", got.Input)
	}
	if len(input.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(input.Messages))
	}
	content, ok := input.Messages[0].Content.([]AliMediaContent)
	if !ok {
		t.Fatalf("Content type = %T, want []AliMediaContent", input.Messages[0].Content)
	}
	if len(content) < 2 {
		t.Fatalf("Content len = %d, want >= 2 (image + prompt text)", len(content))
	}
	if content[0].Image == "" {
		t.Errorf("first content block should carry the uploaded image, got %+v", content[0])
	}
	lastText := content[len(content)-1].Text
	if lastText != "make it blue" {
		t.Errorf("last content block Text = %q, want prompt %q appended after images", lastText, "make it blue")
	}
	if got.Parameters.Watermark == nil || *got.Parameters.Watermark {
		t.Errorf("Watermark = %v, want false (propagated from request)", got.Parameters.Watermark)
	}
}

func TestOaiFormEdit2AliImageEdit_MissingImage(t *testing.T) {
	c := prov_ali_repl_vertex_multipartRequest(t, map[string]string{"prompt": "x"}, "", nil)
	info := &relaycommon.RelayInfo{}
	_, err := oaiFormEdit2AliImageEdit(c, info, dto.ImageRequest{Model: "m", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error when no image uploaded, got nil")
	}
	if !strings.Contains(err.Error(), "get image base64s from form failed") {
		t.Errorf("error = %v, want wrapped 'get image base64s from form failed'", err)
	}
}

// ---------------------------------------------------------------------------
// responseAli2OpenAIImage: response translation. Results take priority over
// Choices per the source; getting this backwards means edits (Choices) get
// silently dropped whenever Results also happens to be populated.
// ---------------------------------------------------------------------------

func TestResponseAli2OpenAIImage(t *testing.T) {
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{StartTime: prov_ali_repl_vertex_fixedStartTime}

	t.Run("results path used when present", func(t *testing.T) {
		resp := &AliResponse{Output: AliOutput{Results: []TaskResult{{Url: "https://img/1.png"}}}}
		got := responseAli2OpenAIImage(c, resp, []byte(`{}`), info, "url")
		if len(got.Data) != 1 || got.Data[0].Url != "https://img/1.png" {
			t.Errorf("Data = %+v, want single item with Results URL", got.Data)
		}
		if got.Created != prov_ali_repl_vertex_fixedStartTime.Unix() {
			t.Errorf("Created = %d, want %d", got.Created, prov_ali_repl_vertex_fixedStartTime.Unix())
		}
	})

	t.Run("choices path used when results is empty", func(t *testing.T) {
		resp := &AliResponse{Output: AliOutput{Choices: []struct {
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
			}{Content: []AliMediaContent{{Image: "b64imagedata"}}}},
		}}}
		got := responseAli2OpenAIImage(c, resp, []byte(`{}`), info, "url")
		if len(got.Data) != 1 || got.Data[0].B64Json != "b64imagedata" {
			t.Errorf("Data = %+v, want choices content surfaced as b64 image", got.Data)
		}
	})

	t.Run("neither results nor choices yields nil data, not a panic", func(t *testing.T) {
		resp := &AliResponse{}
		got := responseAli2OpenAIImage(c, resp, []byte(`{}`), info, "url")
		if got.Data != nil {
			t.Errorf("Data = %+v, want nil for empty upstream response", got.Data)
		}
	})
}

// ---------------------------------------------------------------------------
// aliImageHandler: sync-model path (no polling/sleep involved), covering the
// success + two distinct upstream-error shapes.
// ---------------------------------------------------------------------------

func TestAliImageHandler_SyncSuccess(t *testing.T) {
	c, w := prov_ali_repl_vertex_newGinContext(t)
	c.Set("response_format", "url")
	realInfo := &relaycommon.RelayInfo{StartTime: prov_ali_repl_vertex_fixedStartTime}

	body := `{"output":{"results":[{"url":"https://img/out.png"}]},"usage":{"image_count":2}}`
	resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser(body), Header: http.Header{}}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, realInfo)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("expected non-nil usage on success")
	}
	if ratio := realInfo.PriceData.OtherRatios["n"]; ratio != 2 {
		t.Errorf("PriceData OtherRatios[n] = %v, want 2 (from usage.image_count, business-critical billing multiplier)", ratio)
	}
	if w.Code != 200 {
		t.Errorf("recorder status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "https://img/out.png") {
		t.Errorf("response body = %q, want it to contain the upstream image URL", w.Body.String())
	}
}

func TestAliImageHandler_UpstreamMessageIsError(t *testing.T) {
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	realInfo := &relaycommon.RelayInfo{StartTime: prov_ali_repl_vertex_fixedStartTime}
	body := `{"message":"InvalidApiKey: bad key","code":"InvalidApiKey"}`
	resp := &http.Response{StatusCode: 401, Body: prov_ali_repl_vertex_nopCloser(body), Header: http.Header{}}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, realInfo)
	if apiErr == nil {
		t.Fatal("expected an error for a top-level ali error message, got nil")
	}
	if usage != nil {
		t.Errorf("usage should be nil on error path, got %+v", usage)
	}
	if !strings.Contains(apiErr.Error(), "InvalidApiKey") {
		t.Errorf("error = %v, want it to surface the upstream message", apiErr.Error())
	}
}

func TestAliImageHandler_MalformedJSONBody(t *testing.T) {
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	realInfo := &relaycommon.RelayInfo{StartTime: prov_ali_repl_vertex_fixedStartTime}
	resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser("not json at all"), Header: http.Header{}}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, realInfo)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body, got nil (would otherwise panic or bill on garbage)")
	}
	if usage != nil {
		t.Errorf("usage should be nil on parse-error path, got %+v", usage)
	}
	if apiErr.GetErrorCode() != "bad_response_body" {
		t.Errorf("ErrorCode = %q, want bad_response_body", apiErr.GetErrorCode())
	}
}
