package routingdecision

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func validPolicy() *entity.RoutingPolicy {
	return &entity.RoutingPolicy{
		TenantID:         "t1",
		PublicModel:      "smart",
		Strategy:         entity.RoutingStrategyDecision,
		Enabled:          true,
		EvaluatorModel:   "evaluator-a",
		MinConfidence:    0.65,
		DefaultCandidate: "cheap",
		Candidates: entity.RoutingCandidates{
			{ID: "cheap", Model: "model-a", Criteria: "simple, short questions"},
			{ID: "strong", Model: "model-b", Criteria: "hard, multi-step problems"},
		},
	}
}

func TestValidatePolicy(t *testing.T) {
	routable := func(m string) bool { return m == "model-a" || m == "model-b" }
	if err := ValidatePolicy(validPolicy(), routable); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	many := func() *entity.RoutingPolicy {
		p := validPolicy()
		p.Candidates = nil
		for i := 0; i < MaxCandidates+1; i++ {
			p.Candidates = append(p.Candidates, entity.RoutingCandidate{ID: "c" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Model: "model-a", Criteria: "x"})
		}
		p.DefaultCandidate = p.Candidates[0].ID
		return p
	}
	cases := []struct {
		name string
		mut  func(*entity.RoutingPolicy)
		code string
	}{
		{"no candidates", func(p *entity.RoutingPolicy) { p.Candidates = nil }, "INVALID_CANDIDATES"},
		{"duplicate id", func(p *entity.RoutingPolicy) { p.Candidates[1].ID = "cheap" }, "DUPLICATE_CANDIDATE"},
		{"reserved id", func(p *entity.RoutingPolicy) { p.Candidates[1].ID = NoPreferenceID }, "INVALID_CANDIDATE_ID"},
		{"bad id chars", func(p *entity.RoutingPolicy) { p.Candidates[1].ID = "has space" }, "INVALID_CANDIDATE_ID"},
		{"model not routable", func(p *entity.RoutingPolicy) { p.Candidates[1].Model = "model-z" }, "MODEL_NOT_ROUTABLE"},
		{"empty criteria", func(p *entity.RoutingPolicy) { p.Candidates[0].Criteria = "" }, "INVALID_CRITERIA"},
		{"criteria over 2048 bytes", func(p *entity.RoutingPolicy) { p.Candidates[0].Criteria = strings.Repeat("a", MaxCriteriaBytes+1) }, "CRITERIA_TOO_LONG"},
		{"instructions over 4096 bytes", func(p *entity.RoutingPolicy) { p.Instructions = strings.Repeat("a", MaxInstructionsBytes+1) }, "INSTRUCTIONS_TOO_LONG"},
		{"min_confidence above 1", func(p *entity.RoutingPolicy) { p.MinConfidence = 1.01 }, "INVALID_MIN_CONFIDENCE"},
		{"min_confidence below 0", func(p *entity.RoutingPolicy) { p.MinConfidence = -0.01 }, "INVALID_MIN_CONFIDENCE"},
		{"default not a candidate", func(p *entity.RoutingPolicy) { p.DefaultCandidate = "ghost" }, "INVALID_DEFAULT_CANDIDATE"},
		{"no default and public model not a candidate", func(p *entity.RoutingPolicy) { p.DefaultCandidate = "" }, "DEFAULT_CANDIDATE_REQUIRED"},
		{"enabled without evaluator", func(p *entity.RoutingPolicy) { p.EvaluatorModel = "" }, "EVALUATOR_REQUIRED"},
		{"wrong strategy", func(p *entity.RoutingPolicy) { p.Strategy = "cost" }, "INVALID_STRATEGY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPolicy()
			tc.mut(p)
			err := ValidatePolicy(p, routable)
			if err == nil || err.Code != tc.code {
				t.Fatalf("ValidatePolicy = %v, want code %s", err, tc.code)
			}
		})
	}
	t.Run("more than 32 candidates", func(t *testing.T) {
		if err := ValidatePolicy(many(), routable); err == nil || err.Code != "INVALID_CANDIDATES" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("boundary values are accepted", func(t *testing.T) {
		p := validPolicy()
		p.MinConfidence = 0
		p.Candidates[0].Criteria = strings.Repeat("a", MaxCriteriaBytes)
		p.Instructions = strings.Repeat("a", MaxInstructionsBytes)
		if err := ValidatePolicy(p, routable); err != nil {
			t.Fatalf("boundary policy rejected: %v", err)
		}
		p.MinConfidence = 1
		if err := ValidatePolicy(p, routable); err != nil {
			t.Fatalf("min_confidence 1 rejected: %v", err)
		}
	})
	t.Run("public model as candidate needs no default", func(t *testing.T) {
		p := validPolicy()
		p.PublicModel = "model-a"
		p.DefaultCandidate = ""
		if err := ValidatePolicy(p, routable); err != nil {
			t.Fatalf("rejected: %v", err)
		}
	})
	t.Run("disabled policy may lack an evaluator", func(t *testing.T) {
		p := validPolicy()
		p.Enabled, p.EvaluatorModel = false, ""
		if err := ValidatePolicy(p, routable); err != nil {
			t.Fatalf("rejected: %v", err)
		}
	})
}

func TestDefaultModel(t *testing.T) {
	cands := []entity.RoutingCandidate{{ID: "cheap", Model: "model-a"}, {ID: "strong", Model: "model-b"}}
	if got := DefaultModel("model-b", "", cands); got != "model-b" {
		t.Errorf("no default: requested model that is a candidate must win, got %q", got)
	}
	if got := DefaultModel("smart", "cheap", cands); got != "model-a" {
		t.Errorf("default candidate = %q, want model-a", got)
	}
	if got := DefaultModel("model-b", "cheap", cands); got != "model-a" {
		t.Errorf("explicit default must win over the requested model, got %q", got)
	}
	if got := DefaultModel("smart", "ghost", cands); got != "" {
		t.Errorf("default filtered out and requested not a candidate: want leave-as-is, got %q", got)
	}
}
