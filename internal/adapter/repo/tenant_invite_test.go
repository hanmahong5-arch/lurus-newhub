package repo

// tenant_invite_test.go — hermetic coverage for the tenant-invite repo
// layer (N2): CreateTenantInvite / ConsumeTenantInvite / RevokeTenantInvite.
// Every test goes red if the corresponding fail-safe behavior is reverted
// (see each test's comment for what regressed).

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var inviteTestDBCounter atomic.Int64

// setupInviteTestDB swaps the package-level repo.DB with an isolated
// in-memory sqlite instance (Tenant + TenantInvite tables only — these
// tests never touch users). Returns a cleanup that restores the previous DB.
func setupInviteTestDB(t *testing.T) (cleanup func()) {
	t.Helper()
	n := inviteTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:invite%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Tenant{}, &entity.TenantInvite{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := DB
	DB = db
	return func() { DB = prev }
}

func seedInviteTenant(t *testing.T, db *gorm.DB, id, slug string) *Tenant {
	t.Helper()
	tenant := &Tenant{
		Id: id, Name: id, Slug: slug, Status: TenantStatusEnabled,
		IDPOrgID: "org_" + id, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
	return tenant
}

// TestConsumeTenantInvite_HappyPath: pending -> consumed, returns the bound
// tenant, and stamps ConsumedByAccountId/ConsumedAt on the row.
func TestConsumeTenantInvite_HappyPath(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	invite, err := CreateTenantInvite("t-acme", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	tenant, err := ConsumeTenantInvite(invite.Code, 555)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if tenant == nil || tenant.Id != "t-acme" {
		t.Fatalf("consume returned tenant = %v, want t-acme", tenant)
	}

	var persisted TenantInvite
	if err := DB.Where("id = ?", invite.Id).First(&persisted).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if persisted.Status != TenantInviteStatusConsumed {
		t.Errorf("status = %d, want consumed(%d)", persisted.Status, TenantInviteStatusConsumed)
	}
	if persisted.ConsumedByAccountId == nil || *persisted.ConsumedByAccountId != 555 {
		t.Errorf("consumed_by_account_id = %v, want 555", persisted.ConsumedByAccountId)
	}
	if persisted.ConsumedAt == nil {
		t.Error("consumed_at must be set")
	}
}

// TestConsumeTenantInvite_Replay_SecondUseFails pins the single-use
// invariant: a second consume of the SAME code must fail with
// ErrInviteAlreadyConsumed, not silently succeed a second time.
func TestConsumeTenantInvite_Replay_SecondUseFails(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	invite, err := CreateTenantInvite("t-acme", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ConsumeTenantInvite(invite.Code, 1); err != nil {
		t.Fatalf("first consume: %v", err)
	}

	tenant, err := ConsumeTenantInvite(invite.Code, 2)
	if !errors.Is(err, ErrInviteAlreadyConsumed) {
		t.Fatalf("second consume err = %v, want ErrInviteAlreadyConsumed", err)
	}
	if tenant != nil {
		t.Errorf("second consume returned tenant = %v, want nil", tenant)
	}

	// The winner's ConsumedByAccountId must stay the FIRST consumer's — a
	// replay must never re-stamp the row.
	var persisted TenantInvite
	if err := DB.Where("id = ?", invite.Id).First(&persisted).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if persisted.ConsumedByAccountId == nil || *persisted.ConsumedByAccountId != 1 {
		t.Errorf("consumed_by_account_id = %v, want 1 (first consumer, unchanged by replay)", persisted.ConsumedByAccountId)
	}
}

// TestConsumeTenantInvite_Expired: a code past its ExpiredTime must be
// rejected even though it is still nominally Pending.
func TestConsumeTenantInvite_Expired(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	invite, err := CreateTenantInvite("t-acme", 1, time.Hour)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Force it into the past.
	if err := DB.Model(&TenantInvite{}).Where("id = ?", invite.Id).
		Update("expired_time", 1).Error; err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	if _, err := ConsumeTenantInvite(invite.Code, 1); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("consume err = %v, want ErrInviteExpired", err)
	}
}

// TestConsumeTenantInvite_Revoked: a revoked code must never be consumable.
func TestConsumeTenantInvite_Revoked(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	invite, err := CreateTenantInvite("t-acme", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := RevokeTenantInvite(invite.Id, "t-acme"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := ConsumeTenantInvite(invite.Code, 1); !errors.Is(err, ErrInviteRevoked) {
		t.Fatalf("consume err = %v, want ErrInviteRevoked", err)
	}
}

// TestConsumeTenantInvite_UnknownCode: a code that was never issued (or is
// garbage) must fail closed with ErrInviteNotFound, never a 500-class error.
func TestConsumeTenantInvite_UnknownCode(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	if _, err := ConsumeTenantInvite("this-code-was-never-issued", 1); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("consume err = %v, want ErrInviteNotFound", err)
	}
	if _, err := ConsumeTenantInvite("", 1); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("consume(\"\") err = %v, want ErrInviteNotFound", err)
	}
}

// TestRevokeTenantInvite_WrongTenant_NotFound: revoking a real, pending
// invite ID under a DIFFERENT tenant id must not succeed — cross-tenant
// admin actions must fail the same way a nonexistent id would.
func TestRevokeTenantInvite_WrongTenant_NotFound(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")
	seedInviteTenant(t, DB, "t-other", "other")

	invite, err := CreateTenantInvite("t-acme", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := RevokeTenantInvite(invite.Id, "t-other"); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("cross-tenant revoke err = %v, want ErrInviteNotFound", err)
	}

	// Still consumable under the real tenant — the cross-tenant attempt must
	// not have mutated the row.
	if _, err := ConsumeTenantInvite(invite.Code, 1); err != nil {
		t.Fatalf("consume after failed cross-tenant revoke: %v", err)
	}
}

// TestCreateTenantInvite_NoExpiry: ttl<=0 leaves ExpiredTime at 0, and a
// zero-ExpiredTime code never expires regardless of how much time passes.
func TestCreateTenantInvite_NoExpiry(t *testing.T) {
	cleanup := setupInviteTestDB(t)
	defer cleanup()
	seedInviteTenant(t, DB, "t-acme", "acme")

	invite, err := CreateTenantInvite("t-acme", 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if invite.ExpiredTime != 0 {
		t.Errorf("ExpiredTime = %d, want 0 (no expiry)", invite.ExpiredTime)
	}
	if invite.Code == "" || len(invite.Code) != 32 {
		t.Errorf("Code = %q, want a 32-char code", invite.Code)
	}
}
