package claude

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// A minimal valid 1x1 transparent PNG, base64-encoded, so DecodeBase64ImageData
// can run its real image.DecodeConfig path without any network access.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func newTestGinContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: system field extraction
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_SystemString(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "system", Content: "You are a pirate."},
			{Role: "user", Content: "Hi"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysArr, ok := result.System.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("System should be []dto.ClaudeMediaMessage, got %T", result.System)
	}
	if len(sysArr) != 1 || sysArr[0].GetText() != "You are a pirate." {
		t.Errorf("System = %+v, want single text block 'You are a pirate.'", sysArr)
	}
	// system message must not leak into Messages
	for _, m := range result.Messages {
		if m.Role == "system" {
			t.Error("system role message must not appear in Messages")
		}
	}
}

// Note: consecutive same-role messages (including consecutive "system"
// messages) are merged by the preceding formatMessages pass into a single
// space-joined message before the system-block extraction ever runs, so two
// consecutive system messages produce ONE accumulated text block, not two.
func TestRequestOpenAI2ClaudeMessage_SystemMultipleMessagesAccumulate(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "system", Content: "Rule one."},
			{Role: "system", Content: "Rule two."},
			{Role: "user", Content: "Hi"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysArr, ok := result.System.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("System should be []dto.ClaudeMediaMessage, got %T", result.System)
	}
	if len(sysArr) != 1 {
		t.Fatalf("expected consecutive system messages merged into 1 block, got %d: %+v", len(sysArr), sysArr)
	}
	if sysArr[0].GetText() != "Rule one. Rule two." {
		t.Errorf("merged system block = %q, want %q", sysArr[0].GetText(), "Rule one. Rule two.")
	}
}

func TestRequestOpenAI2ClaudeMessage_SystemArrayContent(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "system", Content: []any{
				dto.MediaContent{Type: "text", Text: "part1"},
				dto.MediaContent{Type: "text", Text: "part2"},
			}},
			{Role: "user", Content: "Hi"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysArr, ok := result.System.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("System should be []dto.ClaudeMediaMessage, got %T", result.System)
	}
	if len(sysArr) != 2 || sysArr[0].GetText() != "part1" || sysArr[1].GetText() != "part2" {
		t.Errorf("system blocks = %+v, want [part1, part2]", sysArr)
	}
}

