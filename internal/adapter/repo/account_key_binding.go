package repo

import (
	"errors"

	"gorm.io/gorm"
)

// AccountKeyBinding maps one platform account + one product to the single live
// newhub key issued for it (migration 049). The partial unique index on
// (identity_account_id, product) WHERE deleted_at IS NULL is the concurrency
// backstop: two simultaneous creates cannot both commit a live binding.
//
// Dual-creation: the embedded SQL migration 049 and the AutoMigrate list in
// main.go both create the table; the gorm tags mirror the SQL (same index
// names) so either order is a no-op for the other.
type AccountKeyBinding struct {
	Id                int64          `json:"id" gorm:"primaryKey"`
	IdentityAccountID int64          `json:"identity_account_id" gorm:"column:identity_account_id;not null;uniqueIndex:ux_account_key_bindings_live,priority:1,where:deleted_at IS NULL"`
	Product           string         `json:"product" gorm:"type:varchar(32);not null;uniqueIndex:ux_account_key_bindings_live,priority:2,where:deleted_at IS NULL"`
	TokenId           int64          `json:"token_id" gorm:"not null;index:idx_account_key_bindings_token"`
	TenantId          string         `json:"tenant_id" gorm:"type:varchar(36);not null;default:'default'"`
	IdempotencyKey    string         `json:"idempotency_key" gorm:"type:varchar(128);not null;default:''"`
	CreatedAt         int64          `json:"created_at" gorm:"not null;default:0"`
	DeletedAt         gorm.DeletedAt `json:"-"`
}

func (AccountKeyBinding) TableName() string { return "account_key_bindings" }

// GetLiveAccountKeyBinding returns the live binding for (account, product) or
// (nil, nil) when none exists.
func GetLiveAccountKeyBinding(accountID int64, product string) (*AccountKeyBinding, error) {
	var b AccountKeyBinding
	err := DB.Where("identity_account_id = ? AND product = ?", accountID, product).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListLiveAccountKeyBindings returns every live binding of an account.
func ListLiveAccountKeyBindings(accountID int64) ([]AccountKeyBinding, error) {
	var out []AccountKeyBinding
	err := DB.Where("identity_account_id = ?", accountID).Order("product asc").Find(&out).Error
	return out, err
}

// GetTokenUnscopedTenant loads a token by id without tenant scoping (internal
// provisioning keys span tenants; the caller already authorised the tenant).
func GetTokenUnscopedTenant(id int64) (*Token, error) {
	var t Token
	if err := WithoutTenantIsolation(DB).First(&t, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}
