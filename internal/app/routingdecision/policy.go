// Package routingdecision implements opt-in decision-model routing: a tenant
// policy names a public model, a set of candidate models with natural-language
// criteria, and an evaluator model; for an eligible first-turn request the
// evaluator picks a candidate and the request is rewritten to it.
//
// The ADR 2026-05-09 forbids silent cost-driven auto-routing, so everything
// here is permanent opt-in: no policy row (or enabled=false) means the request
// path does one in-memory lookup and nothing else. Every rewrite is announced
// (X-Routed-Model / X-Routing-Reason) and audited, and every failure falls
// back to the default candidate rather than failing the request.
//
// The package is pure of gin routing concerns: eligibility, validation, the
// policy index and the decision logic live here; header/body/audit glue lives
// in middleware.ApplyDecisionRouting.
package routingdecision

import (
	"fmt"
	"math"
	"regexp"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// Policy limits. They are the documented contract of the admin API and also
// bound what the evaluator is sent, so a policy can never inflate the
// evaluation request.
const (
	MaxCandidates        = 32
	MaxCriteriaBytes     = 2048
	MaxInstructionsBytes = 4096
	MaxCandidateIDLen    = 64
	MaxModelNameLen      = 128

	// NoPreferenceID is the id of the always-present "none of the above"
	// option the evaluator may answer. A candidate may not use it.
	NoPreferenceID = "no_preference"
)

var candidateIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidationError is a rejected policy. Code is a stable machine value for the
// admin API, Message the human explanation. Nothing is persisted when it is
// returned: validation is all-or-nothing.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func invalid(code, format string, args ...any) *ValidationError {
	return &ValidationError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ValidatePolicy checks a whole policy. routable reports whether a model is a
// public model the tenant can route to today (abilities visible to the
// tenant); a nil routable skips that check (unit tests of the pure rules).
func ValidatePolicy(p *entity.RoutingPolicy, routable func(model string) bool) *ValidationError {
	if p.PublicModel == "" || len(p.PublicModel) > MaxModelNameLen {
		return invalid("INVALID_MODEL", "public model must be 1-%d bytes", MaxModelNameLen)
	}
	if p.Strategy != entity.RoutingStrategyDecision {
		return invalid("INVALID_STRATEGY", "strategy must be %q", entity.RoutingStrategyDecision)
	}
	if math.IsNaN(p.MinConfidence) || p.MinConfidence < 0 || p.MinConfidence > 1 {
		return invalid("INVALID_MIN_CONFIDENCE", "min_confidence must be within [0, 1]")
	}
	if len(p.Instructions) > MaxInstructionsBytes {
		return invalid("INSTRUCTIONS_TOO_LONG", "instructions exceed %d bytes", MaxInstructionsBytes)
	}
	if len(p.EvaluatorModel) > MaxModelNameLen {
		return invalid("INVALID_EVALUATOR", "evaluator_model exceeds %d bytes", MaxModelNameLen)
	}
	if p.Enabled && p.EvaluatorModel == "" {
		return invalid("EVALUATOR_REQUIRED", "evaluator_model is required to enable a policy")
	}
	n := len(p.Candidates)
	if n < 1 || n > MaxCandidates {
		return invalid("INVALID_CANDIDATES", "candidates must contain 1-%d entries, got %d", MaxCandidates, n)
	}
	seen := make(map[string]struct{}, n)
	defaultOK := false
	publicModelCandidate := false
	for i, c := range p.Candidates {
		if c.ID == "" || len(c.ID) > MaxCandidateIDLen || !candidateIDRe.MatchString(c.ID) {
			return invalid("INVALID_CANDIDATE_ID", "candidates[%d].id must be 1-%d characters of letters, digits, '_', '.', '-'", i, MaxCandidateIDLen)
		}
		if c.ID == NoPreferenceID {
			return invalid("INVALID_CANDIDATE_ID", "candidates[%d].id %q is reserved", i, NoPreferenceID)
		}
		if _, dup := seen[c.ID]; dup {
			return invalid("DUPLICATE_CANDIDATE", "candidates[%d].id %q is duplicated", i, c.ID)
		}
		seen[c.ID] = struct{}{}
		if c.Model == "" || len(c.Model) > MaxModelNameLen {
			return invalid("INVALID_CANDIDATE_MODEL", "candidates[%d].model must be 1-%d bytes", i, MaxModelNameLen)
		}
		if routable != nil && !routable(c.Model) {
			return invalid("MODEL_NOT_ROUTABLE", "candidates[%d].model %q is not a public model this tenant can route", i, c.Model)
		}
		if c.Criteria == "" {
			return invalid("INVALID_CRITERIA", "candidates[%d].criteria is required", i)
		}
		if len(c.Criteria) > MaxCriteriaBytes {
			return invalid("CRITERIA_TOO_LONG", "candidates[%d].criteria exceeds %d bytes", i, MaxCriteriaBytes)
		}
		if c.ID == p.DefaultCandidate {
			defaultOK = true
		}
		if c.Model == p.PublicModel {
			publicModelCandidate = true
		}
	}
	if p.DefaultCandidate != "" && !defaultOK {
		return invalid("INVALID_DEFAULT_CANDIDATE", "default_candidate %q is not one of the candidate ids", p.DefaultCandidate)
	}
	if p.DefaultCandidate == "" && !publicModelCandidate {
		// With no default the fallback is the requested model itself, which
		// must then be a candidate; otherwise a failed evaluation would have
		// nowhere defined to go.
		return invalid("DEFAULT_CANDIDATE_REQUIRED", "default_candidate is required when the public model is not itself a candidate")
	}
	return nil
}

// DefaultModel resolves where a request goes when the evaluator cannot or
// will not choose. An explicit default_candidate wins; when the policy leaves
// it empty the fallback is the originally requested model, provided that
// model is itself a candidate (validation guarantees one of the two exists).
// "" means neither is available among cands (e.g. both were filtered out by
// the token's model limit), i.e. leave the request as the caller sent it.
func DefaultModel(requested, defaultCandidate string, cands []entity.RoutingCandidate) string {
	if defaultCandidate != "" {
		for _, c := range cands {
			if c.ID == defaultCandidate {
				return c.Model
			}
		}
	}
	for _, c := range cands {
		if c.Model == requested {
			return requested
		}
	}
	return ""
}
