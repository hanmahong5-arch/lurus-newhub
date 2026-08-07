package vertex

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
)

// ---------------------------------------------------------------------------
// ConvertClaudeRequest: model-id remapping + anthropic_version stamping.
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("known public model id remapped to vertex-internal id via context", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}}
		req := &dto.ClaudeRequest{Model: "claude-3-opus-20240229", MaxTokens: 1024}
		result, err := a.ConvertClaudeRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		vReq, ok := result.(*VertexAIClaudeRequest)
		if !ok {
			t.Fatalf("result type = %T, want *VertexAIClaudeRequest", result)
		}
		if vReq.AnthropicVersion != anthropicVersion {
			t.Errorf("AnthropicVersion = %q, want %q", vReq.AnthropicVersion, anthropicVersion)
		}
		if vReq.MaxTokens != 1024 {
			t.Errorf("MaxTokens = %d, want 1024 (fields copied through)", vReq.MaxTokens)
		}
		if got := c.GetString("request_model"); got != "claude-3-opus@20240229" {
			t.Errorf("request_model context = %q, want the mapped vertex model id", got)
		}
	})

	t.Run("unmapped model id falls back to the request's own model string", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-unknown-future-model"}}
		req := &dto.ClaudeRequest{Model: "claude-unknown-future-model"}
		_, err := a.ConvertClaudeRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := c.GetString("request_model"); got != "claude-unknown-future-model" {
			t.Errorf("request_model context = %q, want fallback to request.Model", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: imagen prompt extraction / size+aspectRatio override,
// plus mode dispatch (claude/gemini/llama/unsupported).
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil)
	if err == nil {
		t.Fatal("expected error for nil request, got nil")
	}
}

func TestAdaptor_ConvertOpenAIRequest_Imagen(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}

	t.Run("prompt extracted from the last user message", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-001"}}
		req := &dto.GeneralOpenAIRequest{
			Model:    "imagen-3.0-generate-001",
			Messages: []dto.Message{{Role: "user", Content: "a mountain at dawn"}},
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected a non-nil converted image request")
		}
		if got := c.GetString("request_model"); got != "imagen-3.0-generate-001" {
			t.Errorf("request_model context = %q, want imagen-3.0-generate-001", got)
		}
	})

	t.Run("no prompt anywhere errors instead of sending an empty generation", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-001"}}
		req := &dto.GeneralOpenAIRequest{Model: "imagen-3.0-generate-001"}
		_, err := a.ConvertOpenAIRequest(c, info, req)
		if err == nil {
			t.Fatal("expected error for missing prompt, got nil")
		}
	})

	t.Run("extra_body aspectRatio overrides default 1:1 size", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-001"}}
		req := &dto.GeneralOpenAIRequest{
			Model:     "imagen-3.0-generate-001",
			Prompt:    "a river valley",
			ExtraBody: json.RawMessage(`{"aspectRatio":"16:9"}`),
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// gemini.Adaptor.ConvertImageRequest maps size (here the overridden
		// aspectRatio string "16:9") straight through into Parameters.AspectRatio.
		payload, ok := result.(dto.GeminiImageRequest)
		if !ok {
			t.Fatalf("result type = %T, want dto.GeminiImageRequest", result)
		}
		if payload.Parameters.AspectRatio != "16:9" {
			t.Errorf("Parameters.AspectRatio = %v, want 16:9 from extra_body override", payload.Parameters.AspectRatio)
		}
	})

	t.Run("extra_body parameters.aspectRatio nested override also applies", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-001"}}
		req := &dto.GeneralOpenAIRequest{
			Model:     "imagen-3.0-generate-001",
			Prompt:    "a river valley",
			ExtraBody: json.RawMessage(`{"parameters":{"aspectRatio":"9:16"}}`),
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload, ok := result.(dto.GeminiImageRequest)
		if !ok {
			t.Fatalf("result type = %T, want dto.GeminiImageRequest", result)
		}
		if payload.Parameters.AspectRatio != "9:16" {
			t.Errorf("Parameters.AspectRatio = %v, want 9:16 from nested parameters override", payload.Parameters.AspectRatio)
		}
	})

	t.Run("extra_body n overrides default single-image count", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-001"}}
		req := &dto.GeneralOpenAIRequest{
			Model:     "imagen-3.0-generate-001",
			Prompt:    "a river valley",
			ExtraBody: json.RawMessage(`{"n":3}`),
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		payload, ok := result.(dto.GeminiImageRequest)
		if !ok {
			t.Fatalf("result type = %T, want dto.GeminiImageRequest", result)
		}
		if payload.Parameters.SampleCount != 3 {
			t.Errorf("Parameters.SampleCount = %v, want 3 from extra_body.n override", payload.Parameters.SampleCount)
		}
	})
}

