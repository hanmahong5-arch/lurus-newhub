package repo

import (
	"errors"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// tenant_payer.go — tenants.payer_user_id (migration 048), the tenant-role
// writer used by the root and tenant-admin role endpoints, and the invite
// redemption for users that already exist.

// ErrLastTenantAdmin: the change would leave the tenant without an admin.
var ErrLastTenantAdmin = errors.New("a tenant must keep at least one admin")

// TenantPayerID returns the payer user id of the tenant (0 = not set).
func TenantPayerID(tenantID string) (int, error) {
	var t Tenant
	if err := WithoutTenantIsolation(DB).Select("id", "payer_user_id").
		Where("id = ?", tenantID).Take(&t).Error; err != nil {
		return 0, err
	}
	return int(t.PayerUserId), nil
}

// ResolveTenantPayer returns the payer user of tenantID, or (nil, nil) when no
// payer is set or the recorded payer is no longer a live member of the tenant.
func ResolveTenantPayer(tenantID string) (*User, error) {
	pid, err := TenantPayerID(tenantID)
	if err != nil || pid <= 0 {
		return nil, err
	}
	u, err := tenantMemberUser(WithoutTenantIsolation(DB), tenantID, pid)
	if errors.Is(err, ErrMemberNotInTenant) {
		return nil, nil
	}
	return u, err
}

// SetTenantMemberRole writes users.tenant_role for a member of tenantID and,
// when payer != nil, sets (true) or clears (false, only if this user is the
// payer) tenants.payer_user_id — one transaction. keepAdmin refuses a change
// that would leave the tenant without any admin. The "default" tenant refuses
// every write (ErrTenantRoleInDefault).
func SetTenantMemberRole(tenantID string, userID int, role string, payer *bool, keepAdmin bool) error {
	if tenantID == "" || tenantID == "default" {
		return ErrTenantRoleInDefault
	}
	if !ValidTenantRole(role) {
		return ErrInvalidTenantRole
	}
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		u, err := tenantMemberUser(tx, tenantID, userID)
		if err != nil {
			return err
		}
		if keepAdmin && u.TenantRole == entity.TenantRoleAdmin && role != entity.TenantRoleAdmin {
			var others int64
			if err := tx.Model(&User{}).
				Where("tenant_id = ? AND tenant_role = ? AND id <> ?", tenantID, entity.TenantRoleAdmin, userID).
				Count(&others).Error; err != nil {
				return err
			}
			if others == 0 {
				return ErrLastTenantAdmin
			}
		}
		if err := setTenantRoleTx(tx, tenantID, userID, role); err != nil {
			return err
		}
		if payer == nil {
			return nil
		}
		if *payer {
			return tx.Model(&Tenant{}).Where("id = ?", tenantID).Update("payer_user_id", int64(userID)).Error
		}
		return tx.Model(&Tenant{}).Where("id = ? AND payer_user_id = ?", tenantID, int64(userID)).
			Update("payer_user_id", int64(0)).Error
	})
	if err != nil {
		return err
	}
	invalidateTenantRoleCache(userID)
	return nil
}

// IsTenantPayer reports whether userID is the recorded payer of tenantID.
func IsTenantPayer(tenantID string, userID int) bool {
	if tenantID == "" || userID <= 0 {
		return false
	}
	pid, err := TenantPayerID(tenantID)
	return err == nil && pid == userID
}

// RedeemInviteForExistingUser redeems a one-time invite for a user that already
// exists. The invite must belong to the user's own tenant (ErrInviteWrongTenant
// otherwise, and the code is left unspent). The grant is applied in the same
// transaction as the consume; an admin is never demoted by a lower-role code.
func RedeemInviteForExistingUser(code string, userID int) (InviteGrant, error) {
	var grant InviteGrant
	if code == "" {
		return grant, ErrInviteNotFound
	}
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", userID).Take(&u).Error; err != nil {
			return ErrMemberNotInTenant
		}
		var invite TenantInvite
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ?", code).First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInviteNotFound
			}
			return err
		}
		// Tenant check before any state check: a foreign code reports the same
		// not-found shape whatever its state, so codes cannot be probed.
		if invite.TenantId != u.TenantId {
			return ErrInviteWrongTenant
		}
		if err := inviteUsable(&invite); err != nil {
			return err
		}
		if invite.TenantId == "default" && (invite.MemberRole != "" || invite.ProjectId > 0) {
			return ErrTenantRoleInDefault
		}
		if invite.MemberRole != "" || invite.ProjectId > 0 {
			role := invite.MemberRole
			if role == "" {
				role = entity.TenantRoleDeptLead
			}
			if u.TenantRole == entity.TenantRoleAdmin {
				role = entity.TenantRoleAdmin // a lower-role code never demotes an admin
			}
			if invite.ProjectId > 0 {
				ok, err := projectLiveInTenant(tx, invite.TenantId, invite.ProjectId)
				if err != nil {
					return err
				}
				if !ok {
					return ErrProjectNotFound
				}
			}
			if err := setTenantRoleTx(tx, invite.TenantId, u.Id, role); err != nil {
				return err
			}
			if invite.ProjectId > 0 {
				u.TenantRole = role
				if err := addMemberTx(tx, invite.TenantId, invite.ProjectId, &u); err != nil {
					return err
				}
			}
		}
		grant = InviteGrant{MemberRole: invite.MemberRole, ProjectID: invite.ProjectId}
		return tx.Model(&TenantInvite{}).Where("id = ?", invite.Id).Updates(map[string]any{
			"status":      TenantInviteStatusConsumed,
			"consumed_at": time.Now(),
		}).Error
	})
	if err != nil {
		return InviteGrant{}, err
	}
	invalidateTenantRoleCache(userID)
	return grant, nil
}

// inviteUsable maps an invite row's state onto the redemption sentinels.
func inviteUsable(invite *TenantInvite) error {
	switch invite.Status {
	case TenantInviteStatusConsumed:
		return ErrInviteAlreadyConsumed
	case TenantInviteStatusRevoked:
		return ErrInviteRevoked
	case TenantInviteStatusPending:
	default:
		return ErrInviteNotFound
	}
	if invite.RevokedAt != 0 {
		return ErrInviteRevoked
	}
	if invite.ExpiredTime != 0 && invite.ExpiredTime < common.GetTimestamp() {
		return ErrInviteExpired
	}
	return nil
}
