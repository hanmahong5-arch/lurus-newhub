package ai360

import (
	"testing"
)

// TestChannelName asserts the exact channel name constant used for routing
// and display purposes.
func TestChannelName(t *testing.T) {
	if ChannelName != "ai360" {
		t.Fatalf("ChannelName = %q, want %q", ChannelName, "ai360")
	}
}

// TestModelList asserts the exact contents (length, order, and each element)
// of the supported model list for this vendor adapter.
func TestModelList(t *testing.T) {
	want := []string{
		"360gpt-turbo",
		"360gpt-turbo-responsibility-8k",
		"360gpt-pro",
		"360gpt2-pro",
		"360GPT_S2_V9",
		"embedding-bert-512-v1",
		"embedding_s1_v1",
		"semantic_similarity_s1_v1",
	}

	if len(ModelList) != len(want) {
		t.Fatalf("len(ModelList) = %d, want %d", len(ModelList), len(want))
	}

	for i, w := range want {
		if ModelList[i] != w {
			t.Errorf("ModelList[%d] = %q, want %q", i, ModelList[i], w)
		}
	}
}

// TestModelListNoDuplicates ensures every model name appears exactly once,
// guarding against accidental copy-paste duplication in the vendor list.
func TestModelListNoDuplicates(t *testing.T) {
	seen := make(map[string]int, len(ModelList))
	for _, m := range ModelList {
		seen[m]++
	}
	for m, count := range seen {
		if count != 1 {
			t.Errorf("model %q appears %d times in ModelList, want 1", m, count)
		}
	}
}
