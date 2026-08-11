package handler

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// R2Bill — billing / tenant / currency / release / provisioning handler tests
//
// These exercise the HTTP handlers in internal_api*.go, internal_currency.go,
// tenant.go, tenant_credit_pool.go, billing.go, v2_billing.go, v2_redemption.go,
// switch_redeem.go, release.go and provisioning.go by invoking them directly
// against gin test contexts backed by the package's in-memory SQLite harness
// (SetupV2TestRouter). We reuse that harness's seed data + the r2chan* context
// builders (same package) and only migrate the extra tables these handlers hit.
// ============================================================================

// r2billSetup reuses the V2 harness and migrates the additional tables the
// R2Bill handlers depend on (currency exchanges, internal api keys, releases).
func r2billSetup(t *testing.T) *V2TestContext {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	extra := []interface{}{
		&entity.CurrencyExchange{},
		&repo.InternalApiKey{},
		&entity.Release{},
		&entity.ReleaseArtifact{},
	}
	for _, tbl := range extra {
		if err := ctx.DB.AutoMigrate(tbl); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}
	return ctx
}

// ---------------------------------------------------------------------------
// internal_currency.go
// ---------------------------------------------------------------------------

func TestR2Bill_InternalGetCurrencyInfo(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	c, w := r2chanNewCtx(http.MethodGet, "/internal/currency/info", nil)
	InternalGetCurrencyInfo(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if len(data["currencies"].([]interface{})) != 3 {
		t.Errorf("expected 3 currencies, got %v", data["currencies"])
	}
	if len(data["vip_bonuses"].([]interface{})) != 5 {
		t.Errorf("expected 5 vip bonus tiers, got %v", data["vip_bonuses"])
	}
}

func TestR2Bill_InternalGetModelPricing(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	c, w := r2chanNewCtx(http.MethodGet, "/internal/currency/models/pricing?group=default", nil)
	InternalGetModelPricing(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if data["group"] != "default" {
		t.Errorf("expected group=default, got %v", data["group"])
	}
	// currency label must be the LUT code
	if data["currency"] == nil {
		t.Error("expected currency code in response")
	}

	// model filter branch (unlikely to match anything → total 0, still success)
	c2, w2 := r2chanNewCtx(http.MethodGet, "/internal/currency/models/pricing?model=__no_such_model__", nil)
	InternalGetModelPricing(c2)
	if r2chanParseBody(t, w2)["success"] != true {
		t.Errorf("filtered pricing not success: %s", w2.Body.String())
	}
}

func TestR2Bill_InternalGetUserBalanceLute(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// invalid id
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	InternalGetUserBalanceLute(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid id, got %d", w.Code)
	}

	// not found
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	InternalGetUserBalanceLute(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing user, got %d", w.Code)
	}

	// valid user
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(ctx.NormalUser.Id)}}
	InternalGetUserBalanceLute(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if int(data["user_id"].(float64)) != ctx.NormalUser.Id {
		t.Errorf("expected user_id=%d, got %v", ctx.NormalUser.Id, data["user_id"])
	}
}

func TestR2Bill_InternalGetExchangeHistory(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// invalid id
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "0"}}
	InternalGetExchangeHistory(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	// seed one exchange for the normal user
	if err := ctx.DB.Create(&entity.CurrencyExchange{
		UserId:         ctx.NormalUser.Id,
		SourceCurrency: "LUC",
		SourceAmount:   10,
		TargetCurrency: "LUT",
		TargetAmount:   5000000,
		ExchangeRate:   500000,
		ReferenceId:    "hist-ref-1",
	}).Error; err != nil {
		t.Fatalf("seed exchange: %v", err)
	}

	c, w = r2chanNewCtx(http.MethodGet, "/?page=1&page_size=20", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(ctx.NormalUser.Id)}}
	InternalGetExchangeHistory(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", data["total"])
	}
}

