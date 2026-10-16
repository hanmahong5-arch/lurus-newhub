package routingdecision

import (
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func validFallbackPolicy() *entity.RoutingPolicy {
	return &entity.RoutingPolicy{
		TenantID:    "t1",
		PublicModel: "primary-a",
		Strategy:    entity.RoutingStrategyFallback,
		Enabled:     true,
		Candidates: entity.RoutingCandidates{
			{ID: "first", Model: "model-a"},
			{ID: "second", Model: "model-b", Criteria: "cheaper"},
		},
	}
}

func TestValidatePolicy_Fallback(t *testing.T) {
	routable := func(m string) bool { return m == "model-a" || m == "model-b" || m == "primary-a" }
	if err := ValidatePolicy(validFallbackPolicy(), routable); err != nil {
		t.Fatalf("valid fallback policy rejected: %v", err)
	}

	// The decision-only fields are ignored: none of them is required, and the
	// ones that are stored as given must not be policed.
	p := validFallbackPolicy()
	p.EvaluatorModel, p.Instructions, p.DefaultCandidate, p.MinConfidence = "", "", "", 7
	if err := ValidatePolicy(p, routable); err != nil {
		t.Fatalf("fallback must not require or police decision-only fields: %v", err)
	}
	p = validFallbackPolicy()
	p.EvaluatorModel, p.DefaultCandidate, p.MinConfidence = "ghost-evaluator", "ghost", -3
	if err := ValidatePolicy(p, routable); err != nil {
		t.Fatalf("fallback must ignore evaluator/default/min_confidence: %v", err)
	}

	eight := func(n int) []entity.RoutingCandidate {
		var out []entity.RoutingCandidate
		for i := 0; i < n; i++ {
			out = append(out, entity.RoutingCandidate{ID: "c" + string(rune('a'+i)), Model: "model-a"})
		}
		return out
	}
	p = validFallbackPolicy()
	p.Candidates = eight(MaxFallbackCandidates)
	if err := ValidatePolicy(p, routable); err != nil {
		t.Fatalf("exactly %d candidates must be accepted: %v", MaxFallbackCandidates, err)
	}

	cases := []struct {
		name string
		mut  func(*entity.RoutingPolicy)
		code string
	}{
		{"unknown strategy", func(p *entity.RoutingPolicy) { p.Strategy = "cost" }, "INVALID_STRATEGY"},
		{"empty strategy", func(p *entity.RoutingPolicy) { p.Strategy = "" }, "INVALID_STRATEGY"},
		{"no candidates", func(p *entity.RoutingPolicy) { p.Candidates = nil }, "INVALID_CANDIDATES"},
		{"more than eight", func(p *entity.RoutingPolicy) { p.Candidates = eight(MaxFallbackCandidates + 1) }, "INVALID_CANDIDATES"},
		{"candidate is the primary", func(p *entity.RoutingPolicy) { p.Candidates[0].Model = "primary-a" }, "INVALID_CANDIDATE_MODEL"},
		{"empty model", func(p *entity.RoutingPolicy) { p.Candidates[0].Model = "" }, "INVALID_CANDIDATE_MODEL"},
		{"model not routable", func(p *entity.RoutingPolicy) { p.Candidates[1].Model = "model-z" }, "MODEL_NOT_ROUTABLE"},
		{"duplicate id", func(p *entity.RoutingPolicy) { p.Candidates[1].ID = "first" }, "DUPLICATE_CANDIDATE"},
		{"bad id", func(p *entity.RoutingPolicy) { p.Candidates[1].ID = "has space" }, "INVALID_CANDIDATE_ID"},
		{"criteria too long", func(p *entity.RoutingPolicy) { p.Candidates[0].Criteria = strings.Repeat("a", MaxCriteriaBytes+1) }, "CRITERIA_TOO_LONG"},
		{"empty public model", func(p *entity.RoutingPolicy) { p.PublicModel = "" }, "INVALID_MODEL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validFallbackPolicy()
			tc.mut(p)
			err := ValidatePolicy(p, routable)
			if err == nil {
				t.Fatalf("expected %s, got nil", tc.code)
			}
			if err.Code != tc.code {
				t.Fatalf("code = %s (%s), want %s", err.Code, err.Message, tc.code)
			}
		})
	}
}

func TestIndex_FallbackLookupIsSeparateFromDecision(t *testing.T) {
	now := time.Unix(1000, 0)
	rows := []entity.RoutingPolicy{
		{TenantID: "t1", PublicModel: "fb", Strategy: entity.RoutingStrategyFallback, Enabled: true},
		{TenantID: "t1", PublicModel: "fb-off", Strategy: entity.RoutingStrategyFallback, Enabled: false},
		{TenantID: "t1", PublicModel: "dec", Strategy: entity.RoutingStrategyDecision, Enabled: true},
	}
	idx := NewIndex(func() ([]entity.RoutingPolicy, error) { return rows, nil }, func() time.Time { return now })

	if idx.LookupFallback("t1", "fb") == nil {
		t.Fatal("enabled fallback policy not found")
	}
	if idx.Lookup("t1", "fb") != nil {
		t.Fatal("a fallback policy must never be served to the decision hook")
	}
	if idx.LookupFallback("t1", "dec") != nil {
		t.Fatal("a decision policy must never be served to the fallback hook")
	}
	if idx.LookupFallback("t1", "fb-off") != nil || idx.LookupFallback("t2", "fb") != nil || idx.LookupFallback("", "fb") != nil || idx.LookupFallback("t1", "") != nil {
		t.Fatal("disabled / other-tenant / empty-key fallback policy matched")
	}
}
