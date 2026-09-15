package repo

// admin_permission_grant_test.go — L4 (auth-security-17/18, console-ux-36).
// Drives the real CreatePermissionGrant/RevokePermissionGrant/
// HasActivePermissionGrant/ListPermissionGrants functions against a real
// SQLite-backed DB (setupSQLiteDB), not hand-built rows — the "active
// unique + revoke" invariant is the guarantee middleware.RootOrGranted's
// fail-closed check depends on.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestAdminPermissionGrantRepo_ActiveUniqueAndRevoke(t *testing.T) {
	defer setupSQLiteDB(t)()

	// A fresh (user, resource, action) has no active grant.
	granted, err := HasActivePermissionGrant(7, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant before create: %v", err)
	}
	if granted {
		t.Fatalf("HasActivePermissionGrant = true before any grant was created")
	}

	grant, err := CreatePermissionGrant(7, "audit", "read", 1)
	if err != nil {
		t.Fatalf("CreatePermissionGrant: %v", err)
	}
	if grant.Id == 0 {
		t.Fatalf("CreatePermissionGrant returned a zero id")
	}
	if grant.TenantId != nil {
		t.Fatalf("TenantId = %v, want nil (grants are global this cycle)", grant.TenantId)
	}

	granted, err = HasActivePermissionGrant(7, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after create: %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant = false right after CreatePermissionGrant")
	}

	// A second grant for the SAME (user, resource, action) while the first
	// is still active must be rejected — this is the "one active grant per
	// user+resource+action" invariant the partial unique index enforces on
	// Postgres and CreatePermissionGrant's own pre-check enforces here.
	if _, err := CreatePermissionGrant(7, "audit", "read", 1); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("CreatePermissionGrant duplicate: err = %v, want ErrGrantExists", err)
	}

	// A grant for a DIFFERENT action is independent and must succeed.
	if _, err := CreatePermissionGrant(7, "audit", "write", 1); err != nil {
		t.Fatalf("CreatePermissionGrant different action: %v", err)
	}

	// Revoke the read grant.
	if err := RevokePermissionGrant(grant.Id); err != nil {
		t.Fatalf("RevokePermissionGrant: %v", err)
	}
	granted, err = HasActivePermissionGrant(7, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after revoke: %v", err)
	}
	if granted {
		t.Fatalf("HasActivePermissionGrant = true after RevokePermissionGrant")
	}

	// Revoking an already-revoked (or absent) id 404s.
	if err := RevokePermissionGrant(grant.Id); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("RevokePermissionGrant on an already-revoked id: err = %v, want ErrGrantNotFound", err)
	}
	if err := RevokePermissionGrant(999999); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("RevokePermissionGrant on a nonexistent id: err = %v, want ErrGrantNotFound", err)
	}

	// After revoking (user,audit,read), a FRESH grant for the same triple
	// must be creatable again — revocation frees the active-uniqueness slot.
	if _, err := CreatePermissionGrant(7, "audit", "read", 1); err != nil {
		t.Fatalf("CreatePermissionGrant after revoke: %v", err)
	}

	grants, err := ListPermissionGrants()
	if err != nil {
		t.Fatalf("ListPermissionGrants: %v", err)
	}
	// The revoked "read" row, the still-active "write" row, and the
	// fresh "read" row created after revoke: 3 total.
	if len(grants) != 3 {
		t.Fatalf("ListPermissionGrants returned %d rows, want 3", len(grants))
	}
}