func TestRequestOpenAI2ClaudeMessage_NoSystemMessageLeavesSystemNil(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "Hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.System != nil {
		t.Errorf("System should stay nil with no system message, got %+v", result.System)
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: message content shapes
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_StringContentPassthrough(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "hello world"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	got, ok := result.Messages[0].Content.(string)
	if !ok || got != "hello world" {
		t.Errorf("Content = %+v, want string 'hello world'", result.Messages[0].Content)
	}
}

func TestRequestOpenAI2ClaudeMessage_FirstMessageNotUser_InjectsPlaceholder(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "assistant", Content: "I'm ready"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages (injected user + assistant), got %d: %+v", len(result.Messages), result.Messages)
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want %q (injected placeholder)", result.Messages[0].Role, "user")
	}
	if result.Messages[1].Role != "assistant" {
		t.Errorf("Messages[1].Role = %q, want %q", result.Messages[1].Role, "assistant")
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolResultAppendsToPrecedingUserMessage(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: "call the tool"},
			{Role: "tool", Content: "tool output", ToolCallId: "call_1"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected tool result folded into the single preceding user message, got %d messages: %+v", len(result.Messages), result.Messages)
	}
	blocks, ok := result.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("Content should be []dto.ClaudeMediaMessage, got %T", result.Messages[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks (text + tool_result), got %d: %+v", len(blocks), blocks)
	}
	last := blocks[len(blocks)-1]
	if last.Type != "tool_result" || last.ToolUseId != "call_1" {
		t.Errorf("last block = %+v, want type=tool_result tool_use_id=call_1", last)
	}
}

// When the very first message is role="tool" (not "user"), the "first
// message must be user" fix-up injects a placeholder user text block ahead
// of it; since that placeholder is already role=user, the tool_result then
// folds into ITS content instead of creating a separate message. Net result:
// 1 message containing both the placeholder text block and the tool_result.
// When a tool result follows a non-user, non-tool message (e.g. an assistant
// tool-call message), there is no preceding user ClaudeMessage to fold into,
// so a brand new user message carrying just the tool_result block is created.
func TestRequestOpenAI2ClaudeMessage_ToolResultAfterAssistant_CreatesNewUserMessage(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: "please call the tool"},
			{Role: "assistant", Content: "calling now"},
			{Role: "tool", Content: "tool output", ToolCallId: "call_2"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, new user-with-tool_result), got %d: %+v", len(result.Messages), result.Messages)
	}
	last := result.Messages[2]
	if last.Role != "user" {
		t.Errorf("Role = %q, want %q", last.Role, "user")
	}
	blocks, ok := last.Content.([]dto.ClaudeMediaMessage)
	if !ok || len(blocks) != 1 || blocks[0].Type != "tool_result" || blocks[0].ToolUseId != "call_2" {
		t.Errorf("Content = %+v, want single tool_result block with tool_use_id=call_2", last.Content)
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolResultAsFirstMessage_BecomesUserRole(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "tool", Content: "orphan tool result", ToolCallId: "call_9"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(result.Messages), result.Messages)
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("Role = %q, want %q", result.Messages[0].Role, "user")
	}
	blocks, ok := result.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(blocks) != 2 {
		t.Fatalf("Content = %+v, want 2 blocks (placeholder text + tool_result)", result.Messages[0].Content)
	}
	last := blocks[len(blocks)-1]
	if last.Type != "tool_result" || last.ToolUseId != "call_9" {
		t.Errorf("last block = %+v, want type=tool_result tool_use_id=call_9", last)
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolUseCallsBecomeContentBlocks(t *testing.T) {
	c := newTestGinContext()
	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{
			ID:   "toolu_1",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "get_weather",
				Arguments: `{"city":"NYC"}`,
			},
		},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: "what's the weather"},
			{Role: "assistant", Content: "", ToolCalls: toolCallsJSON},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := result.Messages[len(result.Messages)-1]
	blocks, ok := last.Content.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("Content should be []dto.ClaudeMediaMessage, got %T", last.Content)
	}
	found := false
	for _, b := range blocks {
		if b.Type == "tool_use" {
			found = true
			if b.Id != "toolu_1" || b.Name != "get_weather" {
				t.Errorf("tool_use block = %+v, want id=toolu_1 name=get_weather", b)
			}
			inputMap, ok := b.Input.(map[string]any)
			if !ok || inputMap["city"] != "NYC" {
				t.Errorf("tool_use Input = %+v, want map with city=NYC", b.Input)
			}
		}
	}
	if !found {
		t.Fatalf("expected a tool_use content block, got %+v", blocks)
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolCallMalformedArgumentsAreSkipped(t *testing.T) {
	c := newTestGinContext()
	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{
			ID:   "toolu_bad",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "broken_tool",
				Arguments: `not-json-at-all`,
			},
		},
	})
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "", ToolCalls: toolCallsJSON},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := result.Messages[len(result.Messages)-1]
	blocks, _ := last.Content.([]dto.ClaudeMediaMessage)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Fatalf("malformed tool call arguments should be skipped, but got tool_use block %+v", b)
		}
	}
}

func TestRequestOpenAI2ClaudeMessage_ConsecutiveSameRoleMessagesMerge(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: "first"},
			{Role: "user", Content: "second"},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected consecutive same-role messages merged into 1, got %d: %+v", len(result.Messages), result.Messages)
	}
	got, ok := result.Messages[0].Content.(string)
	if !ok || !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("merged Content = %+v, want to contain both 'first' and 'second'", result.Messages[0].Content)
	}
}

func TestRequestOpenAI2ClaudeMessage_NilContentBecomesEllipsis(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: nil},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.Messages[0].Content.(string)
	if !ok || got != "..." {
		t.Errorf("Content = %+v, want '...' placeholder for nil content", result.Messages[0].Content)
	}
}

