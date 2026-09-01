package repo

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TenantInvite = entity.TenantInvite

// Re-export status constants from entity.
const (
	TenantInviteStatusPending  = entity.TenantInviteStatusPending
	TenantInviteStatusConsumed = entity.TenantInviteStatusConsumed
	TenantInviteStatusRevoked  = entity.TenantInviteStatusRevoked
)

var (
	// ErrInviteNotFound covers "no row with this code/id" and, for Revoke,
	// "row exists but doesn't belong to this tenant" (same not-found shape
	// IDOR-safe handlers elsewhere in this repo use — see project.go).
	ErrInviteNotFound        = errors.New("tenant invite not found")
	ErrInviteExpired         = errors.New("tenant invite has expired")
	ErrInviteAlreadyConsumed = errors.New("tenant invite already consumed")
	ErrInviteRevoked         = errors.New("tenant invite has been revoked")
)

// CreateTenantInvite mints a root-issued, one-time onboarding code bound to
// tenantID. ttl <= 0 means no expiry (ExpiredTime stays 0, mirroring
// Redemption.ExpiredTime's own "0 = never" convention).
func CreateTenantInvite(tenantID string, createdByUserID int, ttl time.Duration) (*TenantInvite, error) {
	if tenantID == "" {
		return nil, errors.New("tenant id required")
	}
	var expiredTime int64
	if ttl > 0 {
		expiredTime = common.GetTimestamp() + int64(ttl/time.Second)
	}
	invite := &TenantInvite{
		TenantId:        tenantID,
		Code:            common.GetUUID(), // 32 hex chars, same shape as Redemption.Key
		Status:          TenantInviteStatusPending,
		ExpiredTime:     expiredTime,
		CreatedByUserId: createdByUserID,
		CreatedAt:       time.Now(),
	}
	if err := DB.Create(invite).Error; err != nil {
		return nil, fmt.Errorf("create tenant invite: %w", err)
	}
	return invite, nil
}

// ConsumeTenantInvite atomically redeems a one-time invite code and returns
// the bound tenant. Mirrors Redeem's SELECT ... FOR UPDATE pattern
// (redemption.go) so two concurrent consumers of the SAME code never both
// win — the loser gets a typed sentinel error, never a partial credit.
//
// Callers on the login critical path (handler.ZitaBootstrap) MUST treat
// every error here as "fall back to the default tenant, still log the user
// in" — an invite failure must never surface as a 500 or block a login.
func ConsumeTenantInvite(code string, accountID int64) (*Tenant, error) {
	if code == "" {
		return nil, ErrInviteNotFound
	}
	var tenant Tenant
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		var invite TenantInvite
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ?", code).First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInviteNotFound
			}
			return err
		}
		switch invite.Status {
		case TenantInviteStatusConsumed:
			return ErrInviteAlreadyConsumed
		case TenantInviteStatusRevoked:
			return ErrInviteRevoked
		case TenantInviteStatusPending:
			// proceed
		default:
			return ErrInviteNotFound
		}
		if invite.ExpiredTime != 0 && invite.ExpiredTime < common.GetTimestamp() {
			return ErrInviteExpired
		}
		if err := tx.Where("id = ?", invite.TenantId).First(&tenant).Error; err != nil {
			return fmt.Errorf("resolve invited tenant: %w", err)
		}
		if err := tx.Model(&TenantInvite{}).Where("id = ?", invite.Id).Updates(map[string]any{
			"status":                 TenantInviteStatusConsumed,
			"consumed_at":            time.Now(),
			"consumed_by_account_id": accountID,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}

// RevokeTenantInvite kills a pending code early (root decided not to send
// it, or sent it to the wrong recipient). Scoped by tenantID so a code
// belonging to a DIFFERENT tenant never revokes under this tenant's admin
// call. Already consumed/revoked codes, and ids that don't match this
// tenant, all report ErrInviteNotFound — the same not-found shape IDOR-safe
// handlers elsewhere use, so a caller can't probe which case it hit.
func RevokeTenantInvite(id int, tenantID string) error {
	result := DB.Model(&TenantInvite{}).
		Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, TenantInviteStatusPending).
		Update("status", TenantInviteStatusRevoked)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInviteNotFound
	}
	return nil
}
