package repo

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
)

// Type aliases pointing to entity package
type UserIdentityMapping = entity.UserIdentityMapping
type OIDCUserClaims = entity.OIDCUserClaims

// GetUserMappingByIDPSubject retrieves user mapping by upstream OIDC subject and Tenant ID.
// NOTE(idp-migration): the SQL/physical column stays zitadel_user_id until the
// column-rename migration is reserved & applied (owner-gated).
func GetUserMappingByIDPSubject(idpSubject string, tenantID string) (*UserIdentityMapping, error) {
	var mapping UserIdentityMapping
	err := DB.Where("zitadel_user_id = ? AND tenant_id = ? AND is_active = ?", idpSubject, tenantID, true).
		First(&mapping).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user mapping not found")
		}
		return nil, err
	}
	return &mapping, nil
}

// GetUserMappingByLurusUserID retrieves user mapping by lurus user ID and tenant ID
func GetUserMappingByLurusUserID(lurusUserID int, tenantID string) (*UserIdentityMapping, error) {
	var mapping UserIdentityMapping
	err := DB.Where("lurus_user_id = ? AND tenant_id = ? AND is_active = ?", lurusUserID, tenantID, true).
		First(&mapping).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user mapping not found")
		}
		return nil, err
	}
	return &mapping, nil
}

// CreateUserMapping creates a new user identity mapping
func CreateUserMapping(lurusUserID int, idpSubject string, tenantID string, email string, displayName string, preferredUsername string) (*UserIdentityMapping, error) {
	// Check if mapping already exists
	existingMapping, _ := GetUserMappingByIDPSubject(idpSubject, tenantID)
	if existingMapping != nil {
		// Update last sync time
		now := time.Now()
		existingMapping.LastSyncAt = &now
		existingMapping.Email = email
		existingMapping.DisplayName = displayName
		existingMapping.PreferredUsername = preferredUsername
		existingMapping.UpdatedAt = now
		err := DB.Save(existingMapping).Error
		return existingMapping, err
	}

	now := time.Now()
	mapping := &UserIdentityMapping{
		LurusUserID:       lurusUserID,
		IDPSubject:        idpSubject,
		TenantID:          tenantID,
		Email:             email,
		DisplayName:       displayName,
		PreferredUsername: preferredUsername,
		LastSyncAt:        &now,
		IsActive:          true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	err := DB.Create(mapping).Error
	if err != nil {
		return nil, err
	}

	return mapping, nil
}

// UpdateUserMapping updates user mapping metadata
func UpdateUserMapping(id int, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return DB.Model(&UserIdentityMapping{}).Where("id = ?", id).Updates(updates).Error
}

// DeactivateUserMapping deactivates a user mapping (soft delete)
func DeactivateUserMapping(id int) error {
	return UpdateUserMapping(id, map[string]interface{}{
		"is_active": false,
	})
}

// DeleteUserMapping hard deletes a user mapping
func DeleteUserMapping(id int) error {
	return DB.Delete(&UserIdentityMapping{}, "id = ?", id).Error
}

// ListUserMappingsByTenant retrieves all user mappings for a tenant
func ListUserMappingsByTenant(tenantID string, offset int, limit int) ([]*UserIdentityMapping, int64, error) {
	var mappings []*UserIdentityMapping
	var total int64

	query := DB.Model(&UserIdentityMapping{}).Where("tenant_id = ? AND is_active = ?", tenantID, true)

	// Get total count
	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err = query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&mappings).Error
	if err != nil {
		return nil, 0, err
	}

	return mappings, total, nil
}

// ListUserMappingsByIDPSubject retrieves all mappings for an OIDC subject across tenants.
func ListUserMappingsByIDPSubject(idpSubject string) ([]*UserIdentityMapping, error) {
	var mappings []*UserIdentityMapping
	err := DB.Where("zitadel_user_id = ? AND is_active = ?", idpSubject, true).
		Order("created_at DESC").
		Find(&mappings).Error
	return mappings, err
}

// SyncUserDataFromIDP syncs user data from OIDC claims to mapping.
func SyncUserDataFromIDP(mappingID int, email string, displayName string, preferredUsername string) error {
	now := time.Now()
	return UpdateUserMapping(mappingID, map[string]interface{}{
		"email":              email,
		"display_name":       displayName,
		"preferred_username": preferredUsername,
		"last_sync_at":       &now,
	})
}

