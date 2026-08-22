package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ============================================================================
// 业务事故防线：注资这条钱路上「谁能注」和「注给谁」
//
// 客户在平台侧付了钱，平台回头调 newhub 把额度打进这个租户的池子里。这条链
// 上历史上真出过 "钱收了额度没有" 的事故（套餐里写的池 slug 和实际 seed 的
// slug 对不上，注资 404，但支付已成功）。本文件盯的是同一条链上另外两个
// 还没有测试守着的口子：
//
//   1. 谁能注资：所有其他按租户 slug 收权的内部端点（发卡、撤卡、发/收 key）
//      都会用 internal_api_key_tenants 白名单做一次「这把 key 能不能动这个
//      租户」的 fail-closed 校验，唯独往池子里打钱的这个端点没有。
//   2. 注给谁：幂等键 event_id 在库里是全局唯一而不是 (租户, 事件) 唯一，
//      同一个 event_id 落到第二个租户时会被当成重放。
//
// 这两条都不是「函数返回值不对」，而是「客户付了钱、系统回 200、余额不动」
// 这类全链无报错的事故，所以断言全部落在 —— 池子余额到底动没动。
// ============================================================================

// 一把窄权限内部 key：同时带 provisioning 和 balance:write 两个 scope，
// 只在 internal_api_key_tenants 里被授权访问租户 A。真实场景对应「经销商
// 自建集成用的那把 key」。
const testApiKeyNarrowTenantScoped = "lurus_ik_testkey_narrow_tenant_00000"

// moneyPathFixture 是两租户的注资台：A 是这把窄 key 被授权的租户，
// B 是它无权触碰的另一个经销商。
type moneyPathFixture struct {
	Router  *gin.Engine
	DB      *gorm.DB
	TenantA *repo.Tenant
	TenantB *repo.Tenant
	PoolAID int64
	PoolBID int64
	Cleanup func()
}

