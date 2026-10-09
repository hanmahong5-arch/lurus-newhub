package handler

import (
	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// contentMetricsSink feeds the content-rule guard's hit/reject events into the
// lurus_content_* series. Labels carry only scope, kind and mode - never a rule
// id or any matched text.
type contentMetricsSink struct{}

func (contentMetricsSink) RuleHit(_ int64, kind, mode, builtin string, count int) {
	metrics.RecordContentRuleHit(kind, mode, builtin, count)
}

func (contentMetricsSink) RuleReject(int64) { metrics.RecordContentRejected() }

// InstallContentMetricsSink plugs the sink into the content-rule guard; called
// once at start-up.
func InstallContentMetricsSink() { contentpolicy.SetMetricsSink(contentMetricsSink{}) }
