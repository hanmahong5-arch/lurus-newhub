package repo

// consume_log_write_failed_test.go — a consume-log insert that fails must
// be counted, not only logged.
//
// RecordConsumeLog runs after the quota debit and the wallet settlement; if
// LOG_DB.Create fails the charge stands and the usage record is gone. Until
// 2026-09-28 that was one LogError line. This test points LOG_DB at a
// hermetic SQLite database WITHOUT a logs table (the insert fails the way a
// broken schema or a lost connection fails: an error from Create) and
// asserts lurus_billing_consume_log_write_failed_total moves by exactly one;
// then migrates the table and asserts a successful insert leaves it alone.
//
// Mutation oracle: remove the Inc() in RecordConsumeLog's error branch and
// the first assertion reads 0.

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

func TestRecordConsumeLog_InsertFailureIsCounted(t *testing.T) {
	InitCol()
	db, err := gorm.Open(sqlite.Open("file:consumelogfail"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	prevLogDB, prevDB, prevEnabled, prevRedis := LOG_DB, DB, common.LogConsumeEnabled, common.RedisEnabled
	LOG_DB, DB, common.LogConsumeEnabled, common.RedisEnabled = db, db, true, false
	defer func() {
		LOG_DB, DB, common.LogConsumeEnabled, common.RedisEnabled = prevLogDB, prevDB, prevEnabled, prevRedis
	}()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("tenant_id", "default") // resolved from the request, so no user-cache (Redis) lookup
	params := RecordConsumeLogParams{
		ChannelId: 1, ModelName: "m", TokenName: "t", Quota: 10,
		PromptTokens: 3, CompletionTokens: 4, Content: "x",
	}

	// No logs table: Create fails.
	before := testutil.ToFloat64(metrics.BillingConsumeLogWriteFailedTotal)
	RecordConsumeLog(c, 1, params)
	if got := testutil.ToFloat64(metrics.BillingConsumeLogWriteFailedTotal) - before; got != 1 {
		t.Fatalf("a failed consume-log insert must add exactly 1 to the counter, got %v", got)
	}

	// With the table the same call succeeds and the counter stays put.
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	before = testutil.ToFloat64(metrics.BillingConsumeLogWriteFailedTotal)
	RecordConsumeLog(c, 1, params)
	if got := testutil.ToFloat64(metrics.BillingConsumeLogWriteFailedTotal) - before; got != 0 {
		t.Fatalf("a successful insert must not touch the counter, got +%v", got)
	}
	var n int64
	if err := db.Model(&Log{}).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("expected the second call to have written one row, count=%d err=%v", n, err)
	}
}