func TestR2Bill_InternalExchangeLucToLut(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// invalid body (missing required fields)
	c, w := r2chanNewCtx(http.MethodPost, "/", []byte("{bad"))
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}

	// negative user id passes `required` (non-zero) but fails <=0 guard
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"user_id": -1, "luc_amount": 5.0, "reference_id": "r-neg",
	})
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative user id, got %d", w.Code)
	}

	// amount over cap
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"user_id": ctx.NormalUser.Id, "luc_amount": 200000.0, "reference_id": "r-cap",
	})
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for over-cap amount, got %d", w.Code)
	}
	if r2chanParseBody(t, w)["error_code"] != "VALIDATION_FAILED" {
		t.Errorf("expected VALIDATION_FAILED, got %s", w.Body.String())
	}

	// unknown user
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"user_id": 987654, "luc_amount": 5.0, "reference_id": "r-nouser",
	})
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown user, got %d", w.Code)
	}

	// disabled user
	disabled := &repo.User{
		Username: "disabled-ex", Role: common.RoleCommonUser,
		Status: common.UserStatusDisabled, Email: "dis-ex@test.local",
		TenantId: ctx.TenantID,
	}
	if err := ctx.DB.Create(disabled).Error; err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"user_id": disabled.Id, "luc_amount": 5.0, "reference_id": "r-dis",
	})
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for disabled user, got %d body=%s", w.Code, w.Body.String())
	}

	// idempotent replay: pre-seed an exchange row with the reference id
	if err := ctx.DB.Create(&entity.CurrencyExchange{
		UserId: ctx.NormalUser.Id, SourceCurrency: "LUC", SourceAmount: 3,
		TargetCurrency: "LUT", TargetAmount: 1500000, ExchangeRate: 500000,
		ReferenceId: "r-idem",
	}).Error; err != nil {
		t.Fatalf("seed idem exchange: %v", err)
	}
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"user_id": ctx.NormalUser.Id, "luc_amount": 3.0, "reference_id": "r-idem",
	})
	InternalExchangeLucToLut(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 idempotent, got %d body=%s", w.Code, w.Body.String())
	}
	if r2chanParseBody(t, w)["idempotent"] != true {
		t.Errorf("expected idempotent=true, got %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// tenant.go
// ---------------------------------------------------------------------------

func TestR2Bill_ListAndGetTenant(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	c, w := r2chanNewCtx(http.MethodGet, "/?page=1&page_size=20", nil)
	ListTenants(c)
	if w.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) < 1 {
		t.Errorf("expected >=1 tenant, got %v", data["total"])
	}

	// GetTenant not found
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "no-such-tenant"}}
	GetTenant(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// GetTenant found
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	GetTenant(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("get tenant failed: %s", w.Body.String())
	}
}

func TestR2Bill_CreateTenant(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// invalid body (missing required)
	c2, w2 := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"name": "x"})
	CreateTenant(c2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fields, got %d", w2.Code)
	}

	// valid create
	c3, w3 := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"zitadel_org_id": "org-new-1",
		"slug":           "brand-new-slug",
		"name":           "Brand New Tenant",
		"plan_type":      "pro",
	})
	c3.Set("tenant_id", ctx.TenantID)
	c3.Set("user_id", ctx.AdminUser.Id)
	CreateTenant(c3)
	if w3.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w3.Code, w3.Body.String())
	}
	if r2chanParseBody(t, w3)["success"] != true {
		t.Errorf("create tenant not success: %s", w3.Body.String())
	}
	var cnt int64
	ctx.DB.Model(&repo.Tenant{}).Where("slug = ?", "brand-new-slug").Count(&cnt)
	if cnt != 1 {
		t.Errorf("expected tenant persisted, count=%d", cnt)
	}
}

func TestR2Bill_UpdateTenant(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// invalid json body
	c, w := r2chanNewCtx(http.MethodPut, "/", []byte("{bad"))
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	UpdateTenant(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}

	// no fields to update
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{})
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	UpdateTenant(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty updates, got %d", w.Code)
	}

	// valid update
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"name": "Renamed Tenant"})
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	c.Set("user_id", ctx.AdminUser.Id)
	UpdateTenant(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update failed: %s", w.Body.String())
	}
	var name string
	ctx.DB.Model(&repo.Tenant{}).Where("id = ?", ctx.TenantID).Select("name").Scan(&name)
	if name != "Renamed Tenant" {
		t.Errorf("expected name persisted, got %q", name)
	}

	// update a non-existent tenant → GetTenantByID after update fails → 404
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"name": "ghost"})
	c.Params = gin.Params{{Key: "id", Value: "ghost-tenant-id"}}
	UpdateTenant(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing tenant after update, got %d", w.Code)
	}
}

