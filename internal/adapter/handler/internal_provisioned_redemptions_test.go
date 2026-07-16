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

// setupProvisionedRedemptionRouter builds an isolated in-memory router for the
// distributor batch redemption endpoints. It seeds one tenant, an all-scopes
// API key and a read-only key (lacks "provisioning"), and returns the router +
// cleanup. Mirrors setupFundRouter.
func setupProvisionedRedemptionRouter(t *testing.T) (*gin.Engine, func(), *repo.Tenant) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := "file:provredeemtest_" + t.Name() + "?mode=memory&cache=shared"
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
		&repo.Redemption{},
		&repo.ProvisionedRedemptionBatch{},
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

	tenant := &repo.Tenant{
		Id:        "tenant-provredeem-test",
		Name:      "Provisioned Redemption Test Tenant",
		Slug:      "provredeem-test",
		Status:    repo.TenantStatusEnabled,
		IDPOrgID:  "org_provredeem_test_unique",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create test tenant: %v", err)
	}

	// All-scopes key: passes RequireScope(provisioning) and the tenant
	// allowlist guard (ScopeAll bypass).
	allScopes, _ := json.Marshal([]string{repo.ScopeAll})
	db.Create(&repo.InternalApiKey{
		Id:      20,
		Name:    "provredeem-test-all",
		KeyHash: hashTestKey(testApiKeyAllScopes),
		Scopes:  string(allScopes),
		Enabled: true,
	})

	// Read-only key: lacks the provisioning scope entirely.
	readScopes, _ := json.Marshal([]string{repo.ScopeBalanceRead})
	db.Create(&repo.InternalApiKey{
		Id:      21,
		Name:    "provredeem-test-readonly",
		KeyHash: hashTestKey(testApiKeyReadOnly),
		Scopes:  string(readScopes),
		Enabled: true,
	})

	router := gin.New()
	internalGroup := router.Group("/internal")
	internalGroup.Use(middleware.InternalApiAuth())

	provGroup := internalGroup.Group("/v1/provisioning")
	provGroup.Use(middleware.RequireScope(repo.ScopeProvisioning))
	provGroup.POST("/tenants/:slug/redemptions", InternalProvisionRedemptions)
	provGroup.POST("/tenants/:slug/redemptions/revoke", InternalRevokeProvisionedRedemptions)

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

	return router, cleanup, tenant
}

// issueBatch POSTs to the issuance endpoint and returns (status, parsed body).
func issueBatch(t *testing.T, router *gin.Engine, slug string, body map[string]interface{}, key string) (int, map[string]interface{}) {
	t.Helper()
	w := internalRequest(router, "POST",
		"/internal/v1/provisioning/tenants/"+slug+"/redemptions",
		body,
		map[string]string{"X-API-Key": key},
	)
	return w.Code, parseResponse(t, w)
}

// revokeBatch POSTs to the revoke endpoint and returns (status, parsed body).
func revokeBatch(t *testing.T, router *gin.Engine, slug string, body map[string]interface{}, key string) (int, map[string]interface{}) {
	t.Helper()
	w := internalRequest(router, "POST",
		"/internal/v1/provisioning/tenants/"+slug+"/redemptions/revoke",
		body,
		map[string]string{"X-API-Key": key},
	)
	return w.Code, parseResponse(t, w)
}

// dataCodes extracts data.codes from a parsed response as []string.
func dataCodes(t *testing.T, resp map[string]interface{}) []string {
	t.Helper()
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data field: %s", mustJSON(resp))
	}
	raw, ok := data["codes"].([]interface{})
	if !ok {
		t.Fatalf("missing data.codes: %s", mustJSON(resp))
	}
	codes := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		codes = append(codes, s)
	}
	return codes
}

