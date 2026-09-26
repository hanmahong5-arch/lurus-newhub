package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// usageReportTotal counts VIP usage-report legs to the platform
// (POST /internal/v1/usage/report) by status (success/error), mirroring
// metrics.BillingUsageMirrorTotal, which does the same for the sibling
// usage-EVENTS endpoint. Error here means this account's VIP accumulation is
// missing a data point; it never means money moved or was lost.
//
// It is declared here rather than in internal/pkg/metrics only because
// cycle-13 L9 does not own that package — see the hand-off note. Moving it
// there is a straight lift; both copies must not exist at once or promauto
// panics on duplicate registration at init.
// ALERTABLE: lurus_billing_usage_report_total
var usageReportTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "lurus",
		Subsystem: "billing",
		Name:      "usage_report_total",
		Help:      "VIP usage reports to the platform by status (success/error)",
	},
	[]string{"status"},
)

// ReportLLMUsage sends a usage record to lurus-platform for VIP accumulation.
// Fire-and-forget — the caller is never blocked and never sees an error — but
// NOT silent: the platform's answer is read, and a refusal is logged and
// counted on usageReportTotal. Before cycle-13 L9 this function closed the
// body without looking at the status, so a wrong-scope 403, a renamed route's
// 404 and a 500 were all indistinguishable from success, including on the
// gRPC transport, which falls back here.
func ReportLLMUsage(ctx context.Context, accountID int64, amountCNY float64) {
	if IdentityServiceURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"account_id": accountID,
		"amount_cny": amountCNY,
	})
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		IdentityServiceURL+"/internal/v1/usage/report",
		bytes.NewReader(body),
	)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity ReportLLMUsage: transport failed for account=%d: %v", accountID, err))
		usageReportTotal.WithLabelValues("error").Inc()
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// 2xx is the whole success set: the platform answers 200 today, but an
	// endpoint that starts answering 201/202 for an accepted report must not
	// read as a failure.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		SysLog(fmt.Sprintf("identity ReportLLMUsage: WARN platform answered status %d for account=%d "+
			"(VIP usage not recorded)", resp.StatusCode, accountID))
		usageReportTotal.WithLabelValues("error").Inc()
		return
	}
	usageReportTotal.WithLabelValues("success").Inc()
}