func TestAdaptor_ConvertOpenAIRequest_ModeDispatch(t *testing.T) {
	t.Run("claude mode maps model name into info.UpstreamModelName", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeClaude}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}}
		req := &dto.GeneralOpenAIRequest{
			Model:    "claude-3-opus-20240229",
			Messages: []dto.Message{{Role: "user", Content: "hello"}},
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.(*VertexAIClaudeRequest); !ok {
			t.Fatalf("result type = %T, want *VertexAIClaudeRequest", result)
		}
		if info.UpstreamModelName != "claude-3-opus-20240229" {
			t.Errorf("UpstreamModelName = %q, want set from the converted claude request's model", info.UpstreamModelName)
		}
		if got := c.GetString("request_model"); got != "claude-3-opus-20240229" {
			t.Errorf("request_model context = %q, want claude-3-opus-20240229", got)
		}
	})

	t.Run("gemini mode delegates to gemini.CovertOpenAI2Gemini", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeGemini}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"}}
		req := &dto.GeneralOpenAIRequest{
			Model:    "gemini-1.5-pro",
			Messages: []dto.Message{{Role: "user", Content: "hello"}},
		}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.(*dto.GeminiChatRequest); !ok {
			t.Fatalf("result type = %T, want *dto.GeminiChatRequest", result)
		}
		if got := c.GetString("request_model"); got != "gemini-1.5-pro" {
			t.Errorf("request_model context = %q, want gemini-1.5-pro", got)
		}
	})

	t.Run("llama mode passes the request through unmodified", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeLlama}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		req := &dto.GeneralOpenAIRequest{Model: "meta/llama3-405b-instruct-maas"}
		result, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != req {
			t.Errorf("llama mode should return the exact same pointer, got %+v", result)
		}
	})

	t.Run("unsupported request mode errors", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: "x"})
		if err == nil {
			t.Fatal("expected error for unsupported request mode, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest / ConvertGeminiRequest: thin wrappers over the gemini
// adaptor -- verify the wiring, not gemini's internal logic.
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest_RejectsNonImagenModel(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"}}
	_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Prompt: "x"})
	if err == nil {
		t.Fatal("expected error: gemini-1.5-pro is not an imagen model, image generation should be rejected")
	}
}

func TestAdaptor_ConvertGeminiRequest_StripsFunctionResponseIDWhenEnabled(t *testing.T) {
	prov_ali_repl_vertex_withRemoveFunctionResponseID(t, true)
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"call-1"`)}},
			}},
		},
	}
	result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeminiChatRequest", result)
	}
	if got.Contents[0].Parts[0].FunctionResponse.ID != nil {
		t.Errorf("FunctionResponse.ID = %s, want stripped when RemoveFunctionResponseIdEnabled=true", got.Contents[0].Parts[0].FunctionResponse.ID)
	}
}

func TestAdaptor_ConvertGeminiRequest_PreservesFunctionResponseIDWhenDisabled(t *testing.T) {
	prov_ali_repl_vertex_withRemoveFunctionResponseID(t, false)
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"call-1"`)}},
			}},
		},
	}
	result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := result.(*dto.GeminiChatRequest)
	if string(got.Contents[0].Parts[0].FunctionResponse.ID) != `"call-1"` {
		t.Errorf("FunctionResponse.ID = %s, want preserved when the setting is disabled", got.Contents[0].Parts[0].FunctionResponse.ID)
	}
}

// prov_ali_repl_vertex_withRemoveFunctionResponseID toggles the global gemini
// RemoveFunctionResponseIdEnabled setting for the duration of the test.
func prov_ali_repl_vertex_withRemoveFunctionResponseID(t *testing.T, enabled bool) {
	t.Helper()
	s := model_setting.GetGeminiSettings()
	prev := s.RemoveFunctionResponseIdEnabled
	s.RemoveFunctionResponseIdEnabled = enabled
	t.Cleanup(func() { model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = prev })
}

// ---------------------------------------------------------------------------
// Not-implemented / stub converters
// ---------------------------------------------------------------------------

func TestAdaptor_StubConverters(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	if _, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest: expected not-implemented error, got nil")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest: expected not-implemented error, got nil")
	}

	// FINDING: ConvertRerankRequest silently returns (nil, nil) instead of a
	// "not implemented" error like every sibling stub above. A caller that
	// forwards a rerank request to the vertex adaptor gets no error and a nil
	// payload, which -- unless every call site special-cases nil -- risks
	// marshaling into an empty/garbage upstream request body instead of
	// failing fast. Locking in the current (surprising) behavior below.
	result, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{Model: "m", Query: "q"})
	if err != nil {
		t.Fatalf("ConvertRerankRequest currently never errors; got %v", err)
	}
	if result != nil {
		t.Errorf("ConvertRerankRequest result = %v, want nil (current, inconsistent-with-siblings behavior)", result)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("GetModelList returned empty list")
	}
	found := false
	for _, m := range models {
		if m == "meta/llama3-405b-instruct-maas" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetModelList() = %v, want it to include the vertex-native llama model", models)
	}
	if got := a.GetChannelName(); got != "vertex-ai" {
		t.Errorf("GetChannelName() = %q, want vertex-ai", got)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: dispatch wiring across request modes. Deep protocol behavior
// belongs to claude/gemini/openai packages; here we verify the correct
// handler is reached and that malformed upstream bodies produce an error
// rather than a panic (3 nil-panics were found in this class of code in a
// prior audit round).
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_MalformedBodyNoPanic(t *testing.T) {
	tests := []struct {
		name      string
		mode      int
		relayMode int
		modelName string
		stream    bool
	}{
		{"llama non-stream", RequestModeLlama, relayconstant.RelayModeChatCompletions, "meta/llama3-405b-instruct-maas", false},
		{"gemini chat non-stream", RequestModeGemini, relayconstant.RelayModeChatCompletions, "gemini-1.5-pro", false},
		{"gemini imagen non-stream", RequestModeGemini, relayconstant.RelayModeChatCompletions, "imagen-3.0-generate-001", false},
		{"claude non-stream", RequestModeClaude, relayconstant.RelayModeChatCompletions, "claude-3-opus@20240229", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{RequestMode: tt.mode}
			c, _ := prov_ali_repl_vertex_newGinContext(t)
			resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser("not valid json"), Header: http.Header{}}
			info := &relaycommon.RelayInfo{
				IsStream:  tt.stream,
				RelayMode: tt.relayMode,
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.modelName,
				},
			}
			_, apiErr := a.DoResponse(c, resp, info)
			if apiErr == nil {
				t.Errorf("expected an error for malformed upstream body in mode %q, got nil", tt.name)
			}
		})
	}
}
