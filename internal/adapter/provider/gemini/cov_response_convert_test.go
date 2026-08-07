package gemini

import (
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// responseGeminiChat2OpenAI (non-streaming Gemini -> OpenAI conversion)
// ---------------------------------------------------------------------------

func TestResponseGeminiChat2OpenAI_EmptyCandidates(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{}}
	got := responseGeminiChat2OpenAI(c, resp)
	if len(got.Choices) != 0 {
		t.Errorf("len(Choices) = %d, want 0 for empty candidates", len(got.Choices))
	}
	if got.Object != "chat.completion" {
		t.Errorf("Object = %q, want chat.completion", got.Object)
	}
}

func TestResponseGeminiChat2OpenAI_PlainTextCandidate(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	finish := "STOP"
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Index:        0,
				FinishReason: &finish,
				Content: dto.GeminiChatContent{
					Parts: []dto.GeminiPart{{Text: "Hello there"}},
				},
			},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	if len(got.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(got.Choices))
	}
	choice := got.Choices[0]
	if choice.FinishReason != constant.FinishReasonStop {
		t.Errorf("FinishReason = %q, want %q", choice.FinishReason, constant.FinishReasonStop)
	}
	if choice.Message.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", choice.Message.Role)
	}
	if choice.Message.StringContent() != "Hello there" {
		t.Errorf("Content = %q, want %q", choice.Message.StringContent(), "Hello there")
	}
}

func TestResponseGeminiChat2OpenAI_FinishReasonMapping(t *testing.T) {
	tests := []struct {
		geminiReason string
		want         string
	}{
		{"STOP", constant.FinishReasonStop},
		{"MAX_TOKENS", constant.FinishReasonLength},
		{"SAFETY", constant.FinishReasonContentFilter},
		{"RECITATION", constant.FinishReasonContentFilter},
		{"OTHER", constant.FinishReasonContentFilter},
	}
	for _, tt := range tests {
		t.Run(tt.geminiReason, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			resp := &dto.GeminiChatResponse{
				Candidates: []dto.GeminiChatCandidate{
					{FinishReason: &tt.geminiReason, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
				},
			}
			got := responseGeminiChat2OpenAI(c, resp)
			if got.Choices[0].FinishReason != tt.want {
				t.Errorf("FinishReason(%q) = %q, want %q", tt.geminiReason, got.Choices[0].FinishReason, tt.want)
			}
		})
	}
}

func TestResponseGeminiChat2OpenAI_NilFinishReason_DefaultsToStop(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	if got.Choices[0].FinishReason != constant.FinishReasonStop {
		t.Errorf("FinishReason = %q, want default %q", got.Choices[0].FinishReason, constant.FinishReasonStop)
	}
}

func TestResponseGeminiChat2OpenAI_InlineDataImage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "abc123"}},
			}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	content := got.Choices[0].Message.StringContent()
	want := "![image](data:image/png;base64,abc123)"
	if content != want {
		t.Errorf("Content = %q, want %q", content, want)
	}
}

func TestResponseGeminiChat2OpenAI_InlineDataNonImageMedia(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "zzz"}},
			}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	content := got.Choices[0].Message.StringContent()
	want := "[media](data:audio/wav;base64,zzz)"
	if content != want {
		t.Errorf("Content = %q, want %q", content, want)
	}
}

func TestResponseGeminiChat2OpenAI_FunctionCall_SetsToolCallsAndFinishReason(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	stopReason := "STOP" // even though upstream says STOP, presence of a function call must win
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				FinishReason: &stopReason,
				Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
					{FunctionCall: &dto.FunctionCall{FunctionName: "search", Arguments: map[string]interface{}{"q": "cats"}}},
				}},
			},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	choice := got.Choices[0]
	if choice.FinishReason != constant.FinishReasonToolCalls {
		t.Errorf("FinishReason = %q, want %q (tool call must override STOP)", choice.FinishReason, constant.FinishReasonToolCalls)
	}
	toolCalls := choice.Message.ParseToolCalls()
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "search" {
		t.Errorf("ToolCalls = %+v, want single 'search' call", toolCalls)
	}
}

