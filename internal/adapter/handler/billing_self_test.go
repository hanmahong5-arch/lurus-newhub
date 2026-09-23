package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ─── Hermetic test setup ─────────────────────────────────────────────────────

type billingSelfCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	userID  int
	cleanup func()
}

var billingSelfDBCounter invoiceCounterType

// setupBillingSelfRouter mirrors setupInvoiceRouter (v2_billing_invoices_test.go)
// but wires SelfBillingUsage, which is authenticated via TokenAuth's
// c.GetInt("id") rather than the v2 tenant_context.
func setupBillingSelfRouter(t *testing.T) *billingSelfCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:billingself%d?mode=memory&cache=shared", billingSelfDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Logf("auto migrate warning: %v", err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevLogEnabled := common.LogConsumeEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = true

	user := &repo.User{
		Username:    "billing-self-tester",
		DisplayName: "Billing Self Tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "billing-self@test.local",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ctx := &billingSelfCtx{db: db, userID: user.Id}

	router := gin.New()
	router.GET("/v1/billing/usage", func(c *gin.Context) {
		c.Set("id", ctx.userID)
		c.Next()
	}, SelfBillingUsage)
	ctx.router = router

	ctx.cleanup = func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		common.LogConsumeEnabled = prevLogEnabled
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(ctx.cleanup)
	return ctx
}

// recordRealConsumeLog drives the real repo.RecordConsumeLog — not a
// hand-built repo.Log{} — so Other is JSON-encoded exactly the way every
// production caller encodes it (common.MapToJsonStr, no whitespace around
// ':', regardless of key order). Mirrors
// internal/adapter/repo/log_billable_test.go's recordConsumeLogForTest.
func recordRealConsumeLog(t *testing.T, ctx *billingSelfCtx, quota int, modelName string, other map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	repo.RecordConsumeLog(c, ctx.userID, repo.RecordConsumeLogParams{
		Quota:     quota,
		ModelName: modelName,
		Other:     other,
	})
}

func getUsage(ctx *billingSelfCtx, query string) *httptest.ResponseRecorder {
	path := "/v1/billing/usage"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestSelfBillingUsage_ExcludesUnbilledRows is cycle13 L2's oracle for the
// by-model usage view (GET /v1/billing/usage): a channel-test-probe row or a
// settlement-failed row must not inflate a customer's own cost total or
// per-model request count, even though both are type=consume rows with a
// nonzero Quota.
func TestSelfBillingUsage_ExcludesUnbilledRows(t *testing.T) {
	ctx := setupBillingSelfRouter(t)

	const model = "gpt-l2-usage-test"
	recordRealConsumeLog(t, ctx, 1000, model, nil)
	recordRealConsumeLog(t, ctx, 2000, model, map[string]interface{}{"source": "channel_test"})
	recordRealConsumeLog(t, ctx, 3000, model, map[string]interface{}{"settlement": "failed"})

	w := getUsage(ctx, "period=30d")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}

	wantCost := currency.QuotaToCNY(1000)
	if got := resp["total_cost_lb"].(float64); got != wantCost {
		t.Errorf("total_cost_lb = %v, want %v (only the clean row's 1000 quota)", got, wantCost)
	}

	byModel := resp["by_model"].([]interface{})
	if len(byModel) != 1 {
		t.Fatalf("by_model len = %d, want 1 (one model, unbilled rows excluded from the group)", len(byModel))
	}
	entry := byModel[0].(map[string]interface{})
	if entry["count"].(float64) != 1 {
		t.Errorf("by_model[0].count = %v, want 1 (unbilled rows excluded)", entry["count"])
	}
	if entry["cost_lb"].(float64) != wantCost {
		t.Errorf("by_model[0].cost_lb = %v, want %v", entry["cost_lb"], wantCost)
	}
}

// TestSelfBillingUsage_HappyPathUnaffected pins the pre-existing behaviour
// for an all-clean period: cycle13 L2 must not change what a customer with
// no probe/settlement-failed rows sees.
func TestSelfBillingUsage_HappyPathUnaffected(t *testing.T) {
	ctx := setupBillingSelfRouter(t)

	recordRealConsumeLog(t, ctx, 500, "gpt-clean-a", nil)
	recordRealConsumeLog(t, ctx, 1500, "gpt-clean-b", nil)

	w := getUsage(ctx, "period=30d")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}

	wantCost := currency.QuotaToCNY(2000)
	if got := resp["total_cost_lb"].(float64); got != wantCost {
		t.Errorf("total_cost_lb = %v, want %v", got, wantCost)
	}
	byModel := resp["by_model"].([]interface{})
	if len(byModel) != 2 {
		t.Fatalf("by_model len = %d, want 2", len(byModel))
	}
}
