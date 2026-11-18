package repo

import (
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// ProvisionTenantUserParams describes the atomic user + identity-mapping + optional token creation request.
type ProvisionTenantUserParams struct {
	SanitizedUsername  string
	TenantID           string
	IDPSubject         string
	Email              string
	DisplayName        string
	Group              string
	InitialQuota       int
	LinkedAccountID    int64
	CreateInitialToken bool
	InitialTokenName   string
	InitialTokenKey    string
}

// ProvisionTenantUserResult holds the committed entities from ProvisionTenantUserTx.
type ProvisionTenantUserResult struct {
	User    *User
	Mapping *UserIdentityMapping
	Token   *Token
	Stage   string
}

// ProvisionTenantUserTx atomically creates a tenant-scoped User, UserIdentityMapping, and optional initial Token.
func ProvisionTenantUserTx(p ProvisionTenantUserParams) (*ProvisionTenantUserResult, error) {
	tx := GetTenantDBWithID(p.TenantID).Begin()
	if tx.Error != nil {
		return &ProvisionTenantUserResult{Stage: "begin"}, tx.Error
	}

	finalUsername := allocateUniqueTenantUsername(tx, p.SanitizedUsername, p.TenantID)
	user := &User{
		Username:    finalUsername,
		TenantId:    p.TenantID,
		Email:       p.Email,
		DisplayName: p.DisplayName,
		Group:       p.Group,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       p.InitialQuota,
	}
	if p.LinkedAccountID > 0 {
		acctID := p.LinkedAccountID
		user.LurusAccountID = &acctID
	}

	if err := tx.Create(user).Error; err != nil {
		tx.Rollback()
		return &ProvisionTenantUserResult{Stage: "user"}, err
	}

	now := time.Now()
	mapping := &UserIdentityMapping{
		LurusUserID:       user.Id,
		IDPSubject:        p.IDPSubject,
		TenantID:          p.TenantID,
		Email:             p.Email,
		DisplayName:       p.DisplayName,
		PreferredUsername: finalUsername,
		LastSyncAt:        &now,
		IsActive:          true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := tx.Create(mapping).Error; err != nil {
		tx.Rollback()
		return &ProvisionTenantUserResult{Stage: "mapping"}, err
	}

	var token *Token
	if p.CreateInitialToken {
		token = &Token{
			UserId:         user.Id,
			TenantId:       p.TenantID,
			Name:           p.InitialTokenName,
			Key:            p.InitialTokenKey,
			CreatedTime:    now.Unix(),
			AccessedTime:   now.Unix(),
			Status:         common.TokenStatusEnabled,
			ExpiredTime:    -1,
			RemainQuota:    p.InitialQuota,
			UnlimitedQuota: p.InitialQuota == 0,
			Group:          p.Group,
		}
		if p.LinkedAccountID > 0 {
			token.IdentityAccountID = p.LinkedAccountID
			token.UnlimitedQuota = true
		}
		if err := tx.Create(token).Error; err != nil {
			tx.Rollback()
			return &ProvisionTenantUserResult{Stage: "token"}, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return &ProvisionTenantUserResult{Stage: "commit"}, err
	}

	return &ProvisionTenantUserResult{
		User:    user,
		Mapping: mapping,
		Token:   token,
	}, nil
}

func allocateUniqueTenantUsername(tx *gorm.DB, sanitized, tenantID string) string {
	finalUsername := sanitized
	suffix := 1
	for {
		var count int64
		tx.Model(&User{}).Where("username = ? AND tenant_id = ?", finalUsername, tenantID).Count(&count)
		if count == 0 {
			return finalUsername
		}
		suffix++
		base := sanitized
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if len(candidate) > 20 {
			base = base[:20-len(fmt.Sprintf("_%d", suffix))]
			candidate = fmt.Sprintf("%s_%d", base, suffix)
		}
		finalUsername = candidate
	}
}
