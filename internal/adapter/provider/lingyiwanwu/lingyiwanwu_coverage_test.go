package lingyiwanwu

import "testing"

// TestModelList asserts the exact contents of the published model list,
// since callers (channel model-list wiring, admin UI dropdowns) depend on
// the precise set and order of model names.
func TestModelList(t *testing.T) {
	want := []string{
		"yi-large", "yi-medium", "yi-vision", "yi-medium-200k", "yi-spark",
		"yi-large-rag", "yi-large-turbo", "yi-large-preview", "yi-large-rag-preview",
	}

	if len(ModelList) != len(want) {
		t.Fatalf("ModelList length = %d, want %d (list: %v)", len(ModelList), len(want), ModelList)
	}
	for i, m := range want {
		if ModelList[i] != m {
			t.Errorf("ModelList[%d] = %q, want %q", i, ModelList[i], m)
		}
	}
}

// TestChannelName asserts the exact channel identifier used for routing
// and admin display; a regression here would silently break channel lookup.
func TestChannelName(t *testing.T) {
	if ChannelName != "lingyiwanwu" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "lingyiwanwu")
	}
}