// TestAdminPermissionGrantRepo_RevokeForUser is the B-F5 oracle (cycle-8 L4
// repair round): a grant issued to a user must not outlive that user's
// deletion or demotion — otherwise re-promotion silently restores audit
// access. RevokePermissionGrantsForUser is the bulk-revoke primitive
// DeleteUserById, the privacy-erasure cascade, and UpdateAdminUserV2's
// role-demotion branch all call.
func TestAdminPermissionGrantRepo_RevokeForUser(t *testing.T) {
	defer setupSQLiteDB(t)()

	if _, err := CreatePermissionGrant(11, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	granted, err := HasActivePermissionGrant(11, "audit", "read")
	if err != nil || !granted {
		t.Fatalf("HasActivePermissionGrant before revoke = (%v,%v), want (true,nil)", granted, err)
	}

	n, err := RevokePermissionGrantsForUser(11)
	if err != nil {
		t.Fatalf("RevokePermissionGrantsForUser: %v", err)
	}
	if n != 1 {
		t.Fatalf("RevokePermissionGrantsForUser returned %d, want 1", n)
	}

	granted, err = HasActivePermissionGrant(11, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after revoke: %v", err)
	}
	if granted {
		t.Fatalf("HasActivePermissionGrant = true after RevokePermissionGrantsForUser — grant must not survive user removal/demotion")
	}

	// A second call (e.g. a demote-then-delete sequence) is a no-op, not an
	// error: there is nothing active left to revoke.
	n, err = RevokePermissionGrantsForUser(11)
	if err != nil {
		t.Fatalf("RevokePermissionGrantsForUser (idempotent call): %v", err)
	}
	if n != 0 {
		t.Fatalf("RevokePermissionGrantsForUser idempotent call returned %d, want 0", n)
	}

	// Re-promoting the same user must NOT silently resurrect the revoked
	// grant — a fresh CreatePermissionGrant call is required.
	granted, err = HasActivePermissionGrant(11, "audit", "read")
	if err != nil || granted {
		t.Fatalf("HasActivePermissionGrant after idempotent revoke = (%v,%v), want (false,nil)", granted, err)
	}

	// Revoking for a user with no grants at all is also a no-op.
	n, err = RevokePermissionGrantsForUser(9999)
	if err != nil {
		t.Fatalf("RevokePermissionGrantsForUser for a user with no grants: %v", err)
	}
	if n != 0 {
		t.Fatalf("RevokePermissionGrantsForUser for a user with no grants returned %d, want 0", n)
	}
}

// TestDeleteUserById_RevokesPermissionGrants is B-F5's oracle for the
// DeleteUserById call site (cycle-8 L4 repair round): deleting a user must
// not leave their delegated permission grant active — otherwise a
// re-created user with the same id would silently inherit it.
func TestDeleteUserById_RevokesPermissionGrants(t *testing.T) {
	defer setupSQLiteDB(t)()

	u := &User{
		Username: "grant-holder-" + common.GetUUID(), DisplayName: "grant holder",
		Email: common.GetUUID() + "@test.local", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, TenantId: "default", Group: "default",
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := CreatePermissionGrant(u.Id, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	if err := DeleteUserById(u.Id); err != nil {
		t.Fatalf("DeleteUserById: %v", err)
	}

	granted, err := HasActivePermissionGrant(u.Id, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after DeleteUserById: %v", err)
	}
	if granted {
		t.Fatalf("permission grant still active for user id=%d after DeleteUserById", u.Id)
	}
}

func TestAdminPermissionGrantRepo_ValidatesInput(t *testing.T) {
	defer setupSQLiteDB(t)()

	if _, err := CreatePermissionGrant(0, "audit", "read", 1); err == nil {
		t.Fatalf("CreatePermissionGrant with user_id=0 should fail")
	}
	if _, err := CreatePermissionGrant(7, "", "read", 1); err == nil {
		t.Fatalf("CreatePermissionGrant with empty resource should fail")
	}
	if _, err := CreatePermissionGrant(7, "audit", "", 1); err == nil {
		t.Fatalf("CreatePermissionGrant with empty action should fail")
	}
	if err := RevokePermissionGrant(0); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("RevokePermissionGrant(0): err = %v, want ErrGrantNotFound", err)
	}
	granted, err := HasActivePermissionGrant(0, "audit", "read")
	if err != nil || granted {
		t.Fatalf("HasActivePermissionGrant(0,...) = (%v,%v), want (false,nil)", granted, err)
	}
}

// TestAdminPermissionGrantRepo_RevokeForUserWritesAuditRows locks the audit
// half of RevokePermissionGrantsForUser. Withdrawing someone's delegated
// access is exactly the event a compliance reader looks for, and the grant
// that unlocked the audit feed is itself in that feed — so the revocation
// must leave a row per grant, not just flip a column. The write is
// asynchronous (governance.RecordAuditEvent), hence the bounded poll.
type grantAuditWriter struct{}

func (grantAuditWriter) CreateAuditEvent(event *entity.AuditEvent) error {
	return DB.Create(event).Error
}

func TestAdminPermissionGrantRepo_RevokeForUserWritesAuditRows(t *testing.T) {
	defer setupSQLiteDB(t)()
	// Production installs the writer at boot (cmd/server); the repo package is
	// below that wiring, so the test pins one at the DB this case created.
	governance.SetAuditWriter(grantAuditWriter{})
	t.Cleanup(func() { governance.SetAuditWriter(nil) })

	if _, err := CreatePermissionGrant(21, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	if _, err := CreatePermissionGrant(21, "audit", "write", 1); err != nil {
		t.Fatalf("seed second grant: %v", err)
	}
	// Only the revocations below are counted, not the two creations.
	var before int64
	if err := DB.Model(&entity.AuditEvent{}).
		Where("action = ?", governance.ActionPermissionRevoked).Count(&before).Error; err != nil {
		t.Fatalf("count audit rows before: %v", err)
	}

	n, err := RevokePermissionGrantsForUser(21)
	if err != nil || n != 2 {
		t.Fatalf("RevokePermissionGrantsForUser = (%d,%v), want (2,nil)", n, err)
	}

	var rows []entity.AuditEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := DB.Where("action = ?", governance.ActionPermissionRevoked).Find(&rows).Error; err != nil {
			t.Fatalf("read audit rows: %v", err)
		}
		if int64(len(rows)) >= before+2 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if int64(len(rows)) != before+2 {
		t.Fatalf("authz.permission_revoked rows = %d, want %d — one per grant revoked", len(rows), before+2)
	}
	for _, row := range rows {
		if !strings.Contains(row.Details, `"grantee_user_id":21`) {
			t.Errorf("audit row details %q does not name the grantee", row.Details)
		}
		if !strings.Contains(row.Details, `"reason":"grantee_removed_or_demoted"`) {
			t.Errorf("audit row details %q does not carry the revocation reason", row.Details)
		}
	}
}

// TestGrant_ExpiredIsNotActive is the L1 oracle (cycle-9 plan §3):
// expires_at participates in the active-grant predicate alongside
// revoked_at. A row with expires_at in the past, still un-revoked, must not
// authorise anything — deleting the "(expires_at IS NULL OR expires_at > ?)"
// clause from HasActivePermissionGrant's WHERE turns this red.
func TestGrant_ExpiredIsNotActive(t *testing.T) {
	defer setupSQLiteDB(t)()

	past := common.GetTimestamp() - 3600
	row := entity.AdminPermissionGrant{
		UserId: 71, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp() - 7200, ExpiresAt: &past,
	}
	if err := DB.Create(&row).Error; err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}

	granted, err := HasActivePermissionGrant(71, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant: %v", err)
	}
	if granted {
		t.Fatalf("HasActivePermissionGrant = true for a grant whose expires_at is in the past and revoked_at is NULL")
	}

	// A grant with expires_at in the FUTURE is still active — expiry is not
	// a blanket rejection of every non-NULL value.
	future := common.GetTimestamp() + 3600
	row2 := entity.AdminPermissionGrant{
		UserId: 72, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp(), ExpiresAt: &future,
	}
	if err := DB.Create(&row2).Error; err != nil {
		t.Fatalf("seed future-expiring grant: %v", err)
	}
	granted, err = HasActivePermissionGrant(72, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant (future expiry): %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant = false for a grant whose expires_at is still in the future")
	}

	// A grant with expires_at NULL (no ttl_seconds given) is permanent,
	// exactly as it behaved before this column existed.
	if _, err := CreatePermissionGrant(73, "audit", "read", 1); err != nil {
		t.Fatalf("CreatePermissionGrant (no ttl): %v", err)
	}
	granted, err = HasActivePermissionGrant(73, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant (no ttl): %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant = false for a grant created without ttl_seconds (expires_at NULL)")
	}
}

// TestGrant_ReGrantAfterExpirySucceedsAndRevokesTheOldRow is the L1 oracle
// for the trap the plan calls out: 034's partial unique index is
// WHERE revoked_at IS NULL, so an expired-but-unrevoked row still occupies
// the active slot. CreatePermissionGrant must stamp revoked_at on that row
// and insert the new one in the same call, not 409. Deleting that
// in-transaction revoke turns this red (ErrGrantExists instead of success).
func TestGrant_ReGrantAfterExpirySucceedsAndRevokesTheOldRow(t *testing.T) {
	defer setupSQLiteDB(t)()

	past := common.GetTimestamp() - 60
	old := entity.AdminPermissionGrant{
		UserId: 81, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp() - 120, ExpiresAt: &past,
	}
	if err := DB.Create(&old).Error; err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}

	fresh, err := CreatePermissionGrant(81, "audit", "read", 2)
	if err != nil {
		t.Fatalf("CreatePermissionGrant after expiry: err = %v, want success (not ErrGrantExists)", err)
	}
	if fresh.Id == old.Id {
		t.Fatalf("CreatePermissionGrant returned the old row's id, want a NEW row")
	}

	var reread entity.AdminPermissionGrant
	if err := DB.First(&reread, old.Id).Error; err != nil {
		t.Fatalf("re-read old row: %v", err)
	}
	if reread.RevokedAt == nil {
		t.Fatalf("old expired row's revoked_at is still NULL after re-grant — the trap: it would 409-lock the slot forever")
	}

	granted, err := HasActivePermissionGrant(81, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant: %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant = false right after the re-grant succeeded")
	}

	var all []entity.AdminPermissionGrant
	if err := DB.Where("user_id = ? AND resource = ? AND action = ?", 81, "audit", "read").Find(&all).Error; err != nil {
		t.Fatalf("list rows: %v", err)
	}
	activeCount := 0
	for _, g := range all {
		if g.RevokedAt == nil {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active (revoked_at IS NULL) rows for (81,audit,read) = %d, want exactly 1", activeCount)
	}
}

// TestGrant_ReGrantWhileLiveStill409 is the pre-existing behaviour the plan
// says must not change: a still-LIVE grant (no expiry, or expiry in the
// future) keeps rejecting a duplicate create with ErrGrantExists.
func TestGrant_ReGrantWhileLiveStill409(t *testing.T) {
	defer setupSQLiteDB(t)()

	if _, err := CreatePermissionGrant(91, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant (no ttl): %v", err)
	}
	if _, err := CreatePermissionGrant(91, "audit", "read", 1); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("re-grant of a live (no-expiry) grant: err = %v, want ErrGrantExists", err)
	}

	future := common.GetTimestamp() + 3600
	live := entity.AdminPermissionGrant{
		UserId: 92, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp(), ExpiresAt: &future,
	}
	if err := DB.Create(&live).Error; err != nil {
		t.Fatalf("seed future-expiring grant: %v", err)
	}
	if _, err := CreatePermissionGrant(92, "audit", "read", 1); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("re-grant while expiry is still in the future: err = %v, want ErrGrantExists", err)
	}
}
