package contentpolicy

import "sync/atomic"

// MetricsSink is the hook the metrics lane plugs into. The guard calls it once
// per rule per request (a hit) and once when a request is refused. Labels are
// deliberately low-cardinality: the rule id is the only high-ish one and is
// bounded by MaxRulesPerScope per scope.
type MetricsSink interface {
	// RuleHit reports count matches of ruleId. kind is mask|reject, mode is
	// observe|enforce, builtin is "" for a custom regex.
	RuleHit(ruleId int64, kind, mode, builtin string, count int)
	// RuleReject reports a request refused by an enforce-mode reject rule.
	RuleReject(ruleId int64)
}

type sinkBox struct{ s MetricsSink }

var sink atomic.Pointer[sinkBox]

// SetMetricsSink installs (or, with nil, removes) the sink.
func SetMetricsSink(s MetricsSink) {
	if s == nil {
		sink.Store(nil)
		return
	}
	sink.Store(&sinkBox{s: s})
}

// ObserveRuleHit forwards a hit to the sink; a no-op without one.
func ObserveRuleHit(ruleId int64, kind, mode, builtin string, count int) {
	if b := sink.Load(); b != nil && count > 0 {
		b.s.RuleHit(ruleId, kind, mode, builtin, count)
	}
}

// ObserveRuleReject forwards a refusal to the sink; a no-op without one.
func ObserveRuleReject(ruleId int64) {
	if b := sink.Load(); b != nil {
		b.s.RuleReject(ruleId)
	}
}

// Report publishes the outcome of one Apply to the sink.
func Report(res Result) {
	for _, h := range res.Hits {
		ObserveRuleHit(h.RuleId, h.Kind, h.Mode, h.Builtin, h.Count)
	}
	if res.Rejected != nil {
		ObserveRuleReject(res.Rejected.RuleId)
	}
}
