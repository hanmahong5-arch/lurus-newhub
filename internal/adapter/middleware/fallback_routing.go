package middleware

// fallback_routing.go - opt-in cross-provider fallback glue (the "fallback"
// strategy of routing_policies, internal/app/routingfallback).
//
// CALL SITE: ApplyDecisionRouting (decision_routing.go) hands every request
// whose model has no enabled DECISION policy to applyFallbackRouting, so the
// distributor's existing single hook covers both strategies. The position is
// therefore identical to decision routing: after the token model-limit check
// and BEFORE channel selection, so the rewritten model is the one selection,
// quota pricing and logging all see.
//
// ELIGIBILITY is decision routing's, by construction (routingdecision.Check):
// non-first-turn, tools / tool_choice, previous_response_id, session affinity,
// reasoning models and pin:/audit:true metadata are never rewritten. A model
// swap under an in-flight tool loop or an audited run is a correctness or
// compliance bug whatever the trigger.
//
// CLOSED STATE: with no enabled fallback policy for (tenant, model) the cost
// is the policy index's one atomic load and one map miss - no database access,
// no channel-cache read, no header, no metric, no body parsing.

import (
	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"
	"github.com/LurusTech/lurus-hub/internal/app/routingfallback"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// fallbackReasonPrefix namespaces X-Routing-Reason so a reader can tell the
// two strategies apart ("decision:applied" vs "fallback:no_channel").
const fallbackReasonPrefix = "fallback:"

// applyFallbackRouting consults the tenant's enabled fallback policy for
// modelRequest.Model, if any. It never fails the request: every path either
// rewrites to a healthy candidate or leaves the request exactly as sent, and
// says which in X-Routing-Reason.
func applyFallbackRouting(c *gin.Context, modelRequest *ModelRequest, tenantID string) {
	pol := routingdecision.LookupFallback(tenantID, modelRequest.Model)
	if pol == nil {
		return
	}
	requested := modelRequest.Model

	body, err := common.GetRequestBody(c)
	if err != nil {
		finishFallback(c, modelRequest, requested, routingfallback.Outcome{Reason: routingfallback.ReasonIneligible}, pol, routingdecision.WhyBody)
		return
	}
	verdict := routingdecision.Check(c.Request.URL.Path, body, routingdecision.Flags{
		SessionAffinity: sessionAffinityRawID(c, modelRequest) != "",
	})
	if !verdict.OK {
		finishFallback(c, modelRequest, requested, routingfallback.Outcome{Reason: routingfallback.ReasonIneligible}, pol, verdict.Why)
		return
	}

	out := routingfallback.Resolve(pol, requested,
		routingfallback.RequestChannels(c, tenantID),
		func(model string) bool { return modelPermitted(c, tenantID, model) })
	finishFallback(c, modelRequest, requested, out, pol, "")
}

// finishFallback rewrites the request (when the outcome names a model),
// announces the decision, counts it and audits it.
func finishFallback(c *gin.Context, mr *ModelRequest, requested string, out routingfallback.Outcome, pol *entity.RoutingPolicy, why string) {
	final := requested
	reason := out.Reason
	targetID := ""
	if out.Target != "" && out.Target != requested {
		if err := rewriteRequestModel(c, out.Target); err != nil {
			// The body could not be rewritten: the request continues as sent.
			// Say so honestly instead of claiming a route that did not happen.
			common.SysLog("routing: fallback body rewrite failed, request left unchanged: " + err.Error())
			reason = routingfallback.ReasonNoCandidate
		} else {
			final = out.Target
			targetID = out.TargetID
			mr.Model = final
		}
	}
	if why != "" {
		c.Set("routing_ineligible_why", why) // for request logs; never sent to the caller
	}
	c.Header(HeaderRoutedModel, final)
	c.Header(HeaderRoutingReason, fallbackReasonPrefix+reason)
	// Same three facts as the headers on the main request's consume row
	// (other.routed), so a log reader can tell a rerouted row from a direct one.
	c.Set(ContextKeyRoutedDecision, map[string]any{
		"requested": requested,
		"model":     final,
		"reason":    fallbackReasonPrefix + reason,
	})
	routingfallback.Record(reason)

	// primary_ok and ineligible are the bulk of an opted-in tenant's traffic
	// and carry nothing the header and counter do not; only an actual
	// reroute, or a wanted-but-impossible one, is audited.
	if reason == routingfallback.ReasonNoChannel || reason == routingfallback.ReasonPrimaryUnavailable || reason == routingfallback.ReasonNoCandidate {
		recordFallbackAudit(c, requested, final, reason, targetID, pol)
	}
}

// fallbackAudit is the audit payload. Identifiers and the reason only: it has
// no field that can hold user text.
type fallbackAudit struct {
	Strategy    string `json:"strategy"`
	Reason      string `json:"reason"`
	Requested   string `json:"requested_model"`
	Routed      string `json:"routed_model"`
	CandidateID string `json:"candidate_id,omitempty"`
}

func recordFallbackAudit(c *gin.Context, requested, final, reason, candidateID string, pol *entity.RoutingPolicy) {
	details, err := json.Marshal(fallbackAudit{
		Strategy: entity.RoutingStrategyFallback, Reason: reason,
		Requested: requested, Routed: final, CandidateID: candidateID,
	})
	if err != nil {
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, c.GetInt("id"),
		governance.ActionRoutingDecision, governance.ResourceRoutingPolicy, int(pol.ID), string(details)))
}
