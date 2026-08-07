package dto

import (
	"encoding/json"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------- audio.go ----------------

func TestAudioRequest_GetTokenCountMeta(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		wantType  types.TokenType
	}{
		{"gpt model uses tokenizer", "gpt-4o-mini-tts", types.TokenTypeTokenizer},
		{"non-gpt model uses text/number counting", "whisper-1", types.TokenTypeTextNumber},
		{"empty model uses text/number counting", "", types.TokenTypeTextNumber},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &AudioRequest{Model: c.model, Input: "hello audio"}
			meta := r.GetTokenCountMeta()
			if meta.TokenType != c.wantType {
				t.Errorf("TokenType = %q, want %q", meta.TokenType, c.wantType)
			}
			if meta.CombineText != "hello audio" {
				t.Errorf("CombineText = %q", meta.CombineText)
			}
		})
	}
}

func TestAudioRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &AudioRequest{StreamFormat: "sse"}
	if !r.IsStream(c) {
		t.Fatal("expected true for sse stream_format")
	}
	r.StreamFormat = "audio"
	if r.IsStream(c) {
		t.Fatal("expected false for non-sse stream_format")
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty modelName must not overwrite")
	}
	r.SetModelName("tts-1")
	if r.Model != "tts-1" {
		t.Fatalf("Model = %q, want tts-1", r.Model)
	}
}

// ---------------- embedding.go ----------------