// setupMoneyPathRouter 把「注资」和「发卡」两个按 slug 收权的内部端点挂在同一个
// 路由上，这样同一把 key 打两个端点的差异一目了然。中间件用生产同款
// (InternalApiAuth + RequireScope)，鉴权逻辑不打桩。
func setupMoneyPathRouter(t *testing.T) *moneyPathFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := "file:moneypath" + formatInt64(testDBCounter.Add(1)) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{}, &repo.Token{}, &repo.InternalApiKey{}, &repo.Option{},
		&repo.Setup{}, &repo.Tenant{}, &repo.TenantConfig{},
		&repo.TenantCreditPool{}, &repo.TenantCreditPoolDraw{}, &repo.CreditPoolFundEvent{},
		&repo.Redemption{}, &repo.ProvisionedRedemptionBatch{},
	}
	for _, tbl := range tables {
		if migrateErr := db.AutoMigrate(tbl); migrateErr != nil {
			if strings.Contains(migrateErr.Error(), "already exists") {
				continue
			}
			t.Fatalf("AutoMigrate %T: %v", tbl, migrateErr)
		}
	}
	// internal_api_key_tenants 由 migration 013 建，不走 GORM —— 这里照抄它的
	// schema，让 InternalKeyAllowedForTenant 查到的是真表而不是「查不到表所以拒绝」。
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS internal_api_key_tenants (
		api_key_id INTEGER NOT NULL,
		tenant_id  VARCHAR(36) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (api_key_id, tenant_id)
	)`).Error; err != nil {
		t.Fatalf("create whitelist table: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	now := time.Now()
	mkTenant := func(id, slug, org string) *repo.Tenant {
		tn := &repo.Tenant{
			Id: id, Name: id, Slug: slug, Status: repo.TenantStatusEnabled,
			IDPOrgID: org, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(tn).Error; err != nil {
			t.Fatalf("create tenant %s: %v", id, err)
		}
		return tn
	}
	mkPool := func(tenantID string) int64 {
		p := &repo.TenantCreditPool{
			TenantID: tenantID, CreatedByUserID: 1,
			CurrentBalance: 0, MaxBalance: 1_000_000,
			ResetPeriod: repo.PoolResetNone, LastResetAt: now,
			AlertThresholdPct: 80, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create pool for %s: %v", tenantID, err)
		}
		return p.ID
	}

	tenantA := mkTenant("tenant-money-a", "money-a", "org_money_a")
	tenantB := mkTenant("tenant-money-b", "money-b", "org_money_b")
	poolA := mkPool(tenantA.Id)
	poolB := mkPool(tenantB.Id)

	// 平台自己的全权 key（ScopeAll 天然绕过租户白名单，这是有意设计）。
	allScopes, _ := json.Marshal([]string{repo.ScopeAll})
	db.Create(&repo.InternalApiKey{
		Id: 40, Name: "moneypath-platform", KeyHash: hashTestKey(testApiKeyAllScopes),
		Scopes: string(allScopes), Enabled: true,
	})

	// 窄权限 key：provisioning + balance:write，只被授权租户 A。
	narrowScopes, _ := json.Marshal([]string{repo.ScopeProvisioning, repo.ScopeBalanceWrite})
	db.Create(&repo.InternalApiKey{
		Id: 41, Name: "moneypath-narrow", KeyHash: hashTestKey(testApiKeyNarrowTenantScoped),
		Scopes: string(narrowScopes), Enabled: true,
	})
	if err := db.Exec(
		`INSERT INTO internal_api_key_tenants (api_key_id, tenant_id) VALUES (?, ?)`,
		41, tenantA.Id).Error; err != nil {
		t.Fatalf("seed whitelist row: %v", err)
	}

	router := gin.New()
	internalGroup := router.Group("/internal")
	internalGroup.Use(middleware.InternalApiAuth())

	fundGroup := internalGroup.Group("/v1/provisioning")
	fundGroup.Use(middleware.RequireScope(repo.ScopeBalanceWrite))
	fundGroup.POST("/tenants/:slug/credit-pool/fund", InternalFundCreditPool)

	provGroup := internalGroup.Group("/v1/provisioning")
	provGroup.Use(middleware.RequireScope(repo.ScopeProvisioning))
	provGroup.POST("/tenants/:slug/redemptions", InternalProvisionRedemptions)

	return &moneyPathFixture{
		Router: router, DB: db,
		TenantA: tenantA, TenantB: tenantB,
		PoolAID: poolA, PoolBID: poolB,
		Cleanup: func() {
			repo.DB = prevDB
			repo.LOG_DB = prevLogDB
			common.UsingSQLite = prevSQLite
			common.UsingPostgreSQL = prevPG
			common.RedisEnabled = prevRedis
			if sqlDB, _ := db.DB(); sqlDB != nil {
				_ = sqlDB.Close()
			}
		},
	}
}

// fundAs POSTs 一次注资并返回 (状态码, 响应体)。
func (f *moneyPathFixture) fundAs(t *testing.T, slug, key string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	w := internalRequest(f.Router, "POST",
		"/internal/v1/provisioning/tenants/"+slug+"/credit-pool/fund",
		body, map[string]string{"X-API-Key": key})
	return w.Code, parseResponse(t, w)
}

// balanceOf 直接读库里的池余额 —— 断言只认这个数，不认接口回的数字。
func (f *moneyPathFixture) balanceOf(t *testing.T, tenantID string) int64 {
	t.Helper()
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("read pool for %s: %v", tenantID, err)
	}
	return pool.CurrentBalance
}

// TestFundCreditPool_TenantScopedKeyBoundary 对同一把窄权限 key，分别打
// 「发卡」和「注资」两个都按租户 slug 收权的端点，量出两者的授权边界差异。
//
// 防的业务事故：一把只该管自己租户的集成 key 能给别家（或自家）的额度池凭空
// 打钱 —— 没付钱就拿到可消费的 LLM 额度，而池子里那笔钱在平台侧没有对应订单。
func TestFundCreditPool_TenantScopedKeyBoundary(t *testing.T) {
	f := setupMoneyPathRouter(t)
	t.Cleanup(f.Cleanup)

	// 基准 1：窄 key 打自己被授权的租户 A，发卡端点放行 —— 证明白名单表和这把 key
	// 都是活的，后面的 403 不是「表不存在所以一律拒绝」的假阳性。
	t.Run("narrow_key_may_issue_cards_for_its_own_tenant", func(t *testing.T) {
		w := internalRequest(f.Router, "POST",
			"/internal/v1/provisioning/tenants/"+f.TenantA.Slug+"/redemptions",
			map[string]interface{}{"event_id": "evt-own-tenant", "count": 1, "quota_per_code": 100},
			map[string]string{"X-API-Key": testApiKeyNarrowTenantScoped})
		if w.Code != http.StatusOK {
			t.Fatalf("窄 key 对自己被授权的租户发卡应放行，got %d body=%s", w.Code, w.Body.String())
		}
	})

	// 基准 2：同一把 key 打别家租户 B 的发卡端点 —— fail-closed，403。
	t.Run("narrow_key_may_not_issue_cards_for_another_tenant", func(t *testing.T) {
		w := internalRequest(f.Router, "POST",
			"/internal/v1/provisioning/tenants/"+f.TenantB.Slug+"/redemptions",
			map[string]interface{}{"event_id": "evt-foreign-tenant", "count": 1, "quota_per_code": 100},
			map[string]string{"X-API-Key": testApiKeyNarrowTenantScoped})
		if w.Code != http.StatusForbidden {
			t.Fatalf("窄 key 对别家租户发卡必须 403，got %d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "TENANT_NOT_AUTHORIZED") {
			t.Errorf("403 应带 TENANT_NOT_AUTHORIZED: %s", w.Body.String())
		}
	})

	// 边界：同一把 key 打别家租户 B 的「注资」端点 —— 必须复用上面同一道
	// InternalKeyAllowedForTenant 守卫，fail-closed 403，池子分毫不动。
	t.Run("narrow_key_may_not_fund_another_tenants_pool", func(t *testing.T) {
		const amount = 12_000

		code, resp := f.fundAs(t, f.TenantB.Slug, testApiKeyNarrowTenantScoped, map[string]interface{}{
			"event_id": "evt-cross-tenant-fund",
			"amount":   amount,
			"source":   "narrow-integration-key",
		})

		if code != http.StatusForbidden {
			t.Fatalf("窄 key 对别家租户注资必须 403，got %d body=%s", code, mustJSON(resp))
		}
		ec, _ := resp["error_code"].(string)
		if ec != "TENANT_NOT_AUTHORIZED" {
			t.Errorf("expected error_code=TENANT_NOT_AUTHORIZED, got %q", ec)
		}
		if got := f.balanceOf(t, f.TenantB.Id); got != 0 {
			t.Fatalf("被拒的注资不应改动余额，got %d want 0", got)
		}
	})

	// 正向对照：窄 key 打自己被授权的租户 A 的「注资」端点必须放行且真的记账 ——
	// 没有这一条，一个把守卫写成 deny-all 的实现（例如误传 tenant.Slug 而不是
	// tenant.Id）在上面两条 403 用例下也会全绿，因为它们只证明了「拒绝」而
	// 从没证明过「本该放行的也放行」。
	t.Run("narrow_key_may_fund_its_own_tenant", func(t *testing.T) {
		const amount = 3_000

		code, resp := f.fundAs(t, f.TenantA.Slug, testApiKeyNarrowTenantScoped, map[string]interface{}{
			"event_id": "evt-own-tenant-fund",
			"amount":   amount,
			"source":   "narrow-integration-key",
		})

		if code != http.StatusOK {
			t.Fatalf("窄 key 对自己被授权的租户注资应放行，got %d body=%s", code, mustJSON(resp))
		}
		if got := f.balanceOf(t, f.TenantA.Id); got != amount {
			t.Fatalf("租户 A 余额 = %d，应为 %d", got, amount)
		}
	})
}

// TestFundCreditPool_EventIDIsGlobalNotPerTenant 用同一个 event_id 先后给两个
// 租户注资。
//
// 防的业务事故：幂等键跨租户串了之后，第二个租户的注资被当成重放 ——
// 接口回 200 success、replayed=true，池子余额纹丝不动，而回给平台的
// new_balance 是「别人池子」的余额。付了钱的客户额度不到账，且全链没有任何
// 一处报错或告警 —— 这正是历史上「续费不注资」那类事故能潜伏一整个计费周期
// 的机制。
func TestFundCreditPool_EventIDIsGlobalNotPerTenant(t *testing.T) {
	f := setupMoneyPathRouter(t)
	t.Cleanup(f.Cleanup)

	const sharedEventID = "evt-shared-across-tenants"
	const amountA = 5_000
	const amountB = 7_000

	// 租户 A 正常注资。
	code, resp := f.fundAs(t, f.TenantA.Slug, testApiKeyAllScopes, map[string]interface{}{
		"event_id": sharedEventID, "amount": amountA, "source": "platform-billing-outbox",
	})
	if code != http.StatusOK {
		t.Fatalf("租户 A 首次注资应成功: status=%d body=%s", code, mustJSON(resp))
	}
	if got := f.balanceOf(t, f.TenantA.Id); got != amountA {
		t.Fatalf("租户 A 余额 = %d，应为 %d", got, amountA)
	}

	// 租户 B 带着同一个 event_id 来注资。
	code, resp = f.fundAs(t, f.TenantB.Slug, testApiKeyAllScopes, map[string]interface{}{
		"event_id": sharedEventID, "amount": amountB, "source": "platform-billing-outbox",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 (当前实现按重放处理), got %d body=%s", code, mustJSON(resp))
	}

	// 核心事实：B 付了钱，池子里一分没进。
	balanceB := f.balanceOf(t, f.TenantB.Id)
	if balanceB != 0 {
		t.Fatalf("缺口行为已改变：租户 B 余额 = %d（不再是 0）—— 请按下方注释更新本用例的期望", balanceB)
	}
	// 而且 A 的余额没有被 B 那 7000 影响 —— 至少钱没打错家。
	if got := f.balanceOf(t, f.TenantA.Id); got != amountA {
		t.Fatalf("租户 A 余额被跨租户重放改动: %d，应仍为 %d", got, amountA)
	}

	// 🔴 已知缺口（2026-08-11 实测）：接口对 B 回的是「成功 + 重放」，而且
	// new_balance 报的是租户 A 池子的余额。平台侧拿到 2xx 就会把这笔注资记成
	// 已完成，对账时看到的余额也是别人的数 —— 客户零额度且无人报错。
	// 正确行为应是：event_id 与 URL 上的租户不一致时拒绝（409/422），
	// 或幂等键按 (tenant_id, event_id) 复合唯一。届时本用例会红，
	// 请把下面三条断言改成新的期望并删掉本注释。
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data field: %s", mustJSON(resp))
	}
	if replayed, _ := data["replayed"].(bool); !replayed {
		t.Fatalf("缺口行为已改变：不再判为 replayed —— 请按上方注释更新本用例的期望")
	}
	if success, _ := resp["success"].(bool); !success {
		t.Fatalf("缺口行为已改变：不再回 success=true —— 请按上方注释更新本用例的期望")
	}
	reported, _ := data["new_balance"].(float64)
	if int64(reported) != amountA {
		t.Fatalf("缺口行为已改变：回报的 new_balance = %.0f（不再是租户 A 的 %d）—— 请按上方注释更新本用例的期望",
			reported, int64(amountA))
	}
	t.Logf("已知缺口成立：租户 B 用同一 event_id 注资 %d，接口回 success=true replayed=true "+
		"new_balance=%.0f（这是租户 A 的余额），而 B 的池子实际余额 = %d",
		amountB, reported, balanceB)
}
