package repo

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
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
//  2. Email fallback — links a pre-OIDC user *of the same tenant*, and only when
//     the claimed address is verified, the match is unambiguous, and the matched
//     account is not privileged. See the comment on the branch for why.
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

	// Step 2: Email fallback — link a pre-OIDC user *of this tenant* who has not
	// migrated yet.
	//
	// This branch converts an IdP-supplied email claim into a local identity, so
	// it is deliberately narrow. It must never (a) cross a tenant boundary,
	// (b) resolve an ambiguous match by preferring privilege, (c) trust an
	// unverified email, or (d) adopt an admin/root account. Any one of those
	// turns "sign up at the IdP with the right address" into account takeover:
	// the callback writes the matched user's id and role straight into the
	// session (handler/oauth.go), and middleware.authHelper trusts that role.
	//
	// The tenant predicate is written out explicitly rather than left to the
	// tenant plugin so the scoping is visible at the call site.
	if claims.Email != "" && claims.EmailVerified {
		var candidates []User
		err := WithoutTenantIsolation(DB).
			Where("tenant_id = ? AND email = ? AND status = ? AND deleted_at IS NULL",
				tenantID, claims.Email, common.UserStatusEnabled).
			Limit(2).
			Find(&candidates).Error
		switch {
		case err != nil:
			// Fall through to step 3 rather than guessing.
		case len(candidates) > 1:
			common.SysLog(fmt.Sprintf(
				"oidc email fallback: refusing ambiguous match for tenant %s (%d enabled users share the claimed address)",
				tenantID, len(candidates)))
		case len(candidates) == 1 && candidates[0].Role >= common.RoleAdminUser:
			// Privileged accounts are linked by an operator, never by a login.
			common.SysLog(fmt.Sprintf(
				"oidc email fallback: refusing to auto-link privileged user %d (role %d) in tenant %s; link it explicitly",
				candidates[0].Id, candidates[0].Role, tenantID))
		case len(candidates) == 1:
			existingUser := candidates[0]
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