func TestR2Bill_TenantLifecycleGuards(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// default-tenant protections
	for name, fn := range map[string]func(*gin.Context){
		"delete":  DeleteTenant,
		"disable": DisableTenant,
		"suspend": SuspendTenant,
	} {
		c, w := r2chanNewCtx(http.MethodPost, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: "default"}}
		fn(c)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s default: expected 403, got %d", name, w.Code)
		}
	}

	// DeleteTenant not found
	c, w := r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "missing-tenant"}}
	DeleteTenant(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// seed a deletable tenant + enable/disable/suspend/delete happy paths
	seed := func(id string) {
		tn := &repo.Tenant{
			Id: id, Name: id, Slug: id, IDPOrgID: "org-" + id,
			Status: repo.TenantStatusEnabled, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := ctx.DB.Create(tn).Error; err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}

	seed("t-enable")
	c, w = r2chanNewCtx(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "t-enable"}}
	EnableTenant(c)
	if w.Code != http.StatusOK {
		t.Errorf("enable: expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	seed("t-disable")
	c, w = r2chanNewCtx(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "t-disable"}}
	DisableTenant(c)
	if w.Code != http.StatusOK {
		t.Errorf("disable: expected 200, got %d", w.Code)
	}

	seed("t-suspend")
	c, w = r2chanNewCtx(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "t-suspend"}}
	SuspendTenant(c)
	if w.Code != http.StatusOK {
		t.Errorf("suspend: expected 200, got %d", w.Code)
	}

	seed("t-delete")
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "t-delete"}}
	c.Set("user_id", ctx.AdminUser.Id)
	DeleteTenant(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("delete: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestR2Bill_GetTenantStats(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// not found
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "no-stats-tenant"}}
	GetTenantStats(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// valid
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: ctx.TenantID}}
	GetTenantStats(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("stats failed: %s", w.Body.String())
	}
}

