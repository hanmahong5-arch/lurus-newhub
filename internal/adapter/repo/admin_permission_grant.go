package repo

// admin_permission_grant.go — delegated admin permission grants (L4,
// auth-security-17/18, console-ux-36). A grant lets a non-root admin
// (role >= RoleAdminUser, < RoleRootUser) reach a narrow root-gated route
// group — this cycle only middleware.RootOrGranted("audit","read") —
// without holding root. Grants are GLOBAL this cycle: every write here
// enforces tenant_id == nil; the column exists only so a future cycle's
// tenant-scoped grants need no second migration.

import (
	"errors"
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

type AdminPermissionGrant = entity.AdminPermissionGrant

var (
	// ErrGrantExists mirrors the partial-unique-index shape: an active
	// (revoked_at IS NULL) row already names this (user_id, resource, action).
	ErrGrantExists = errors.New("permission grant already exists")
	// ErrGrantNotFound covers "no row with this id" and "row exists but is
	// already revoked" — DELETE on either answers 404, same not-found shape
	// IDOR-safe handlers elsewhere in this package use.
	ErrGrantNotFound = errors.New("permission grant not found")
)

// CreatePermissionGrant grants userID (resource, action), attributed to
// grantedBy. Global only: the row's TenantId is always nil. Runs inside a
// transaction so the existence check and the insert observe the same
// snapshot — the SQL migration's partial unique index is the authoritative
// race guard on Postgres (isUniqueViolation below is the belt to that
// suspenders); the pre-check alone is what makes the guarantee visible on
// the hermetic SQLite tier, which never runs the partial-index migration.
func CreatePermissionGrant(userID int, resource, action string, grantedBy int) (*AdminPermissionGrant, error) {
	if userID <= 0 || resource == "" || action == "" {
		return nil, errors.New("user id, resource and action are required")
	}
	var grant AdminPermissionGrant
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing AdminPermissionGrant
		res := tx.Where("user_id = ? AND tenant_id IS NULL AND resource = ? AND action = ? AND revoked_at IS NULL",
			userID, resource, action).Limit(1).Find(&existing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return ErrGrantExists
		}
		grant = AdminPermissionGrant{
			UserId:    userID,
			TenantId:  nil,
			Resource:  resource,
			Action:    action,
			GrantedBy: grantedBy,
			CreatedAt: common.GetTimestamp(),
		}
		if createErr := tx.Create(&grant).Error; createErr != nil {
			if isUniqueViolation(createErr) {
				return ErrGrantExists
			}
			return fmt.Errorf("create permission grant: %w", createErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

// RevokePermissionGrant sets revoked_at on an active grant. 404-shaped
// ErrGrantNotFound for an absent id OR an already-revoked one — a second
// revoke of the same id is not an error a caller can act on differently
// from "there is nothing active to revoke".
func RevokePermissionGrant(id int) error {
	if id <= 0 {
		return ErrGrantNotFound
	}
	now := common.GetTimestamp()
	result := DB.Model(&AdminPermissionGrant{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke permission grant: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrGrantNotFound
	}
	return nil
}

// RevokePermissionGrantsForUser revokes every ACTIVE grant userID holds and
// records one authz.permission_revoked audit row per grant revoked
// (actor=system — this fires from user deletion, privacy erasure, and role
// demotion, not from an admin explicitly withdrawing a single grant via
// RevokeGrantV2, which stays actor=admin). Grants must not outlive their
// grantee: without this, deleting or demoting a user left their grant row
// active, and re-promoting (or re-creating) the same user id silently
// restored the access they used to hold (cycle-8 L4 repair round, B-F5).
// Called from DeleteUserById (user.go), the privacy-erasure cascade
// (lifecycle/privacy_erasure.go), and both admin user-update handlers
// (v2_admin_users.go UpdateAdminUserV2 and handler/user.go UpdateUser)
// whenever a role update drops the user below common.RoleAdminUser.
// Idempotent: a user with no active grants returns (0, nil), not an error —
// callers do not need to check HasActivePermissionGrant first.
func RevokePermissionGrantsForUser(userID int) (int64, error) {
	if userID <= 0 {
		return 0, nil
	}
	var grants []AdminPermissionGrant
	if err := DB.Where("user_id = ? AND revoked_at IS NULL", userID).Find(&grants).Error; err != nil {
		return 0, fmt.Errorf("load active grants for user %d: %w", userID, err)
	}
	if len(grants) == 0 {
		return 0, nil
	}
	now := common.GetTimestamp()
	result := DB.Model(&AdminPermissionGrant{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now)
	if result.Error != nil {
		return 0, fmt.Errorf("revoke permission grants for user %d: %w", userID, result.Error)
	}
	for _, g := range grants {
		detail := fmt.Sprintf(`{"grant_id":%d,"grantee_user_id":%d,"resource":%q,"action":%q,"tenant_id":null,"reason":"grantee_removed_or_demoted"}`,
			g.Id, userID, g.Resource, g.Action)
		governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
			"", governance.ActorSystem, userID,
			governance.ActionPermissionRevoked, governance.ResourceAuthz, g.Id, detail))
	}
	return result.RowsAffected, nil
}

// ListPermissionGrants returns every grant (active and revoked), newest
// first — the console's authz page and its audit trail both want the full
// history, not just the currently-active set.
func ListPermissionGrants() ([]AdminPermissionGrant, error) {
	var grants []AdminPermissionGrant
	if err := DB.Order("id DESC").Find(&grants).Error; err != nil {
		return nil, fmt.Errorf("list permission grants: %w", err)
	}
	return grants, nil
}

// HasActivePermissionGrant reports whether userID holds an ACTIVE
// (revoked_at IS NULL), GLOBAL (tenant_id IS NULL) grant for
// (resource, action) — the fail-closed check middleware.RootOrGranted runs
// for every non-root SESSION-path caller (Bearer-JWT callers are rejected
// before any lookup, see root_or_granted.go). A lookup error is NOT treated as "granted":
// the caller (RootOrGranted) must fail closed on both "no row" and
// "lookup failed", never open on a DB hiccup.
func HasActivePermissionGrant(userID int, resource, action string) (bool, error) {
	if userID <= 0 || resource == "" || action == "" {
		return false, nil
	}
	var row AdminPermissionGrant
	res := DB.Where("user_id = ? AND tenant_id IS NULL AND resource = ? AND action = ? AND revoked_at IS NULL",
		userID, resource, action).Limit(1).Find(&row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