// NOTE: the empty-role normalization (relay-claude.go:250-253 wrote back to
// textRequest.Messages[i] but built the outgoing message from the range copy)
// is covered by TestFixClaudeRequestOpenAI2ClaudeMessage_EmptyRoleDefaultsToUser
// in fix_claude_relay_guards_test.go, which additionally checks the content and
// the mutated source message.

func TestRequestOpenAI2ClaudeMessage_ImageBase64Block(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				dto.MediaContent{Type: "image_url", ImageUrl: &dto.MessageImageUrl{Url: tinyPNGBase64}},
			}},
		},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blocks, ok := result.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(blocks) != 1 {
		t.Fatalf("Content = %+v, want single image block", result.Messages[0].Content)
	}
	b := blocks[0]
	if b.Type != "image" {
		t.Errorf("Type = %q, want %q", b.Type, "image")
	}
	if b.Source == nil || b.Source.MediaType != "image/png" {
		t.Errorf("Source = %+v, want MediaType image/png", b.Source)
	}
	if b.Source == nil || b.Source.Data != tinyPNGBase64 {
		t.Errorf("Source.Data mismatch, got %v", b.Source)
	}
}

func TestRequestOpenAI2ClaudeMessage_ImageBase64Malformed_ReturnsError(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				dto.MediaContent{Type: "image_url", ImageUrl: &dto.MessageImageUrl{Url: "not-valid-base64!!!"}},
			}},
		},
	}
	_, err := RequestOpenAI2ClaudeMessage(c, req)
	if err == nil {
		t.Fatal("expected error for malformed base64 image data")
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: max_tokens / thinking adapter
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_MaxTokensDefaultsFromSettingsWhenZero(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want default 8192", result.MaxTokens)
	}
}

func TestRequestOpenAI2ClaudeMessage_MaxTokensExplicitPreserved(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 256,
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", result.MaxTokens)
	}
}

func TestRequestOpenAI2ClaudeMessage_ThinkingSuffixSetsBudgetAndStripsSuffix(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-4-20250514-thinking",
		MaxTokens: 2000,
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Thinking == nil {
		t.Fatal("expected Thinking to be set for -thinking suffix model")
	}
	if result.Thinking.Type != "enabled" {
		t.Errorf("Thinking.Type = %q, want %q", result.Thinking.Type, "enabled")
	}
	// budget = 80% of max_tokens(2000) = 1600
	if result.Thinking.GetBudgetTokens() != 1600 {
		t.Errorf("BudgetTokens = %d, want 1600", result.Thinking.GetBudgetTokens())
	}
	if result.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want -thinking suffix stripped", result.Model)
	}
	if result.TopP != 0 {
		t.Errorf("TopP = %f, want forced to 0 under extended thinking", result.TopP)
	}
	if result.Temperature == nil || *result.Temperature != 1.0 {
		t.Errorf("Temperature = %v, want forced to 1.0 under extended thinking", result.Temperature)
	}
}

