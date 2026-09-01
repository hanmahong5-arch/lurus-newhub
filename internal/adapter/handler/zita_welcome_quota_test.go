package handler

// Pins the welcome-quota parity fix for bridged auto-created users.
// Live-probed 2026-08-31: a fresh zita-bootstrap user landed with quota 0
// (Insert stamps common.QuotaForNewUser, an option defaulting to 0) and
// 402'd on its very first relay call, while the OIDC auto-create path grants
// the per-tenant quota.new_user_quota policy (default 10000). Both paths are
// "a new user just arrived" — they must grant the same welcome quota.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestAutoCreateBridgedUser_GrantsWelcomeQuota(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const accountID = int64(929292)
	got, err := autoCreateBridgedUser(accountID, "default")
	if err != nil {
		t.Fatalf("autoCreateBridgedUser: %v", err)
	}
	if got == nil {
		t.Fatal("autoCreateBridgedUser returned nil user")
	}

	// No tenant_configs row seeded → the documented default (10000) applies,
	// matching CreateUserFromIDPClaims (user_mapping.go).
	if got.Quota != 10000 {
		t.Errorf("welcome quota = %d, want 10000 (parity with the OIDC auto-create grant)", got.Quota)
	}

	// The grant must be persisted, not just on the returned struct.
	var row repo.User
	if err := ctx.DB.Where("lurus_account_id = ?", accountID).First(&row).Error; err != nil {
		t.Fatalf("load created row: %v", err)
	}
	if row.Quota != 10000 {
		t.Errorf("persisted quota = %d, want 10000", row.Quota)
	}
}
