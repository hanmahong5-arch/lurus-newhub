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

// setupFundRouter builds an isolated in-memory router for InternalFundCreditPool
// tests. It seeds one tenant + one credit pool (max_balance 100_000), two API
// keys (all-scopes and read-only), and returns the router + cleanup.
//
// Isolation: each call opens a distinct SQLite database name so parallel tests
// do not share state (matches the testDBCounter pattern used elsewhere in this
// package).
func setupFundRouter(t *testing.T) (*gin.Engine, func(), *repo.Tenant, *repo.TenantCreditPool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := "file:fundtest_" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Token{},
		&repo.InternalApiKey{},
		&repo.Option{},
		&repo.Setup{},
		&repo.Tenant{},
		&repo.TenantConfig{},
		&repo.TenantCreditPool{},
		&repo.TenantCreditPoolDraw{},
		&repo.CreditPoolFundEvent{},
	}
	for _, tbl := range tables {
		if migrateErr := db.AutoMigrate(tbl); migrateErr != nil {
			if strings.Contains(migrateErr.Error(), "already exists") {
				continue
			}
			t.Fatalf("AutoMigrate %T: %v", tbl, migrateErr)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	// Seed tenant. ZitadelOrgID must satisfy the NOT NULL + UNIQUE constraint.
	tenant := &repo.Tenant{
		Id:           "tenant-fund-test",
		Name:         "Fund Test Tenant",
		Slug:         "fund-test",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_fund_test_unique",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create test tenant: %v", err)
	}

	// Seed credit pool (max_balance=100_000, so we can test ceiling)
	now := time.Now()
	pool := &repo.TenantCreditPool{
		TenantID:          tenant.Id,
		CreatedByUserID:   1,
		CurrentBalance:    0,
		MaxBalance:        100_000,
		ResetPeriod:       repo.PoolResetMonthly,
		LastResetAt:       now,
		AlertThresholdPct: 80,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	db.Create(pool)

	// Seed all-scopes key (matches testApiKeyAllScopes constant from testutil_integration_test.go)
	allScopes, _ := json.Marshal([]string{repo.ScopeAll})
	db.Create(&repo.InternalApiKey{
		Id:      10,
		Name:    "fund-test-all",
		KeyHash: hashTestKey(testApiKeyAllScopes),
		Scopes:  string(allScopes),
		Enabled: true,
	})

	// Seed read-only key (lacks balance:write)
	readScopes, _ := json.Marshal([]string{repo.ScopeBalanceRead})
	db.Create(&repo.InternalApiKey{
		Id:      11,
		Name:    "fund-test-readonly",
		KeyHash: hashTestKey(testApiKeyReadOnly),
		Scopes:  string(readScopes),
		Enabled: true,
	})

	// Build router wiring only the fund endpoint
	router := gin.New()
	internalGroup := router.Group("/internal")
	internalGroup.Use(middleware.InternalApiAuth())

	fundGroup := internalGroup.Group("/v1/provisioning")
	fundGroup.Use(middleware.RequireScope(repo.ScopeBalanceWrite))
	fundGroup.POST("/tenants/:slug/credit-pool/fund", InternalFundCreditPool)

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return router, cleanup, tenant, pool
}

// ============================================================
// Table-driven tests
// ============================================================

// TestInternalFundCreditPool covers the five required behaviours:
//  1. Normal fund → 200 + new_balance updated
//  2. Same event_id replay → 200 + replayed=true + no double-credit
//  3. Invalid amount (zero / negative) → 400
//  4. Unknown tenant slug → 404
//  5. Read-only API key (lacks balance:write) → 403
func TestInternalFundCreditPool(t *testing.T) {
	router, cleanup, tenant, _ := setupFundRouter(t)
	t.Cleanup(cleanup)

	// Helper: POST to the fund endpoint and return the parsed response.
	fund := func(body map[string]interface{}, key string) (int, map[string]interface{}) {
		w := internalRequest(router, "POST",
			"/internal/v1/provisioning/tenants/"+tenant.Slug+"/credit-pool/fund",
			body,
			map[string]string{"X-API-Key": key},
		)
		resp := parseResponse(t, w)
		return w.Code, resp
	}

	t.Run("normal_fund_succeeds", func(t *testing.T) {
		code, resp := fund(map[string]interface{}{
			"event_id": "evt-001",
			"amount":   5000,
			"source":   "platform-billing-outbox",
		}, testApiKeyAllScopes)

		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", code, mustJSON(resp))
		}
		assertSuccess(t, resp)
		data, ok := resp["data"].(map[string]interface{})
		if !ok {
			t.Fatal("missing data field")
		}
		if data["replayed"] != false {
			t.Errorf("expected replayed=false, got %v", data["replayed"])
		}
		newBal, _ := data["new_balance"].(float64)
		if newBal != 5000 {
			t.Errorf("expected new_balance=5000, got %.0f", newBal)
		}

		// Verify pool in DB
		pool, err := repo.GetTenantCreditPool(tenant.Id)
		if err != nil {
			t.Fatalf("get pool: %v", err)
		}
		if pool.CurrentBalance != 5000 {
			t.Errorf("DB pool.CurrentBalance=%d want 5000", pool.CurrentBalance)
		}
	})

	t.Run("idempotent_replay_no_double_credit", func(t *testing.T) {
		// Second call with the same event_id must return 200 replayed=true
		// and must NOT increment the balance a second time.
		code, resp := fund(map[string]interface{}{
			"event_id": "evt-001", // same as above
			"amount":   5000,
			"source":   "platform-billing-outbox",
		}, testApiKeyAllScopes)

		if code != http.StatusOK {
			t.Fatalf("expected 200 on replay, got %d body=%s", code, mustJSON(resp))
		}
		assertSuccess(t, resp)
		data, ok := resp["data"].(map[string]interface{})
		if !ok {
			t.Fatal("missing data field on replay")
		}
		if data["replayed"] != true {
			t.Errorf("expected replayed=true, got %v", data["replayed"])
		}

		// Balance must still be 5000, not 10000
		pool, err := repo.GetTenantCreditPool(tenant.Id)
		if err != nil {
			t.Fatalf("get pool after replay: %v", err)
		}
		if pool.CurrentBalance != 5000 {
			t.Errorf("double-credit detected: pool.CurrentBalance=%d want 5000", pool.CurrentBalance)
		}
	})

	t.Run("zero_amount_rejected", func(t *testing.T) {
		code, resp := fund(map[string]interface{}{
			"event_id": "evt-bad-zero",
			"amount":   0,
			"source":   "platform-billing-outbox",
		}, testApiKeyAllScopes)

		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		ec, _ := resp["error_code"].(string)
		if ec != "INVALID_AMOUNT" {
			t.Errorf("expected error_code=INVALID_AMOUNT, got %q", ec)
		}
	})

	t.Run("negative_amount_rejected", func(t *testing.T) {
		code, resp := fund(map[string]interface{}{
			"event_id": "evt-bad-neg",
			"amount":   -100,
			"source":   "platform-billing-outbox",
		}, testApiKeyAllScopes)

		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		ec, _ := resp["error_code"].(string)
		if ec != "INVALID_AMOUNT" {
			t.Errorf("expected error_code=INVALID_AMOUNT, got %q", ec)
		}
	})

	t.Run("missing_event_id_rejected", func(t *testing.T) {
		code, resp := fund(map[string]interface{}{
			"amount": 1000,
			"source": "platform-billing-outbox",
		}, testApiKeyAllScopes)

		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		ec, _ := resp["error_code"].(string)
		if ec != "MISSING_EVENT_ID" {
			t.Errorf("expected error_code=MISSING_EVENT_ID, got %q", ec)
		}
	})

	t.Run("unknown_tenant_returns_404", func(t *testing.T) {
		w := internalRequest(router, "POST",
			"/internal/v1/provisioning/tenants/no-such-tenant/credit-pool/fund",
			map[string]interface{}{
				"event_id": "evt-unknown",
				"amount":   1000,
				"source":   "platform-billing-outbox",
			},
			map[string]string{"X-API-Key": testApiKeyAllScopes},
		)
		resp := parseResponse(t, w)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d body=%s", w.Code, mustJSON(resp))
		}
		ec, _ := resp["error_code"].(string)
		if ec != "TENANT_NOT_FOUND" {
			t.Errorf("expected error_code=TENANT_NOT_FOUND, got %q", ec)
		}
	})

	t.Run("readonly_key_forbidden", func(t *testing.T) {
		w := internalRequest(router, "POST",
			"/internal/v1/provisioning/tenants/"+tenant.Slug+"/credit-pool/fund",
			map[string]interface{}{
				"event_id": "evt-scope-check",
				"amount":   1000,
				"source":   "platform-billing-outbox",
			},
			map[string]string{"X-API-Key": testApiKeyReadOnly},
		)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", w.Code)
		}
	})
}

