package systemone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// Tolerances for the decision-model response contract. The upstream rounds
// probabilities for display, so exact equality would reject honest answers;
// anything beyond 0.01 means the payload is internally contradictory and a
// caller routing on it (e.g. the decision router) would act on garbage.
const (
	probabilitySumTolerance    = 0.01
	choiceProbabilityTolerance = 0.01
	// scorePerLevelTolerance scales with the number of gaps between levels: the
	// expectation of N levels spans N-1 index steps.
	scorePerLevelTolerance = 0.01
	floatEpsilon           = 1e-9
)

// wireAnswer is the subset of an answer that the contract checks cover.
type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        json.RawMessage    `json:"choice"`
	Score         json.RawMessage    `json:"score"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// validateAnswers checks the upstream answers against the contract and, when
// the originating request is known, against what was asked. A nil error means
// the answers are safe to hand to the caller. The error text names the
// offending question but never echoes upstream free text.
func validateAnswers(req *dto.SystemOneRequest, answers map[string]json.RawMessage) error {
	ids := make([]string, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// A question with no answer is tolerated: the hosted API documents
	// examples whose answer set is a subset of the request, and rejecting an
	// otherwise valid reply would cost the caller a billed 502.
	for _, id := range ids {
		var a wireAnswer
		if err := json.Unmarshal(answers[id], &a); err != nil {
			return fmt.Errorf("answer %q is malformed", id)
		}
		var q *dto.SystemOneQuestion
		if req != nil {
			rq, ok := req.Questions[id]
			if !ok {
				return fmt.Errorf("answer %q does not match any requested question", id)
			}
			q = &rq
			if a.Type != rq.Type {
				return fmt.Errorf("answer %q has type %q, the question asked for %q", id, a.Type, rq.Type)
			}
		}
		if err := validateOne(id, &a, q); err != nil {
			return err
		}
	}
	return nil
}

func validateOne(id string, a *wireAnswer, q *dto.SystemOneQuestion) error {
	if len(a.Probabilities) > 0 {
		sum := 0.0
		for k, p := range a.Probabilities {
			if math.IsNaN(p) || p < 0 || p > 1+floatEpsilon {
				return fmt.Errorf("answer %q: probability for %q is out of range", id, k)
			}
			sum += p
		}
		if math.Abs(sum-1) > probabilitySumTolerance+floatEpsilon {
			return fmt.Errorf("answer %q: probabilities sum to %.4f, want 1 +/- %.2f", id, sum, probabilitySumTolerance)
		}
	}
	switch a.Type {
	case "choice":
		return validateChoice(id, a, q)
	case "score":
		return validateScore(id, a, q)
	}
	return nil
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func validateChoice(id string, a *wireAnswer, q *dto.SystemOneQuestion) error {
	// A null/absent choice is an abstention (min_confidence not reached).
	if isNull(a.Choice) {
		return nil
	}
	var choice string
	if json.Unmarshal(a.Choice, &choice) != nil {
		return fmt.Errorf("answer %q: choice is not a string", id)
	}
	p, ok := a.Probabilities[choice]
	if !ok {
		return fmt.Errorf("answer %q: chosen option has no probability", id)
	}
	maxP := 0.0
	for _, v := range a.Probabilities {
		maxP = math.Max(maxP, v)
	}
	if p < maxP-choiceProbabilityTolerance-floatEpsilon {
		return fmt.Errorf("answer %q: chosen option has probability %.4f, below the maximum %.4f", id, p, maxP)
	}
	if q != nil {
		keys, ok := criteriaKeys(q.Criteria)
		if ok {
			if _, in := keys[choice]; !in {
				return fmt.Errorf("answer %q: chosen option is not one of the requested criteria", id)
			}
			for k := range a.Probabilities {
				if _, in := keys[k]; !in {
					return fmt.Errorf("answer %q: probabilities name an option that was not requested", id)
				}
			}
		}
	}
	return nil
}

// criteriaKeys returns the option names of an object-shaped criteria. The
// second result is false when criteria is not an object (array-shaped criteria
// are numbered by the upstream and are not name-checked).
func criteriaKeys(raw json.RawMessage) (map[string]struct{}, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out, true
}

func validateScore(id string, a *wireAnswer, q *dto.SystemOneQuestion) error {
	if q != nil {
		var levels []json.RawMessage
		if json.Unmarshal(q.Criteria, &levels) == nil && levels != nil {
			if len(a.Legend) != len(levels) {
				return fmt.Errorf("answer %q: legend has %d levels, the question defined %d", id, len(a.Legend), len(levels))
			}
			for i, raw := range levels {
				var want string
				if json.Unmarshal(raw, &want) != nil {
					continue
				}
				got, ok := a.Legend[strconv.Itoa(i)]
				if !ok || got != want {
					return fmt.Errorf("answer %q: legend level %d does not match the requested criteria", id, i)
				}
			}
		}
	}
	if isNull(a.Score) || len(a.Probabilities) == 0 {
		return nil
	}
	var score float64
	if json.Unmarshal(a.Score, &score) != nil || math.IsNaN(score) {
		return fmt.Errorf("answer %q: score is not a number", id)
	}
	expect := 0.0
	for k, p := range a.Probabilities {
		idx, err := strconv.Atoi(k)
		if err != nil {
			return fmt.Errorf("answer %q: score probabilities must be keyed by level index", id)
		}
		expect += float64(idx) * p
	}
	n := len(a.Probabilities)
	if math.Abs(score-expect) > scorePerLevelTolerance*float64(n-1)+floatEpsilon {
		return fmt.Errorf("answer %q: score %.4f disagrees with the probability-weighted level %.4f", id, score, expect)
	}
	return nil
}
