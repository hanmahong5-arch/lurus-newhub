package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// switch_reconciliation_auth_test.go — cycle-14 L9 defect (4).
//
// The handler's own header comment said its auth "mirrors UserHeartbeat".
// It did not: it looked the token up (with no status filter on the query) and
// went straight to aggregating. It never read Token.Status and never resolved
// the owning user, so a token an operator had disabled — and a token whose
// owner had been banned — both kept answering with that account's
// billing-relevant totals. The heartbeat refuses both
// (user_heartbeat.go), and so does the shared raw-token helper the two
// sibling Switch endpoints use (switch_user_info.go).
//
// The third case has no sibling to copy: a token row with UserId == 0. The
// aggregation is scoped by `user_id = ?`, so such a token used to sum EVERY
// row in the install that also carries user_id 0 — rows belonging to other
// credentials. It must be refused, not "scoped to the token".

// TestSwitchReconciliation_RefusesDisabledToken: an operator-disabled token
// must stop answering, exactly as it stops answering the heartbeat.
func TestSwitchReconciliation_RefusesDisabledToken(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)
	rc.seedConsumeLog(t, "fixture-model-a", 500, 300, 200, 1500)

	if err := rc.db.Model(&repo.Token{}).Where("id = ?", tok.Id).
		Update("status", common.TokenStatusDisabled).Error; err != nil {
		t.Fatalf("disable token: %v", err)
	}

	w := rc.post(tok.Key, map[string]int64{"start_time": 0, "end_time": 0})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled token: HTTP %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if resp := decodeReconcile(t, w); resp.Success {
		t.Errorf("disabled token got a success envelope: %s", w.Body.String())
	}
}

// TestSwitchReconciliation_RefusesBannedUser: the token row may still be
// enabled, but a banned owner revokes every one of their credentials.
func TestSwitchReconciliation_RefusesBannedUser(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)
	rc.seedConsumeLog(t, "fixture-model-a", 500, 300, 200, 1500)

	if err := rc.db.Model(&repo.User{}).Where("id = ?", rc.user.Id).
		Update("status", common.UserStatusDisabled).Error; err != nil {
		t.Fatalf("ban user: %v", err)
	}

	w := rc.post(tok.Key, map[string]int64{"start_time": 0, "end_time": 0})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("banned user: HTTP %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if resp := decodeReconcile(t, w); resp.Success {
		t.Errorf("banned user got a success envelope: %s", w.Body.String())
	}
}

// TestSwitchReconciliation_RefusesOwnerlessToken: a token with no owning user
// id must be refused rather than aggregated. Seeded alongside a foreign
// user_id-0 consume row, so a handler that "scopes the aggregation to the
// token" by keeping user_id = 0 would hand that row back and fail here.
func TestSwitchReconciliation_RefusesOwnerlessToken(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)

	if err := rc.db.Model(&repo.Token{}).Where("id = ?", tok.Id).
		Update("user_id", 0).Error; err != nil {
		t.Fatalf("orphan token: %v", err)
	}
	// A consume row owned by nobody — what the unscoped aggregation would sum.
	if err := rc.db.Create(&repo.Log{
		UserId: 0, TenantId: rc.tenant.Id, Type: repo.LogTypeConsume,
		ModelName: "fixture-model-a", Quota: 4242, PromptTokens: 4242, CreatedAt: 1500,
	}).Error; err != nil {
		t.Fatalf("seed ownerless consume log: %v", err)
	}

	w := rc.post(tok.Key, map[string]int64{"start_time": 0, "end_time": 0})
	if w.Code == http.StatusOK {
		t.Fatalf("a token with no owning user id was served totals: body=%s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("ownerless token: HTTP %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

// TestSwitchReconciliation_SuspendedTenant403KeepsItsWire is the wire-contract
// lock on the tenant refusal. Routing this handler through the shared raw-token
// helper (defect 4) also routes it through that helper's REFUSAL text, which is
// a CJK sentence — so adopting the helper silently rewrote a response body that
// a shipped Switch client reads. The three things this endpoint has always
// answered with must survive: HTTP 403, error_code TENANT_DISABLED, and an
// ASCII message, per the repo's "API error.message is English-only" contract
// (internal/adapter/middleware/wire_message_language_gate_test.go). It is the
// same sentence UserHeartbeat still answers for the identical condition
// (user_heartbeat.go), which is the surface this handler's header says it
// mirrors.
//
// BLIND SPOT: this asserts the bytes of ONE refusal on ONE route. It says
// nothing about /switch/user/info and /switch/user/topup, which answer the CJK
// sentence for this same condition and are not in the language gate's
// whitelist — see the hand-off note.
func TestSwitchReconciliation_SuspendedTenant403KeepsItsWire(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)
	rc.seedConsumeLog(t, "fixture-model-a", 500, 300, 200, 1500)

	if err := rc.db.Model(&repo.Tenant{}).Where("id = ?", rc.tenant.Id).
		Update("status", repo.TenantStatusSuspended).Error; err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}

	w := rc.post(tok.Key, map[string]int64{"start_time": 0, "end_time": 0})
	if w.Code != http.StatusForbidden {
		t.Fatalf("suspended tenant: HTTP %d, want 403; body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%q)", err, w.Body.String())
	}
	if body.Success {
		t.Errorf("suspended tenant got a success envelope: %s", w.Body.String())
	}
	if body.ErrorCode != switchTenantDisabledCode {
		t.Errorf("error_code = %q, want %q", body.ErrorCode, switchTenantDisabledCode)
	}
	for i, r := range body.Message {
		if r >= 0x80 {
			t.Fatalf("message carries a non-ASCII rune %q at byte %d: %q — "+
				"the wire contract for error.message is English-only", r, i, body.Message)
		}
	}
	// The literal, deliberately NOT the production constant: a test that
	// compares a response against the same constant that produced it cannot
	// see the constant being rewritten, which is exactly the regression here.
	const want = "owning tenant is disabled or suspended"
	if body.Message != want {
		t.Errorf("message = %q, want %q (the sentence UserHeartbeat answers for the same condition)",
			body.Message, want)
	}
}

// TestSwitchReconciliation_EnabledTokenStillWorks is the do-not-regress half —
// the tightened auth must not refuse the credential it is meant to serve.
func TestSwitchReconciliation_EnabledTokenStillWorks(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)
	rc.seedConsumeLog(t, "fixture-model-a", 77, 40, 37, 1500)

	w := rc.post(tok.Key, map[string]int64{"start_time": 0, "end_time": 0})
	if w.Code != http.StatusOK {
		t.Fatalf("healthy token: HTTP %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if resp := decodeReconcile(t, w); resp.Data.TotalQuota != 77 {
		t.Errorf("total_quota = %d, want 77", resp.Data.TotalQuota)
	}
}
