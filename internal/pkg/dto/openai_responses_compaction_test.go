package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompactionRequest_DropsUnsupportedFields is the oracle for
// wire-formats-03's subset gate at the DTO layer: decoding a raw body that
// smuggles fields dto.OpenAIResponsesRequest genuinely has but the compact
// allow-list does not forward (store/temperature/max_output_tokens — a live
// smuggled-field assertion, since these ARE real parent fields, unlike a
// made-up name that could never appear regardless of the code under test),
// plus tools/reasoning/text (decoded for client compatibility but withheld
// from the vendor per the operator ruling matching the upstream reference),
// then re-marshalling ToOpenAIResponsesRequest()'s return value, must
// produce bytes that do not contain any of those keys — only the documented
// subset survives the round trip.
func TestCompactionRequest_DropsUnsupportedFields(t *testing.T) {
	raw := `{
		"model": "gpt-4o-mini",
		"input": "hi",
		"instructions": "be terse",
		"previous_response_id": "resp_abc",
		"tools": [{"type":"web_search_preview"}],
		"parallel_tool_calls": true,
		"reasoning": {"effort":"low"},
		"service_tier": "default",
		"prompt_cache_key": "k1",
		"prompt_cache_retention": "24h",
		"text": {"format":{"type":"text"}},
		"store": false,
		"temperature": 0.9,
		"max_output_tokens": 99
	}`

	var compact OpenAIResponsesCompactionRequest
	if err := json.Unmarshal([]byte(raw), &compact); err != nil {
		t.Fatalf("unmarshal into compact DTO: %v", err)
	}

	full := compact.ToOpenAIResponsesRequest()
	out, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal converted request: %v", err)
	}
	outStr := string(out)

	// These WOULD appear if ToOpenAIResponsesRequest ever started copying
	// more than the documented subset — store/temperature/max_output_tokens
	// are real dto.OpenAIResponsesRequest fields, and tools/reasoning/text
	// are decoded on the compact type but deliberately withheld from the
	// vendor.
	for _, smuggled := range []string{"\"store\"", "0.9", "\"max_output_tokens\":99", "web_search_preview", "\"low\"", "\"format\""} {
		if strings.Contains(outStr, smuggled) {
			t.Errorf("re-encoded subset contains dropped field %q: %s", smuggled, outStr)
		}
	}

	// Sanity: the documented (forwarded) subset itself DID survive the round trip.
	for _, kept := range []string{"gpt-4o-mini", "be terse", "resp_abc", "default", "k1", "24h"} {
		if !strings.Contains(outStr, kept) {
			t.Errorf("re-encoded subset lost documented field value %q: %s", kept, outStr)
		}
	}

	if full.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini", full.Model)
	}
}

func TestCompactionRequest_GetTokenCountMetaCountsInputAndInstructions(t *testing.T) {
	compact := &OpenAIResponsesCompactionRequest{
		Model:        "gpt-4o-mini",
		Input:        json.RawMessage(`"the input text"`),
		Instructions: json.RawMessage(`"the instructions text"`),
	}
	meta := compact.GetTokenCountMeta()
	if !strings.Contains(meta.CombineText, "the input text") {
		t.Errorf("CombineText = %q, want to contain Input", meta.CombineText)
	}
	if !strings.Contains(meta.CombineText, "the instructions text") {
		t.Errorf("CombineText = %q, want to contain Instructions", meta.CombineText)
	}
}

func TestCompactionRequest_IsStreamAlwaysFalse(t *testing.T) {
	compact := &OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini"}
	if compact.IsStream(nil) {
		t.Error("IsStream(nil) = true, want false — compact never streams")
	}
}

func TestCompactionRequest_SetModelName(t *testing.T) {
	compact := &OpenAIResponsesCompactionRequest{Model: "old"}
	compact.SetModelName("new-model")
	if compact.Model != "new-model" {
		t.Errorf("Model = %q, want new-model", compact.Model)
	}
	compact.SetModelName("")
	if compact.Model != "new-model" {
		t.Errorf("SetModelName(\"\") changed Model to %q, want unchanged new-model", compact.Model)
	}
}