func TestResponseGeminiChat2OpenAI_ThoughtPart_SetsReasoningContent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "because X causes Y", Thought: true},
			}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	if got.Choices[0].Message.ReasoningContent != "because X causes Y" {
		t.Errorf("ReasoningContent = %q, want %q", got.Choices[0].Message.ReasoningContent, "because X causes Y")
	}
	if got.Choices[0].Message.StringContent() != "" {
		t.Errorf("Content = %q, want empty (thought text must not leak into content)", got.Choices[0].Message.StringContent())
	}
}

func TestResponseGeminiChat2OpenAI_ExecutableCodeAndResult(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "python", Code: "print(1)"}},
				{CodeExecutionResult: &dto.GeminiPartCodeExecutionResult{Output: "1"}},
			}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	content := got.Choices[0].Message.StringContent()
	wantCode := "```python\nprint(1)\n```"
	wantOutput := "```output\n1\n```"
	if content != wantCode+"\n"+wantOutput {
		t.Errorf("Content = %q, want code block followed by output block", content)
	}
}

func TestResponseGeminiChat2OpenAI_FiltersLoneNewlineText(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "line1"},
				{Text: "\n"},
				{Text: "line2"},
			}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	content := got.Choices[0].Message.StringContent()
	if content != "line1\nline2" {
		t.Errorf("Content = %q, want %q (lone-newline part filtered, joined with \\n)", content, "line1\nline2")
	}
}

func TestResponseGeminiChat2OpenAI_MultipleCandidates_IndexPreserved(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Index: 0, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "a"}}}},
			{Index: 1, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "b"}}}},
		},
	}
	got := responseGeminiChat2OpenAI(c, resp)
	if len(got.Choices) != 2 {
		t.Fatalf("len(Choices) = %d, want 2", len(got.Choices))
	}
	if got.Choices[0].Index != 0 || got.Choices[1].Index != 1 {
		t.Errorf("Indices = [%d,%d], want [0,1]", got.Choices[0].Index, got.Choices[1].Index)
	}
}

// ---------------------------------------------------------------------------
// streamResponseGeminiChat2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseGeminiChat2OpenAI_StopClearsFinishReason(t *testing.T) {
	stop := "STOP"
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{FinishReason: &stop, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}},
		},
	}
	got, isStop := streamResponseGeminiChat2OpenAI(resp)
	if !isStop {
		t.Error("isStop = false, want true for STOP finishReason")
	}
	// candidate.FinishReason is nilled out on the candidate BEFORE computing
	// choice.FinishReason from it, so the stream chunk itself carries no
	// finish_reason (the terminal marker is emitted separately by the caller).
	if got.Choices[0].FinishReason != nil {
		t.Errorf("FinishReason = %v, want nil (STOP is signaled via isStop, not inline)", *got.Choices[0].FinishReason)
	}
}

func TestStreamResponseGeminiChat2OpenAI_NonStopFinishReasonKept(t *testing.T) {
	maxTok := "MAX_TOKENS"
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{FinishReason: &maxTok, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}},
		},
	}
	got, isStop := streamResponseGeminiChat2OpenAI(resp)
	if isStop {
		t.Error("isStop = true, want false for MAX_TOKENS")
	}
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != constant.FinishReasonLength {
		t.Fatalf("FinishReason = %v, want %q", got.Choices[0].FinishReason, constant.FinishReasonLength)
	}
}

func TestStreamResponseGeminiChat2OpenAI_SafetyMapsToContentFilter(t *testing.T) {
	safety := "SAFETY"
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{FinishReason: &safety, Content: dto.GeminiChatContent{}},
		},
	}
	got, isStop := streamResponseGeminiChat2OpenAI(resp)
	if isStop {
		t.Error("isStop = true, want false for SAFETY")
	}
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != constant.FinishReasonContentFilter {
		t.Fatalf("FinishReason = %v, want %q", got.Choices[0].FinishReason, constant.FinishReasonContentFilter)
	}
}

