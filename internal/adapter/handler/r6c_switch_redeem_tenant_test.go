package handler

// r6c_switch_redeem_tenant_test.go — G5a follow-up (Round 6, lane R6-C).
//
// The Round 5 ledger flagged r5d_switch_redeem_default_tenant_test.go's
// TestSwitchRedeemAnonymous_DefaultTenantCodeRejected as a hollow wiring
// lock: it queries
//
//	username = switchEndUserUsernamePrefix + fingerprint
//
// (the FULL fingerprint), but findOrCreateSwitchEndUser (switch_redeem.go)
// stores switchEndUserUsernamePrefix + fingerprint[:14] whenever the
// fingerprint is longer than 14 chars. That test's own fingerprint,
// "fingerprint-default-tenant-001", is 30 chars, so the Count() query is
// structurally always 0 — it would still read 0 even if the tenant gate
// were moved to AFTER findOrCreateSwitchEndUser and a real "sw-eu-…"
// account got provisioned for a rejected code. This file's job is to close
// that gap with an assertion that matches the handler's actual truncation
// rule, plus a broader prefix-scan that would catch the defect even if the
// truncation length changes again.
//
// Scope, stated honestly (an earlier draft of this comment overclaimed and
// was corrected 2026-08-27): the tests below go through setupSwitchRedeemRouter
// in switch_redeem_test.go, which builds its OWN gin.Engine and registers
// SwitchRedeemAnonymous at the production path itself. That is a hand-copy of
// api-v2-router.go's registration, so these tests observe handler BEHAVIOR
// under a real HTTP round-trip — they do NOT observe whether production still
// registers the route. Measured: commenting out
// `switchGroup.POST("/redeem", handler.SwitchRedeemAnonymous)` in
// api-v2-router.go leaves both tests in this file green.
//
// The registration half is locked separately, in the router package, by
// internal/adapter/handler/router/r6d_switch_redeem_mount_test.go
// (TestSetApiV2Router_SwitchRedeem_MountedAndRejectsDefaultTenantCode), which
// calls SetApiV2Router and was verified to go red under exactly that mutation.
// The two files together cover dispatch + behavior; neither covers it alone.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// r6cSwitchUsername reproduces findOrCreateSwitchEndUser's truncation rule
// (switch_redeem.go: prefix + first-14-chars-of-fingerprint) so the test
// asserts against the username the handler would ACTUALLY write, not a
// guess. Mirroring the production rule here — rather than importing a
// shared helper — is deliberate: if the two ever drift, the mutation
// exercised below (moving the tenant gate after provisioning) still has to
// leave zero rows under the LIKE 'sw-eu-%' scan, which does not depend on
// the truncation length at all.
func r6cSwitchUsername(fingerprint string) string {
	fp := fingerprint
	if len(fp) > 14 {
		fp = fp[:14]
	}
	return switchEndUserUsernamePrefix + fp
}

// TestR6CSwitchRedeemAnonymous_DefaultTenantCodeRejected_NoAnonymousUser is
// the corrected wiring lock for G5a's anonymous-endpoint half: a "default"
// tenant code must be refused by POST /api/v2/switch/redeem, and — this is
// the part the prior lock could not actually see — no "sw-eu-…" account
// and no relay token may be sedimented for the rejected attempt.
func TestR6CSwitchRedeemAnonymous_DefaultTenantCodeRejected_NoAnonymousUser(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	key := common.GetRandomString(32)
	redemption := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    "default",
		Key:         key,
		Name:        "R6C Default Tenant Code",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(redemption); err != nil {
		t.Fatalf("seed default-tenant redemption: %v", err)
	}

	// Fingerprint is 25 chars — longer than the 14-char truncation window,
	// same shape as a real device fingerprint, so the exact-username
	// assertion below actually exercises the truncation rule.
	fingerprint := "r6c-default-tenant-fp-001"
	if len(fingerprint) <= 14 {
		t.Fatalf("test fixture bug: fingerprint must be >14 chars to exercise truncation, got %d", len(fingerprint))
	}

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

	// Exact match against the username the handler would ACTUALLY write
	// (prefix + truncated fingerprint) — this is what r5d's version got
	// wrong by querying the untruncated fingerprint.
	exactUsername := r6cSwitchUsername(fingerprint)
	var exactCount int64
	if err := repo.WithoutTenantIsolation(ctx.DB).Model(&repo.User{}).
		Where("username = ?", exactUsername).
		Count(&exactCount).Error; err != nil {
		t.Fatalf("count switch users (exact): %v", err)
	}
	if exactCount != 0 {
		t.Errorf("expected NO sw-eu- user at username %q for a rejected default-tenant code, got %d", exactUsername, exactCount)
	}

	// Broader prefix scan: catches the defect even if the truncation rule
	// changes, and is the check that would actually fail if the gate moved
	// to after findOrCreateSwitchEndUser.
	var prefixCount int64
	if err := repo.WithoutTenantIsolation(ctx.DB).Model(&repo.User{}).
		Where("username LIKE ?", switchEndUserUsernamePrefix+"%").
		Count(&prefixCount).Error; err != nil {
		t.Fatalf("count switch users (prefix scan): %v", err)
	}
	if prefixCount != 0 {
		t.Errorf("expected NO sw-eu-* user of any shape for a rejected default-tenant code, got %d", prefixCount)
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

	var reloaded repo.Redemption
	if err := repo.WithoutTenantIsolation(ctx.DB).Where(`"key" = ?`, key).First(&reloaded).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if reloaded.Status != common.RedemptionCodeStatusEnabled {
		t.Errorf("expected the code to remain Enabled after a rejected attempt, got status=%d", reloaded.Status)
	}
}

// TestR6CSwitchRedeemAnonymous_LegitTenantCodeStillProvisionsAccount is the
// regression guard operator decision D4 asked for: the tenant gate must be
// scoped to "default" specifically. A code minted for a real reseller
// tenant is Switch's primary acquisition flow, and this must keep working
// end-to-end — including the part the test above proves is now blocked for
// "default": an anonymous "sw-eu-…" account and a bounded relay token
// actually get provisioned.
func TestR6CSwitchRedeemAnonymous_LegitTenantCodeStillProvisionsAccount(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	key := common.GetRandomString(32)
	seedSwitchRedemption(t, ctx, key, common.RedemptionCodeStatusEnabled, 0, 100000)

	fingerprint := "r6c-legit-tenant-fp-002"
	w := postSwitchRedeem(t, r, map[string]string{
		"code":        key,
		"fingerprint": fingerprint,
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

	exactUsername := r6cSwitchUsername(fingerprint)
	var user repo.User
	if err := repo.WithoutTenantIsolation(ctx.DB).Where("username = ? AND tenant_id = ?", exactUsername, ctx.TenantID).
		First(&user).Error; err != nil {
		t.Fatalf("expected a provisioned sw-eu- user at username %q in tenant %q, got: %v", exactUsername, ctx.TenantID, err)
	}

	var tokenCount int64
	if err := repo.WithoutTenantIsolation(ctx.DB).Model(&repo.Token{}).
		Where("name = ? AND user_id = ?", "switch-enduser", user.Id).
		Count(&tokenCount).Error; err != nil {
		t.Fatalf("count switch tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Errorf("expected exactly 1 relay token issued for the provisioned user, got %d", tokenCount)
	}
}