func TestR2Bill_TenantConfigs(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// GetTenantConfigs: no tenant context → 401
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	GetTenantConfigs(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without tenant ctx, got %d", w.Code)
	}

	// GetTenantConfigs: valid
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("tenant_id", ctx.TenantID)
	GetTenantConfigs(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("get configs failed: %s", w.Body.String())
	}

	// UpdateTenantConfig: no tenant ctx → 401
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"value": "v"})
	c.Params = gin.Params{{Key: "key", Value: "some_key"}}
	UpdateTenantConfig(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	// UpdateTenantConfig: invalid body (missing required value)
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{})
	c.Set("tenant_id", ctx.TenantID)
	c.Params = gin.Params{{Key: "key", Value: "some_key"}}
	UpdateTenantConfig(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing value, got %d", w.Code)
	}

	// UpdateTenantConfig: valid
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"value": "hello", "config_type": "string"})
	c.Set("tenant_id", ctx.TenantID)
	c.Params = gin.Params{{Key: "key", Value: "greeting"}}
	UpdateTenantConfig(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update config failed: %s", w.Body.String())
	}
	got, err := repo.GetTenantConfig(ctx.TenantID, "greeting")
	if err != nil || got.ConfigValue != "hello" {
		t.Errorf("expected persisted config value=hello, got %+v err=%v", got, err)
	}

	// UpdateTenantConfig: system config is protected → 403
	if err := repo.SetTenantConfig(ctx.TenantID, "sys_key", "x", "string", "", true); err != nil {
		t.Fatalf("seed system config: %v", err)
	}
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"value": "override"})
	c.Set("tenant_id", ctx.TenantID)
	c.Params = gin.Params{{Key: "key", Value: "sys_key"}}
	UpdateTenantConfig(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for system config, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// billing.go
// ---------------------------------------------------------------------------

func TestR2Bill_GetSubscriptionAndUsage(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	prev := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = false
	defer func() { common.DisplayTokenStatEnabled = prev }()

	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("id", ctx.NormalUser.Id)
	GetSubscription(c)
	if w.Code != http.StatusOK {
		t.Fatalf("subscription code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["object"] != "billing_subscription" {
		t.Errorf("expected billing_subscription, got %v", resp["object"])
	}

	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("id", ctx.NormalUser.Id)
	GetUsage(c)
	if w.Code != http.StatusOK {
		t.Fatalf("usage code=%d body=%s", w.Code, w.Body.String())
	}
	if r2chanParseBody(t, w)["object"] != "list" {
		t.Errorf("expected object=list, got %s", w.Body.String())
	}
}

func TestR2Bill_GetIdentityOverview(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// no tenant context / empty subject → 401
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("tenant_context", &middleware.TenantContext{TenantID: ctx.TenantID})
	GetIdentityOverview(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for empty subject, got %d", w.Code)
	}

	// subject present but no identity account (platform unreachable) → 404
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("tenant_context", &middleware.TenantContext{TenantID: ctx.TenantID, IDPSubject: "sub-xyz"})
	GetIdentityOverview(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing identity account, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// v2_billing.go
// ---------------------------------------------------------------------------

func TestR2Bill_V2BillingSummaryAndMethods(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// GetBillingSummary: no linked account → 503
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	GetBillingSummary(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unlinked account, got %d", w.Code)
	}

	// GetBillingPaymentMethods: platform down → empty list, still 200 success
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	GetBillingPaymentMethods(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("payment methods failed: %s", w.Body.String())
	}

	// GetBillingCheckoutStatus: empty order → 400
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "order_no", Value: ""}}
	GetBillingCheckoutStatus(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty order_no, got %d", w.Code)
	}
}

func TestR2Bill_CreateBillingCheckout(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// unlinked account → 503
	c, w := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 10.0, "payment_method": "alipay"})
	CreateBillingCheckout(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	// linked account but invalid body → 400
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 0})
	c.Set("identity_account_id", int64(555))
	CreateBillingCheckout(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

func TestR2Bill_TopUpV2(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	tctx := &middleware.TenantContext{TenantID: ctx.TenantID, UserID: ctx.NormalUser.Id}

	// no tenant context → 401
	c, w := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 5.0})
	TopUpV2(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without tenant ctx, got %d", w.Code)
	}

	// tenant ctx present but no platform account → 503
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 5.0})
	c.Set("tenant_context", tctx)
	TopUpV2(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unlinked account, got %d", w.Code)
	}

	// account linked but missing idempotency header → 400
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 5.0})
	c.Set("tenant_context", tctx)
	c.Set("identity_account_id", int64(777))
	TopUpV2(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing idempotency, got %d body=%s", w.Code, w.Body.String())
	}

	// amount out of range → 400
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 999999.0})
	c.Set("tenant_context", tctx)
	c.Set("identity_account_id", int64(777))
	c.Request.Header.Set("X-Idempotency-Key", "ik-range")
	TopUpV2(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for out-of-range amount, got %d", w.Code)
	}

	// idempotent replay: pre-seed a topup log carrying the IK tag
	repo.RecordLogWithTenant(ctx.NormalUser.Id, ctx.TenantID, repo.LogTypeTopup,
		"prior transfer [IK:ik-dup]")
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"amount_cny": 5.0})
	c.Set("tenant_context", tctx)
	c.Set("identity_account_id", int64(777))
	c.Request.Header.Set("X-Idempotency-Key", "ik-dup")
	TopUpV2(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 idempotent, got %d body=%s", w.Code, w.Body.String())
	}
	if r2chanParseBody(t, w)["idempotent"] != true {
		t.Errorf("expected idempotent=true, got %s", w.Body.String())
	}
}