func TestStreamResponseGeminiChat2OpenAI_ToolCallDelta(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "f1", Arguments: map[string]interface{}{}}},
				{FunctionCall: &dto.FunctionCall{FunctionName: "f2", Arguments: map[string]interface{}{}}},
			}}},
		},
	}
	got, _ := streamResponseGeminiChat2OpenAI(resp)
	choice := got.Choices[0]
	if choice.FinishReason == nil || *choice.FinishReason != constant.FinishReasonToolCalls {
		t.Fatalf("FinishReason = %v, want %q", choice.FinishReason, constant.FinishReasonToolCalls)
	}
	if len(choice.Delta.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(choice.Delta.ToolCalls))
	}
	// tool call index is set incrementally (0, 1, ...) for delta streaming
	if choice.Delta.ToolCalls[0].Index == nil || *choice.Delta.ToolCalls[0].Index != 0 {
		t.Errorf("ToolCalls[0].Index = %v, want 0", choice.Delta.ToolCalls[0].Index)
	}
	if choice.Delta.ToolCalls[1].Index == nil || *choice.Delta.ToolCalls[1].Index != 1 {
		t.Errorf("ToolCalls[1].Index = %v, want 1", choice.Delta.ToolCalls[1].Index)
	}
}

func TestStreamResponseGeminiChat2OpenAI_ThoughtDelta(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "thinking...", Thought: true},
			}}},
		},
	}
	got, _ := streamResponseGeminiChat2OpenAI(resp)
	delta := got.Choices[0].Delta
	if delta.GetReasoningContent() != "thinking..." {
		t.Errorf("ReasoningContent = %q, want %q", delta.GetReasoningContent(), "thinking...")
	}
	if delta.GetContentString() != "" {
		t.Errorf("Content = %q, want empty for thought-only delta", delta.GetContentString())
	}
}

func TestStreamResponseGeminiChat2OpenAI_InlineImageDelta(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/jpeg", Data: "xyz"}},
			}}},
		},
	}
	got, _ := streamResponseGeminiChat2OpenAI(resp)
	want := "![image](data:image/jpeg;base64,xyz)"
	if got.Choices[0].Delta.GetContentString() != want {
		t.Errorf("Content = %q, want %q", got.Choices[0].Delta.GetContentString(), want)
	}
}

func TestStreamResponseGeminiChat2OpenAI_InlineNonImageMediaDropped(t *testing.T) {
	// unlike the non-streaming converter, the streaming path only emits
	// image-prefixed inline data; non-image media produces no text at all.
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "zzz"}},
			}}},
		},
	}
	got, _ := streamResponseGeminiChat2OpenAI(resp)
	if got.Choices[0].Delta.GetContentString() != "" {
		t.Errorf("Content = %q, want empty (non-image inline data dropped in stream path)", got.Choices[0].Delta.GetContentString())
	}
}

func TestStreamResponseGeminiChat2OpenAI_EmptyCandidates(t *testing.T) {
	resp := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{}}
	got, isStop := streamResponseGeminiChat2OpenAI(resp)
	if isStop {
		t.Error("isStop = true, want false for empty candidates")
	}
	if len(got.Choices) != 0 {
		t.Errorf("len(Choices) = %d, want 0", len(got.Choices))
	}
	if got.Object != "chat.completion.chunk" {
		t.Errorf("Object = %q, want chat.completion.chunk", got.Object)
	}
}

func TestStreamResponseGeminiChat2OpenAI_ExecutableCodeDelta(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "go", Code: "fmt.Println(1)"}},
			}}},
		},
	}
	got, _ := streamResponseGeminiChat2OpenAI(resp)
	want := "```go\nfmt.Println(1)\n```\n"
	if got.Choices[0].Delta.GetContentString() != want {
		t.Errorf("Content = %q, want %q", got.Choices[0].Delta.GetContentString(), want)
	}
}
