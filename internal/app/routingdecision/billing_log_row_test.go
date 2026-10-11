package routingdecision

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var settleDBCounter atomic.Int64

// The evaluation's own consume row is what a tenant sees for the evaluator
// call, so it carries the unified metering columns (migration 053) exactly
// as a relayed request's row does, plus other.routing_eval. Uses an explicit
// zero price so no wallet or quota is touched: this is about the row.
func TestSettleEval_WritesAMeteredEvaluationRow(t *testing.T) {
	setRatios(t, `{"evaluator-a":0}`)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:rdsettle%d?mode=memory&cache=shared", settleDBCounter.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Log{}, &repo.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB, prevLogDB, prevSQLite, prevConsume, prevRedis := repo.DB, repo.LOG_DB, common.UsingSQLite, common.LogConsumeEnabled, common.RedisEnabled
	repo.DB, repo.LOG_DB, common.UsingSQLite, common.LogConsumeEnabled, common.RedisEnabled = db, db, true, true, false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.UsingSQLite, common.LogConsumeEnabled, common.RedisEnabled = prevDB, prevLogDB, prevSQLite, prevConsume, prevRedis
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})

	for _, tc := range []struct {
		name       string
		input      int
		wantSource string
	}{
		{"usage reported", 1000, "upstream"},
		{"usage missing", 0, "unreported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyUserId, 42)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			c.Set("token_name", "settle-row-"+tc.wantSource)
			c.Set("tenant_id", "default")

			settleEval(c, "evaluator-a", &EvalOutput{InputTokens: tc.input, OutputTokens: 7, ChannelID: 5, Group: "default"}, 12*time.Millisecond)

			var row repo.Log
			if err := db.Where("token_name = ?", "settle-row-"+tc.wantSource).First(&row).Error; err != nil {
				t.Fatalf("evaluation row not written: %v", err)
			}
			if row.ModelName != "evaluator-a" || !strings.Contains(row.Other, `"routing_eval":true`) {
				t.Fatalf("row = model %q other %s; want the evaluator model flagged routing_eval", row.ModelName, row.Other)
			}
			if row.UsageUnit != "token" || row.UsageQuantity != int64(tc.input) || row.UsageSource != tc.wantSource {
				t.Fatalf("metering = %q/%d/%q, want token/%d/%s", row.UsageUnit, row.UsageQuantity, row.UsageSource, tc.input, tc.wantSource)
			}
		})
	}
}
