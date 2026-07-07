package xinference

import (
	"encoding/json"
	"testing"
)

func TestModelList(t *testing.T) {
	want := []string{
		"bge-reranker-v2-m3",
		"jina-reranker-v2",
	}
	if len(ModelList) != len(want) {
		t.Fatalf("ModelList length = %d, want %d", len(ModelList), len(want))
	}
	for i, m := range want {
		if ModelList[i] != m {
			t.Errorf("ModelList[%d] = %q, want %q", i, ModelList[i], m)
		}
	}
}

func TestChannelName(t *testing.T) {
	if ChannelName != "xinference" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "xinference")
	}
}

func TestXinRerankResponseDocument_JSONRoundTrip(t *testing.T) {
	doc := XinRerankResponseDocument{
		Document:       "some text",
		Index:          3,
		RelevanceScore: 0.987,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got XinRerankResponseDocument
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if got.Document != doc.Document {
		t.Errorf("Document = %v, want %v", got.Document, doc.Document)
	}
	if got.Index != doc.Index {
		t.Errorf("Index = %d, want %d", got.Index, doc.Index)
	}
	if got.RelevanceScore != doc.RelevanceScore {
		t.Errorf("RelevanceScore = %v, want %v", got.RelevanceScore, doc.RelevanceScore)
	}

	// omitempty: zero-value Document should be omitted from the JSON output.
	zero := XinRerankResponseDocument{Index: 0, RelevanceScore: 0}
	zb, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal zero error: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(zb, &raw); err != nil {
		t.Fatalf("Unmarshal zero error: %v", err)
	}
	if _, ok := raw["document"]; ok {
		t.Errorf("expected 'document' key omitted for zero value, got raw=%s", zb)
	}
	if _, ok := raw["index"]; !ok {
		t.Errorf("expected 'index' key present, got raw=%s", zb)
	}
	if _, ok := raw["relevance_score"]; !ok {
		t.Errorf("expected 'relevance_score' key present, got raw=%s", zb)
	}
}

func TestXinRerankResponse_JSONRoundTrip(t *testing.T) {
	resp := XinRerankResponse{
		Results: []XinRerankResponseDocument{
			{Document: "a", Index: 0, RelevanceScore: 0.9},
			{Document: "b", Index: 1, RelevanceScore: 0.1},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got XinRerankResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("Results length = %d, want 2", len(got.Results))
	}
	for i, want := range resp.Results {
		if got.Results[i].Document != want.Document {
			t.Errorf("Results[%d].Document = %v, want %v", i, got.Results[i].Document, want.Document)
		}
		if got.Results[i].Index != want.Index {
			t.Errorf("Results[%d].Index = %d, want %d", i, got.Results[i].Index, want.Index)
		}
		if got.Results[i].RelevanceScore != want.RelevanceScore {
			t.Errorf("Results[%d].RelevanceScore = %v, want %v", i, got.Results[i].RelevanceScore, want.RelevanceScore)
		}
	}

	// Empty response should marshal to a results array (not null), matching
	// typical API expectations even when there are no results.
	empty := XinRerankResponse{}
	eb, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal empty error: %v", err)
	}
	var rawEmpty map[string]any
	if err := json.Unmarshal(eb, &rawEmpty); err != nil {
		t.Fatalf("Unmarshal empty error: %v", err)
	}
	if _, ok := rawEmpty["results"]; !ok {
		t.Errorf("expected 'results' key present for empty response, got raw=%s", eb)
	}
}
