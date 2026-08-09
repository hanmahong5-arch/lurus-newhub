package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func newTestGinContext(t *testing.T, target string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c
}

// ---- Message.StringContent ----

func TestMessage_StringContent(t *testing.T) {
	tests := []struct {
		name    string
		content any
		want    string
	}{
		{"nil content", nil, ""},
		{"plain string", "hello world", "hello world"},
		{
			"array of text parts concatenated",
			[]any{
				map[string]any{"type": "text", "text": "foo"},
				map[string]any{"type": "text", "text": "bar"},
			},
			"foobar",
		},
		{
			"array skips non-text types",
			[]any{
				map[string]any{"type": "image_url", "image_url": "http://x"},
				map[string]any{"type": "text", "text": "kept"},
			},
			"kept",
		},
		{
			"array with non-map item is skipped",
			[]any{"not-a-map", map[string]any{"type": "text", "text": "x"}},
			"x",
		},
		{"unsupported type (int) returns empty", 42, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Message{Content: tt.content}
			if got := m.StringContent(); got != tt.want {
				t.Errorf("StringContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- Message content setters ----

func TestMessage_ContentSetters(t *testing.T) {
	m := Message{}

	m.SetStringContent("abc")
	if !m.IsStringContent() {
		t.Fatal("expected IsStringContent true after SetStringContent")
	}
	if m.Content != "abc" {
		t.Fatalf("Content = %v, want abc", m.Content)
	}

	media := []MediaContent{{Type: ContentTypeText, Text: "hi"}}
	m.SetMediaContent(media)
	if m.IsStringContent() {
		t.Fatal("expected IsStringContent false after SetMediaContent")
	}
	parsed := m.ParseContent()
	if len(parsed) != 1 || parsed[0].Text != "hi" {
		t.Fatalf("ParseContent after SetMediaContent = %+v", parsed)
	}

	m.SetNullContent()
	if m.Content != nil {
		t.Fatalf("Content after SetNullContent = %v, want nil", m.Content)
	}
	if got := m.ParseContent(); got != nil {
		t.Fatalf("ParseContent after SetNullContent = %+v, want nil", got)
	}
}

// ---- Message.ParseContent ----

func TestMessage_ParseContent(t *testing.T) {
	t.Run("nil content returns nil", func(t *testing.T) {
		m := Message{}
		if got := m.ParseContent(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("cached parsedContent short-circuits reparse", func(t *testing.T) {
		m := Message{Content: "should not be used"}
		// Manually seed cache (unexported field, same package).
		m.parsedContent = []MediaContent{{Type: ContentTypeText, Text: "cached"}}
		got := m.ParseContent()
		if len(got) != 1 || got[0].Text != "cached" {
			t.Fatalf("expected cached value returned, got %+v", got)
		}
	})

	t.Run("string content produces single text part", func(t *testing.T) {
		m := Message{Content: "plain"}
		got := m.ParseContent()
		if len(got) != 1 || got[0].Type != ContentTypeText || got[0].Text != "plain" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("non-array non-string content returns nil", func(t *testing.T) {
		m := Message{Content: 12345}
		if got := m.ParseContent(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("already-parsed MediaContent items pass through", func(t *testing.T) {
		m := Message{Content: []any{MediaContent{Type: ContentTypeText, Text: "direct"}}}
		got := m.ParseContent()
		if len(got) != 1 || got[0].Text != "direct" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("array item missing type field is skipped", func(t *testing.T) {
		m := Message{Content: []any{map[string]any{"text": "no-type"}}}
		if got := m.ParseContent(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("array item not a map is skipped", func(t *testing.T) {
		m := Message{Content: []any{42}}
		if got := m.ParseContent(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("image_url as string uses default detail high", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeImageURL, "image_url": "http://img"},
		}}
		got := m.ParseContent()
		if len(got) != 1 {
			t.Fatalf("got %+v", got)
		}
		img := got[0].GetImageMedia()
		if img == nil || img.Url != "http://img" || img.Detail != "high" {
			t.Fatalf("img = %+v", img)
		}
	})

	t.Run("image_url as object with explicit detail overrides default", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeImageURL, "image_url": map[string]any{
				"url": "http://img2", "detail": "low",
			}},
		}}
		got := m.ParseContent()
		img := got[0].GetImageMedia()
		if img == nil || img.Url != "http://img2" || img.Detail != "low" {
			t.Fatalf("img = %+v", img)
		}
	})

	t.Run("input_audio requires both data and format", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeInputAudio, "input_audio": map[string]any{
				"data": "base64data", "format": "mp3",
			}},
		}}
		got := m.ParseContent()
		if len(got) != 1 {
			t.Fatalf("got %+v", got)
		}
		audio := got[0].GetInputAudio()
		if audio == nil || audio.Data != "base64data" || audio.Format != "mp3" {
			t.Fatalf("audio = %+v", audio)
		}
	})

	t.Run("input_audio missing format is dropped entirely", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeInputAudio, "input_audio": map[string]any{
				"data": "base64data",
			}},
		}}
		if got := m.ParseContent(); len(got) != 0 {
			t.Fatalf("got %+v, want empty (missing format field)", got)
		}
	})

	t.Run("file with file_id takes precedence over filename/file_data", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeFile, "file": map[string]any{
				"file_id": "file-123", "filename": "ignored.txt", "file_data": "ignored",
			}},
		}}
		got := m.ParseContent()
		f := got[0].GetFile()
		if f == nil || f.FileId != "file-123" || f.FileName != "" {
			t.Fatalf("file = %+v", f)
		}
	})

	t.Run("file without file_id falls back to filename+file_data", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeFile, "file": map[string]any{
				"filename": "doc.pdf", "file_data": "base64",
			}},
		}}
		got := m.ParseContent()
		f := got[0].GetFile()
		if f == nil || f.FileName != "doc.pdf" || f.FileData != "base64" {
			t.Fatalf("file = %+v", f)
		}
	})

	t.Run("video_url text extracted", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": ContentTypeVideoUrl, "video_url": "http://video"},
		}}
		got := m.ParseContent()
		v := got[0].GetVideoUrl()
		if v == nil || v.Url != "http://video" {
			t.Fatalf("video = %+v", v)
		}
	})

	t.Run("unknown content type produces no entry", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": "unknown_type", "text": "x"},
		}}
		if got := m.ParseContent(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("empty array yields nil without caching", func(t *testing.T) {
		m := Message{Content: []any{}}
		got := m.ParseContent()
		if len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

// ---- MediaContent getters ----

func TestMediaContent_GetImageMedia(t *testing.T) {
	t.Run("nil ImageUrl returns nil", func(t *testing.T) {
		mc := MediaContent{}
		if got := mc.GetImageMedia(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
	t.Run("already-typed pointer is returned as-is", func(t *testing.T) {
		want := &MessageImageUrl{Url: "u", Detail: "d"}
		mc := MediaContent{ImageUrl: want}
		got := mc.GetImageMedia()
		if got != want {
			t.Fatalf("expected same pointer returned")
		}
	})
	t.Run("map is converted with fields extracted", func(t *testing.T) {
		mc := MediaContent{ImageUrl: map[string]any{
			"url": "u2", "detail": "auto", "mime_type": "image/png",
		}}
		got := mc.GetImageMedia()
		if got == nil || got.Url != "u2" || got.Detail != "auto" || got.MimeType != "image/png" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("unsupported concrete type falls through to nil", func(t *testing.T) {
		mc := MediaContent{ImageUrl: 12345}
		if got := mc.GetImageMedia(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

func TestMediaContent_GetInputAudio(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		mc := MediaContent{}
		if got := mc.GetInputAudio(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("map converted", func(t *testing.T) {
		mc := MediaContent{InputAudio: map[string]any{"data": "d", "format": "wav"}}
		got := mc.GetInputAudio()
		if got == nil || got.Data != "d" || got.Format != "wav" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("unsupported type returns nil", func(t *testing.T) {
		mc := MediaContent{InputAudio: "not-a-map"}
		if got := mc.GetInputAudio(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestMediaContent_GetFile(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		mc := MediaContent{}
		if got := mc.GetFile(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("map converted", func(t *testing.T) {
		mc := MediaContent{File: map[string]any{"file_id": "fid", "file_name": "n"}}
		got := mc.GetFile()
		if got == nil || got.FileId != "fid" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestMediaContent_GetVideoUrl(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		mc := MediaContent{}
		if got := mc.GetVideoUrl(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("pointer passthrough", func(t *testing.T) {
		want := &MessageVideoUrl{Url: "v"}
		mc := MediaContent{VideoUrl: want}
		if got := mc.GetVideoUrl(); got != want {
			t.Fatalf("expected same pointer")
		}
	})
	t.Run("map converted", func(t *testing.T) {
		mc := MediaContent{VideoUrl: map[string]any{"url": "v2"}}
		got := mc.GetVideoUrl()
		if got == nil || got.Url != "v2" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestMessageImageUrl_IsRemoteImage(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://example.com/x.png", true},
		{"https://example.com/x.png", true},
		{"data:image/png;base64,AAAA", false},
		{"", false},
	}
	for _, c := range cases {
		m := &MessageImageUrl{Url: c.url}
		if got := m.IsRemoteImage(); got != c.want {
			t.Errorf("IsRemoteImage(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// ---- Message.Prefix ----

func TestMessage_PrefixHelpers(t *testing.T) {
	m := Message{}
	if m.GetPrefix() != false {
		t.Fatal("nil Prefix should default to false")
	}
	m.SetPrefix(true)
	if !m.GetPrefix() {
		t.Fatal("expected true after SetPrefix(true)")
	}
	m.SetPrefix(false)
	if m.GetPrefix() {
		t.Fatal("expected false after SetPrefix(false)")
	}
}

// ---- Message tool calls ----

func TestMessage_ToolCallsRoundTrip(t *testing.T) {
	t.Run("nil ToolCalls returns nil", func(t *testing.T) {
		m := Message{}
		if got := m.ParseToolCalls(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("malformed JSON yields nil", func(t *testing.T) {
		m := Message{ToolCalls: json.RawMessage(`{not valid json`)}
		if got := m.ParseToolCalls(); got != nil {
			t.Fatalf("got %+v, want nil for malformed JSON", got)
		}
	})

	t.Run("SetToolCalls then ParseToolCalls round trips", func(t *testing.T) {
		m := Message{}
		calls := []ToolCallRequest{{ID: "call_1", Type: "function", Function: FunctionRequest{Name: "do_thing"}}}
		m.SetToolCalls(calls)
		got := m.ParseToolCalls()
		if len(got) != 1 || got[0].ID != "call_1" || got[0].Function.Name != "do_thing" {
			t.Fatalf("got %+v", got)
		}
	})
}

// ---- GeneralOpenAIRequest ----

func TestGeneralOpenAIRequest_GetSystemRoleName(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"o3-mini", "developer"},
		{"o1-mini", "system"},
		{"o1-preview", "system"},
		{"o1", "developer"},
		{"gpt-5-turbo", "developer"},
		{"gpt-4o", "system"},
		{"claude-3-opus", "system"},
		{"", "system"},
	}
	for _, c := range cases {
		r := &GeneralOpenAIRequest{Model: c.model}
		if got := r.GetSystemRoleName(); got != c.want {
			t.Errorf("GetSystemRoleName(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

func TestGeneralOpenAIRequest_GetMaxTokens(t *testing.T) {
	cases := []struct {
		name                string
		maxTokens           uint
		maxCompletionTokens uint
		want                uint
	}{
		{"only max_tokens", 100, 0, 100},
		{"completion tokens preferred when non-zero", 100, 50, 50},
		{"both zero", 0, 0, 0},
	}
	for _, c := range cases {
		r := &GeneralOpenAIRequest{MaxTokens: c.maxTokens, MaxCompletionTokens: c.maxCompletionTokens}
		if got := r.GetMaxTokens(); got != c.want {
			t.Errorf("%s: GetMaxTokens() = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestGeneralOpenAIRequest_ParseInput(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		r := &GeneralOpenAIRequest{}
		if got := r.ParseInput(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
	t.Run("string input", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Input: "hello"}
		got := r.ParseInput()
		if len(got) != 1 || got[0] != "hello" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("array input keeps only strings", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Input: []any{"a", 1, "b", nil, true}}
		got := r.ParseInput()
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("unsupported type returns nil", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Input: map[string]any{"x": 1}}
		if got := r.ParseInput(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

func TestGeneralOpenAIRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")

	r := &GeneralOpenAIRequest{Stream: true}
	if !r.IsStream(c) {
		t.Fatal("expected IsStream true")
	}
	r.Stream = false
	if r.IsStream(c) {
		t.Fatal("expected IsStream false")
	}

	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty modelName should not overwrite, got %q", r.Model)
	}
	r.SetModelName("new-model")
	if r.Model != "new-model" {
		t.Fatalf("Model = %q, want new-model", r.Model)
	}
}

func TestGeneralOpenAIRequest_ToMap(t *testing.T) {
	r := &GeneralOpenAIRequest{Model: "gpt-4o", Stream: true, N: 3}
	m := r.ToMap()
	if m["model"] != "gpt-4o" {
		t.Fatalf(`ToMap()["model"] = %v, want "gpt-4o"`, m["model"])
	}
	if m["stream"] != true {
		t.Fatalf(`ToMap()["stream"] = %v, want true`, m["stream"])
	}
	// N=3 marshals as JSON number -> decoded back as float64.
	if n, ok := m["n"].(float64); !ok || n != 3 {
		t.Fatalf(`ToMap()["n"] = %v (%T), want float64(3)`, m["n"], m["n"])
	}
	// zero-value omitempty fields must not appear.
	if _, ok := m["temperature"]; ok {
		t.Fatalf("unexpected temperature key present in ToMap output: %v", m["temperature"])
	}
}

func TestGeneralOpenAIRequest_GetTokenCountMeta(t *testing.T) {
	t.Run("prompt string", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Prompt: "hi there"}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "hi there" {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
	})

	t.Run("prompt array mixed types keeps only strings", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Prompt: []any{"a", 1, "b"}}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "a\nb" {
			t.Fatalf("CombineText = %q, want a\\nb", meta.CombineText)
		}
	})

	t.Run("prompt of other type falls back to %v formatting", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Prompt: 42}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "42" {
			t.Fatalf("CombineText = %q, want 42", meta.CombineText)
		}
	})

	t.Run("max_completion_tokens wins when larger", func(t *testing.T) {
		r := &GeneralOpenAIRequest{MaxTokens: 10, MaxCompletionTokens: 50}
		meta := r.GetTokenCountMeta()
		if meta.MaxTokens != 50 {
			t.Fatalf("MaxTokens = %d, want 50", meta.MaxTokens)
		}
	})

	t.Run("max_tokens wins when completion tokens not larger", func(t *testing.T) {
		r := &GeneralOpenAIRequest{MaxTokens: 100, MaxCompletionTokens: 10}
		meta := r.GetTokenCountMeta()
		if meta.MaxTokens != 100 {
			t.Fatalf("MaxTokens = %d, want 100", meta.MaxTokens)
		}
	})

	t.Run("message name counted even when content is nil", func(t *testing.T) {
		name := "alice"
		// The NameCount increment used to sit inside the `if message.Content != nil`
		// guard, so a message whose content was stripped had its name dropped from
		// both NameCount and CombineText even though it is still sent upstream.
		r := &GeneralOpenAIRequest{Messages: []Message{{Role: "user", Name: &name, Content: nil}}}
		meta := r.GetTokenCountMeta()
		if meta.NameCount != 1 {
			t.Fatalf("NameCount = %d, want 1 — a name-only message is still billed for its name", meta.NameCount)
		}
	})

	t.Run("message name counted when content present", func(t *testing.T) {
		name := "bob"
		r := &GeneralOpenAIRequest{Messages: []Message{{Role: "user", Name: &name, Content: "hi"}}}
		meta := r.GetTokenCountMeta()
		if meta.NameCount != 1 {
			t.Fatalf("NameCount = %d, want 1", meta.NameCount)
		}
		if meta.MessagesCount != 1 {
			t.Fatalf("MessagesCount = %d, want 1", meta.MessagesCount)
		}
	})

	t.Run("image_url media collected as file meta", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": ContentTypeImageURL, "image_url": map[string]any{"url": "http://img", "detail": "low"}},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "http://img" || meta.Files[0].Detail != "low" {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("image_url with empty url is not collected", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": ContentTypeImageURL, "image_url": map[string]any{"url": ""}},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 0 {
			t.Fatalf("Files = %+v, want empty for blank url", meta.Files)
		}
	})

	t.Run("input_audio media collected", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": ContentTypeInputAudio, "input_audio": map[string]any{"data": "b64", "format": "mp3"}},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "b64" {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("file media collected", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": ContentTypeFile, "file": map[string]any{"file_id": "fid"}},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("video_url media collected", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": ContentTypeVideoUrl, "video_url": "http://v"},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "http://v" {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("text parts appended to combine text", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Messages: []Message{{
			Role:    "user",
			Content: []any{map[string]any{"type": ContentTypeText, "text": "hello"}},
		}}}
		meta := r.GetTokenCountMeta()
		if meta.CombineText == "" {
			t.Fatal("expected non-empty CombineText")
		}
	})

	t.Run("tools counted with name/description/parameters", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Tools: []ToolCallRequest{
			{Type: "function", Function: FunctionRequest{Name: "fn1", Description: "does stuff", Parameters: map[string]any{"a": 1}}},
			{Type: "function", Function: FunctionRequest{Name: "fn2"}},
		}}
		meta := r.GetTokenCountMeta()
		if meta.ToolsCount != 2 {
			t.Fatalf("ToolsCount = %d, want 2", meta.ToolsCount)
		}
	})

	t.Run("input field parsed via ParseInput contributes text", func(t *testing.T) {
		r := &GeneralOpenAIRequest{Input: "embed this"}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "embed this" {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
	})
}

// ---- OpenAIResponsesRequest ----

func TestOpenAIResponsesRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &OpenAIResponsesRequest{Stream: true}
	if !r.IsStream(c) {
		t.Fatal("expected true")
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty name must not overwrite, got %q", r.Model)
	}
	r.SetModelName("new")
	if r.Model != "new" {
		t.Fatalf("Model = %q, want new", r.Model)
	}
}

func TestOpenAIResponsesRequest_GetToolsMap(t *testing.T) {
	t.Run("empty tools returns nil", func(t *testing.T) {
		r := &OpenAIResponsesRequest{}
		if got := r.GetToolsMap(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
	t.Run("valid tools array parsed", func(t *testing.T) {
		r := &OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"function","name":"f1"}]`)}
		got := r.GetToolsMap()
		if len(got) != 1 || got[0]["name"] != "f1" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestOpenAIResponsesRequest_ParseInput(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		r := &OpenAIResponsesRequest{}
		if got := r.ParseInput(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("string input becomes single input_text", func(t *testing.T) {
		r := &OpenAIResponsesRequest{Input: json.RawMessage(`"hello there"`)}
		got := r.ParseInput()
		if len(got) != 1 || got[0].Type != "input_text" || got[0].Text != "hello there" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("array with string content becomes input_text", func(t *testing.T) {
		r := &OpenAIResponsesRequest{Input: json.RawMessage(`[{"type":"message","role":"user","content":"plain text"}]`)}
		got := r.ParseInput()
		if len(got) != 1 || got[0].Type != "input_text" || got[0].Text != "plain text" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("array with nested content parts: text/image/file", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":[
			{"type":"input_text","text":"hi"},
			{"type":"input_image","image_url":"http://img"},
			{"type":"input_image","image_url":{"url":"http://img2"}},
			{"type":"input_file","file_url":"http://file"},
			{"type":"input_file","file_url":{"url":"http://file2"}},
			{"type":"unsupported_type"}
		]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		got := r.ParseInput()
		if len(got) != 5 {
			t.Fatalf("got %d items, want 5: %+v", len(got), got)
		}
		if got[0].Type != "input_text" || got[0].Text != "hi" {
			t.Fatalf("item0 = %+v", got[0])
		}
		if got[1].Type != "input_image" || got[1].ImageUrl != "http://img" {
			t.Fatalf("item1 = %+v", got[1])
		}
		if got[2].ImageUrl != "http://img2" {
			t.Fatalf("item2 = %+v", got[2])
		}
		if got[3].Type != "input_file" || got[3].FileUrl != "http://file" {
			t.Fatalf("item3 = %+v", got[3])
		}
		if got[4].FileUrl != "http://file2" {
			t.Fatalf("item4 = %+v", got[4])
		}
	})

	t.Run("array item without type field in content is skipped", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":[{"text":"no type key"}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		if got := r.ParseInput(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("content array item that is not an object is skipped", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":["just a string", 42, {"type":"input_text","text":"kept"}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		got := r.ParseInput()
		if len(got) != 1 || got[0].Text != "kept" {
			t.Fatalf("got %+v, want only the object item kept", got)
		}
	})
}

func TestOpenAIResponsesRequest_GetTokenCountMeta(t *testing.T) {
	t.Run("input_image with url contributes file meta not text", func(t *testing.T) {
		// ParseInput's "input_image" case used to drop item["detail"], so every
		// Responses-API image was estimated at the default tier regardless of what
		// the client asked for. fix_token_count_meta_test.go covers the detail
		// placements; this case also pins OriginData and the MaxOutputTokens ->
		// MaxTokens mapping.
		raw := `[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"http://img","detail":"high"}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw), MaxOutputTokens: 500}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "http://img" {
			t.Fatalf("Files = %+v", meta.Files)
		}
		if meta.Files[0].Detail != "high" {
			t.Fatalf("Detail = %q, want \"high\" — the client-supplied detail must reach the image estimate", meta.Files[0].Detail)
		}
		if meta.MaxTokens != 500 {
			t.Fatalf("MaxTokens = %d, want 500", meta.MaxTokens)
		}
	})

	t.Run("input_file with url contributes file meta", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":[{"type":"input_file","file_url":"http://file"}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "http://file" || meta.Files[0].FileType != types.FileTypeFile {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("input_text type contributes to combine text via else branch", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":[{"type":"input_text","text":"plain words"}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		meta := r.GetTokenCountMeta()
		if !strings.Contains(meta.CombineText, "plain words") {
			t.Fatalf("CombineText = %q, want plain words", meta.CombineText)
		}
	})

	t.Run("input_file with empty url contributes nothing", func(t *testing.T) {
		raw := `[{"type":"message","role":"user","content":[{"type":"input_file","file_url":""}]}]`
		r := &OpenAIResponsesRequest{Input: json.RawMessage(raw)}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 0 {
			t.Fatalf("Files = %+v, want empty", meta.Files)
		}
	})

	t.Run("instructions/metadata/text/tool_choice/prompt/tools all appended when present", func(t *testing.T) {
		r := &OpenAIResponsesRequest{
			Instructions: json.RawMessage(`"be nice"`),
			Metadata:     json.RawMessage(`{"k":"v"}`),
			Text:         json.RawMessage(`{"format":"plain"}`),
			ToolChoice:   json.RawMessage(`"auto"`),
			Prompt:       json.RawMessage(`"a prompt"`),
			Tools:        json.RawMessage(`[{"type":"function"}]`),
		}
		meta := r.GetTokenCountMeta()
		for _, want := range []string{"be nice", `{"k":"v"}`, `{"format":"plain"}`, `"auto"`, `"a prompt"`, `[{"type":"function"}]`} {
			if !strings.Contains(meta.CombineText, want) {
				t.Errorf("CombineText missing %q; got %q", want, meta.CombineText)
			}
		}
	})

	t.Run("empty request produces empty combine text and zero max tokens", func(t *testing.T) {
		r := &OpenAIResponsesRequest{}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "" || meta.MaxTokens != 0 {
			t.Fatalf("meta = %+v, want zero-valued", meta)
		}
	})
}