// TestInternalProvisionRedemptions_Idempotent covers the core contract:
//  1. First call → 200, replayed=false, N codes minted and persisted with the
//     tenant / quota / name / expiry the request asked for.
//  2. Replay with the same event_id → 200, replayed=true, the ORIGINAL codes
//     come back and no extra redemption rows exist.
func TestInternalProvisionRedemptions_Idempotent(t *testing.T) {
	router, cleanup, tenant := setupProvisionedRedemptionRouter(t)
	t.Cleanup(cleanup)

	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"event_id":       "evt-batch-001",
		"count":          3,
		"quota_per_code": 2500,
		"name_prefix":    "campaign-x",
		"expires_at":     expiresAt.Format(time.RFC3339),
	}

	code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", code, mustJSON(resp))
	}
	assertSuccess(t, resp)
	data := resp["data"].(map[string]interface{})
	if data["replayed"] != false {
		t.Errorf("expected replayed=false, got %v", data["replayed"])
	}
	firstCodes := dataCodes(t, resp)
	if len(firstCodes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(firstCodes))
	}

	// Verify persisted redemption rows: tenant, quota, status, name, expiry.
	var rows []*repo.Redemption
	if err := repo.DB.Where("tenant_id = ?", tenant.Id).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load redemptions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 redemption rows in DB, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Key != firstCodes[i] {
			t.Errorf("row %d key=%q want %q", i, r.Key, firstCodes[i])
		}
		if r.Quota != 2500 {
			t.Errorf("row %d quota=%d want 2500", i, r.Quota)
		}
		if r.Status != common.RedemptionCodeStatusEnabled {
			t.Errorf("row %d status=%d want enabled(%d)", i, r.Status, common.RedemptionCodeStatusEnabled)
		}
		wantName := "campaign-x-" + string(rune('1'+i))
		if r.Name != wantName {
			t.Errorf("row %d name=%q want %q", i, r.Name, wantName)
		}
		if r.ExpiredTime != expiresAt.Unix() {
			t.Errorf("row %d expired_time=%d want %d", i, r.ExpiredTime, expiresAt.Unix())
		}
	}

	// Replay: same event_id → replayed=true, same codes, no new rows.
	code, resp = issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
	if code != http.StatusOK {
		t.Fatalf("expected 200 on replay, got %d body=%s", code, mustJSON(resp))
	}
	assertSuccess(t, resp)
	data = resp["data"].(map[string]interface{})
	if data["replayed"] != true {
		t.Errorf("expected replayed=true, got %v", data["replayed"])
	}
	replayCodes := dataCodes(t, resp)
	if len(replayCodes) != len(firstCodes) {
		t.Fatalf("replay returned %d codes, want %d", len(replayCodes), len(firstCodes))
	}
	for i := range firstCodes {
		if replayCodes[i] != firstCodes[i] {
			t.Errorf("replay code %d = %q, want original %q", i, replayCodes[i], firstCodes[i])
		}
	}

	var count int64
	repo.DB.Model(&repo.Redemption{}).Where("tenant_id = ?", tenant.Id).Count(&count)
	if count != 3 {
		t.Errorf("replay minted extra codes: %d redemption rows, want 3", count)
	}
	var batches int64
	repo.DB.Model(&repo.ProvisionedRedemptionBatch{}).Where("event_id = ?", "evt-batch-001").Count(&batches)
	if batches != 1 {
		t.Errorf("expected exactly 1 batch ledger row, got %d", batches)
	}
}

// TestInternalProvisionRedemptions_Validation covers the reject paths:
// count out of [1,500], non-positive quota, missing event_id, malformed
// expires_at, unknown tenant, and a key without the provisioning scope.
func TestInternalProvisionRedemptions_Validation(t *testing.T) {
	router, cleanup, tenant := setupProvisionedRedemptionRouter(t)
	t.Cleanup(cleanup)

	valid := func() map[string]interface{} {
		return map[string]interface{}{
			"event_id":       "evt-validation",
			"count":          5,
			"quota_per_code": 100,
		}
	}

	t.Run("count_above_500_rejected", func(t *testing.T) {
		body := valid()
		body["count"] = 501
		code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "INVALID_COUNT")
	})

	t.Run("count_zero_rejected", func(t *testing.T) {
		body := valid()
		body["count"] = 0
		code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "INVALID_COUNT")
	})

	t.Run("non_positive_quota_rejected", func(t *testing.T) {
		body := valid()
		body["quota_per_code"] = 0
		code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "INVALID_QUOTA_PER_CODE")
	})

	t.Run("missing_event_id_rejected", func(t *testing.T) {
		body := valid()
		delete(body, "event_id")
		code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "MISSING_EVENT_ID")
	})

	t.Run("malformed_expires_at_rejected", func(t *testing.T) {
		body := valid()
		body["expires_at"] = "2026/12/31"
		code, resp := issueBatch(t, router, tenant.Slug, body, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "INVALID_EXPIRES_AT")
	})

	t.Run("unknown_tenant_returns_404", func(t *testing.T) {
		code, resp := issueBatch(t, router, "no-such-tenant", valid(), testApiKeyAllScopes)
		if code != http.StatusNotFound {
			t.Errorf("expected 404, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "TENANT_NOT_FOUND")
	})

	t.Run("key_without_provisioning_scope_forbidden", func(t *testing.T) {
		code, _ := issueBatch(t, router, tenant.Slug, valid(), testApiKeyReadOnly)
		if code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", code)
		}
	})

	// None of the rejected calls above may have minted anything.
	var count int64
	repo.DB.Model(&repo.Redemption{}).Count(&count)
	if count != 0 {
		t.Errorf("rejected requests minted %d redemption rows, want 0", count)
	}
}