func TestEmbeddingRequest_ParseInput(t *testing.T) {
	t.Run("nil input returns non-nil empty slice", func(t *testing.T) {
		r := &EmbeddingRequest{}
		got := r.ParseInput()
		if got == nil {
			t.Fatal("expected non-nil empty slice for nil Input (differs from GeneralOpenAIRequest.ParseInput)")
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("string input", func(t *testing.T) {
		r := &EmbeddingRequest{Input: "embed this"}
		got := r.ParseInput()
		if len(got) != 1 || got[0] != "embed this" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("array input filters non-strings", func(t *testing.T) {
		r := &EmbeddingRequest{Input: []any{"a", 2, "b", false}}
		got := r.ParseInput()
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("unsupported type returns nil slice", func(t *testing.T) {
		r := &EmbeddingRequest{Input: map[string]any{"x": 1}}
		got := r.ParseInput()
		if got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

func TestEmbeddingRequest_GetTokenCountMetaAndIsStream(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &EmbeddingRequest{Input: []any{"one", "two"}}
	if r.IsStream(c) {
		t.Fatal("embedding requests must never stream")
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "one\ntwo" {
		t.Fatalf("CombineText = %q, want one\\ntwo", meta.CombineText)
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty modelName must not overwrite")
	}
	r.SetModelName("text-embedding-3")
	if r.Model != "text-embedding-3" {
		t.Fatalf("Model = %q", r.Model)
	}
}

// ---------------- error.go ----------------

func TestGeneralErrorResponse_ToMessage(t *testing.T) {
	t.Run("error object with message", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`{"message":"obj msg","type":"invalid"}`)}
		if got := e.ToMessage(); got != "obj msg" {
			t.Fatalf("got %q, want obj msg", got)
		}
	})

	t.Run("error object with empty message falls back to top-level Message", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`{"type":"invalid"}`), Message: "fallback msg"}
		if got := e.ToMessage(); got != "fallback msg" {
			t.Fatalf("got %q, want fallback msg", got)
		}
	})

	t.Run("error as plain JSON string", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`"string error"`)}
		if got := e.ToMessage(); got != "string error" {
			t.Fatalf("got %q, want string error", got)
		}
	})

	t.Run("error as empty JSON string falls back to Msg field", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`""`), Msg: "msg field"}
		if got := e.ToMessage(); got != "msg field" {
			t.Fatalf("got %q, want msg field", got)
		}
	})

	t.Run("error as number falls into default branch returning raw bytes", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`42`)}
		if got := e.ToMessage(); got != "42" {
			t.Fatalf("got %q, want raw '42' (default branch behavior)", got)
		}
	})

	t.Run("error as JSON null is treated as non-empty raw bytes, masking fallback fields", func(t *testing.T) {
		// FINDING: internal/pkg/dto/error.go ToMessage() — json.RawMessage captures
		// the literal 4 bytes "null" for a JSON null error field, so
		// `len(e.Error) > 0` is true and GetJsonType returns "null", which falls
		// into the `default: return string(e.Error)` branch. Expected: an
		// explicitly-null "error" field should be treated the same as an absent
		// one, falling through to check e.Message/e.Msg/etc. Actual: callers get
		// back the literal string "null" instead of the real fallback message,
		// which is confusing/wrong if surfaced to end users or logs.
		e := GeneralErrorResponse{Error: json.RawMessage(`null`), Message: "real message"}
		got := e.ToMessage()
		if got != "null" {
			t.Fatalf("got %q, want literal \"null\" (documents current masking behavior)", got)
		}
	})

	t.Run("no error field, uses Message before Msg/Err/ErrorMsg", func(t *testing.T) {
		e := GeneralErrorResponse{Message: "m1", Msg: "m2", Err: "m3", ErrorMsg: "m4"}
		if got := e.ToMessage(); got != "m1" {
			t.Fatalf("got %q, want m1 (priority order)", got)
		}
	})

	t.Run("falls back through Msg, Err, ErrorMsg in order", func(t *testing.T) {
		if got := (GeneralErrorResponse{Msg: "m2", Err: "m3"}).ToMessage(); got != "m2" {
			t.Fatalf("got %q, want m2", got)
		}
		if got := (GeneralErrorResponse{Err: "m3", ErrorMsg: "m4"}).ToMessage(); got != "m3" {
			t.Fatalf("got %q, want m3", got)
		}
		if got := (GeneralErrorResponse{ErrorMsg: "m4"}).ToMessage(); got != "m4" {
			t.Fatalf("got %q, want m4", got)
		}
	})

	t.Run("falls back to nested Header.Message", func(t *testing.T) {
		e := GeneralErrorResponse{}
		e.Header.Message = "header msg"
		if got := e.ToMessage(); got != "header msg" {
			t.Fatalf("got %q, want header msg", got)
		}
	})

	t.Run("falls back to nested Response.Error.Message as last resort", func(t *testing.T) {
		e := GeneralErrorResponse{}
		e.Response.Error.Message = "response msg"
		if got := e.ToMessage(); got != "response msg" {
			t.Fatalf("got %q, want response msg", got)
		}
	})

	t.Run("completely empty response yields empty string", func(t *testing.T) {
		if got := (GeneralErrorResponse{}).ToMessage(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

func TestGeneralErrorResponse_TryToOpenAIError(t *testing.T) {
	t.Run("valid object with message", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`{"message":"bad","type":"invalid_request_error","param":"model","code":"x"}`)}
		got := e.TryToOpenAIError()
		if got == nil || got.Message != "bad" || got.Type != "invalid_request_error" || got.Param != "model" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("empty Error field returns nil", func(t *testing.T) {
		e := GeneralErrorResponse{}
		if got := e.TryToOpenAIError(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("object with empty message returns nil", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`{"type":"invalid"}`)}
		if got := e.TryToOpenAIError(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("malformed JSON returns nil", func(t *testing.T) {
		e := GeneralErrorResponse{Error: json.RawMessage(`{not valid`)}
		if got := e.TryToOpenAIError(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

// ---------------- rerank.go ----------------

func TestRerankRequest_GetTokenCountMeta(t *testing.T) {
	t.Run("documents formatted and query appended", func(t *testing.T) {
		r := &RerankRequest{Documents: []any{"doc1", 42, map[string]any{"text": "doc3"}}, Query: "search term"}
		meta := r.GetTokenCountMeta()
		if !jsonContains(meta.CombineText, "doc1") || !jsonContains(meta.CombineText, "search term") {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
	})

	t.Run("empty query not appended", func(t *testing.T) {
		r := &RerankRequest{Documents: []any{"doc1"}}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "doc1" {
			t.Fatalf("CombineText = %q, want doc1", meta.CombineText)
		}
	})

	t.Run("empty documents and query yields empty text", func(t *testing.T) {
		r := &RerankRequest{}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "" {
			t.Fatalf("CombineText = %q, want empty", meta.CombineText)
		}
	})
}

func TestRerankRequest_GetReturnDocuments(t *testing.T) {
	r := &RerankRequest{}
	if r.GetReturnDocuments() {
		t.Fatal("expected false for nil pointer")
	}
	truth := true
	r.ReturnDocuments = &truth
	if !r.GetReturnDocuments() {
		t.Fatal("expected true")
	}
	falsy := false
	r.ReturnDocuments = &falsy
	if r.GetReturnDocuments() {
		t.Fatal("expected false")
	}
}

func TestRerankRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &RerankRequest{}
	if r.IsStream(c) {
		t.Fatal("rerank requests never stream")
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty modelName must not overwrite")
	}
	r.SetModelName("rerank-v2")
	if r.Model != "rerank-v2" {
		t.Fatalf("Model = %q", r.Model)
	}
}

// ---------------- openai_video.go ----------------

func TestOpenAIVideo_SetProgressStr(t *testing.T) {
	cases := []struct {
		name  string
		start int
		input string
		want  int
	}{
		{"percent suffix stripped", 0, "50%", 50},
		{"no percent suffix still parses", 0, "75", 75},
		{"full completion", 0, "100%", 100},
		{"negative progress parses", 0, "-5%", -5},
		{"invalid string resets progress to zero", 88, "not-a-number", 0},
		{"empty string resets progress to zero", 42, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := &OpenAIVideo{Progress: c.start}
			v.SetProgressStr(c.input)
			if v.Progress != c.want {
				t.Errorf("Progress = %d, want %d", v.Progress, c.want)
			}
		})
	}
}

func TestOpenAIVideo_SetMetadata(t *testing.T) {
	v := &OpenAIVideo{}
	if v.Metadata != nil {
		t.Fatal("Metadata should start nil")
	}
	v.SetMetadata("key1", "value1")
	if v.Metadata == nil || v.Metadata["key1"] != "value1" {
		t.Fatalf("Metadata = %+v", v.Metadata)
	}
	v.SetMetadata("key2", 99)
	if len(v.Metadata) != 2 || v.Metadata["key2"] != 99 {
		t.Fatalf("Metadata = %+v, want 2 keys", v.Metadata)
	}
}

func TestNewOpenAIVideo(t *testing.T) {
	v := NewOpenAIVideo()
	if v.Object != "video" {
		t.Fatalf("Object = %q, want video", v.Object)
	}
	if v.Progress != 0 || v.ID != "" {
		t.Fatalf("expected zero-valued struct otherwise, got %+v", v)
	}
}

// ---------------- suno.go ----------------

func TestTaskResponse_IsSuccess(t *testing.T) {
	success := TaskResponse[string]{Code: TaskSuccessCode}
	if !success.IsSuccess() {
		t.Fatal("expected true for matching success code")
	}
	failure := TaskResponse[string]{Code: "failed"}
	if failure.IsSuccess() {
		t.Fatal("expected false for non-success code")
	}
	empty := TaskResponse[string]{}
	if empty.IsSuccess() {
		t.Fatal("expected false for empty code")
	}
}

// ---------------- notify.go ----------------

func TestNewNotify(t *testing.T) {
	values := []interface{}{"v1", 2}
	n := NewNotify(NotifyTypeQuotaExceed, "Title", "Content {{value}}", values)
	if n.Type != NotifyTypeQuotaExceed || n.Title != "Title" || n.Content != "Content {{value}}" {
		t.Fatalf("got %+v", n)
	}
	if len(n.Values) != 2 || n.Values[0] != "v1" || n.Values[1] != 2 {
		t.Fatalf("Values = %+v", n.Values)
	}
}

// ---------------- channel_settings.go ----------------

func TestChannelOtherSettings_IsOpenRouterEnterprise(t *testing.T) {
	var nilSettings *ChannelOtherSettings
	if nilSettings.IsOpenRouterEnterprise() {
		t.Fatal("nil receiver should return false")
	}

	s := &ChannelOtherSettings{}
	if s.IsOpenRouterEnterprise() {
		t.Fatal("nil pointer field should return false")
	}

	truth := true
	s.OpenRouterEnterprise = &truth
	if !s.IsOpenRouterEnterprise() {
		t.Fatal("expected true")
	}

	falsy := false
	s.OpenRouterEnterprise = &falsy
	if s.IsOpenRouterEnterprise() {
		t.Fatal("expected false")
	}
}

// ---------------- request_common.go ----------------

func TestBaseRequest_GetTokenCountMetaAndIsStream(t *testing.T) {
	c := newTestGinContext(t, "/")
	b := &BaseRequest{}
	meta := b.GetTokenCountMeta()
	if meta.TokenType != types.TokenTypeTokenizer {
		t.Fatalf("TokenType = %q, want tokenizer", meta.TokenType)
	}
	if b.IsStream(c) {
		t.Fatal("BaseRequest.IsStream must always be false")
	}
}
