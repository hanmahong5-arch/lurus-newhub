package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordTenantModelDenied_IncrementsByTenantAndAction(t *testing.T) {
	TenantModelDeniedTotal.Reset()

	RecordTenantModelDenied("t-allow", "observed")
	RecordTenantModelDenied("t-allow", "observed")
	RecordTenantModelDenied("t-allow", "enforced")
	RecordTenantModelDenied("t-other", "observed")

	if got := testutil.ToFloat64(TenantModelDeniedTotal.WithLabelValues("t-allow", "observed")); got != 2 {
		t.Errorf("t-allow/observed = %v, want 2", got)
	}
	if got := testutil.ToFloat64(TenantModelDeniedTotal.WithLabelValues("t-allow", "enforced")); got != 1 {
		t.Errorf("t-allow/enforced = %v, want 1", got)
	}
	if got := testutil.ToFloat64(TenantModelDeniedTotal.WithLabelValues("t-other", "observed")); got != 1 {
		t.Errorf("t-other/observed = %v, want 1", got)
	}
}

func TestTenantModelDeniedTotal_NameAndLabels(t *testing.T) {
	expected := `
# HELP lurus_gateway_tenant_model_denied_total Relay requests the tenant model allow-list would deny, by tenant and action (observed/enforced)
# TYPE lurus_gateway_tenant_model_denied_total counter
lurus_gateway_tenant_model_denied_total{action="enforced",tenant_id="t-x"} 1
`
	TenantModelDeniedTotal.Reset()
	RecordTenantModelDenied("t-x", "enforced")
	if err := testutil.CollectAndCompare(TenantModelDeniedTotal, strings.NewReader(expected), "lurus_gateway_tenant_model_denied_total"); err != nil {
		t.Errorf("unexpected collected metric: %v", err)
	}
}
