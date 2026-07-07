package reasoning

import "testing"

func TestTrimEffortSuffix(t *testing.T) {
	cases := []struct {
		name       string
		modelName  string
		wantModel  string
		wantLevel  string
		wantExists bool
	}{
		{"high suffix", "gpt-5-high", "gpt-5", "high", true},
		{"medium suffix", "gpt-5-medium", "gpt-5", "medium", true},
		{"low suffix", "gpt-5-low", "gpt-5", "low", true},
		{"minimal suffix", "gpt-5-minimal", "gpt-5", "minimal", true},
		{"no suffix", "gpt-5", "gpt-5", "", false},
		{"empty string", "", "", "", false},
		{"suffix-like but not exact match", "gpt-5-highest", "gpt-5-highest", "", false},
		{"suffix only", "-high", "", "high", true},
		{"multiple hyphens before suffix", "claude-3-opus-medium", "claude-3-opus", "medium", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotModel, gotLevel, gotExists := TrimEffortSuffix(c.modelName)
			if gotModel != c.wantModel {
				t.Errorf("model = %q, want %q", gotModel, c.wantModel)
			}
			if gotLevel != c.wantLevel {
				t.Errorf("level = %q, want %q", gotLevel, c.wantLevel)
			}
			if gotExists != c.wantExists {
				t.Errorf("exists = %v, want %v", gotExists, c.wantExists)
			}
		})
	}
}

func TestEffortSuffixesContents(t *testing.T) {
	want := []string{"-high", "-medium", "-low", "-minimal"}
	if len(EffortSuffixes) != len(want) {
		t.Fatalf("len(EffortSuffixes) = %d, want %d", len(EffortSuffixes), len(want))
	}
	for i, s := range want {
		if EffortSuffixes[i] != s {
			t.Errorf("EffortSuffixes[%d] = %q, want %q", i, EffortSuffixes[i], s)
		}
	}
}
