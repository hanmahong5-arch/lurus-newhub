package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// TopUpV2 — Validation Tests (no external service dependency)
// ============================================================================

func TestTopUpV2_MissingIdempotencyKey(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{"amount_cny": 10.0}
	w := V2RequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body)

	AssertV2Error(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); !ok || msg != "X-Idempotency-Key header is required" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

func TestTopUpV2_InvalidAmount_TooLow(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{"amount_cny": 0.5}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-low",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Error(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	msg := resp["message"].(string)
	if msg != "amount must be between 1 and 10000 CNY" {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestTopUpV2_InvalidAmount_TooHigh(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{"amount_cny": 20000.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-high",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Error(t, w, http.StatusBadRequest)
}

func TestTopUpV2_InvalidAmount_Negative(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{"amount_cny": -5.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-neg",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Error(t, w, http.StatusBadRequest)
}

func TestTopUpV2_MissingBody(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-nobody",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), nil, headers)

	AssertV2Error(t, w, http.StatusBadRequest)
}

func TestTopUpV2_NoPlatformAccount(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Build router WITHOUT identity_account_id in mock auth
	router := ctx.Router

	// Create a separate route that doesn't set identity_account_id
	noAccountRouter := setupNoAccountRouter(t, ctx)

	body := map[string]interface{}{"amount_cny": 10.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-noaccount",
	}

	// Use the no-account router
	_ = router // original router is not used in this specific test
	w := V2Request(noAccountRouter, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Error(t, w, http.StatusServiceUnavailable)
	resp := ParseV2Response(t, w)
	if msg := resp["message"].(string); msg != "platform account not linked" {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestTopUpV2_IdempotencyDuplicate(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed a topup log with an IK tag to simulate a previous transfer
	ikKey := "dup-key-12345"
	log := &repo.Log{
		UserId:    ctx.UserID,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeTopup,
		Content:   fmt.Sprintf("Wallet transfer: 10.00 CNY -> quota. [IK:%s]", ikKey),
		CreatedAt: common.GetTimestamp(),
	}
	ctx.DB.Create(log)

	body := map[string]interface{}{"amount_cny": 10.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": ikKey,
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if idempotent, ok := resp["idempotent"].(bool); !ok || !idempotent {
		t.Errorf("expected idempotent=true, got %v", resp["idempotent"])
	}
	if resp["message"] != "Already processed" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
}

// ============================================================================
// GetTopUpsV2 — Tests
// ============================================================================

func TestGetTopUpsV2_EmptyHistory(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
	if total := data["total"].(float64); total != 0 {
		t.Errorf("expected total=0, got %v", total)
	}
}

func TestGetTopUpsV2_WithData(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed 3 topup logs
	for i := 0; i < 3; i++ {
		log := &repo.Log{
			UserId:    ctx.UserID,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Wallet transfer #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i),
		}
		ctx.DB.Create(log)
	}

	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
	if total := data["total"].(float64); total != 3 {
		t.Errorf("expected total=3, got %v", total)
	}
}

func TestGetTopUpsV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed 25 topup logs
	for i := 0; i < 25; i++ {
		log := &repo.Log{
			UserId:    ctx.UserID,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Wallet transfer #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i),
		}
		ctx.DB.Create(log)
	}

	// Page 1 (default size 20)
	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups?p=1&size=20", ctx.TenantID), nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 20 {
		t.Errorf("page 1: expected 20 items, got %d", len(items))
	}
	if total := data["total"].(float64); total != 25 {
		t.Errorf("expected total=25, got %v", total)
	}

	// Page 2
	w = V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups?p=2&size=20", ctx.TenantID), nil)

	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 5 {
		t.Errorf("page 2: expected 5 items, got %d", len(items))
	}
}

func TestGetTopUpsV2_UserIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed logs for normal user (ctx.UserID)
	for i := 0; i < 2; i++ {
		log := &repo.Log{
			UserId:    ctx.UserID,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Normal user topup #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i),
		}
		ctx.DB.Create(log)
	}

	// Seed logs for admin user
	for i := 0; i < 3; i++ {
		log := &repo.Log{
			UserId:    ctx.AdminUser.Id,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Admin user topup #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i),
		}
		ctx.DB.Create(log)
	}

	// Normal user should only see their 2 logs
	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil)

	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("normal user: expected 2 items, got %d", len(items))
	}

	// Admin user should only see their 3 logs
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil, nil)

	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	items = data["items"].([]interface{})
	if len(items) != 3 {
		t.Errorf("admin user: expected 3 items, got %d", len(items))
	}
}

// ============================================================================
// billingView — ForbiddenFields + CrossTenantIsolation
// ============================================================================

// TestGetTopUpsV2_ForbiddenFields asserts that the topup history endpoint
// never exposes payment_method_id, raw provider transaction IDs, or internal
// tenant-scoped billing counters in the response items. The response shape is
// a gin.H literal (not a raw entity) so this test verifies the literal keys.
func TestGetTopUpsV2_ForbiddenFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed one topup log.
	log := &repo.Log{
		UserId:    ctx.UserID,
		TenantId:  ctx.TenantID,
		Type:      repo.LogTypeTopup,
		Content:   "Wallet transfer: 5.00 CNY -> quota. [IK:testik]",
		Quota:     25000,
		CreatedAt: common.GetTimestamp(),
	}
	ctx.DB.Create(log)

	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0].(map[string]interface{})

	// These fields must not appear in the response.
	for _, forbidden := range []string{
		"payment_method_id", "stripe_id", "alipay_id", "wechat_id",
		"tenant_id", "user_id", "internal_counter",
	} {
		if _, ok := item[forbidden]; ok {
			t.Errorf("forbidden field %q leaked through billing topup response", forbidden)
		}
	}

	// Whitelisted fields must be present.
	for _, required := range []string{"id", "content", "quota", "created_at"} {
		if _, ok := item[required]; !ok {
			t.Errorf("expected field %q in billing topup response, not found", required)
		}
	}
}