// TestInternalRevokeProvisionedRedemptions verifies that revoke disables ONLY
// the still-unused codes of the batch, is idempotent (second revoke = 0), and
// 404s on an unknown event_id.
func TestInternalRevokeProvisionedRedemptions(t *testing.T) {
	router, cleanup, tenant := setupProvisionedRedemptionRouter(t)
	t.Cleanup(cleanup)

	// Issue a batch of 3.
	code, resp := issueBatch(t, router, tenant.Slug, map[string]interface{}{
		"event_id":       "evt-revoke-001",
		"count":          3,
		"quota_per_code": 100,
	}, testApiKeyAllScopes)
	if code != http.StatusOK {
		t.Fatalf("issue batch: expected 200, got %d body=%s", code, mustJSON(resp))
	}
	codes := dataCodes(t, resp)
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(codes))
	}

	// Simulate one code already redeemed (same quoting convention as repo.Redeem).
	usedCode := codes[0]
	if err := repo.DB.Model(&repo.Redemption{}).
		Where(`"key" = ?`, usedCode).
		Updates(map[string]interface{}{
			"status":        common.RedemptionCodeStatusUsed,
			"used_user_id":  7,
			"redeemed_time": common.GetTimestamp(),
		}).Error; err != nil {
		t.Fatalf("mark code used: %v", err)
	}

	// Revoke: only the 2 unused codes may be disabled.
	code, resp = revokeBatch(t, router, tenant.Slug, map[string]interface{}{
		"event_id": "evt-revoke-001",
		"reason":   "distributor contract terminated",
	}, testApiKeyAllScopes)
	if code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d body=%s", code, mustJSON(resp))
	}
	assertSuccess(t, resp)
	data := resp["data"].(map[string]interface{})
	revoked, _ := data["revoked_count"].(float64)
	if revoked != 2 {
		t.Errorf("expected revoked_count=2, got %v", data["revoked_count"])
	}

	// DB state: used code untouched, the other two disabled.
	var used repo.Redemption
	if err := repo.DB.Where(`"key" = ?`, usedCode).First(&used).Error; err != nil {
		t.Fatalf("load used code: %v", err)
	}
	if used.Status != common.RedemptionCodeStatusUsed {
		t.Errorf("used code status mutated: got %d want %d", used.Status, common.RedemptionCodeStatusUsed)
	}
	for _, c := range codes[1:] {
		var r repo.Redemption
		if err := repo.DB.Where(`"key" = ?`, c).First(&r).Error; err != nil {
			t.Fatalf("load code %q: %v", c, err)
		}
		if r.Status != common.RedemptionCodeStatusDisabled {
			t.Errorf("code %q status=%d want disabled(%d)", c, r.Status, common.RedemptionCodeStatusDisabled)
		}
	}

	// Second revoke is a no-op (naturally idempotent).
	code, resp = revokeBatch(t, router, tenant.Slug, map[string]interface{}{
		"event_id": "evt-revoke-001",
	}, testApiKeyAllScopes)
	if code != http.StatusOK {
		t.Fatalf("second revoke: expected 200, got %d body=%s", code, mustJSON(resp))
	}
	data = resp["data"].(map[string]interface{})
	if revoked, _ := data["revoked_count"].(float64); revoked != 0 {
		t.Errorf("second revoke: expected revoked_count=0, got %v", data["revoked_count"])
	}

	t.Run("unknown_event_id_returns_404", func(t *testing.T) {
		code, resp := revokeBatch(t, router, tenant.Slug, map[string]interface{}{
			"event_id": "evt-never-issued",
		}, testApiKeyAllScopes)
		if code != http.StatusNotFound {
			t.Errorf("expected 404, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "BATCH_NOT_FOUND")
	})

	t.Run("missing_event_id_rejected", func(t *testing.T) {
		code, resp := revokeBatch(t, router, tenant.Slug, map[string]interface{}{
			"reason": "no event id",
		}, testApiKeyAllScopes)
		if code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%s", code, mustJSON(resp))
		}
		assertErrorCode(t, resp, "MISSING_EVENT_ID")
	})
}
