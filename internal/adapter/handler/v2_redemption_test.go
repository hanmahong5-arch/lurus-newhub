package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// V2 Redemption Controller Tests
// ============================================================================

func TestRedeemCodeV2_InvalidFormat(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	invalidCodes := []string{
		"short", // Too short
		"this-is-way-too-long-for-a-redemption-code-32chars", // Too long
		"",                                    // Empty
		"12345678901234567890123456789012345", // 35 chars (not 32)
	}

	for _, code := range invalidCodes {
		if code == "" {
			continue // Empty code fails at binding level
		}

		body := map[string]string{
			"code": code,
		}

		w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for code %q (len=%d), got %d", code, len(code), w.Code)
		}
	}
}

func TestRedeemCodeV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a redemption code
	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)
	initialQuota := ctx.NormalUser.Quota

	body := map[string]string{
		"code": redemption.Key,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	quotaAdded := int(data["quota_added"].(float64))
	if quotaAdded != redemption.Quota {
		t.Errorf("expected quota_added=%d, got %d", redemption.Quota, quotaAdded)
	}

	// Verify user's quota increased
	var updatedUser struct {
		Quota int
	}
	ctx.DB.Table("users").Where("id = ?", ctx.NormalUser.Id).Select("quota").First(&updatedUser)
	expectedQuota := initialQuota + redemption.Quota
	if updatedUser.Quota != expectedQuota {
		t.Errorf("expected user quota=%d, got %d", expectedQuota, updatedUser.Quota)
	}
}

func TestRedeemCodeV2_AlreadyRedeemed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create and redeem a code
	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	body := map[string]string{
		"code": redemption.Key,
	}

	// First redemption should succeed
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)
	AssertV2Status(t, w, http.StatusOK)

	// Second redemption should fail
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)
	resp := AssertV2Error(t, w, http.StatusBadRequest)
	// cycle13 L3: the response now carries a machine-readable error_code
	// alongside the (Chinese, switch-contract-pinned) message text, so a v2
	// console/API caller doesn't have to string-match the message to tell an
	// already-used code apart from an expired or cross-tenant one.
	if code, _ := resp["error_code"].(string); code != repo.RedemptionErrorCodeUsed {
		t.Errorf("error_code = %q, want %q; body: %s", code, repo.RedemptionErrorCodeUsed, w.Body.String())
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "已使用") {
		t.Errorf("message = %q, want it to contain the switch-classifier substring '已使用'", msg)
	}
}

// TestRedeemCodeV2_RawDBErrorReturnsGenericMessage is the v2 sibling of
// switch_redeem_test.go's TestSwitchRedeemAnonymous_RawDBErrorSanitized:
// before cycle13 L3, RedeemCodeV2 echoed repo.Redeem's error verbatim
// (err.Error()) into the response body — for a genuine transaction/driver
// failure that included raw SQL error text (column/constraint names). It now
// goes through repo.RedemptionErrorMessage like the other two redeem
// handlers, so the caller only ever sees the fixed, safe fallback text.
func TestRedeemCodeV2_RawDBErrorReturnsGenericMessage(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	// Break users.quota so repo.Redeem's `UPDATE users SET quota = quota + ?`
	// fails with a raw driver error instead of one of its sentinels.
	if err := ctx.DB.Exec(`ALTER TABLE users DROP COLUMN quota`).Error; err != nil {
		t.Fatalf("drop quota column: %v", err)
	}

	body := map[string]string{"code": redemption.Key}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)
	// A hub-side fault is a 500 (redemptionFailureStatus), not the 400 a
	// mistyped code gets.
	resp := AssertV2Error(t, w, http.StatusInternalServerError)

	msg, _ := resp["message"].(string)
	if msg != repo.ErrRedemptionFailed.Error() {
		t.Errorf("message = %q, want the generic fallback %q", msg, repo.ErrRedemptionFailed.Error())
	}
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "quota") || strings.Contains(lower, "column") || strings.Contains(lower, "sql") {
		t.Errorf("message leaks raw DB error text: %q", msg)
	}
	if code, _ := resp["error_code"].(string); code != repo.RedemptionErrorCodeFailed {
		t.Errorf("error_code = %q, want %q", code, repo.RedemptionErrorCodeFailed)
	}
}

func TestRedeemCodeV2_NonexistentCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]string{
		"code": "12345678901234567890123456789012", // Valid format but doesn't exist
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestListRedemptionsV2_AdminOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create some redemption codes
	SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	// Try as non-admin
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/redemptions", nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)

	// Try as admin
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/redemptions", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
}

func TestCreateRedemptionV2_BatchGeneration(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":  "Batch Test Codes",
		"quota": 50000,
		"count": 5, // Generate 5 codes
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/redemptions", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusCreated)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	codes := data["codes"].([]interface{})
	if len(codes) != 5 {
		t.Errorf("expected 5 codes, got %d", len(codes))
	}

	count := int(data["count"].(float64))
	if count != 5 {
		t.Errorf("expected count=5, got %d", count)
	}

	// Verify each code has an ID and key
	for i, code := range codes {
		c := code.(map[string]interface{})
		if c["id"] == nil {
			t.Errorf("code %d missing id", i)
		}
		key, ok := c["key"].(string)
		if !ok || len(key) != 32 {
			t.Errorf("code %d has invalid key: %v", i, c["key"])
		}
	}
}