func TestR2Bill_GetTopUpsV2(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// no tenant context → 401
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	GetTopUpsV2(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	// valid, with one seeded topup log
	repo.RecordLogWithTenant(ctx.NormalUser.Id, ctx.TenantID, repo.LogTypeTopup, "a topup")
	c, w = r2chanNewCtx(http.MethodGet, "/?p=1&size=20", nil)
	c.Set("tenant_context", &middleware.TenantContext{TenantID: ctx.TenantID, UserID: ctx.NormalUser.Id})
	GetTopUpsV2(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("topups failed: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// internal_api.go / internal_api_ext.go
// ---------------------------------------------------------------------------

func TestR2Bill_InternalGetUserByPhone(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "phone", Value: "13800000000"}}
	InternalGetUserByPhone(c)
	if w.Code != http.StatusGone {
		t.Errorf("expected 410 Gone, got %d", w.Code)
	}
	if r2chanParseBody(t, w)["error_code"] != "DEPRECATED" {
		t.Errorf("expected DEPRECATED, got %s", w.Body.String())
	}
}

func TestR2Bill_AdminListApiKeys(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Create(&repo.InternalApiKey{
		Name: "listed-key", KeyHash: "hash-listed", Scopes: `["*"]`, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	AdminListApiKeys(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("list api keys failed: %s", w.Body.String())
	}

	// scopes list endpoint (pure)
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	AdminGetApiKeyScopes(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Errorf("scopes failed: %s", w.Body.String())
	}
}

func TestR2Bill_InternalTokenHandlers(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "tok-r2bill")

	// --- InternalGetToken ---
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "0"}}
	InternalGetToken(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("get token invalid: expected 400, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	InternalGetToken(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("get token missing: expected 404, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(tok.Id)}}
	InternalGetToken(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("get token failed: %s", w.Body.String())
	}

	// --- InternalGetTokenUsage ---
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	InternalGetTokenUsage(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("token usage invalid: expected 400, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(tok.Id)}}
	InternalGetTokenUsage(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("token usage failed: %s", w.Body.String())
	}

	// --- InternalUpdateToken ---
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{})
	c.Params = gin.Params{{Key: "id", Value: "0"}}
	InternalUpdateToken(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update token invalid id: expected 400, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{})
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	InternalUpdateToken(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("update token missing: expected 404, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodPut, "/", []byte("{bad"))
	c.Params = gin.Params{{Key: "id", Value: intToStr(tok.Id)}}
	InternalUpdateToken(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update token bad body: expected 400, got %d", w.Code)
	}
	newName := "renamed-token"
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"name": newName})
	c.Params = gin.Params{{Key: "id", Value: intToStr(tok.Id)}}
	InternalUpdateToken(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update token failed: %s", w.Body.String())
	}
	var persisted string
	ctx.DB.Model(&repo.Token{}).Where("id = ?", tok.Id).Select("name").Scan(&persisted)
	if persisted != newName {
		t.Errorf("expected token renamed, got %q", persisted)
	}

	// --- InternalDeleteToken ---
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "-1"}}
	InternalDeleteToken(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("delete token invalid: expected 400, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "999999"}}
	InternalDeleteToken(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("delete token missing: expected 404, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(tok.Id)}}
	InternalDeleteToken(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("delete token failed: %s", w.Body.String())
	}
}

func TestR2Bill_InternalGetUserByZitadelSub(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// empty sub → 400
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "sub", Value: ""}}
	InternalGetUserByZitadelSub(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty sub, got %d", w.Code)
	}

	// unknown sub → 404
	c, w = r2chanNewCtx(http.MethodGet, "/?tenant_id=default", nil)
	c.Params = gin.Params{{Key: "sub", Value: "no-such-sub"}}
	InternalGetUserByZitadelSub(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown sub, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// provisioning.go
// ---------------------------------------------------------------------------

func TestR2Bill_CreateProvisionedKey(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	// resolve the tenant slug seeded by the harness
	tenant, err := repo.GetTenantByID(ctx.TenantID)
	if err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	adminKey := &repo.InternalApiKey{Id: 90, Name: "prov-admin", Scopes: `["*"]`, Enabled: true, CreatedBy: ctx.AdminUser.Id}

	// empty slug → 400
	c, w := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"name": "k"})
	c.Params = gin.Params{{Key: "slug", Value: ""}}
	CreateProvisionedKey(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty slug, got %d", w.Code)
	}

	// tenant not found → 404
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"name": "k"})
	c.Params = gin.Params{{Key: "slug", Value: "no-such-slug"}}
	CreateProvisionedKey(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing tenant, got %d", w.Code)
	}

	// valid tenant + missing name → 400
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{})
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", adminKey)
	CreateProvisionedKey(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d body=%s", w.Code, w.Body.String())
	}

	// narrow key not authorized for tenant → 403
	narrow := &repo.InternalApiKey{Id: 91, Name: "narrow", Scopes: `["provisioning"]`, Enabled: true}
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"name": "k"})
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", narrow)
	CreateProvisionedKey(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cross-tenant key, got %d body=%s", w.Code, w.Body.String())
	}

	// valid create → 201
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{"name": "prov-key-1", "remain_quota": 1000})
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", adminKey)
	CreateProvisionedKey(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("create provisioned key not success: %s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if data["key"] == nil || data["key"].(string) == "" {
		t.Error("expected plaintext key in response")
	}
}

