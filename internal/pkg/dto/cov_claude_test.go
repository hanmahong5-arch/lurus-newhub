package dto

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestClaudeMediaMessage_TextHelpers(t *testing.T) {
	c := &ClaudeMediaMessage{}
	if got := c.GetText(); got != "" {
		t.Fatalf("got %q, want empty for nil Text", got)
	}
	c.SetText("hi")
	if got := c.GetText(); got != "hi" {
		t.Fatalf("got %q, want hi", got)
	}
}

func TestClaudeMediaMessage_ContentHelpers(t *testing.T) {
	t.Run("nil content", func(t *testing.T) {
		c := &ClaudeMediaMessage{}
		if c.IsStringContent() {
			t.Fatal("expected false for nil content")
		}
		if got := c.GetStringContent(); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("string content", func(t *testing.T) {
		c := &ClaudeMediaMessage{}
		c.SetContent("plain text")
		if !c.IsStringContent() {
			t.Fatal("expected true")
		}
		if got := c.GetStringContent(); got != "plain text" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("array content concatenates text blocks only", func(t *testing.T) {
		c := &ClaudeMediaMessage{Content: []any{
			map[string]any{"type": "text", "text": "a"},
			map[string]any{"type": "image", "source": "ignored"},
			map[string]any{"type": "text", "text": "b"},
			"not-a-map-skip",
		}}
		if c.IsStringContent() {
			t.Fatal("array content should not be string content")
		}
		if got := c.GetStringContent(); got != "ab" {
			t.Fatalf("got %q, want ab", got)
		}
	})

	t.Run("unsupported content type returns empty", func(t *testing.T) {
		c := &ClaudeMediaMessage{Content: 12345}
		if got := c.GetStringContent(); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestClaudeMediaMessage_GetJsonRowString(t *testing.T) {
	c := &ClaudeMediaMessage{Type: "text", Text: strPtr("hello")}
	s := c.GetJsonRowString()
	if s == "" {
		t.Fatal("expected non-empty json string")
	}
	if !jsonContains(s, `"text":"hello"`) {
		t.Fatalf("json = %s, missing text field", s)
	}
}

func jsonContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestClaudeMediaMessage_ParseMediaContent(t *testing.T) {
	t.Run("valid nested media content", func(t *testing.T) {
		c := &ClaudeMediaMessage{Content: []any{
			map[string]any{"type": "text", "text": "nested"},
		}}
		got := c.ParseMediaContent()
		if len(got) != 1 || got[0].Type != "text" || got[0].GetText() != "nested" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("nil content yields empty result", func(t *testing.T) {
		c := &ClaudeMediaMessage{}
		got := c.ParseMediaContent()
		if len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})
}

func TestClaudeMessage_ContentHelpers(t *testing.T) {
	t.Run("nil content", func(t *testing.T) {
		m := &ClaudeMessage{}
		if m.IsStringContent() {
			t.Fatal("expected false")
		}
		if got := m.GetStringContent(); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("string content via setter", func(t *testing.T) {
		m := &ClaudeMessage{}
		m.SetStringContent("hello")
		if !m.IsStringContent() {
			t.Fatal("expected true")
		}
		if got := m.GetStringContent(); got != "hello" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("array content concatenation", func(t *testing.T) {
		m := &ClaudeMessage{}
		m.SetContent([]any{
			map[string]any{"type": "text", "text": "x"},
			map[string]any{"type": "text", "text": "y"},
			"not-a-map-skip",
			map[string]any{"type": "image"},                // non-text type: skipped
			map[string]any{"type": "text", "text": 12345}, // text field not a string: skipped
		})
		if got := m.GetStringContent(); got != "xy" {
			t.Fatalf("got %q, want xy", got)
		}
	})

	t.Run("unsupported content type falls through to empty string", func(t *testing.T) {
		m := &ClaudeMessage{Content: 999}
		if got := m.GetStringContent(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("ParseContent roundtrip", func(t *testing.T) {
		m := &ClaudeMessage{}
		m.SetContent([]any{map[string]any{"type": "text", "text": "z"}})
		got, err := m.ParseContent()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].GetText() != "z" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestThinking_GetBudgetTokens(t *testing.T) {
	th := &Thinking{}
	if got := th.GetBudgetTokens(); got != 0 {
		t.Fatalf("got %d, want 0 for nil pointer", got)
	}
	budget := 2048
	th.BudgetTokens = &budget
	if got := th.GetBudgetTokens(); got != 2048 {
		t.Fatalf("got %d, want 2048", got)
	}
}

func TestClaudeRequest_SystemHelpers(t *testing.T) {
	t.Run("string system", func(t *testing.T) {
		c := &ClaudeRequest{}
		c.SetStringSystem("be helpful")
		if !c.IsStringSystem() {
			t.Fatal("expected true")
		}
		if got := c.GetStringSystem(); got != "be helpful" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("non-string system returns empty from GetStringSystem", func(t *testing.T) {
		c := &ClaudeRequest{System: []any{map[string]any{"type": "text", "text": "sys"}}}
		if c.IsStringSystem() {
			t.Fatal("expected false")
		}
		if got := c.GetStringSystem(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("ParseSystem returns media blocks", func(t *testing.T) {
		c := &ClaudeRequest{System: []any{
			map[string]any{"type": "text", "text": "sys1"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64"}},
		}}
		got := c.ParseSystem()
		if len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestClaudeRequest_ToolsHelpers(t *testing.T) {
	t.Run("GetTools nil when Tools unset", func(t *testing.T) {
		c := &ClaudeRequest{}
		if got := c.GetTools(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("AddTool initializes and appends", func(t *testing.T) {
		c := &ClaudeRequest{}
		c.AddTool(&Tool{Name: "t1"})
		c.AddTool(&Tool{Name: "t2"})
		tools := c.GetTools()
		if len(tools) != 2 {
			t.Fatalf("got %d tools, want 2", len(tools))
		}
	})

	t.Run("AddTool on non-slice Tools reinitializes to single-element slice", func(t *testing.T) {
		c := &ClaudeRequest{Tools: "not-a-slice"}
		c.AddTool(&Tool{Name: "only"})
		tools := c.GetTools()
		if len(tools) != 1 {
			t.Fatalf("got %+v, want single element", tools)
		}
	})

	t.Run("GetTools returns nil for non-slice Tools value", func(t *testing.T) {
		c := &ClaudeRequest{Tools: "not-a-slice"}
		if got := c.GetTools(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

func TestProcessTools(t *testing.T) {
	tools := []any{
		&Tool{Name: "ptr-tool"},
		Tool{Name: "value-tool"},
		&ClaudeWebSearchTool{Name: "ptr-search"},
		ClaudeWebSearchTool{Name: "value-search"},
		"unsupported-entry",
		42,
	}
	normal, webSearch := ProcessTools(tools)
	if len(normal) != 2 {
		t.Fatalf("normal tools = %+v, want len 2", normal)
	}
	if len(webSearch) != 2 {
		t.Fatalf("web search tools = %+v, want len 2", webSearch)
	}
	if normal[0].Name != "ptr-tool" || normal[1].Name != "value-tool" {
		t.Fatalf("normal tools = %+v", normal)
	}
	if webSearch[0].Name != "ptr-search" || webSearch[1].Name != "value-search" {
		t.Fatalf("web search tools = %+v", webSearch)
	}
}

func TestClaudeRequest_SearchToolNameByToolCallId(t *testing.T) {
	c := &ClaudeRequest{Messages: []ClaudeMessage{
		{Role: "assistant", Content: []any{
			map[string]any{"type": "tool_use", "id": "call_abc", "name": "search_web"},
		}},
	}}
	if got := c.SearchToolNameByToolCallId("call_abc"); got != "search_web" {
		t.Fatalf("got %q, want search_web", got)
	}
	if got := c.SearchToolNameByToolCallId("missing_id"); got != "" {
		t.Fatalf("got %q, want empty for missing id", got)
	}
}

func TestClaudeRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &ClaudeRequest{Stream: true}
	if !r.IsStream(c) {
		t.Fatal("expected true")
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty name must not overwrite, got %q", r.Model)
	}
	r.SetModelName("claude-3")
	if r.Model != "claude-3" {
		t.Fatalf("Model = %q, want claude-3", r.Model)
	}
}

func TestClaudeRequest_GetTokenCountMeta(t *testing.T) {
	t.Run("string system contributes to combine text", func(t *testing.T) {
		r := &ClaudeRequest{System: "system prompt", MaxTokens: 1024}
		meta := r.GetTokenCountMeta()
		if meta.MaxTokens != 1024 {
			t.Fatalf("MaxTokens = %d, want 1024", meta.MaxTokens)
		}
		if !jsonContains(meta.CombineText, "system prompt") {
			t.Fatalf("CombineText = %q, missing system prompt", meta.CombineText)
		}
	})

	t.Run("empty string system contributes nothing", func(t *testing.T) {
		r := &ClaudeRequest{System: ""}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "" {
			t.Fatalf("CombineText = %q, want empty", meta.CombineText)
		}
	})

	t.Run("array system with text and image parts", func(t *testing.T) {
		r := &ClaudeRequest{System: []any{
			map[string]any{"type": "text", "text": "sys-text"},
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "http://sys-img"}},
		}}
		meta := r.GetTokenCountMeta()
		if !jsonContains(meta.CombineText, "sys-text") {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "http://sys-img" {
			t.Fatalf("Files = %+v", meta.Files)
		}
	})

	t.Run("array system image falls back to Source.Data when Url empty", func(t *testing.T) {
		r := &ClaudeRequest{System: []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "sys-b64"}},
		}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "sys-b64" {
			t.Fatalf("Files = %+v, want sys-b64 from Source.Data fallback", meta.Files)
		}
	})

	t.Run("array system image with nil source contributes nothing", func(t *testing.T) {
		r := &ClaudeRequest{System: []any{
			map[string]any{"type": "image"},
		}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 0 {
			t.Fatalf("Files = %+v, want empty for image with no source", meta.Files)
		}
	})

	t.Run("message image with nil source contributes nothing", func(t *testing.T) {
		r := &ClaudeRequest{Messages: []ClaudeMessage{{
			Role:    "user",
			Content: []any{map[string]any{"type": "image"}},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 0 {
			t.Fatalf("Files = %+v, want empty for image with no source", meta.Files)
		}
	})

	t.Run("string message content counted", func(t *testing.T) {
		r := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: "hi there"}}}
		meta := r.GetTokenCountMeta()
		if meta.MessagesCount != 1 {
			t.Fatalf("MessagesCount = %d, want 1", meta.MessagesCount)
		}
		if !jsonContains(meta.CombineText, "hi there") {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
	})

	t.Run("array message content: text/image/tool_use/tool_result", func(t *testing.T) {
		r := &ClaudeRequest{Messages: []ClaudeMessage{{
			Role: "assistant",
			Content: []any{
				map[string]any{"type": "text", "text": "reply"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "b64data"}},
				map[string]any{"type": "tool_use", "name": "calc", "input": map[string]any{"a": 1}},
				map[string]any{"type": "tool_result", "content": "42"},
			},
		}}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 1 || meta.Files[0].OriginData != "b64data" {
			t.Fatalf("Files = %+v", meta.Files)
		}
		for _, want := range []string{"reply", "calc", "42"} {
			if !jsonContains(meta.CombineText, want) {
				t.Errorf("CombineText missing %q; got %q", want, meta.CombineText)
			}
		}
	})

	t.Run("tools counted for normal and web search tools", func(t *testing.T) {
		r := &ClaudeRequest{}
		r.AddTool(&Tool{Name: "calculator", Description: "does math", InputSchema: map[string]interface{}{"type": "object"}})
		r.AddTool(&ClaudeWebSearchTool{Name: "web_search", UserLocation: &ClaudeWebSearchUserLocation{City: "SF"}})
		meta := r.GetTokenCountMeta()
		if meta.ToolsCount != 2 {
			t.Fatalf("ToolsCount = %d, want 2", meta.ToolsCount)
		}
		for _, want := range []string{"calculator", "does math", "web_search"} {
			if !jsonContains(meta.CombineText, want) {
				t.Errorf("CombineText missing %q", want)
			}
		}
	})
}

func TestClaudeResponse_IndexHelpers(t *testing.T) {
	r := &ClaudeResponse{}
	if got := r.GetIndex(); got != 0 {
		t.Fatalf("got %d, want 0 for nil index", got)
	}
	r.SetIndex(5)
	if got := r.GetIndex(); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

func TestClaudeResponse_GetClaudeError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		r := &ClaudeResponse{}
		if got := r.GetClaudeError(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("value type passthrough", func(t *testing.T) {
		r := &ClaudeResponse{Error: types.ClaudeError{Type: "invalid_request_error", Message: "bad"}}
		got := r.GetClaudeError()
		if got == nil || got.Message != "bad" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("pointer type passthrough", func(t *testing.T) {
		e := &types.ClaudeError{Message: "ptr-bad"}
		r := &ClaudeResponse{Error: e}
		if got := r.GetClaudeError(); got != e {
			t.Fatal("expected same pointer identity")
		}
	})

	t.Run("map converted", func(t *testing.T) {
		r := &ClaudeResponse{Error: map[string]interface{}{"type": "overloaded_error", "message": "busy"}}
		got := r.GetClaudeError()
		if got == nil || got.Type != "overloaded_error" || got.Message != "busy" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("string becomes upstream_error", func(t *testing.T) {
		r := &ClaudeResponse{Error: "raw string error"}
		got := r.GetClaudeError()
		if got == nil || got.Type != "upstream_error" || got.Message != "raw string error" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("unknown type falls back to formatted message", func(t *testing.T) {
		r := &ClaudeResponse{Error: 999}
		got := r.GetClaudeError()
		if got == nil || got.Type != "unknown_upstream_error" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestClaudeUsage_CacheHelpers(t *testing.T) {
	t.Run("nil receiver is safe and returns zero", func(t *testing.T) {
		var u *ClaudeUsage
		if got := u.GetCacheCreation5mTokens(); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
		if got := u.GetCacheCreation1hTokens(); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
		if got := u.GetCacheCreationTotalTokens(); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})

	t.Run("nil CacheCreation returns zero for 5m/1h", func(t *testing.T) {
		u := &ClaudeUsage{}
		if got := u.GetCacheCreation5mTokens(); got != 0 {
			t.Fatalf("got %d", got)
		}
		if got := u.GetCacheCreation1hTokens(); got != 0 {
			t.Fatalf("got %d", got)
		}
	})

	t.Run("populated cache creation fields", func(t *testing.T) {
		u := &ClaudeUsage{CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 30, Ephemeral1hInputTokens: 70}}
		if got := u.GetCacheCreation5mTokens(); got != 30 {
			t.Fatalf("got %d, want 30", got)
		}
		if got := u.GetCacheCreation1hTokens(); got != 70 {
			t.Fatalf("got %d, want 70", got)
		}
	})

	t.Run("GetCacheCreationTotalTokens prefers legacy field when positive", func(t *testing.T) {
		u := &ClaudeUsage{
			CacheCreationInputTokens: 200,
			CacheCreation:            &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 30, Ephemeral1hInputTokens: 70},
		}
		if got := u.GetCacheCreationTotalTokens(); got != 200 {
			t.Fatalf("got %d, want 200 (legacy field takes priority)", got)
		}
	})

	t.Run("GetCacheCreationTotalTokens sums 5m+1h when legacy field is zero", func(t *testing.T) {
		u := &ClaudeUsage{CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 30, Ephemeral1hInputTokens: 70}}
		if got := u.GetCacheCreationTotalTokens(); got != 100 {
			t.Fatalf("got %d, want 100", got)
		}
	})
}