// TestGetTopUpsV2_CrossTenantIsolation seeds logs for two different tenants
// and verifies the endpoint only returns the caller's own tenant data.
func TestGetTopUpsV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed 2 topup logs for the test tenant user.
	for i := 0; i < 2; i++ {
		l := &repo.Log{
			UserId:    ctx.UserID,
			TenantId:  ctx.TenantID,
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Own-tenant transfer #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i),
		}
		ctx.DB.Create(l)
	}

	// Seed 3 topup logs for a different tenant (same user_id to maximise leak
	// surface — any query without tenant filter would return 5 instead of 2).
	for i := 0; i < 3; i++ {
		l := &repo.Log{
			UserId:    ctx.UserID,
			TenantId:  "other-tenant-billing",
			Type:      repo.LogTypeTopup,
			Content:   fmt.Sprintf("Cross-tenant transfer #%d", i+1),
			CreatedAt: common.GetTimestamp() + int64(i+10),
		}
		ctx.DB.Create(l)
	}

	w := V2RequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("/api/v2/%s/billing/topups", ctx.TenantID), nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("expected 2 items (own-tenant only), got %d — possible cross-tenant leak", len(items))
	}
	if total := data["total"].(float64); total != 2 {
		t.Errorf("expected total=2, got %v — cross-tenant rows must not be counted", total)
	}
}

// ============================================================================
// Helpers
// ============================================================================

// setupNoAccountRouter creates a test router where identity_account_id is NOT set (simulating no platform account).
func setupNoAccountRouter(t *testing.T, ctx *V2TestContext) *gin.Engine {
	t.Helper()
	router := gin.New()

	mockAuth := func(c *gin.Context) {
		tenantCtx := &middleware.TenantContext{
			TenantID:      ctx.TenantID,
			UserID:        ctx.UserID,
			IDPSubject: "zitadel_test_user",
			Email:         "test@test.local",
			Username:      "testuser",
		}
		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", ctx.TenantID)
		c.Set("user_id", ctx.UserID)
		// identity_account_id is intentionally NOT set
		c.Next()
	}

	v2 := router.Group("/api/v2/:tenant_slug")
	v2.Use(mockAuth)
	{
		v2.POST("/billing/topup", TopUpV2)
	}

	return router
}

// ============================================================================
// TopUpV2 — product-id SSOT (L4 / PRODUCT-ID-SSOT): the wallet debit — and
// the rollback credit on a mid-flight failure — must be filed under
// ratio_setting.DefaultSourceProduct ("llm-api"), not the retired "lurus-api"
// literal. A platform reading the wallet ledger by product_id must see the
// SAME id newhub's own entitlement gate asks about.
// ============================================================================

func TestTopUpV2_DebitRecordsDefaultSourceProduct(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	var gotProductIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProductID string `json:"product_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotProductIDs = append(gotProductIDs, body.ProductID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"balance_after": 990.0,
		})
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	body := map[string]interface{}{"amount_cny": 10.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-product-debit",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Status(t, w, http.StatusOK)
	if len(gotProductIDs) != 1 {
		t.Fatalf("identity server hit %d times, want 1 (debit only); product_ids=%v", len(gotProductIDs), gotProductIDs)
	}
	if gotProductIDs[0] != ratio_setting.DefaultSourceProduct {
		t.Errorf("debit product_id = %q, want %q", gotProductIDs[0], ratio_setting.DefaultSourceProduct)
	}
}

func TestTopUpV2_RollbackCreditRecordsDefaultSourceProduct(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	var gotProductIDs []string
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProductID string `json:"product_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotProductIDs = append(gotProductIDs, body.ProductID)
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"balance_after": 990.0,
		})
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	// Force repo.IncreaseUserQuota to fail deterministically (quotaAmount goes
	// negative) so TopUpV2 takes the rollback branch without touching DB
	// integrity — increaseUserQuota rejects a negative quota outright.
	prevRatio := common.QuotaPerUnit
	common.QuotaPerUnit = -1
	defer func() { common.QuotaPerUnit = prevRatio }()

	body := map[string]interface{}{"amount_cny": 10.0}
	headers := map[string]string{
		"X-Test-Tenant-ID":  ctx.TenantID,
		"X-Test-User-ID":    fmt.Sprintf("%d", ctx.UserID),
		"X-Idempotency-Key": "test-ik-product-rollback",
	}
	w := V2Request(ctx.Router, http.MethodPost,
		fmt.Sprintf("/api/v2/%s/billing/topup", ctx.TenantID), body, headers)

	AssertV2Error(t, w, http.StatusInternalServerError)
	if len(gotProductIDs) != 2 {
		t.Fatalf("identity server hit %d times, want 2 (debit + rollback credit); paths=%v", len(gotProductIDs), gotPaths)
	}
	if gotPaths[1] == "" || !strings.HasSuffix(gotPaths[1], "/wallet/credit") {
		t.Fatalf("second call path = %q, want the wallet/credit rollback endpoint", gotPaths[1])
	}
	if gotProductIDs[1] != ratio_setting.DefaultSourceProduct {
		t.Errorf("rollback credit product_id = %q, want %q", gotProductIDs[1], ratio_setting.DefaultSourceProduct)
	}
}