func TestR2Bill_RevokeProvisionedKey(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	tenant, err := repo.GetTenantByID(ctx.TenantID)
	if err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	// Revoke enforces the same (api_key, tenant) whitelist as Create/List, so
	// the calls that get past the tenant lookup need an authorized key on the
	// context — the router's auth middleware puts it there in production.
	adminKey := &repo.InternalApiKey{Id: 94, Name: "revoke-admin", Scopes: `["*"]`, Enabled: true}

	// invalid key_id → 400
	c, w := r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}, {Key: "key_id", Value: "abc"}}
	RevokeProvisionedKey(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid key_id, got %d", w.Code)
	}

	// tenant not found → 404
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: "missing-slug"}, {Key: "key_id", Value: "1"}}
	RevokeProvisionedKey(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing tenant, got %d", w.Code)
	}

	// narrow key not authorized for this tenant → 403
	narrow := &repo.InternalApiKey{Id: 95, Name: "narrow-revoke", Scopes: `["provisioning"]`, Enabled: true}
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}, {Key: "key_id", Value: "888888"}}
	c.Set("internal_api_key", narrow)
	RevokeProvisionedKey(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for narrow key, got %d", w.Code)
	}

	// token not found in tenant → 404
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}, {Key: "key_id", Value: "888888"}}
	c.Set("internal_api_key", adminKey)
	RevokeProvisionedKey(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing token, got %d", w.Code)
	}

	// valid revoke: seed a provisioned token for this tenant
	provTok := &repo.Token{
		TenantId: tenant.Id, Name: "to-revoke", Key: common.GetRandomString(32),
		Status: common.TokenStatusEnabled, ExpiredTime: -1, Group: "default",
		CreatedTime: time.Now().Unix(), AccessedTime: time.Now().Unix(),
	}
	if err := ctx.DB.Create(provTok).Error; err != nil {
		t.Fatalf("seed provisioned token: %v", err)
	}
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}, {Key: "key_id", Value: intToStr(provTok.Id)}}
	c.Set("internal_api_key", adminKey)
	RevokeProvisionedKey(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("revoke failed: %s", w.Body.String())
	}
}

