package middleware

// decision_routing.go - opt-in decision-model routing glue (migration 054,
// internal/app/routingdecision).
//
// EXPECTED CALL SITE (to be wired in Distribute() by the integrator; this file
// deliberately does not edit distributor.go):
//
//	inside the `else` branch that selects a channel (the one that is NOT the
//	sk-<key>-<channelId> pin), AFTER the token model-limit check
//	(`if modelLimitEnable { ... }`) and BEFORE `if shouldSelectChannel {`
//	runs channel selection:
//
//	    if shouldSelectChannel && modelRequest != nil {
//	        if tc, terr := GetTenantContext(c); terr == nil && tc != nil {
//	            ApplyDecisionRouting(c, modelRequest, tc.TenantID)
//	        }
//	    }
//
// PRECONDITIONS the call relies on:
//   - modelRequest came from getModelRequest(c), so the request body is cached
//     under common.KeyRequestBody and modelRequest.Model is the model the
//     caller asked for;
//   - the tenant allow-list and the token model limit have already passed for
//     that REQUESTED model. This file re-applies both to every rewrite target
//     (a rewrite must never reach a model the caller could not have asked for
//     directly), but it does not re-run them for the requested model;
//   - it runs before app.CacheGetRandomSatisfiedChannel, so the rewritten model
//     is the one selection, quota pricing and logging all see. After a rewrite
//     modelRequest.Model, the cached body's "model" and the gin key
//     "original_model" all carry the routed model.
//
// CLOSED STATE: with no enabled policy for (tenant, model) the function does
// one atomic load and one map miss (routingdecision.Lookup) and returns - no
// database access, no headers, no metrics, no body parsing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// Response headers announcing a routing decision. They are set only when a
// policy matched the request, so a tenant with no policy sees byte-identical
// responses to today.
const (
	HeaderRoutedModel   = "X-Routed-Model"
	HeaderRoutingReason = "X-Routing-Reason"
	// ContextKeyRoutedDecision is the gin key under which a policy hit leaves
	// {requested, model, reason} for the consume log (other.routed).
	ContextKeyRoutedDecision = "routing_decision"
)

// decisionEvaluator is the evaluation backend; tests substitute a fake.
var decisionEvaluator routingdecision.Evaluator = routingdecision.ChannelEvaluator{}

// ApplyDecisionRouting evaluates and applies the tenant's decision-routing
// policy for modelRequest.Model, if one is enabled and the request is
// eligible. It never fails the request: every error path degrades to the
// default route and says so in X-Routing-Reason.
func ApplyDecisionRouting(c *gin.Context, modelRequest *ModelRequest, tenantID string) {
	if modelRequest == nil || modelRequest.Model == "" || tenantID == "" || c == nil || c.Request == nil {
		return
	}
	// Other wire formats are not this feature's business: skip silently, with
	// no header or metric, before any policy lookup.
	if !routingdecision.SupportedPath(c.Request.URL.Path) {
		return
	}
	pol := routingdecision.Lookup(tenantID, modelRequest.Model)
	if pol == nil {
		return
	}
	requested := modelRequest.Model

	body, err := common.GetRequestBody(c)
	if err != nil {
		finishDecision(c, modelRequest, requested, routingdecision.Outcome{Reason: routingdecision.ReasonIneligible}, pol, routingdecision.WhyBody, false)
		return
	}
	verdict := routingdecision.Check(c.Request.URL.Path, body, routingdecision.Flags{
		SessionAffinity: sessionAffinityRawID(c, modelRequest) != "",
	})
	if !verdict.OK {
		finishDecision(c, modelRequest, requested, routingdecision.Outcome{Reason: routingdecision.ReasonIneligible}, pol, verdict.Why, false)
		return
	}

	// A rewrite target must be a model this caller could have asked for.
	cands := make([]entity.RoutingCandidate, 0, len(pol.Candidates))
	for _, cd := range pol.Candidates {
		if cd.Model == requested || modelPermitted(c, tenantID, cd.Model) {
			cands = append(cands, cd)
		}
	}

	out := routingdecision.Decide(c, decisionEvaluator, pol, requested, verdict.UserText, cands)
	finishDecision(c, modelRequest, requested, out, pol, "", true)
}

