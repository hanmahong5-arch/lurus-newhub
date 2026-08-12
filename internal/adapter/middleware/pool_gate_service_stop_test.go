package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// ============================================================================
// 业务事故防线：额度花完了还在继续放行 / 补了款还在被拦
//
// 这一组测的是「停供-复供」这条客户能直接感知的业务流程，不是某个函数的返回值：
//
//   - 经销商预付的额度池刷到 0 之后，中转还继续把请求打给上游 LLM ⇒ 每一条
//     后续请求都是我们自己掏钱买的 token，客户端毫无感知，账单月底才炸。
//   - 池子已经被 relay_overdraft 记成负数（请求已经打出去、token 已经烧掉，
//     事后补记的欠账）之后仍然放行 ⇒ 欠得越多放得越多，越亏越狠。
//   - 反过来，客户刚补完款，网关还在返回 402 ⇒ 付了钱用不了，比不能用更伤客户。
//   - 合同约定的不限量客户（max_balance = -1）余额显示 0 时被误拦 ⇒ 大客户全线断供。
//
// 这四条都只有在「余额 → 放行/拦截」这个决策点上端到端跑一遍才看得出来，
// 所以用真实的 sqlite 池行 + 真实的 PoolBalanceCheck 中间件，不打桩。
// ============================================================================

// seedGatePool inserts one real tenant_credit_pools row so
// repo.GetTenantCreditPool inside the middleware hits the genuine query path
// (no fake, no stub) — the pool state is the only thing under test.
func seedGatePool(t *testing.T, db *gorm.DB, tenantID string, balance, maxBalance int64) {
	t.Helper()
	now := time.Now()
	pool := &entity.TenantCreditPool{
		TenantID:          tenantID,
		CreatedByUserID:   1,
		CurrentBalance:    balance,
		MaxBalance:        maxBalance,
		ResetPeriod:       entity.PoolResetNone,
		LastResetAt:       now,
		AlertThresholdPct: 80,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := db.Create(pool).Error; err != nil {
		t.Fatalf("seed pool for %s: %v", tenantID, err)
	}
}

// callGate fires one request through the production middleware chain shape
// (tenant context injected by the TokenAuth stand-in, then PoolBalanceCheck).
func callGate(tenantID string) *httptest.ResponseRecorder {
	r := buildPoolGateRouter(tenantID, "default")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
	return w
}

// TestPoolGate_DrainedTenantIsCutOffAndResumesAfterTopup walks the whole
// stop-service / resume-service business flow against real pool rows.
func TestPoolGate_DrainedTenantIsCutOffAndResumesAfterTopup(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	// 事故一：额度刚好花到 0。再放行一条就是白送上游成本。
	t.Run("balance_hits_zero_stops_service", func(t *testing.T) {
		seedGatePool(t, db, "t-drained", 0, 100_000)

		w := callGate("t-drained")
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("额度已用尽仍放行 —— 后续每条请求都是我们自付的上游成本: status=%d body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "pool_exhausted") {
			t.Errorf("402 必须带 pool_exhausted 语义码，客户端才知道是余额问题而不是故障: %s", w.Body.String())
		}
	})

	// 事故二：池子已经被事后补记的 overdraft 扣成负数，仍然继续放行 ⇒ 越欠越多。
	t.Run("overdrafted_negative_balance_stays_cut_off", func(t *testing.T) {
		seedGatePool(t, db, "t-overdrafted", -8_000, 100_000)

		w := callGate("t-overdrafted")
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("已经透支为负还在放行 —— 欠账会一路滚大: status=%d body=%s",
				w.Code, w.Body.String())
		}
	})

	// 事故三：客户补了款还被拦 —— 付了钱用不了，比停供更伤客户。
	t.Run("topup_resumes_service_immediately", func(t *testing.T) {
		if err := db.Model(&entity.TenantCreditPool{}).
			Where("tenant_id = ?", "t-drained").
			Update("current_balance", 1).Error; err != nil {
			t.Fatalf("topup pool: %v", err)
		}

		w := callGate("t-drained")
		if w.Code != http.StatusOK {
			t.Fatalf("补款后仍被拦 —— 客户付了钱用不了: status=%d body=%s", w.Code, w.Body.String())
		}
	})

	// 事故四：合同不限量的大客户（max_balance = -1）余额显示 0 时被误拦 ⇒ 全线断供。
	t.Run("contracted_unlimited_tenant_is_never_cut_off", func(t *testing.T) {
		seedGatePool(t, db, "t-unlimited", 0, entity.PoolMaxBalanceUnlimited)

		w := callGate("t-unlimited")
		if w.Code != http.StatusOK {
			t.Fatalf("合同不限量客户被余额 0 误拦 —— 大客户全线断供: status=%d body=%s",
				w.Code, w.Body.String())
		}
	})
}