func TestR2Bill_ListProvisionedKeysGuards(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()

	tenant, err := repo.GetTenantByID(ctx.TenantID)
	if err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	adminKey := &repo.InternalApiKey{Id: 92, Name: "list-admin", Scopes: `["*"]`, Enabled: true}

	// empty slug → 400
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: ""}}
	ListProvisionedKeys(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 empty slug, got %d", w.Code)
	}

	// bad limit → 400
	c, w = r2chanNewCtx(http.MethodGet, "/?limit=0", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", adminKey)
	ListProvisionedKeys(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 bad limit, got %d body=%s", w.Code, w.Body.String())
	}

	// bad offset → 400
	c, w = r2chanNewCtx(http.MethodGet, "/?offset=-1", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", adminKey)
	ListProvisionedKeys(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 bad offset, got %d", w.Code)
	}

	// narrow key not authorized → 403
	narrow := &repo.InternalApiKey{Id: 93, Name: "narrow-list", Scopes: `["provisioning"]`, Enabled: true}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", narrow)
	ListProvisionedKeys(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for narrow key, got %d", w.Code)
	}

	// valid list → 200
	c, w = r2chanNewCtx(http.MethodGet, "/?limit=20&offset=0", nil)
	c.Params = gin.Params{{Key: "slug", Value: tenant.Slug}}
	c.Set("internal_api_key", adminKey)
	ListProvisionedKeys(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("list provisioned keys failed: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// release.go — requires the package-level releaseService bound to the test DB
// ---------------------------------------------------------------------------

func r2billInitReleaseService(t *testing.T) func() {
	t.Helper()
	prev := releaseService
	InitReleaseService() // binds to the currently-swapped repo.DB (sqlite)
	return func() { releaseService = prev }
}

func r2billSeedRelease(t *testing.T, ctx *V2TestContext, productID string) *entity.Release {
	t.Helper()
	now := time.Now()
	rel := &entity.Release{
		ProductId: productID, Version: "1.2.3", Title: "R", ChangelogMd: "# changes",
		ReleaseType: "stable", IsPublished: true, IsPrerelease: false,
		CreatedAt: now, UpdatedAt: now, PublishedAt: &now,
	}
	if err := ctx.DB.Create(rel).Error; err != nil {
		t.Fatalf("seed release: %v", err)
	}
	return rel
}

func TestR2Bill_ReleaseHandlers(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()
	restore := r2billInitReleaseService(t)
	defer restore()

	rel := r2billSeedRelease(t, ctx, "lurus-cli")

	// ListReleases → 200 success
	c, w := r2chanNewCtx(http.MethodGet, "/?product_id=lurus-cli&page_size=10", nil)
	ListReleases(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("list releases failed: %s", w.Body.String())
	}

	// GetLatestRelease: empty product id → 400
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "product_id", Value: ""}}
	GetLatestRelease(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty product_id, got %d", w.Code)
	}

	// GetLatestRelease: product with no release → 404
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "product_id", Value: "no-such-product"}}
	GetLatestRelease(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no release, got %d", w.Code)
	}

	// GetLatestRelease: existing product → 200
	c, w = r2chanNewCtx(http.MethodGet, "/?current_version=1.0.0", nil)
	c.Params = gin.Params{{Key: "product_id", Value: "lurus-cli"}}
	GetLatestRelease(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("latest release failed: %s", w.Body.String())
	}

	// GetReleaseByID: invalid → 400, missing → 404, valid → 200
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetReleaseByID(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid release id, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "888888"}}
	GetReleaseByID(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 missing release, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(rel.Id, 10)}}
	GetReleaseByID(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("get release failed: %s", w.Body.String())
	}

	// GetChangelog: invalid → 400, missing → 404, valid → 200
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetChangelog(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid changelog id, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "888888"}}
	GetChangelog(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 missing changelog, got %d", w.Code)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(rel.Id, 10)}}
	GetChangelog(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("changelog failed: %s", w.Body.String())
	}
}

func TestR2Bill_DownloadArtifactGuards(t *testing.T) {
	ctx := r2billSetup(t)
	defer ctx.Cleanup()
	restore := r2billInitReleaseService(t)
	defer restore()

	rel := r2billSeedRelease(t, ctx, "lurus-gui")

	// invalid release id → 400
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}, {Key: "artifact_id", Value: "1"}}
	DownloadArtifact(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid release id, got %d", w.Code)
	}

	// invalid artifact id → 400
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "artifact_id", Value: "abc"}}
	DownloadArtifact(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid artifact id, got %d", w.Code)
	}

	// artifact not found → 404
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(rel.Id, 10)}, {Key: "artifact_id", Value: "777777"}}
	DownloadArtifact(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 missing artifact, got %d", w.Code)
	}

	// artifact belongs to a different release → 400
	art := &entity.ReleaseArtifact{
		ReleaseId: rel.Id + 1000, Platform: "linux", Arch: "amd64",
		Filename: "f.bin", FileSize: 10, StoragePath: "p/f.bin", ChecksumSha256: "abc",
	}
	if err := ctx.DB.Create(art).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(rel.Id, 10)}, {Key: "artifact_id", Value: strconv.FormatInt(art.Id, 10)}}
	DownloadArtifact(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for mismatched artifact, got %d body=%s", w.Code, w.Body.String())
	}
}

// ensure app import is used even if release helpers are trimmed later.
var _ = app.GenerateTokenKey
