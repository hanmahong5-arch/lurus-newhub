package fakeupstream

import (
	"math"
	"testing"
)

func TestChoiceDistribution(t *testing.T) {
	label, probs, ok := choiceDistribution(map[string]any{"a": "plain", "b": "a cheap option", "c": "other"})
	if !ok || label != "b" {
		t.Fatalf("label=%q ok=%v, want b", label, ok)
	}
	sum := 0.0
	for _, v := range probs {
		sum += v.(float64)
	}
	if probs["b"].(float64) != 0.8 || math.Abs(sum-1) > 1e-9 {
		t.Fatalf("distribution %v sums to %v", probs, sum)
	}

	// "cheap" outranks "vague" when both appear; label order breaks ties inside a keyword.
	label, probs, _ = choiceDistribution(map[string]any{"a": "vague", "b": "cheap", "c": "cheap"})
	if label != "b" || probs["b"].(float64) != 0.8 {
		t.Fatalf("label=%q probs=%v", label, probs)
	}

	// No keyword: the caller keeps the historical single-label answer.
	if _, _, ok := choiceDistribution(map[string]any{"a": "x", "b": "y"}); ok {
		t.Fatal("a keyword-free criteria set must not be steered")
	}
	// A single option is certain whatever the keyword.
	if label, probs, ok := choiceDistribution(map[string]any{"only": "cheap"}); !ok || label != "only" || probs["only"].(float64) != 1.0 {
		t.Fatalf("single option: %q %v %v", label, probs, ok)
	}
}
