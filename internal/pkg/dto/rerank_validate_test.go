package dto

import "testing"

func TestUsageContradictory(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		want bool
	}{
		{"all zero", Usage{}, false},
		{"total only", Usage{TotalTokens: 5}, false},
		{"consistent", Usage{PromptTokens: 5, TotalTokens: 5}, false},
		{"prompt with zero total", Usage{PromptTokens: 7}, true},
		{"negative prompt", Usage{PromptTokens: -1, TotalTokens: 5}, true},
		{"negative completion", Usage{CompletionTokens: -1, TotalTokens: 5}, true},
		{"negative total", Usage{TotalTokens: -3}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UsageContradictory(tc.u); got != tc.want {
				t.Errorf("UsageContradictory(%+v) = %v, want %v", tc.u, got, tc.want)
			}
		})
	}
}

func TestCheckRerankOrder(t *testing.T) {
	mk := func(s ...float64) []RerankResponseResult {
		out := make([]RerankResponseResult, len(s))
		for i, v := range s {
			out[i] = RerankResponseResult{Index: i, RelevanceScore: v}
		}
		return out
	}
	cases := []struct {
		name    string
		in      []RerankResponseResult
		wantErr bool
	}{
		{"nil", nil, false},
		{"single", mk(0.1), false},
		{"descending", mk(0.9, 0.5, 0.1), false},
		{"ties", mk(0.5, 0.5, 0.5), false},
		{"ascending", mk(0.1, 0.9), true},
		{"late inversion", mk(0.9, 0.5, 0.7), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckRerankOrder(tc.in); (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestStripRerankDocuments(t *testing.T) {
	r := []RerankResponseResult{{Document: "a"}, {Document: map[string]any{"text": "b"}}}
	StripRerankDocuments(r)
	for i := range r {
		if r[i].Document != nil {
			t.Errorf("result %d still has a document", i)
		}
	}
}
