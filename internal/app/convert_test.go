package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestStopReasonOpenAI2Claude(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"stop to end_turn", "stop", "end_turn"},
		{"stop_sequence unchanged", "stop_sequence", "stop_sequence"},
		{"length to max_tokens", "length", "max_tokens"},
		{"max_tokens unchanged", "max_tokens", "max_tokens"},
		{"tool_calls to tool_use", "tool_calls", "tool_use"},
		{"unknown passthrough", "unknown_reason", "unknown_reason"},
		{"empty passthrough", "", ""},
		{"content_filter passthrough", "content_filter", "content_filter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stopReasonOpenAI2Claude(tt.input)
			if result != tt.expected {
				t.Errorf("stopReasonOpenAI2Claude(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestToJSONString(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil", nil, "null"},
		{"empty string", "", `""`},
		{"simple string", "hello", `"hello"`},
		{"number", 42, "42"},
		{"float", 3.14, "3.14"},
		{"bool", true, "true"},
		{"empty map", map[string]interface{}{}, "{}"},
		{"simple map", map[string]interface{}{"key": "value"}, `{"key":"value"}`},
		{"nested map", map[string]interface{}{"outer": map[string]interface{}{"inner": 1}}, `{"outer":{"inner":1}}`},
		{"array", []int{1, 2, 3}, "[1,2,3]"},
		{"empty array", []string{}, "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toJSONString(tt.input)
			if result != tt.expected {
				t.Errorf("toJSONString(%v) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestToJSONString_UnmarshalableInput(t *testing.T) {
	// Test with something that can't be marshaled
	// Functions can't be marshaled to JSON
	fn := func() {}
	result := toJSONString(fn)
	// Should return "{}" on error
	if result != "{}" {
		t.Errorf("toJSONString(func) = %q, want {}", result)
	}

	// Channels can't be marshaled either
	ch := make(chan int)
	result = toJSONString(ch)
	if result != "{}" {
		t.Errorf("toJSONString(chan) = %q, want {}", result)
	}
}

func TestConvertGeminiRoleToOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"user unchanged", "user", "user"},
		{"model to assistant", "model", "assistant"},
		{"function unchanged", "function", "function"},
		{"unknown defaults to user", "unknown", "user"},
		{"empty defaults to user", "", "user"},
		{"system defaults to user", "system", "user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertGeminiRoleToOpenAI(tt.input)
			if result != tt.expected {
				t.Errorf("convertGeminiRoleToOpenAI(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractTextFromGeminiParts(t *testing.T) {
	tests := []struct {
		name     string
		parts    []dto.GeminiPart
		expected string
	}{
		{
			name:     "empty parts",
			parts:    []dto.GeminiPart{},
			expected: "",
		},
		{
			name: "single text part",
			parts: []dto.GeminiPart{
				{Text: "Hello world"},
			},
			expected: "Hello world",
		},
		{
			name: "multiple text parts",
			parts: []dto.GeminiPart{
				{Text: "Hello"},
				{Text: "world"},
			},
			expected: "Hello\nworld",
		},
		{
			name: "mixed parts with non-text",
			parts: []dto.GeminiPart{
				{Text: "Text content"},
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "base64data"}},
				{Text: "More text"},
			},
			expected: "Text content\nMore text",
		},
		{
			name: "no text parts",
			parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "base64data"}},
			},
			expected: "",
		},
		{
			name: "empty text parts",
			parts: []dto.GeminiPart{
				{Text: ""},
				{Text: "Hello"},
				{Text: ""},
			},
			expected: "Hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextFromGeminiParts(tt.parts)
			if result != tt.expected {
				t.Errorf("extractTextFromGeminiParts() = %q, want %q",
					result, tt.expected)
			}
		})
	}
}

func TestGenerateStopBlock(t *testing.T) {
	tests := []struct {
		name          string
		index         int
		expectedType  string
		expectedIndex int
	}{
		{"index 0", 0, "content_block_stop", 0},
		{"index 1", 1, "content_block_stop", 1},
		{"index 5", 5, "content_block_stop", 5},
		{"large index", 100, "content_block_stop", 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateStopBlock(tt.index)

			if result == nil {
				t.Fatal("generateStopBlock returned nil")
			}
			if result.Type != tt.expectedType {
				t.Errorf("Type = %q, want %q", result.Type, tt.expectedType)
			}
			if result.Index == nil {
				t.Fatal("Index is nil")
			}
			if *result.Index != tt.expectedIndex {
				t.Errorf("Index = %d, want %d", *result.Index, tt.expectedIndex)
			}
		})
	}
}

