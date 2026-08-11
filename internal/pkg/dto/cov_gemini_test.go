package dto

import (
	"encoding/json"
	"testing"
)

func TestGeminiChatRequest_UnmarshalJSON_SnakeCamelSystemInstruction(t *testing.T) {
	t.Run("camelCase only", func(t *testing.T) {
		raw := `{"contents":[],"systemInstruction":{"role":"system","parts":[{"text":"camel"}]}}`
		var r GeminiChatRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if r.SystemInstructions == nil || len(r.SystemInstructions.Parts) != 1 || r.SystemInstructions.Parts[0].Text != "camel" {
			t.Fatalf("SystemInstructions = %+v", r.SystemInstructions)
		}
	})

	t.Run("snake_case only", func(t *testing.T) {
		raw := `{"contents":[],"system_instruction":{"role":"system","parts":[{"text":"snake"}]}}`
		var r GeminiChatRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if r.SystemInstructions == nil || r.SystemInstructions.Parts[0].Text != "snake" {
			t.Fatalf("SystemInstructions = %+v", r.SystemInstructions)
		}
	})

	t.Run("both provided: snake_case wins", func(t *testing.T) {
		raw := `{"contents":[],"systemInstruction":{"parts":[{"text":"camel"}]},"system_instruction":{"parts":[{"text":"snake-wins"}]}}`
		var r GeminiChatRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if r.SystemInstructions == nil || r.SystemInstructions.Parts[0].Text != "snake-wins" {
			t.Fatalf("SystemInstructions = %+v, want snake_case to win", r.SystemInstructions)
		}
	})

	t.Run("neither provided leaves nil", func(t *testing.T) {
		raw := `{"contents":[]}`
		var r GeminiChatRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if r.SystemInstructions != nil {
			t.Fatalf("SystemInstructions = %+v, want nil", r.SystemInstructions)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		// Called directly (not via top-level json.Unmarshal): a syntactically
		// invalid outer document never reaches a custom UnmarshalJSON method
		// because the stdlib decoder validates JSON syntax before dispatching,
		// so we exercise the internal common.Unmarshal error path explicitly.
		var r GeminiChatRequest
		if err := r.UnmarshalJSON([]byte(`{"contents":`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})

	t.Run("regular contents field still populated", func(t *testing.T) {
		raw := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
		var r GeminiChatRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(r.Contents) != 1 || r.Contents[0].Parts[0].Text != "hi" {
			t.Fatalf("Contents = %+v", r.Contents)
		}
	})
}

func TestGeminiThinkingConfig_UnmarshalJSON(t *testing.T) {
	t.Run("camelCase fields", func(t *testing.T) {
		raw := `{"includeThoughts":true,"thinkingBudget":100,"thinkingLevel":"high"}`
		var c GeminiThinkingConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if !c.IncludeThoughts || c.ThinkingBudget == nil || *c.ThinkingBudget != 100 || c.ThinkingLevel != "high" {
			t.Fatalf("config = %+v", c)
		}
	})

	t.Run("snake_case overrides camelCase", func(t *testing.T) {
		raw := `{"includeThoughts":false,"include_thoughts":true,"thinkingBudget":10,"thinking_budget":20,"thinkingLevel":"low","thinking_level":"high"}`
		var c GeminiThinkingConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if !c.IncludeThoughts {
			t.Errorf("IncludeThoughts = %v, want true (snake_case override)", c.IncludeThoughts)
		}
		if c.ThinkingBudget == nil || *c.ThinkingBudget != 20 {
			t.Errorf("ThinkingBudget = %v, want 20", c.ThinkingBudget)
		}
		if c.ThinkingLevel != "high" {
			t.Errorf("ThinkingLevel = %q, want high", c.ThinkingLevel)
		}
	})

	t.Run("empty snake_case thinking_level does not override camelCase", func(t *testing.T) {
		raw := `{"thinkingLevel":"medium","thinking_level":""}`
		var c GeminiThinkingConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if c.ThinkingLevel != "medium" {
			t.Fatalf("ThinkingLevel = %q, want medium (empty snake_case should not override)", c.ThinkingLevel)
		}
	})

	t.Run("SetThinkingBudget helper", func(t *testing.T) {
		c := &GeminiThinkingConfig{}
		c.SetThinkingBudget(512)
		if c.ThinkingBudget == nil || *c.ThinkingBudget != 512 {
			t.Fatalf("ThinkingBudget = %v", c.ThinkingBudget)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		var c GeminiThinkingConfig
		if err := c.UnmarshalJSON([]byte(`{"thinkingBudget":`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestGeminiInlineData_UnmarshalJSON(t *testing.T) {
	t.Run("camelCase mimeType", func(t *testing.T) {
		raw := `{"mimeType":"image/png","data":"AAA"}`
		var g GeminiInlineData
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if g.MimeType != "image/png" || g.Data != "AAA" {
			t.Fatalf("g = %+v", g)
		}
	})

	t.Run("snake_case mime_type takes priority", func(t *testing.T) {
		raw := `{"mimeType":"image/png","mime_type":"image/jpeg","data":"BBB"}`
		var g GeminiInlineData
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if g.MimeType != "image/jpeg" {
			t.Fatalf("MimeType = %q, want image/jpeg (snake_case priority)", g.MimeType)
		}
	})

	t.Run("empty mime_type falls back to camelCase", func(t *testing.T) {
		raw := `{"mimeType":"audio/mp3","mime_type":""}`
		var g GeminiInlineData
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if g.MimeType != "audio/mp3" {
			t.Fatalf("MimeType = %q, want audio/mp3", g.MimeType)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		var g GeminiInlineData
		if err := g.UnmarshalJSON([]byte(`{"data":`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestGeminiPart_UnmarshalJSON(t *testing.T) {
	t.Run("camelCase inlineData", func(t *testing.T) {
		raw := `{"text":"hello","inlineData":{"mimeType":"image/png","data":"XYZ"}}`
		var p GeminiPart
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.Text != "hello" || p.InlineData == nil || p.InlineData.Data != "XYZ" {
			t.Fatalf("p = %+v", p)
		}
	})

	t.Run("snake_case inline_data takes priority", func(t *testing.T) {
		raw := `{"inlineData":{"data":"camel-data"},"inline_data":{"data":"snake-data"}}`
		var p GeminiPart
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.InlineData == nil || p.InlineData.Data != "snake-data" {
			t.Fatalf("InlineData = %+v, want snake-data to win", p.InlineData)
		}
	})

	t.Run("neither inlineData present leaves nil", func(t *testing.T) {
		raw := `{"text":"just text"}`
		var p GeminiPart
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.InlineData != nil {
			t.Fatalf("InlineData = %+v, want nil", p.InlineData)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		var p GeminiPart
		if err := p.UnmarshalJSON([]byte(`{"text":`)); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestGeminiChatRequest_GetTokenCountMeta(t *testing.T) {
	t.Run("text parts joined and max tokens from generation config", func(t *testing.T) {
		r := &GeminiChatRequest{
			Contents: []GeminiChatContent{
				{Role: "user", Parts: []GeminiPart{{Text: "hello"}, {Text: "world"}}},
			},
			GenerationConfig: GeminiChatGenerationConfig{MaxOutputTokens: 256},
		}
		meta := r.GetTokenCountMeta()
		if meta.CombineText != "hello\nworld" {
			t.Fatalf("CombineText = %q", meta.CombineText)
		}
		if meta.MaxTokens != 256 {
			t.Fatalf("MaxTokens = %d, want 256", meta.MaxTokens)
		}
	})

	t.Run("zero max output tokens leaves MaxTokens at zero", func(t *testing.T) {
		r := &GeminiChatRequest{GenerationConfig: GeminiChatGenerationConfig{MaxOutputTokens: 0}}
		meta := r.GetTokenCountMeta()
		if meta.MaxTokens != 0 {
			t.Fatalf("MaxTokens = %d, want 0", meta.MaxTokens)
		}
	})

	t.Run("inline data classified by mime type prefix", func(t *testing.T) {
		r := &GeminiChatRequest{Contents: []GeminiChatContent{
			{Parts: []GeminiPart{
				{InlineData: &GeminiInlineData{MimeType: "image/png", Data: "img-data"}},
				{InlineData: &GeminiInlineData{MimeType: "audio/wav", Data: "audio-data"}},
				{InlineData: &GeminiInlineData{MimeType: "video/mp4", Data: "video-data"}},
				{InlineData: &GeminiInlineData{MimeType: "application/pdf", Data: "file-data"}},
			}},
		}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 4 {
			t.Fatalf("Files = %+v, want 4", meta.Files)
		}
		wantTypes := map[int]string{0: "image", 1: "audio", 2: "video", 3: "file"}
		for i, want := range wantTypes {
			if string(meta.Files[i].FileType) != want {
				t.Errorf("Files[%d].FileType = %q, want %q", i, meta.Files[i].FileType, want)
			}
		}
	})

	t.Run("inline data with empty data is not collected", func(t *testing.T) {
		r := &GeminiChatRequest{Contents: []GeminiChatContent{
			{Parts: []GeminiPart{{InlineData: &GeminiInlineData{MimeType: "image/png", Data: ""}}}},
		}}
		meta := r.GetTokenCountMeta()
		if len(meta.Files) != 0 {
			t.Fatalf("Files = %+v, want empty", meta.Files)
		}
	})
}

func TestGeminiChatRequest_IsStream(t *testing.T) {
	t.Run("alt=sse means streaming", func(t *testing.T) {
		c := newTestGinContext(t, "/?alt=sse")
		r := &GeminiChatRequest{}
		if !r.IsStream(c) {
			t.Fatal("expected true")
		}
	})
	t.Run("no alt query means not streaming", func(t *testing.T) {
		c := newTestGinContext(t, "/")
		r := &GeminiChatRequest{}
		if r.IsStream(c) {
			t.Fatal("expected false")
		}
	})
	t.Run("alt=json means not streaming", func(t *testing.T) {
		c := newTestGinContext(t, "/?alt=json")
		r := &GeminiChatRequest{}
		if r.IsStream(c) {
			t.Fatal("expected false")
		}
	})
}

func TestGeminiChatRequest_ToolsRoundTrip(t *testing.T) {
	t.Run("SetTools with empty slice writes empty array", func(t *testing.T) {
		r := &GeminiChatRequest{}
		r.SetTools(nil)
		if string(r.Tools) != "[]" {
			t.Fatalf("Tools = %s, want []", r.Tools)
		}
	})

	t.Run("SetTools then GetTools round trips array", func(t *testing.T) {
		r := &GeminiChatRequest{}
		r.SetTools([]GeminiChatTool{{GoogleSearch: map[string]any{}}})
		got := r.GetTools()
		if len(got) != 1 {
			t.Fatalf("got %+v, want 1 tool", got)
		}
	})

	t.Run("SetTools with unmarshalable payload leaves Tools untouched", func(t *testing.T) {
		r := &GeminiChatRequest{Tools: json.RawMessage(`"sentinel"`)}
		// chan values cannot be JSON-marshaled, forcing the error branch.
		r.SetTools([]GeminiChatTool{{GoogleSearch: make(chan int)}})
		if string(r.Tools) != `"sentinel"` {
			t.Fatalf("Tools = %s, want unchanged sentinel value after marshal failure", r.Tools)
		}
	})

	t.Run("GetTools parses object form as single-element slice", func(t *testing.T) {
		r := &GeminiChatRequest{Tools: json.RawMessage(`{"googleSearch":{}}`)}
		got := r.GetTools()
		if len(got) != 1 {
			t.Fatalf("got %+v, want 1", got)
		}
	})

	t.Run("GetTools with malformed array JSON returns nil", func(t *testing.T) {
		r := &GeminiChatRequest{Tools: json.RawMessage(`[{bad`)}
		if got := r.GetTools(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("GetTools with malformed object JSON returns nil", func(t *testing.T) {
		r := &GeminiChatRequest{Tools: json.RawMessage(`{bad`)}
		if got := r.GetTools(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("GetTools with neither array nor object prefix returns nil", func(t *testing.T) {
		r := &GeminiChatRequest{Tools: json.RawMessage(`null`)}
		if got := r.GetTools(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})
}

func TestGeminiEmbeddingRequest(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &GeminiEmbeddingRequest{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "embed me"}, {Text: "and this"}}}}
	if r.IsStream(c) {
		t.Fatal("embedding requests must never stream")
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "embed me\nand this" {
		t.Fatalf("CombineText = %q", meta.CombineText)
	}
	r.Model = "old"
	r.SetModelName("")
	if r.Model != "old" {
		t.Fatalf("empty modelName should not overwrite")
	}
	r.SetModelName("gemini-embed")
	if r.Model != "gemini-embed" {
		t.Fatalf("Model = %q, want gemini-embed", r.Model)
	}
}

func TestGeminiBatchEmbeddingRequest(t *testing.T) {
	c := newTestGinContext(t, "/")
	r := &GeminiBatchEmbeddingRequest{Requests: []*GeminiEmbeddingRequest{
		{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "one"}}}},
		{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "two"}}}},
	}}
	if r.IsStream(c) {
		t.Fatal("batch embedding requests must never stream")
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "one\ntwo" {
		t.Fatalf("CombineText = %q, want one\\ntwo", meta.CombineText)
	}

	r.SetModelName("gemini-embed-batch")
	for i, req := range r.Requests {
		if req.Model != "gemini-embed-batch" {
			t.Fatalf("Requests[%d].Model = %q, want propagated model name", i, req.Model)
		}
	}
}

func TestGeminiBatchEmbeddingRequest_EmptyRequestSkipped(t *testing.T) {
	r := &GeminiBatchEmbeddingRequest{Requests: []*GeminiEmbeddingRequest{
		{Content: GeminiChatContent{}}, // produces empty CombineText, should be skipped in join
		{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "real"}}}},
	}}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "real" {
		t.Fatalf("CombineText = %q, want real (empty sub-request skipped)", meta.CombineText)
	}
}
