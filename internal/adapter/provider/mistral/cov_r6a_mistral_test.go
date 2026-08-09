package mistral

import (
	"encoding/json"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// TestR6AMistral_DuplicateNonConformingToolCallIds_WithinSameMessage_RewrittenToSameId
// covers the idMap-reuse branch inside the tool_calls loop itself (as opposed
// to reuse across a later message's tool_call_id, which is already covered
// elsewhere): when a single assistant message carries two tool_calls that
// share the same non-conforming upstream id, Mistral's validator would
// reject a mismatched pair of duplicate rewrites, so both occurrences must
// be rewritten to the identical replacement id.
func TestR6AMistral_DuplicateNonConformingToolCallIds_WithinSameMessage_RewrittenToSameId(t *testing.T) {
	toolCalls := []dto.ToolCallRequest{
		{ID: "dup_call_id_not_conforming", Type: "function", Function: dto.FunctionRequest{Name: "get_weather"}},
		{ID: "dup_call_id_not_conforming", Type: "function", Function: dto.FunctionRequest{Name: "get_time"}},
	}
	tcJSON, _ := json.Marshal(toolCalls)
	assistantMsg := dto.Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: json.RawMessage(tcJSON),
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{assistantMsg},
	}

	got := requestOpenAI2Mistral(req)

	rewritten := got.Messages[0].ParseToolCalls()
	if len(rewritten) != 2 {
		t.Fatalf("expected 2 tool calls preserved, got %d", len(rewritten))
	}
	if rewritten[0].ID == "dup_call_id_not_conforming" || rewritten[1].ID == "dup_call_id_not_conforming" {
		t.Fatal("non-conforming ids must be rewritten")
	}
	if rewritten[0].ID != rewritten[1].ID {
		t.Errorf("both occurrences of the same source id must rewrite to the SAME new id: got %q and %q", rewritten[0].ID, rewritten[1].ID)
	}
	if !mistralToolCallIdRegexp.MatchString(rewritten[0].ID) {
		t.Errorf("rewritten id %q does not match Mistral's required ^[a-zA-Z0-9]{9}$ pattern", rewritten[0].ID)
	}
}