// TestInternalFundCreditPool_CeilingExceeded verifies that funding beyond
// max_balance returns 409 POOL_CEILING_EXCEEDED without modifying the pool.
func TestInternalFundCreditPool_CeilingExceeded(t *testing.T) {
	router, cleanup, tenant, pool := setupFundRouter(t)
	t.Cleanup(cleanup)

	// Pre-fill pool to near ceiling (max_balance = 100_000).
	if err := repo.DB.Model(&repo.TenantCreditPool{}).
		Where("id = ?", pool.ID).
		Update("current_balance", 99_000).Error; err != nil {
		t.Fatalf("pre-fill pool: %v", err)
	}

	w := internalRequest(router, "POST",
		"/internal/v1/provisioning/tenants/"+tenant.Slug+"/credit-pool/fund",
		map[string]interface{}{
			"event_id": "evt-ceiling",
			"amount":   5000, // 99_000 + 5_000 = 104_000 > 100_000
			"source":   "platform-billing-outbox",
		},
		map[string]string{"X-API-Key": testApiKeyAllScopes},
	)
	resp := parseResponse(t, w)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d body=%s", w.Code, mustJSON(resp))
	}
	ec, _ := resp["error_code"].(string)
	if ec != "POOL_CEILING_EXCEEDED" {
		t.Errorf("expected error_code=POOL_CEILING_EXCEEDED, got %q", ec)
	}

	// Balance must not have changed.
	fresh, _ := repo.GetTenantCreditPool(tenant.Id)
	if fresh.CurrentBalance != 99_000 {
		t.Errorf("pool balance mutated on ceiling error: got %d want 99_000", fresh.CurrentBalance)
	}
}