// finishDecision rewrites the request (when the outcome names a different
// model), announces the decision, records the metric, bills the evaluation and
// audits. evaluated is false for outcomes decided before any evaluator call.
func finishDecision(c *gin.Context, mr *ModelRequest, requested string, out routingdecision.Outcome, pol *entity.RoutingPolicy, why string, evaluated bool) {
	final := requested
	reason := out.Reason
	if out.Target != "" && out.Target != requested {
		if err := rewriteRequestModel(c, out.Target); err != nil {
			// Could not rewrite the body: the request continues as sent. Say
			// so honestly instead of claiming a route that did not happen.
			common.SysLog("routing: body rewrite failed, request left unchanged: " + err.Error())
			reason = routingdecision.ReasonEvaluatorUnavailable
		} else {
			final = out.Target
			mr.Model = final
		}
	}
	if why != "" {
		c.Set("routing_ineligible_why", why) // for request logs; never sent to the caller
	}
	c.Header(HeaderRoutedModel, final)
	c.Header(HeaderRoutingReason, "decision:"+reason)
	// The main request's consume row carries the same three facts as the
	// headers (other.routed, app.GenerateTextOtherInfo) so a log reader can
	// tell a routed row from a direct one without the audit trail.
	c.Set(ContextKeyRoutedDecision, map[string]any{
		"requested": requested,
		"model":     final,
		"reason":    reason,
	})
	metrics.RecordRoutingDecision(reason, out.Latency)

	if out.Eval != nil {
		routingdecision.Settle(c, pol.EvaluatorModel, out.Eval, out.Latency)
	}
	// ineligible / single_candidate are decided without the evaluator, are the
	// bulk of traffic for an opted-in tenant (every tool turn), and carry
	// nothing the header and counter do not: they are not audited. Every
	// outcome that involved the evaluator is.
	if evaluated && reason != routingdecision.ReasonSingleCandidate && reason != routingdecision.ReasonIneligible {
		recordDecisionAudit(c, requested, final, reason, out, pol)
	}
}

// rewriteRequestModel makes model the request's model in the places the relay
// reads it after the distributor: the cached request body (the relay re-parses
// it), the body stream and Content-Length, and the gin key "original_model".
// The caller updates the in-flight ModelRequest itself.
func rewriteRequestModel(c *gin.Context, model string) error {
	body, err := common.GetRequestBody(c)
	if err != nil {
		return err
	}
	nb, err := routingdecision.RewriteModel(body, model)
	if err != nil {
		return err
	}
	c.Set(common.KeyRequestBody, nb)
	c.Request.Body = io.NopCloser(bytes.NewReader(nb))
	c.Request.ContentLength = int64(len(nb))
	c.Set("original_model", model)
	return nil
}

// modelPermitted applies the same gates Distribute() applied to the requested
// model to a rewrite target: the token's model limit, the platform allow-list
// (enforce mode) and the tenant's own selection.
func modelPermitted(c *gin.Context, tenantID, model string) bool {
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		if !ok {
			return false
		}
		limit, _ := s.(map[string]bool)
		if _, ok := limit[ratio_setting.FormatMatchingModelName(model)]; !ok {
			return false
		}
	}
	if list, configured, err := tenantpolicy.LoadModelAllowlist(tenantID); err == nil && configured &&
		tenantpolicy.Mode() == tenantpolicy.ModeEnforce && !tenantpolicy.ModelAllowed(list, model) {
		return false
	}
	if sel, configured, err := tenantpolicy.LoadSelection(tenantID); err == nil && configured && !tenantpolicy.ModelAllowed(sel, model) {
		return false
	}
	return true
}

// decisionAudit is the audit payload. It carries identifiers, numbers and the
// probability distribution; it has no field that can hold user text, and the
// evaluator error is reduced to a fixed kind so an upstream message never
// lands in the chain.
type decisionAudit struct {
	Reason          string             `json:"reason"`
	Requested       string             `json:"requested_model"`
	Routed          string             `json:"routed_model"`
	CandidateID     string             `json:"candidate_id,omitempty"`
	Choice          string             `json:"choice,omitempty"`
	Confidence      float64            `json:"confidence"`
	MinConfidence   float64            `json:"min_confidence"`
	Probabilities   map[string]float64 `json:"probabilities,omitempty"`
	EvaluatorModel  string             `json:"evaluator_model"`
	EvalInputTokens int                `json:"eval_input_tokens"`
	EvalOutTokens   int                `json:"eval_output_tokens"`
	LatencyMs       int64              `json:"latency_ms"`
	ErrorKind       string             `json:"error_kind,omitempty"`
}

func recordDecisionAudit(c *gin.Context, requested, final, reason string, out routingdecision.Outcome, pol *entity.RoutingPolicy) {
	d := decisionAudit{
		Reason: reason, Requested: requested, Routed: final,
		CandidateID: out.TargetID, Choice: out.Choice, Confidence: out.Confidence,
		MinConfidence: pol.MinConfidence, Probabilities: out.Probabilities,
		EvaluatorModel: pol.EvaluatorModel, LatencyMs: out.Latency.Milliseconds(),
		ErrorKind: errorKind(out.Err),
	}
	if out.Eval != nil {
		d.EvalInputTokens, d.EvalOutTokens = out.Eval.InputTokens, out.Eval.OutputTokens
	}
	details, err := json.Marshal(d)
	if err != nil {
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, c.GetInt("id"),
		governance.ActionRoutingDecision, governance.ResourceRoutingPolicy, int(pol.ID), string(details)))
}

func errorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, routingdecision.ErrEvaluatorBusy):
		return "busy"
	case errors.Is(err, routingdecision.ErrNoEvaluatorChannel):
		return "no_channel"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case strings.Contains(err.Error(), "unknown option"):
		return "invalid_choice"
	default:
		return "failed"
	}
}