// Benchmark tests
func BenchmarkStopReasonOpenAI2Claude(b *testing.B) {
	reasons := []string{"stop", "length", "tool_calls", "content_filter", "unknown"}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, reason := range reasons {
			stopReasonOpenAI2Claude(reason)
		}
	}
}

func BenchmarkToJSONString(b *testing.B) {
	data := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": []string{"a", "b", "c"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toJSONString(data)
	}
}

func BenchmarkConvertGeminiRoleToOpenAI(b *testing.B) {
	roles := []string{"user", "model", "function", "unknown"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, role := range roles {
			convertGeminiRoleToOpenAI(role)
		}
	}
}

func BenchmarkExtractTextFromGeminiParts(b *testing.B) {
	parts := []dto.GeminiPart{
		{Text: "Hello"},
		{Text: "world"},
		{InlineData: &dto.GeminiInlineData{}},
		{Text: "more text"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractTextFromGeminiParts(parts)
	}
}

// Test JSON round-trip consistency
func TestToJSONString_RoundTrip(t *testing.T) {
	original := map[string]interface{}{
		"string": "hello",
		"number": float64(42), // JSON numbers are floats
		"bool":   true,
		"null":   nil,
		"array":  []interface{}{"a", float64(1), true},
	}

	jsonStr := toJSONString(original)

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify key fields
	if decoded["string"] != original["string"] {
		t.Errorf("string mismatch: got %v, want %v", decoded["string"], original["string"])
	}
	if decoded["number"] != original["number"] {
		t.Errorf("number mismatch: got %v, want %v", decoded["number"], original["number"])
	}
	if decoded["bool"] != original["bool"] {
		t.Errorf("bool mismatch: got %v, want %v", decoded["bool"], original["bool"])
	}
}

// --- conversion_dropped diagnostics (cycle 9, L5) ---
//
// nonOpenRouterInfo (convert_claude_to_openai_test.go) and geminiInfo
// (convert_gemini_to_openai_test.go) are reused here; same package, no
// exports needed.

func TestClaudeToOpenAI_ReportsDroppedFields(t *testing.T) {
	temp := 0.7
	budget := 2000
	req := dto.ClaudeRequest{
		Model:       "claude-3-5-sonnet",
		MaxTokens:   100,
		Temperature: &temp,
		TopP:        0.9,
		TopK:        40,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
		StopSequences: []string{"STOP"},
		ToolChoice:    map[string]interface{}{"type": "auto"},
		// A non-function tool type is kept as name/description/input_schema
		// only by the tool conversion (see claudeToolsLossy), so it must be
		// reported even though `tools` is nominally mapped.
		Tools: []map[string]interface{}{
			{"type": "web_search_20250305", "name": "web_search"},
		},
		// enabled on a model whose OriginModelName carries no "-thinking"
		// suffix (nonOpenRouterInfo below) and info.ChannelType is not
		// OpenRouter, so the branch in ClaudeToOpenAIRequest produces
		// neither Reasoning nor a model-name change.
		Thinking:          &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		ContextManagement: json.RawMessage(`{"edits":[]}`),
		OutputConfig:      json.RawMessage(`{"x":1}`),
		OutputFormat:      json.RawMessage(`{"type":"text"}`),
		Container:         json.RawMessage(`{"id":"c1"}`),
		McpServers:        json.RawMessage(`[{"name":"s1"}]`),
		// Carries a key besides user_id, so claudeMetadataHasExtraKeys is
		// true and `metadata` is reported (see the user_id-only tests below
		// for the case where it must NOT be reported).
		Metadata:          json.RawMessage(`{"user_id":"u1","extra":"x"}`),
		ServiceTier:       "priority",
		MaxTokensToSample: 200,
		Prompt:            "legacy prompt",
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exact set: every field above that ClaudeToOpenAIRequest does not map
	// or maps only partially, sorted — neither a missing name nor an
	// invented one.
	want := []string{
		"container", "context_management", "max_tokens_to_sample",
		"mcp_servers", "metadata", "output_config", "output_format",
		"prompt", "service_tier", "thinking", "tool_choice", "tools", "top_k",
	}
	if !reflect.DeepEqual(info.ConversionDropped, want) {
		t.Errorf("ConversionDropped = %v, want %v", info.ConversionDropped, want)
	}
}

// TestClaudeToOpenAI_ThinkingReportedWhenBranchDoesNothing pins R5: `thinking`
// is reported from the thinking branch's actual OUTCOME (no Reasoning
// payload, no model-name change), never from info.ChannelType/isOpenRouter —
// checking channel type would leak upstream identity into a TierPublic key.
func TestClaudeToOpenAI_ThinkingReportedWhenBranchDoesNothing(t *testing.T) {
	budget := 4096
	req := dto.ClaudeRequest{
		Model:     "deepseek-chat",
		MaxTokens: 100,
		Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	info := nonOpenRouterInfo() // OriginModelName "claude-3-5-sonnet", no "-thinking" suffix
	out, err := ClaudeToOpenAIRequest(req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Reasoning != nil {
		t.Fatalf("out.Reasoning = %s, want nil (non-OpenRouter branch must not set it)", out.Reasoning)
	}
	if out.Model != req.Model {
		t.Fatalf("out.Model = %q, want unchanged %q", out.Model, req.Model)
	}
	if !reflect.DeepEqual(info.ConversionDropped, []string{"thinking"}) {
		t.Errorf("ConversionDropped = %v, want [thinking]", info.ConversionDropped)
	}
}

// TestClaudeToOpenAI_ThinkingSuffixAppliedNotReported covers the branch's
// other real outcome: a model name that DOES already carry the "-thinking"
// suffix gets it applied to the outgoing model, so `thinking` must not be
// reported as dropped even though there is no Reasoning payload.
func TestClaudeToOpenAI_ThinkingSuffixAppliedNotReported(t *testing.T) {
	budget := 4096
	req := dto.ClaudeRequest{
		Model:     "deepseek-chat",
		MaxTokens: 100,
		Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	info := nonOpenRouterInfo()
	info.OriginModelName = "deepseek-chat-thinking"
	out, err := ClaudeToOpenAIRequest(req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Model != "deepseek-chat-thinking" {
		t.Fatalf("out.Model = %q, want the -thinking suffix applied", out.Model)
	}
	if info.ConversionDropped != nil {
		t.Errorf("ConversionDropped = %v, want nil (the branch applied the suffix)", info.ConversionDropped)
	}
}

// TestClaudeToOpenAI_MetadataUserIdOnlyNotReported pins R8: newhub itself
// consumes metadata.user_id (deriveEndUserHash -> other.end_user), so a
// metadata object containing ONLY user_id must not be reported as dropped —
// it would send a caller to a ticket about attribution that already works.
func TestClaudeToOpenAI_MetadataUserIdOnlyNotReported(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 100,
		Metadata:  json.RawMessage(`{"user_id":"end-user-42"}`),
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ConversionDropped != nil {
		t.Errorf("ConversionDropped = %v, want nil (metadata carried only user_id, which newhub consumes)", info.ConversionDropped)
	}
}

// TestClaudeToOpenAI_MetadataExtraKeysReported is the other half of R8: a
// metadata object that carries anything beyond user_id truly never reaches
// the vendor, so it must still be reported.
func TestClaudeToOpenAI_MetadataExtraKeysReported(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 100,
		Metadata:  json.RawMessage(`{"user_id":"end-user-42","campaign":"q3"}`),
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(info.ConversionDropped, []string{"metadata"}) {
		t.Errorf("ConversionDropped = %v, want [metadata]", info.ConversionDropped)
	}
}

// TestClaudeToOpenAI_ToolsCacheControlReported covers the other lossy-tool
// shape named by R7: a plain "function" tool that also carries a
// cache_control block. The conversion keeps only name/description/
// input_schema, so cache_control never reaches the vendor.
func TestClaudeToOpenAI_ToolsCacheControlReported(t *testing.T) {
	req := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 100,
		Tools: []map[string]interface{}{
			{
				"name":          "get_weather",
				"input_schema":  map[string]interface{}{"type": "object"},
				"cache_control": map[string]interface{}{"type": "ephemeral"},
			},
		},
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(info.ConversionDropped, []string{"tools"}) {
		t.Errorf("ConversionDropped = %v, want [tools]", info.ConversionDropped)
	}
}

func TestClaudeToOpenAI_MappedFieldsNotReportedAsDropped(t *testing.T) {
	temp := 0.7
	req := dto.ClaudeRequest{
		Model:         "claude-3-5-sonnet",
		MaxTokens:     100,
		Temperature:   &temp,
		TopP:          0.9,
		Stream:        true,
		StopSequences: []string{"STOP"},
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ConversionDropped != nil {
		t.Errorf("ConversionDropped = %v, want nil (this request set only fields the converter maps)", info.ConversionDropped)
	}
}

func TestGeminiToOpenAI_ReportsDroppedFields(t *testing.T) {
	thinkingBudget := 100
	presence := float32(0.5)
	frequency := float32(0.25)
	logprobs := int32(3)

	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		SafetySettings: []dto.GeminiChatSafetySettings{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
		},
		ToolConfig:    &dto.ToolConfig{FunctionCallingConfig: &dto.FunctionCallingConfig{Mode: "AUTO"}},
		CachedContent: "cachedContents/abc123",
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseMimeType:   "application/json",
			ResponseSchema:     map[string]interface{}{"type": "object"},
			ResponseJsonSchema: json.RawMessage(`{"type":"object"}`),
			PresencePenalty:    &presence,
			FrequencyPenalty:   &frequency,
			ResponseLogprobs:   true,
			Logprobs:           &logprobs,
			MediaResolution:    "MEDIA_RESOLUTION_HIGH",
			Seed:               42,
			ResponseModalities: []string{"TEXT"},
			ThinkingConfig:     &dto.GeminiThinkingConfig{ThinkingBudget: &thinkingBudget},
			SpeechConfig:       json.RawMessage(`{"voice":"x"}`),
			ImageConfig:        json.RawMessage(`{"aspectRatio":"1:1"}`),
		},
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"cachedContent", "frequencyPenalty", "imageConfig", "logprobs",
		"mediaResolution", "presencePenalty", "responseJsonSchema",
		"responseLogprobs", "responseMimeType", "responseModalities",
		"responseSchema", "safetySettings", "seed", "speechConfig",
		"thinkingConfig", "toolConfig",
	}
	if !reflect.DeepEqual(info.ConversionDropped, want) {
		t.Errorf("ConversionDropped = %v, want %v", info.ConversionDropped, want)
	}
}

func TestGeminiToOpenAI_MappedFieldsNotReportedAsDropped(t *testing.T) {
	temp := 0.5
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			Temperature:     &temp,
			TopP:            0.9,
			TopK:            40,
			MaxOutputTokens: 100,
			StopSequences:   []string{"STOP"},
			CandidateCount:  1,
		},
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ConversionDropped != nil {
		t.Errorf("ConversionDropped = %v, want nil (this request set only fields the converter maps)", info.ConversionDropped)
	}
}

func TestConversionDiagnostics_EmptyWhenNothingDropped(t *testing.T) {
	claudeInfo := nonOpenRouterInfo()
	claudeReq := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 100,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hi"},
		},
	}
	if _, err := ClaudeToOpenAIRequest(claudeReq, claudeInfo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claudeInfo.ConversionDropped != nil {
		t.Errorf("Claude: ConversionDropped = %v, want nil for a minimal request", claudeInfo.ConversionDropped)
	}

	gInfo := geminiInfo()
	gReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hi"}}},
		},
	}
	if _, err := GeminiToOpenAIRequest(gReq, gInfo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gInfo.ConversionDropped != nil {
		t.Errorf("Gemini: ConversionDropped = %v, want nil for a minimal request", gInfo.ConversionDropped)
	}
}

func TestConversionDiagnostics_Bounded(t *testing.T) {
	names := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		names = append(names, fmt.Sprintf("field_%03d", i))
	}

	got := boundDroppedFields(names)

	if len(got) != conversionDroppedMax {
		t.Fatalf("len(got) = %d, want %d (the bound)", len(got), conversionDroppedMax)
	}
	if got[len(got)-1] != conversionDroppedTruncated {
		t.Errorf("last element = %q, want the truncation marker %q", got[len(got)-1], conversionDroppedTruncated)
	}
	// The kept names must be the lexicographically-first 15, not an arbitrary
	// subset — field_000..field_014 sort before field_015 and beyond.
	for i := 0; i < conversionDroppedMax-1; i++ {
		want := fmt.Sprintf("field_%03d", i)
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestConversionDiagnostics_BoundedDeduplicatesAndSorts(t *testing.T) {
	got := boundDroppedFields([]string{"top_k", "tool_choice", "top_k", "container"})
	want := []string{"container", "tool_choice", "top_k"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boundDroppedFields = %v, want %v", got, want)
	}
}

// TestConversionDiagnostics_BoundsNameLength pins R9: a single name past
// conversionDroppedNameMax is cut to exactly that length, not silently kept
// long or dropped outright. Reachability today is via a future field name,
// not either converter, since none of the wire names they emit come close —
// the bound exists so that can never change unnoticed.
func TestConversionDiagnostics_BoundsNameLength(t *testing.T) {
	long := "this_field_name_is_way_past_the_thirty_two_byte_cap"
	if len(long) <= conversionDroppedNameMax {
		t.Fatalf("test fixture bug: len(%q) = %d, want > %d", long, len(long), conversionDroppedNameMax)
	}

	got := boundDroppedFields([]string{long})

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if len(got[0]) != conversionDroppedNameMax {
		t.Errorf("len(got[0]) = %d, want %d", len(got[0]), conversionDroppedNameMax)
	}
	if got[0] != long[:conversionDroppedNameMax] {
		t.Errorf("got[0] = %q, want %q", got[0], long[:conversionDroppedNameMax])
	}
}

// TestGeminiToOpenAI_GoogleSearchToolReported pins R4: a tools entry with no
// functionDeclarations — Google's server-side googleSearch grounding tool —
// is reported under its own dotted wire name because the tool conversion
// only ever reads tool.FunctionDeclarations.
func TestGeminiToOpenAI_GoogleSearchToolReported(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		Tools: json.RawMessage(`[{"googleSearch":{}}]`),
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exact set: only the one tool the caller sent, under its dotted name.
	want := []string{"tools.googleSearch"}
	if !reflect.DeepEqual(info.ConversionDropped, want) {
		t.Errorf("ConversionDropped = %v, want %v", info.ConversionDropped, want)
	}
}

// TestGeminiToOpenAI_OtherBuiltinToolsReported covers the other three dotted
// names R4 names: googleSearchRetrieval, codeExecution, urlContext.
func TestGeminiToOpenAI_OtherBuiltinToolsReported(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		Tools: json.RawMessage(`[{"googleSearchRetrieval":{}},{"codeExecution":{}},{"urlContext":{}}]`),
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"tools.codeExecution", "tools.googleSearchRetrieval", "tools.urlContext"}
	if !reflect.DeepEqual(info.ConversionDropped, want) {
		t.Errorf("ConversionDropped = %v, want %v", info.ConversionDropped, want)
	}
}

// TestGeminiToOpenAI_FunctionDeclarationsToolNotReported proves the negative:
// a tools entry that DOES carry functionDeclarations (the mapped case) must
// not be reported, so the googleSearch/etc check above cannot be a
// false-positive-on-every-tools-array bug in disguise.
func TestGeminiToOpenAI_FunctionDeclarationsToolNotReported(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
		Tools: json.RawMessage(`[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object"}}]}]`),
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ConversionDropped != nil {
		t.Errorf("ConversionDropped = %v, want nil (functionDeclarations is mapped)", info.ConversionDropped)
	}
}

// TestGeminiToOpenAI_RequestsFieldReported pins R6: the top-level batch
// `requests` field is read only by the vertex adaptor and token counting,
// never by GeminiToOpenAIRequest, so a caller who sent it on a non-vertex
// channel gets no diagnostic today.
func TestGeminiToOpenAI_RequestsFieldReported(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Requests: []dto.GeminiChatRequest{
			{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "batched"}}}}},
		},
	}

	info := geminiInfo()
	if _, err := GeminiToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(info.ConversionDropped, []string{"requests"}) {
		t.Errorf("ConversionDropped = %v, want [requests]", info.ConversionDropped)
	}
}
