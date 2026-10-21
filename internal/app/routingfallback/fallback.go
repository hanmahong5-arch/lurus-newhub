// Package routingfallback implements opt-in cross-provider fallback, the
// second routing strategy of routing_policies (strategy = "fallback").
//
// A tenant names a public model and an ORDERED list of other public models.
// While the requested model has at least one usable channel nothing happens.
// When it has none, or every one of its channels has an OPEN circuit breaker,
// the request is rewritten to the first candidate that does have a usable
// channel; if none does, the request is left alone and the ordinary 503 is
// what the caller sees. Unlike decision routing there is no evaluator: the
// choice is a pure function of the policy, the channel cache and the breaker
// state, so it costs no upstream call and no money.
//
// The ADR 2026-05-09 forbids silent cost-driven auto-routing, so the strategy
// is permanent opt-in (a policy row, born disabled) and every consulted
// request is announced (X-Routed-Model / X-Routing-Reason) and counted.
//
// This package is free of gin routing and header concerns; those live in
// middleware.applyFallbackRouting. The breaker registry lives in package
// handler, which middleware cannot import, so the breaker state arrives
// through the BreakerAllOpen seam, registered at init by handler.
package routingfallback

import (
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Reasons are the suffixes of X-Routing-Reason ("fallback:<reason>") and the
// values of the lurus_routing_fallback_total{reason} label. A closed set keeps
// the label cardinality fixed whatever tenants configure.
const (
	ReasonPrimaryOK          = "primary_ok"
	ReasonNoChannel          = "no_channel"
	ReasonPrimaryUnavailable = "primary_unavailable"
	ReasonIneligible         = "ineligible"
	ReasonNoCandidate        = "no_candidate"
)

// Reasons lists every legal reason value.
var Reasons = []string{ReasonPrimaryOK, ReasonNoChannel, ReasonPrimaryUnavailable, ReasonIneligible, ReasonNoCandidate}

// BreakerAllOpen reports whether every channel in ids currently has an open
// circuit breaker (a breaker whose cooldown has elapsed no longer counts: its
// next admission is a probe), and, when true, how long until the first of
// them may be probed. An empty ids is never "all open". nil means no breaker
// view is wired (hermetic tests, boot not finished): nothing is ever open and
// the strategy degrades to "do nothing". handler/breaker_view.go sets it.
var BreakerAllOpen func(ids []int) (bool, time.Duration)

// ChannelIDs lists the enabled channel ids that can serve (tenant, group,
// model) according to the channel cache. A variable so tests can supply a
// world without a database.
var ChannelIDs = cachedChannelIDs

var fallbackTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "lurus",
		Subsystem: "routing_fallback",
		Name:      "total",
		Help:      "Requests that consulted an enabled fallback routing policy, by outcome reason",
	},
	[]string{"reason"},
)

func init() {
	// Pre-register every series at 0 so a dashboard can tell "no request was
	// ever consulted" from "the series is not wired".
	for _, r := range Reasons {
		fallbackTotal.WithLabelValues(r)
	}
}

// Record counts one consulted request. A reason outside the closed set is
// folded into no_candidate so a bug can never mint a new series.
func Record(reason string) {
	for _, r := range Reasons {
		if r == reason {
			fallbackTotal.WithLabelValues(reason).Inc()
			return
		}
	}
	fallbackTotal.WithLabelValues(ReasonNoCandidate).Inc()
}

// Outcome is the verdict for one request.
type Outcome struct {
	Reason string
	// Target is the model to rewrite the request to; "" means leave it alone.
	Target string
	// TargetID is the policy candidate id behind Target (for the audit trail).
	TargetID string
}

func allOpen(ids []int) bool {
	if BreakerAllOpen == nil {
		return false
	}
	open, _ := BreakerAllOpen(ids)
	return open
}

// usable reports whether model has at least one channel and not all of them
// are behind an open breaker.
func usable(channels func(model string) []int, model string) bool {
	ids := channels(model)
	return len(ids) > 0 && !allOpen(ids)
}

// Resolve decides one request for an enabled fallback policy. channels lists
// the candidate channel ids of a model for this request's tenant and group;
// permitted tells whether the caller may use a model directly (token model
// limit, tenant allow-list and selection): a rewrite must never reach a model
// the caller could not have asked for.
func Resolve(pol *entity.RoutingPolicy, requested string, channels func(model string) []int, permitted func(model string) bool) Outcome {
	var reason string
	primary := channels(requested)
	switch {
	case len(primary) == 0:
		reason = ReasonNoChannel
	case allOpen(primary):
		reason = ReasonPrimaryUnavailable
	default:
		return Outcome{Reason: ReasonPrimaryOK}
	}
	for _, cd := range pol.Candidates {
		if cd.Model == "" || cd.Model == requested || !permitted(cd.Model) {
			continue
		}
		if usable(channels, cd.Model) {
			return Outcome{Reason: reason, Target: cd.Model, TargetID: cd.ID}
		}
	}
	return Outcome{Reason: ReasonNoCandidate}
}

// GroupsFor expands the request's using-group into the concrete groups whose
// channels can serve it: "auto" is the user's auto-group list, anything else
// is itself.
func GroupsFor(usingGroup, userGroup string, autoGroups func(userGroup string) []string) []string {
	if usingGroup == "" {
		return nil
	}
	if usingGroup == "auto" {
		if autoGroups == nil {
			return nil
		}
		return autoGroups(userGroup)
	}
	return []string{usingGroup}
}

// UnionChannelIDs merges the channel ids of model across groups, in first-seen
// order and without duplicates.
func UnionChannelIDs(tenantID string, groups []string, model string, lookup func(tenantID, group, model string) []int) []int {
	var out []int
	seen := map[int]struct{}{}
	for _, g := range groups {
		for _, id := range lookup(tenantID, g, model) {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// RequestChannels returns a per-request lookup of the channel ids that could
// serve a model: the tenant-scoped, group-scoped candidate set the selector
// itself would draw from.
func RequestChannels(c *gin.Context, tenantID string) func(model string) []int {
	groups := GroupsFor(
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		common.GetContextKeyString(c, constant.ContextKeyUserGroup),
		app.GetUserAutoGroup,
	)
	return func(model string) []int {
		return UnionChannelIDs(tenantID, groups, model, ChannelIDs)
	}
}

// cachedChannelIDs enumerates the channels the selector would consider for
// (tenant, group, model) by running the selector's own cache lookup (exact
// name, then normalised name, then the tenant filter) with a predicate that
// records every channel it is shown and rejects all of them. Rejecting
// everything means the selector never reaches its weighted draw, so this is a
// read of the cache and nothing else.
func cachedChannelIDs(tenantID, group, model string) []int {
	var ids []int
	_, _ = repo.GetRandomSatisfiedChannelWhere(tenantID, group, model, 0, func(ch *repo.Channel) bool {
		ids = append(ids, ch.Id)
		return false
	})
	return ids
}
