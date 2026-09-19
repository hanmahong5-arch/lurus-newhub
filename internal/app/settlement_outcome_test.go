package app

// settlement_outcome_test.go — the oracle for SettleConsume/
// FlagSettlementOutcome. Drives the real functions with PostConsumeQuotaFn
// swapped (the same seam convention as a2_legacy_debit_gate_test.go over
// debitWalletGRPC), never a hand-built stand-in for the unit under test.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus"
	prommodel "github.com/prometheus/client_model/go"
)

// settlementFailedCount reads the cumulative counter value for one `path`
// label of lurus_billing_settlement_failed_total.
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

func hookPostConsumeQuotaFn(t *testing.T, fn func(*relaycommon.RelayInfo, int, int, bool) error) {
	t.Helper()
	prev := PostConsumeQuotaFn
	PostConsumeQuotaFn = fn
	t.Cleanup(func() { PostConsumeQuotaFn = prev })
}

// TestSettleConsume_CountsAndFlagsOnFailure: on a failed seam call,
// SettleConsume must return the seam's error AND increment the per-path
// counter — and the caller's other map, run through FlagSettlementOutcome
// with that error, must carry settlement="failed".
func TestSettleConsume_CountsAndFlagsOnFailure(t *testing.T) {
	seamErr := errors.New("wallet unreachable")
	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return seamErr
	})

	before := settlementFailedCount(t, "claude")

	relayInfo := &relaycommon.RelayInfo{}
	got := SettleConsume(context.Background(), relayInfo, 100, 0, "claude")
	if !errors.Is(got, seamErr) {
		t.Fatalf("SettleConsume error = %v, want %v", got, seamErr)
	}

	after := settlementFailedCount(t, "claude")
	if after-before != 1 {
		t.Errorf("BillingSettlementFailedTotal{path=claude} increased by %v, want 1", after-before)
	}

	other := map[string]interface{}{}
	FlagSettlementOutcome(other, got)
	if other["settlement"] != "failed" {
		t.Errorf(`other["settlement"] = %v, want "failed"`, other["settlement"])
	}
}

// TestSettleConsume_SuccessLeavesRowUnflagged: on a successful seam call,
// SettleConsume must return nil, must NOT touch the counter, and
// FlagSettlementOutcome must leave the map exactly as it found it — no
// unconditional "settlement" key regardless of outcome.
func TestSettleConsume_SuccessLeavesRowUnflagged(t *testing.T) {
	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return nil
	})

	before := settlementFailedCount(t, "audio")

	relayInfo := &relaycommon.RelayInfo{}
	got := SettleConsume(context.Background(), relayInfo, 100, 0, "audio")
	if got != nil {
		t.Fatalf("SettleConsume error = %v, want nil", got)
	}

	after := settlementFailedCount(t, "audio")
	if after != before {
		t.Errorf("BillingSettlementFailedTotal{path=audio} changed on success: before %v after %v", before, after)
	}

	other := map[string]interface{}{"unrelated": "kept"}
	FlagSettlementOutcome(other, got)
	if _, present := other["settlement"]; present {
		t.Errorf(`other["settlement"] = %v, want absent on success`, other["settlement"])
	}
	if other["unrelated"] != "kept" {
		t.Errorf("FlagSettlementOutcome must not touch unrelated keys, got %v", other)
	}
}

// readLastLogOther reads the Other JSON of the most recent LOG_DB row for
// (userId, model), the same lookup wire_log_parity_test.go uses.
func readLastLogOther(t *testing.T, userId int, model string) map[string]interface{} {
	t.Helper()
	var row repo.Log
	if err := repo.LOG_DB.Where("user_id = ? AND model_name = ?", userId, model).
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("no consume log row recorded: %v", err)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(row.Other), &other); err != nil {
		t.Fatalf("unmarshal log row Other %q: %v", row.Other, err)
	}
	return other
}

// TestPostClaudeConsumeQuota_SettlementFailureFlagsLogRow drives the REAL
// PostClaudeConsumeQuota (not a hand-built Other map) with PostConsumeQuotaFn
// forced to fail, and asserts the resulting LOG_DB row carries
// other.settlement="failed" and the {path="claude"} counter moved by 1.
func TestPostClaudeConsumeQuota_SettlementFailureFlagsLogRow(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	seamErr := errors.New("settle: platform unreachable")
	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return seamErr
	})

	before := settlementFailedCount(t, "claude")

	c := createTestGinContext()
	const model = "settlement-flag-claude"
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostClaudeConsumeQuota(c, relayInfo, usage)

	other := readLastLogOther(t, userId, model)
	if other["settlement"] != "failed" {
		t.Errorf(`log row other["settlement"] = %v, want "failed"`, other["settlement"])
	}
	if after := settlementFailedCount(t, "claude"); after-before != 1 {
		t.Errorf("BillingSettlementFailedTotal{path=claude} increased by %v, want 1", after-before)
	}
}

// TestPostAudioConsumeQuota_SettlementFailureFlagsLogRow is the audio-site
// sibling of the Claude test above.
func TestPostAudioConsumeQuota_SettlementFailureFlagsLogRow(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	seamErr := errors.New("settle: platform unreachable")
	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return seamErr
	})

	before := settlementFailedCount(t, "audio")

	c := createTestGinContext()
	const model = "settlement-flag-audio"
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens:  60,
			AudioTokens: 40,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			TextTokens:  30,
			AudioTokens: 10,
		},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostAudioConsumeQuota(c, relayInfo, usage, "extra")

	other := readLastLogOther(t, userId, model)
	if other["settlement"] != "failed" {
		t.Errorf(`log row other["settlement"] = %v, want "failed"`, other["settlement"])
	}
	if after := settlementFailedCount(t, "audio"); after-before != 1 {
		t.Errorf("BillingSettlementFailedTotal{path=audio} increased by %v, want 1", after-before)
	}
}