// TestInternalFundCreditPool_NoPool verifies that funding a tenant with no pool
// row returns 404 POOL_NOT_FOUND (pool absence ≠ unlimited for the fund path).
func TestInternalFundCreditPool_NoPool(t *testing.T) {
	router, cleanup, _, _ := setupFundRouter(t)
	t.Cleanup(cleanup)

	// Add a tenant with no pool row. ZitadelOrgID must be unique across the DB.
	noPoolTenant := &repo.Tenant{
		Id:           "tenant-no-pool",
		Name:         "No Pool Tenant",
		Slug:         "no-pool",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_no_pool_unique",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := repo.DB.Create(noPoolTenant).Error; err != nil {
		t.Fatalf("create no-pool tenant: %v", err)
	}

	w := internalRequest(router, "POST",
		"/internal/v1/provisioning/tenants/no-pool/credit-pool/fund",
		map[string]interface{}{
			"event_id": "evt-no-pool",
			"amount":   1000,
			"source":   "platform-billing-outbox",
		},
		map[string]string{"X-API-Key": testApiKeyAllScopes},
	)
	resp := parseResponse(t, w)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", w.Code, mustJSON(resp))
	}
	ec, _ := resp["error_code"].(string)
	if ec != "POOL_NOT_FOUND" {
		t.Errorf("expected error_code=POOL_NOT_FOUND, got %q", ec)
	}
}

// mustJSON marshals v to a JSON string for use in error messages.
func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
