package repo

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// log_billable_test.go — repo-level oracles for BillableConsumePredicate and
// GetUserLogStatByPeriod (cycle13 L2): real repo.RecordConsumeLog writes,
// landed through the hermetic sqlite fixture, then queried back — not a
// hand-built Log{} for the marker rows. The stronger, handler-level proof
// that this predicate's markers actually line up with the production
// writers (probeChannel, SettleConsume) lives in internal/adapter/handler
// (TestProbeChannel_RowIsUnbilled, v2_billing_invoices_test.go) — this
// package cannot import handler or app without a build cycle (both import
// repo); see BillableConsumePredicate's doc comment in log.go for why the
// marker substrings are pinned here as literals instead.

// recordConsumeLogForTest drives the real RecordConsumeLog — not a
// hand-built Log{} — so Other is JSON-encoded exactly the way every
// production caller encodes it (common.MapToJsonStr, no whitespace around
// ':', regardless of key order).
func recordConsumeLogForTest(t *testing.T, userID, quota int, modelName string, other map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	RecordConsumeLog(c, userID, RecordConsumeLogParams{
		Quota:     quota,
		ModelName: modelName,
		Other:     other,
	})
}

// TestBillableConsumePredicate_ExcludesRealWrittenMarkerRows seeds a clean
// row, a channel-test-probe row and a settlement-failed row through the real
// RecordConsumeLog write path, then queries them back through
// BillableConsumePredicate: only the clean row's quota must survive.
func TestBillableConsumePredicate_ExcludesRealWrittenMarkerRows(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prevConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prevConsume })

	user := seedUser(t, "log-billable-tester", "log-billable@test.local", common.RoleCommonUser, common.UserStatusEnabled, "")

	recordConsumeLogForTest(t, user.Id, 1000, "probe-model", nil)
	recordConsumeLogForTest(t, user.Id, 2000, "probe-model", map[string]interface{}{"source": "channel_test"})
	recordConsumeLogForTest(t, user.Id, 3000, "probe-model", map[string]interface{}{"settlement": "failed"})

	var billable []Log
	if err := LOG_DB.Model(&Log{}).Scopes(BillableConsumePredicate).
		Where("user_id = ?", user.Id).Find(&billable).Error; err != nil {
		t.Fatalf("query billable rows: %v", err)
	}
	if len(billable) != 1 {
		t.Fatalf("billable rows = %d, want 1", len(billable))
	}
	if billable[0].Quota != 1000 {
		t.Errorf("billable[0].Quota = %d, want 1000", billable[0].Quota)
	}

	var allCount int64
	if err := LOG_DB.Model(&Log{}).Where("user_id = ? AND type = ?", user.Id, LogTypeConsume).Count(&allCount).Error; err != nil {
		t.Fatalf("count all rows: %v", err)
	}
	if allCount != 3 {
		t.Fatalf("all consume rows = %d, want 3 (sanity: all three rows actually landed before the predicate filtered any)", allCount)
	}
}

// TestGetUserLogStatByPeriod_ExcludesUnbilledRows is GetUserLogStatByPeriod's
// own oracle; internal/adapter/handler's
// TestSelfBillingUsage_ExcludesUnbilledRows covers the same behaviour one
// layer up, through the real SelfBillingUsage handler.
func TestGetUserLogStatByPeriod_ExcludesUnbilledRows(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prevConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prevConsume })

	user := seedUser(t, "log-billable-stat-tester", "log-billable-stat@test.local", common.RoleCommonUser, common.UserStatusEnabled, "")

	recordConsumeLogForTest(t, user.Id, 1000, "probe-model", nil)
	recordConsumeLogForTest(t, user.Id, 2000, "probe-model", map[string]interface{}{"source": "channel_test"})
	recordConsumeLogForTest(t, user.Id, 3000, "probe-model", map[string]interface{}{"settlement": "failed"})

	stats, err := GetUserLogStatByPeriod(user.Id, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("GetUserLogStatByPeriod: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats len = %d, want 1 (unbilled rows must not add a second group)", len(stats))
	}
	if stats[0].TotalQuota != 1000 {
		t.Errorf("TotalQuota = %d, want 1000", stats[0].TotalQuota)
	}
	if stats[0].Count != 1 {
		t.Errorf("Count = %d, want 1", stats[0].Count)
	}
}