// GetUserByIDPSubject retrieves lurus user by OIDC subject and tenant.
// This is a helper function that combines mapping lookup and user retrieval.
func GetUserByIDPSubject(idpSubject string, tenantID string) (*User, *UserIdentityMapping, error) {
	// Get mapping
	mapping, err := GetUserMappingByIDPSubject(idpSubject, tenantID)
	if err != nil {
		return nil, nil, err
	}

	// Get user
	user, err := GetUserById(mapping.LurusUserID, false)
	if err != nil {
		return nil, nil, err
	}

	return user, mapping, nil
}

// CreateUserFromIDPClaims creates a new lurus user from upstream OIDC JWT claims
// and establishes the identity mapping.
//
// Lookup order:
//  1. Exact mapping match (idp subject column + tenant_id)
//  2. Email fallback — matches pre-OIDC users across all tenants, auto-creates mapping
//  3. Auto-create new user (if OIDC_AUTO_CREATE_USER=true)
func CreateUserFromIDPClaims(claims *OIDCUserClaims, tenantID string) (*User, *UserIdentityMapping, error) {
	// Step 1: Check if mapping already exists
	existingMapping, _ := GetUserMappingByIDPSubject(claims.Sub, tenantID)
	if existingMapping != nil {
		user, err := GetUserById(existingMapping.LurusUserID, false)
		if err != nil {
			return nil, nil, err
		}
		return user, existingMapping, nil
	}

	// Step 2: Email fallback — link pre-existing users who haven't migrated to OIDC yet.
	// Search across all tenants (legacy users may have tenant_id="default").
	if claims.Email != "" {
		var existingUser User
		err := WithoutTenantIsolation(DB).
			Where("email = ? AND status = 1 AND deleted_at IS NULL", claims.Email).
			Order("role DESC"). // prefer highest-privilege match (root > admin > user)
			First(&existingUser).Error
		if err == nil {
			mapping, mapErr := CreateUserMapping(
				existingUser.Id,
				claims.Sub,
				tenantID,
				claims.Email,
				claims.Name,
				claims.PreferredUsername,
			)
			if mapErr != nil {
				return nil, nil, fmt.Errorf("email fallback: failed to create mapping: %w", mapErr)
			}
			// Backfill display_name if empty
			if existingUser.DisplayName == "" && claims.Name != "" {
				WithoutTenantIsolation(DB).Model(&existingUser).Update("display_name", claims.Name)
			}
			return &existingUser, mapping, nil
		}
	}

	// Step 3: Auto-create new user
	tenant, err := GetTenantByID(tenantID)
	if err != nil {
		return nil, nil, err
	}

	canAdd, err := TenantCanAddUser(tenant)
	if err != nil {
		return nil, nil, err
	}
	if !canAdd {
		return nil, nil, errors.New("tenant has reached maximum user limit")
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	username = ensureUniqueUsername(username, tenantID)

	defaultQuota := GetTenantConfigInt(tenantID, "quota.new_user_quota", 10000)

	user := &User{
		Username:    username,
		Email:       claims.Email,
		DisplayName: claims.Name,
		Role:        1, // RoleCommonUser
		Status:      1, // UserStatusEnabled
		Quota:       defaultQuota,
		UsedQuota:   0,
		Group:       "default",
	}

	tenantDB := WithTenantID(DB, tenantID)
	err = tenantDB.Create(user).Error
	if err != nil {
		return nil, nil, err
	}

	mapping, err := CreateUserMapping(
		user.Id,
		claims.Sub,
		tenantID,
		claims.Email,
		claims.Name,
		claims.PreferredUsername,
	)
	if err != nil {
		tenantDB.Delete(user)
		return nil, nil, err
	}

	return user, mapping, nil
}

// ensureUniqueUsername ensures username is unique within tenant
func ensureUniqueUsername(baseUsername string, tenantID string) string {
	username := baseUsername
	suffix := 1
	tenantDB := WithTenantID(DB, tenantID)

	for {
		var count int64
		tenantDB.Model(&User{}).Where("username = ?", username).Count(&count)
		if count == 0 {
			return username
		}
		suffix++
		username = baseUsername + fmt.Sprintf("_%d", suffix)
	}
}
