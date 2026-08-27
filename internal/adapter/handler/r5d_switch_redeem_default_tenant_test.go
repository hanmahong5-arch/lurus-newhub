package handler

// r5d_switch_redeem_default_tenant_test.go — G5a: the anonymous
// POST /api/v2/switch/redeem endpoint must not treat a "default"-tenant
// redemption code as a globally-valid activation code. See
// switch_redeem.go's switchRedeemAllowDefaultTenant gate for the fix.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestSwitchRedeemAnonymous_DefaultTenantCodeRejected asserts the rejection
// envelope and that no relay token was issued.
//
// Correction 2026-08-27 — this comment used to claim the account-count
// assertion below also catches a LATE rejection (a gate moved to after
// findOrCreateSwitchEndUser). It does not: the query at :62 uses
// switchEndUserUsernamePrefix + the full 30-char fingerprint, while
// findOrCreateSwitchEndUser truncates the fingerprint to 14 chars, so that
// Count() is structurally 0 whatever the handler does. Measured under exactly
// that mutation: this test PASSED while an account was provisioned. The
// username assertion is left as-is (harmless, and its sibling below on the
// token count does have teeth); the late-rejection case is covered by
// r6c_switch_redeem_tenant_test.go, which matches the truncation rule and
// additionally scans `username LIKE 'sw-eu-%'`.
func TestSwitchRedeemAnonymous_DefaultTenantCodeRejected(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	key := common.GetRandomString(32)
	redemption := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    "default",
		Key:         key,
		Name:        "Default Tenant Code",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(redemption); err != nil {
		t.Fatalf("seed default-tenant redemption: %v", err)
	}

	fingerprint := "fingerprint-default-tenant-001"
	w := postSwitchRedeem(t, r, map[string]string{
		"code":        key,
		"fingerprint": fingerprint,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (envelope), got %d, body: %s", w.Code, w.Body.String())
	}
	env := parseEnvelope(t, w)
	if success, _ := env["success"].(bool); success {
		t.Fatalf("expected success=false for a default-tenant code, got envelope: %s", w.Body.String())
	}
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "不属于当前租户") {
		t.Errorf("expected the tenant-mismatch sentinel message, got: %q", msg)
	}

	// The defect's real cost: no anonymous account, no token.
	var userCount int64
	if err := repo.WithoutTenantIsolation(ctx.DB).Model(&repo.User{}).
		Where("username = ?", switchEndUserUsernamePrefix+fingerprint).
		Count(&userCount).Error; err != nil {
		t.Fatalf("count switch users: %v", err)
	}
	if userCount != 0 {
		t.Errorf("expected NO sw-eu- user to be provisioned for a rejected default-tenant code, got %d", userCount)
	}

	var tokenCount int64
	if err := repo.WithoutTenantIsolation(ctx.DB).Model(&repo.Token{}).
		Where("name = ?", "switch-enduser").
		Count(&tokenCount).Error; err != nil {
		t.Fatalf("count switch tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("expected NO relay token to be issued for a rejected default-tenant code, got %d", tokenCount)
	}

	// The redemption code itself must stay usable (not burned by a rejected
	// attempt) — repo.Redeem's own tenant check burns nothing on a mismatch,
	// but this endpoint's earlier gate must not either.
	var reloaded repo.Redemption
	if err := repo.WithoutTenantIsolation(ctx.DB).Where(`"key" = ?`, key).First(&reloaded).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if reloaded.Status != common.RedemptionCodeStatusEnabled {
		t.Errorf("expected the code to remain Enabled after a rejected attempt, got status=%d", reloaded.Status)
	}
}

// TestSwitchRedeemAnonymous_RealTenantCodeStillWorks is the regression
// guard: a code minted for an actual reseller tenant (the supported flow)
// must still succeed and still surface a non-empty tenant_slug — the G5a
// gate must be scoped to "default" specifically, not to every code.
// switch_redeem_test.go's TestSwitchRedeemAnonymous_Success already covers
// this shape; this is a narrower duplicate kept in this file so the
// default-tenant rejection above and the real-tenant success below are
// visible side-by-side.
func TestSwitchRedeemAnonymous_RealTenantCodeStillWorks(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	key := common.GetRandomString(32)
	seedSwitchRedemption(t, ctx, key, common.RedemptionCodeStatusEnabled, 0, 100000)

	w := postSwitchRedeem(t, r, map[string]string{
		"code":        key,
		"fingerprint": "fingerprint-real-tenant-001",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	env := parseEnvelope(t, w)
	if success, _ := env["success"].(bool); !success {
		t.Fatalf("expected success=true for a real-tenant code, got envelope: %s", w.Body.String())
	}
	data, ok := env["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data field, body: %s", w.Body.String())
	}
	slug, _ := data["tenant_slug"].(string)
	if slug == "" {
		t.Errorf("expected non-empty tenant_slug for a real-tenant code")
	}
}

// TestSwitchRedeemAnonymous_EscapeHatchRestoresOldBehavior flips
// switchRedeemAllowDefaultTenant on directly (package-level var — same
// pattern other tests in this package use for globals like common.RedisEnabled)
// and asserts a default-tenant code succeeds again, matching the byte-for-byte
// pre-fix behavior the escape hatch promises.
func TestSwitchRedeemAnonymous_EscapeHatchRestoresOldBehavior(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	prev := switchRedeemAllowDefaultTenant
	switchRedeemAllowDefaultTenant = true
	defer func() { switchRedeemAllowDefaultTenant = prev }()

	key := common.GetRandomString(32)
	redemption := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    "default",
		Key:         key,
		Name:        "Default Tenant Code Escape Hatch",
		Quota:       50000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(redemption); err != nil {
		t.Fatalf("seed default-tenant redemption: %v", err)
	}

	w := postSwitchRedeem(t, r, map[string]string{
		"code":        key,
		"fingerprint": "fingerprint-escape-hatch-001",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	env := parseEnvelope(t, w)
	if success, _ := env["success"].(bool); !success {
		t.Fatalf("expected success=true with the escape hatch on, got envelope: %s", w.Body.String())
	}
	data, ok := env["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data field, body: %s", w.Body.String())
	}
	quota, _ := data["quota"].(float64)
	if int(quota) != redemption.Quota {
		t.Errorf("expected quota=%d, got %v", redemption.Quota, data["quota"])
	}
}
