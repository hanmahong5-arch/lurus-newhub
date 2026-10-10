package routingdecision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

// Reasons. They are the suffixes of X-Routing-Reason ("decision:<reason>") and
// the values of the lurus_routing_decision_total{reason} label.
const (
	ReasonApplied              = "applied"
	ReasonLowConfidence        = "low_confidence"
	ReasonNoPreference         = "no_preference"
	ReasonEvaluatorUnavailable = "evaluator_unavailable"
	ReasonIneligible           = "ineligible"
	ReasonSingleCandidate      = "single_candidate"
)

// Defaults of the evaluation budget. The timeout is tunable per deployment by
// ROUTING_DECISION_TIMEOUT_MS; the concurrency cap is fixed so a burst of
// routed requests cannot turn the evaluator into a thundering herd.
const (
	DefaultTimeout = 1000 * time.Millisecond
	MaxConcurrent  = 8
)

// Timeout returns the evaluation deadline from ROUTING_DECISION_TIMEOUT_MS
// (milliseconds, 1..60000), defaulting to DefaultTimeout. An unparsable or
// out-of-range value falls back to the default rather than disabling the
// bound: an unbounded evaluator call would put the whole request on the
// evaluator's latency.
func Timeout() time.Duration {
	raw := os.Getenv("ROUTING_DECISION_TIMEOUT_MS")
	if raw == "" {
		return DefaultTimeout
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 1 || ms > 60000 {
		return DefaultTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

// ErrNoEvaluatorChannel means no channel could serve the evaluator model for
// this tenant.
var ErrNoEvaluatorChannel = errors.New("routing: no channel for the evaluator model")

// ErrEvaluatorBusy means the process-wide evaluation limit was reached; the
// request proceeds on its default route instead of waiting.
var ErrEvaluatorBusy = errors.New("routing: evaluator concurrency limit reached")

// EvalInput is what the evaluator is asked. It carries the user text, so it is
// never logged or audited.
type EvalInput struct {
	TenantID     string
	Model        string // evaluator model
	Text         string
	Instructions string
	Candidates   []entity.RoutingCandidate
}

// EvalOutput is the evaluator's answer for the single routing question.
type EvalOutput struct {
	Choice        string             // a candidate id or NoPreferenceID
	Probabilities map[string]float64 // per option, includes NoPreferenceID
	InputTokens   int
	OutputTokens  int
	ChannelID     int
	ChannelType   int
	Group         string
	UpstreamModel string
}

// Evaluator runs one evaluation. Implementations must honour ctx's deadline.
type Evaluator interface {
	Evaluate(ctx context.Context, c *gin.Context, in EvalInput) (*EvalOutput, error)
}

// Outcome is the result of a decision. Target is the model to route to;
// "" means "leave the request as the caller sent it".
type Outcome struct {
	Reason        string
	Target        string
	TargetID      string
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
	Eval          *EvalOutput // nil when the evaluator did not answer
	Latency       time.Duration
	Err           error // evaluator failure, for logs only
}

// limiter bounds in-flight evaluations process-wide. Non-blocking by design:
// a full limiter means the evaluator is already busy, and waiting would add
// latency to a request whose fallback (the default candidate) is free.
var limiter = make(chan struct{}, MaxConcurrent)

func tryAcquire() bool {
	select {
	case limiter <- struct{}{}:
		return true
	default:
		return false
	}
}

func release() { <-limiter }

// Decide runs the decision for an eligible request. cands are the policy's
// candidates already narrowed to what the caller may use (the token's model
// limit). Failures never propagate: they become evaluator_unavailable with the
// default target.
func Decide(c *gin.Context, ev Evaluator, p *entity.RoutingPolicy, requested, text string, cands []entity.RoutingCandidate) Outcome {
	def := DefaultModel(requested, p.DefaultCandidate, cands)

	if len(cands) == 0 {
		return Outcome{Reason: ReasonIneligible}
	}
	if len(cands) == 1 {
		// Nothing to choose between: send the request to the only allowed
		// candidate without paying for an evaluation.
		return Outcome{Reason: ReasonSingleCandidate, Target: cands[0].Model, TargetID: cands[0].ID}
	}
	if ev == nil {
		return Outcome{Reason: ReasonEvaluatorUnavailable, Target: def, TargetID: idOf(cands, def), Err: ErrNoEvaluatorChannel}
	}
	if !tryAcquire() {
		return Outcome{Reason: ReasonEvaluatorUnavailable, Target: def, TargetID: idOf(cands, def), Err: ErrEvaluatorBusy}
	}
	defer release()

	parent := context.Background()
	if c != nil && c.Request != nil {
		parent = c.Request.Context() // a caller that hangs up cancels its own evaluation
	}
	ctx, cancel := context.WithTimeout(parent, Timeout())
	defer cancel()
	start := time.Now()
	out, err := safeEvaluate(ctx, ev, c, EvalInput{
		TenantID:     p.TenantID,
		Model:        p.EvaluatorModel,
		Text:         text,
		Instructions: p.Instructions,
		Candidates:   cands,
	})
	lat := time.Since(start)
	if err != nil || out == nil {
		return Outcome{Reason: ReasonEvaluatorUnavailable, Target: def, TargetID: idOf(cands, def), Latency: lat, Err: err}
	}

	res := Outcome{Choice: out.Choice, Probabilities: out.Probabilities, Eval: out, Latency: lat}
	if out.Choice == NoPreferenceID {
		res.Reason, res.Target, res.TargetID = ReasonNoPreference, def, idOf(cands, def)
		res.Confidence = out.Probabilities[NoPreferenceID]
		return res
	}
	var chosen *entity.RoutingCandidate
	for i := range cands {
		if cands[i].ID == out.Choice {
			chosen = &cands[i]
			break
		}
	}
	if chosen == nil {
		// The evaluator named something we never offered: treat the answer as
		// unusable rather than guessing.
		res.Reason, res.Target, res.TargetID = ReasonEvaluatorUnavailable, def, idOf(cands, def)
		res.Err = errors.New("routing: evaluator chose an unknown option")
		return res
	}
	res.Confidence = out.Probabilities[out.Choice]
	if res.Confidence < p.MinConfidence {
		res.Reason, res.Target, res.TargetID = ReasonLowConfidence, def, idOf(cands, def)
		return res
	}
	res.Reason, res.Target, res.TargetID = ReasonApplied, chosen.Model, chosen.ID
	return res
}

func idOf(cands []entity.RoutingCandidate, model string) string {
	for _, c := range cands {
		if c.Model == model {
			return c.ID
		}
	}
	return ""
}

// safeEvaluate converts an evaluator panic into an error. Routing is an
// optimisation layered in front of the relay: a bug in it must degrade to the
// default route, never turn the caller's request into a 500.
func safeEvaluate(ctx context.Context, ev Evaluator, c *gin.Context, in EvalInput) (out *EvalOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("routing: evaluator panicked: %v", r)
		}
	}()
	return ev.Evaluate(ctx, c, in)
}