func TestCreateRedemptionV2_MaxCount(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try to create more than 100 codes
	body := map[string]interface{}{
		"name":  "Too Many Codes",
		"quota": 1000,
		"count": 101, // Exceeds max
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/redemptions", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestCreateRedemptionV2_NameTooLong(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	longName := make([]byte, 51)
	for i := range longName {
		longName[i] = 'a'
	}

	body := map[string]interface{}{
		"name":  string(longName),
		"quota": 1000,
		"count": 1,
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/redemptions", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Name too long (max 50 characters)" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteRedemptionV2_TenantVerification(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a redemption in a different tenant
	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)
	redemption.TenantId = "other-tenant-123"
	ctx.DB.Save(redemption)

	// Try to delete from the test tenant
	path := fmt.Sprintf("/api/v2/test-tenant/redemptions/%d", redemption.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Access denied" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteRedemptionV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a redemption
	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	// Delete it
	path := fmt.Sprintf("/api/v2/test-tenant/redemptions/%d", redemption.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	// Verify it's deleted
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/redemptions", nil, []string{"admin"})
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 0 {
		t.Errorf("expected 0 redemptions after deletion, got %d", total)
	}
}

func TestListRedemptionsV2_KeyMasking(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a redemption
	redemption := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)
	originalKey := redemption.Key

	// List redemptions
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/redemptions", nil, []string{"admin"})
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	redemptions := data["redemptions"].([]interface{})

	if len(redemptions) == 0 {
		t.Fatal("expected at least 1 redemption")
	}

	r := redemptions[0].(map[string]interface{})
	maskedKey, ok := r["key"].(string)
	if !ok {
		t.Fatal("expected key field")
	}

	// Key should be masked (not the original) for enabled codes
	if r["status"].(float64) == float64(common.RedemptionCodeStatusEnabled) {
		if maskedKey == originalKey {
			t.Error("key should be masked in response")
		}
		if !strings.Contains(maskedKey, "****") {
			t.Errorf("expected masked key to contain ****, got: %s", maskedKey)
		}
	}
}

func TestCreateRedemptionV2_ExpiredTime(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try to create with expired time in the past
	pastTime := common.GetTimestamp() - 3600 // 1 hour ago

	body := map[string]interface{}{
		"name":         "Expired Code",
		"quota":        1000,
		"count":        1,
		"expired_time": pastTime,
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/redemptions", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Expiration time must be in the future" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestListRedemptionsV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create 15 redemptions
	for i := 0; i < 15; i++ {
		SeedV2Redemption(t, ctx, ctx.AdminUser.Id)
	}

	// Get first page
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/redemptions?page=1&page_size=10", nil, []string{"admin"})
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	redemptions := data["redemptions"].([]interface{})
	if len(redemptions) != 10 {
		t.Errorf("expected 10 redemptions on first page, got %d", len(redemptions))
	}

	total := int(data["total"].(float64))
	if total != 15 {
		t.Errorf("expected total=15, got %d", total)
	}
}

func TestRedeemCodeV2_MissingCode(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Send request with empty code field
	body := map[string]string{
		"code": "",
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestCreateRedemptionV2_QuotaExceedsMax(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Quota exceeds maximum allowed value (max = 1e9 * QuotaPerUnit = 5e14)
	body := map[string]interface{}{
		"name":  "Excessive Quota",
		"quota": 999999999999999, // Above 5e14 max
		"count": 1,
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/redemptions", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Quota value exceeds maximum allowed" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteRedemptionV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, "/api/v2/test-tenant/redemptions/invalid", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Invalid redemption ID" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteRedemptionV2_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, "/api/v2/test-tenant/redemptions/99999", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Redemption code not found" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

// TestListRedemptionsV2_CrossTenantIsolation guards against the pre-fix bug
// where ListRedemptionsV2 called repo.GetAllRedemptions (no tenant filter)
// and admins of any tenant could enumerate codes belonging to other tenants.
// Also asserts the response excludes view-struct forbidden fields.
func TestListRedemptionsV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	other := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    "other-tenant-xyz",
		Key:         common.GetRandomString(32),
		Name:        "Cross-tenant code",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(other); err != nil {
		t.Fatalf("failed to seed cross-tenant redemption: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/redemptions", nil, []string{"admin"})
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 redemption (tenant-scoped), got %d — possible cross-tenant leak", total)
	}

	redemptions := data["redemptions"].([]interface{})
	if len(redemptions) != 1 {
		t.Fatalf("expected 1 row, got %d", len(redemptions))
	}

	r := redemptions[0].(map[string]interface{})
	if name, _ := r["name"].(string); name == "Cross-tenant code" {
		t.Error("cross-tenant redemption leaked into the response")
	}

	for _, f := range []string{"user_id", "tenant_id", "deleted_at", "DeletedAt"} {
		if _, exists := r[f]; exists {
			t.Errorf("forbidden field %q leaked through view struct", f)
		}
	}
}