func TestRequestOpenAI2ClaudeMessage_ThinkingSuffixClampsLowMaxTokens(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-4-20250514-thinking",
		MaxTokens: 100, // below the 1280 floor required for extended thinking
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MaxTokens != 1280 {
		t.Errorf("MaxTokens = %d, want clamped to 1280", result.MaxTokens)
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningEffortLevels(t *testing.T) {
	tests := []struct {
		effort     string
		wantBudget int
	}{
		{"low", 1280},
		{"medium", 2048},
		{"high", 4096},
	}
	for _, tt := range tests {
		t.Run(tt.effort, func(t *testing.T) {
			c := newTestGinContext()
			req := dto.GeneralOpenAIRequest{
				Model:           "claude-3-opus-20240229", // no -thinking suffix, isolates this branch
				ReasoningEffort: tt.effort,
				Messages:        []dto.Message{{Role: "user", Content: "hi"}},
			}
			result, err := RequestOpenAI2ClaudeMessage(c, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Thinking == nil {
				t.Fatal("expected Thinking to be set")
			}
			if result.Thinking.GetBudgetTokens() != tt.wantBudget {
				t.Errorf("BudgetTokens = %d, want %d", result.Thinking.GetBudgetTokens(), tt.wantBudget)
			}
		})
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningEffortUnknownLeavesThinkingNil(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:           "claude-3-opus-20240229",
		ReasoningEffort: "ultra-mega", // not one of low/medium/high
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Thinking != nil {
		t.Errorf("Thinking = %+v, want nil for unrecognized effort value", result.Thinking)
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningFieldOverridesBudget(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:           "claude-3-opus-20240229",
		ReasoningEffort: "low", // would set 1280
		Reasoning:       json.RawMessage(`{"max_tokens":3333}`),
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Thinking == nil || result.Thinking.GetBudgetTokens() != 3333 {
		t.Errorf("BudgetTokens = %v, want overridden to 3333 by reasoning field", result.Thinking)
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningFieldZeroMaxTokensDoesNotOverride(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:           "claude-3-opus-20240229",
		ReasoningEffort: "medium",                             // sets 2048
		Reasoning:       json.RawMessage(`{"effort":"high"}`), // MaxTokens absent -> 0
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Thinking == nil || result.Thinking.GetBudgetTokens() != 2048 {
		t.Errorf("BudgetTokens = %v, want unchanged 2048 (reasoning.max_tokens=0 must not override)", result.Thinking)
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningFieldMalformedJSON_ReturnsError(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		Reasoning: json.RawMessage(`{not-json`),
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}
	_, err := RequestOpenAI2ClaudeMessage(c, req)
	if err == nil {
		t.Fatal("expected error for malformed reasoning JSON")
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: stop sequences
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_StopSequenceString(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Stop:     "STOP_HERE",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.StopSequences) != 1 || result.StopSequences[0] != "STOP_HERE" {
		t.Errorf("StopSequences = %+v, want [STOP_HERE]", result.StopSequences)
	}
}

func TestRequestOpenAI2ClaudeMessage_StopSequenceArray(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Stop:     []interface{}{"A", "B"},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.StopSequences) != 2 || result.StopSequences[0] != "A" || result.StopSequences[1] != "B" {
		t.Errorf("StopSequences = %+v, want [A B]", result.StopSequences)
	}
}

func TestRequestOpenAI2ClaudeMessage_StopSequenceEmptyArray(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Stop:     []interface{}{},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopSequences == nil {
		t.Fatal("expected StopSequences to be a non-nil empty slice for empty array input")
	}
	if len(result.StopSequences) != 0 {
		t.Errorf("StopSequences = %+v, want empty", result.StopSequences)
	}
}

func TestRequestOpenAI2ClaudeMessage_StopNilLeavesStopSequencesNil(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopSequences != nil {
		t.Errorf("StopSequences = %+v, want nil", result.StopSequences)
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: tools / web search / tool_choice
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_ToolsConverted(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "get_weather",
					Description: "Gets the weather",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
						"required":             []any{"city"},
						"additionalProperties": false,
					},
				},
			},
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := result.Tools.([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("Tools = %+v, want single converted tool", result.Tools)
	}
	tool, ok := tools[0].(*dto.Tool)
	if !ok {
		t.Fatalf("tool = %T, want *dto.Tool", tools[0])
	}
	// a schema key other than type/properties/required (e.g.
	// additionalProperties) must be copied through verbatim.
	if v, ok := tool.InputSchema["additionalProperties"]; !ok || v != false {
		t.Errorf("InputSchema[additionalProperties] = %v (present=%v), want false", v, ok)
	}
	if tool.Name != "get_weather" || tool.Description != "Gets the weather" {
		t.Errorf("tool = %+v, want Name=get_weather Description='Gets the weather'", tool)
	}
	if tool.InputSchema["type"] != "object" {
		t.Errorf("InputSchema[type] = %v, want object", tool.InputSchema["type"])
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolsWithNonMapParametersAreSkipped(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:       "bad_tool",
					Parameters: "not-a-map", // wrong type -> should be skipped
				},
			},
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := result.Tools.([]any)
	if !ok {
		t.Fatalf("Tools = %+v, want []any", result.Tools)
	}
	if len(tools) != 0 {
		t.Errorf("expected malformed tool with non-map Parameters to be skipped, got %+v", tools)
	}
}

func TestRequestOpenAI2ClaudeMessage_WebSearchOptionsLowMaxUses(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus-20240229",
		WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "low",
			UserLocation:      json.RawMessage(`{"approximate":{"timezone":"America/Los_Angeles","country":"US","region":"CA","city":"SF"}}`),
		},
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := result.Tools.([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("Tools = %+v, want single web_search tool", result.Tools)
	}
	wsTool, ok := tools[0].(*dto.ClaudeWebSearchTool)
	if !ok {
		t.Fatalf("tool = %T, want *dto.ClaudeWebSearchTool", tools[0])
	}
	if wsTool.MaxUses != WebSearchMaxUsesLow {
		t.Errorf("MaxUses = %d, want %d", wsTool.MaxUses, WebSearchMaxUsesLow)
	}
	if wsTool.UserLocation == nil || wsTool.UserLocation.City != "SF" || wsTool.UserLocation.Timezone != "America/Los_Angeles" {
		t.Errorf("UserLocation = %+v, want City=SF Timezone=America/Los_Angeles", wsTool.UserLocation)
	}
}

func TestRequestOpenAI2ClaudeMessage_WebSearchOptionsMaxUsesTiers(t *testing.T) {
	tests := []struct {
		size string
		want int
	}{
		{"medium", WebSearchMaxUsesMedium},
		{"high", WebSearchMaxUsesHigh},
		{"unrecognized", 0},
	}
	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			c := newTestGinContext()
			req := dto.GeneralOpenAIRequest{
				Model:            "claude-3-opus-20240229",
				WebSearchOptions: &dto.WebSearchOptions{SearchContextSize: tt.size},
				Messages:         []dto.Message{{Role: "user", Content: "hi"}},
			}
			result, err := RequestOpenAI2ClaudeMessage(c, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tools, _ := result.Tools.([]any)
			wsTool := tools[0].(*dto.ClaudeWebSearchTool)
			if wsTool.MaxUses != tt.want {
				t.Errorf("MaxUses = %d, want %d", wsTool.MaxUses, tt.want)
			}
		})
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolChoiceRequiredMapsToAny(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:      "claude-3-opus-20240229",
		ToolChoice: "required",
		Messages:   []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc, ok := result.ToolChoice.(*dto.ClaudeToolChoice)
	if !ok || tc.Type != "any" {
		t.Errorf("ToolChoice = %+v, want type=any", result.ToolChoice)
	}
}

func TestRequestOpenAI2ClaudeMessage_ParallelToolCallsFalseDisables(t *testing.T) {
	c := newTestGinContext()
	parallel := false
	req := dto.GeneralOpenAIRequest{
		Model:            "claude-3-opus-20240229",
		ParallelTooCalls: &parallel,
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc, ok := result.ToolChoice.(*dto.ClaudeToolChoice)
	if !ok {
		t.Fatalf("ToolChoice = %+v, want *dto.ClaudeToolChoice", result.ToolChoice)
	}
	if !tc.DisableParallelToolUse {
		t.Error("DisableParallelToolUse should be true when parallel_tool_calls=false")
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage: prompt/stream fields are cleared/passed
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_PromptAlwaysEmpty(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Prompt:   "leftover legacy prompt field",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Prompt != "" {
		t.Errorf("Prompt = %q, want empty (Claude messages mode uses Messages, not Prompt)", result.Prompt)
	}
}

func TestRequestOpenAI2ClaudeMessage_StreamFlagPropagated(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Stream:   true,
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Stream {
		t.Error("Stream should be true when propagated from source request")
	}
}

func TestRequestOpenAI2ClaudeMessage_EmptyMessagesProducesEmptySlice(t *testing.T) {
	c := newTestGinContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{},
	}
	result, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("Messages = %+v, want empty", result.Messages)
	}
}
