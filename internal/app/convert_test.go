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
	req := dto.ClaudeRequest{
		Model:       "claude-3-5-sonnet",
		MaxTokens:   100,
		Temperature: &temp,
		TopP:        0.9,
		TopK:        40,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
		StopSequences:     []string{"STOP"},
		ToolChoice:        map[string]interface{}{"type": "auto"},
		ContextManagement: json.RawMessage(`{"edits":[]}`),
		OutputConfig:      json.RawMessage(`{"x":1}`),
		OutputFormat:      json.RawMessage(`{"type":"text"}`),
		Container:         json.RawMessage(`{"id":"c1"}`),
		McpServers:        json.RawMessage(`[{"name":"s1"}]`),
		Metadata:          json.RawMessage(`{"user_id":"u1"}`),
		ServiceTier:       "priority",
		MaxTokensToSample: 200,
		Prompt:            "legacy prompt",
	}

	info := nonOpenRouterInfo()
	if _, err := ClaudeToOpenAIRequest(req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exact set: every field above that ClaudeToOpenAIRequest does not map,
	// sorted — neither a missing name nor an invented one.
	want := []string{
		"container", "context_management", "max_tokens_to_sample",
		"mcp_servers", "metadata", "output_config", "output_format",
		"prompt", "service_tier", "tool_choice", "top_k",
	}
	if !reflect.DeepEqual(info.ConversionDropped, want) {
		t.Errorf("ConversionDropped = %v, want %v", info.ConversionDropped, want)
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
