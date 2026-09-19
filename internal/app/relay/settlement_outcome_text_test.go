package relay

// settlement_outcome_text_test.go — the L7 oracle for the OpenAI-compatible
// text site (postConsumeQuota, relay/compatible_handler.go). Drives the REAL
// handler with app.PostConsumeQuotaFn swapped to fail, and asserts the
// resulting LOG_DB row carries other.settlement="failed" and the
// {path="text"} counter moved — the same shape as
// internal/app/settlement_outcome_test.go's Claude/audio siblings, forced
// through relay's own call into app.SettleConsume rather than reimplemented
// here.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus"
	prommodel "github.com/prometheus/client_model/go"
)

func settlementFailedCount(t *testing.T, path string) float64 {
	t.Helper()
	metric, ok := metrics.BillingSettlementFailedTotal.WithLabelValues(path).(prometheus.Metric)
	if !ok {
		t.Fatalf("BillingSettlementFailedTotal counter does not implement prometheus.Metric")
	}
	var m prommodel.Metric
	if err := metric.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	counter := m.GetCounter()
	if counter == nil {
		t.Fatalf("metric is not a counter")
	}
	return counter.GetValue()
}

// TestPostConsumeQuota_TextSite_SettlementFailureFlagsLogRow swaps
// app.PostConsumeQuotaFn (the seam SettleConsume calls) to fail, drives the
// real postConsumeQuota text-site handler, and asserts the LOG_DB row it
// writes carries other.settlement="failed" — proving the flag reaches the
// relay package's call site, not just app.SettleConsume in isolation.
func TestPostConsumeQuota_TextSite_SettlementFailureFlagsLogRow(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	// setupRelayDB turns off consume-log writes for its other callers; this
	// test's oracle IS the consume-log row, so it needs them on (same
	// pattern as responses_handler_test.go).
	prevLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prevLogConsume })

	prevFn := app.PostConsumeQuotaFn
	seamErr := errors.New("settle: platform unreachable")
	app.PostConsumeQuotaFn = func(*relaycommon.RelayInfo, int, int, bool) error {
		return seamErr
	}
	t.Cleanup(func() { app.PostConsumeQuotaFn = prevFn })

	before := settlementFailedCount(t, "text")

	const username = "settlement-flag-text"
	u := &repo.User{Username: username, Quota: 100_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	info := newRelayInfo(u.Id, 0, 0)
	info.IsPlayground = true
	info.UserQuota = 100_000_000
	info.PriceData = types.PriceData{
		ModelRatio:      2,
		CompletionRatio: 1,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
	}

	c, _ := newJSONContext(http.MethodPost, "/", nil)
	c.Set("token_name", "tkn")

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}
	postConsumeQuota(c, info, usage)

	var row repo.Log
	if err := repo.LOG_DB.Where("user_id = ? AND model_name = ?", u.Id, info.OriginModelName).
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("no consume log row recorded: %v", err)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(row.Other), &other); err != nil {
		t.Fatalf("unmarshal log row Other %q: %v", row.Other, err)
	}
	if other["settlement"] != "failed" {
		t.Errorf(`log row other["settlement"] = %v, want "failed"`, other["settlement"])
	}

	if after := settlementFailedCount(t, "text"); after-before != 1 {
		t.Errorf("BillingSettlementFailedTotal{path=text} increased by %v, want 1", after-before)
	}
}
