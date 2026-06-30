package repo

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

type Tenant = entity.Tenant
type TenantStats = entity.TenantStats

// Re-export tenant status constants from entity
const (
	TenantStatusEnabled   = entity.TenantStatusEnabled
	TenantStatusDisabled  = entity.TenantStatusDisabled
	TenantStatusSuspended = entity.TenantStatusSuspended
)

// Re-export tenant plan type constants from entity
const (
	TenantPlanFree       = entity.TenantPlanFree
	TenantPlanPro        = entity.TenantPlanPro
	TenantPlanEnterprise = entity.TenantPlanEnterprise
)

// Re-export tenant context key constants from entity
const (
	TenantIDContextKey     = entity.TenantIDContextKey
	SkipTenantIsolationKey = entity.SkipTenantIsolationKey
)

// GetTenantByID retrieves a tenant by its ID
func GetTenantByID(id string) (*Tenant, error) {
	var tenant Tenant
	err := DB.Where("id = ?", id).First(&tenant).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tenant not found")
		}
		return nil, err
	}
	return &tenant, nil
}

// GetTenantBySlug retrieves a tenant by its slug
func GetTenantBySlug(slug string) (*Tenant, error) {
	var tenant Tenant
	err := DB.Where("slug = ?", slug).First(&tenant).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tenant not found")
		}
		return nil, err
	}
	return &tenant, nil
}

// GetTenantByIDPOrgID retrieves a tenant by upstream OIDC Organization ID.
// NOTE(idp-migration): the SQL/physical column stays zitadel_org_id until the
// column-rename migration is reserved & applied (owner-gated). Only the Go
// function name is vendor-neutralized.
func GetTenantByIDPOrgID(orgID string) (*Tenant, error) {
	var tenant Tenant
	err := DB.Where("zitadel_org_id = ?", orgID).First(&tenant).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tenant not found for OIDC Org ID")
		}
		return nil, err
	}
	return &tenant, nil
}

// CreateTenantFromIDP creates a new tenant from upstream OIDC Organization data.
// Auto-called when a user from a new OIDC Organization logs in.
func CreateTenantFromIDP(orgID string, orgDomain string, orgName string) (*Tenant, error) {
	// Check if tenant already exists
	existingTenant, _ := GetTenantByIDPOrgID(orgID)
	if existingTenant != nil {
		return existingTenant, nil
	}

	// Generate tenant ID (can use orgID or generate new UUID)
	tenantID := GenerateID() // You can implement this function or use orgID directly

	tenant := &Tenant{
		Id:        tenantID,
		IDPOrgID:  orgID,
		Slug:      orgDomain, // Use OIDC org domain as slug
		Name:      orgName,
		Status:    TenantStatusEnabled,
		PlanType:  TenantPlanFree, // Default to free plan
		MaxUsers:  100,
		MaxQuota:  1000000, // 1M tokens
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := DB.Create(tenant).Error
	if err != nil {
		return nil, err
	}

	return tenant, nil
}

// UpdateTenant updates tenant information
func UpdateTenant(id string, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return DB.Model(&Tenant{}).Where("id = ?", id).Updates(updates).Error
}

// DisableTenant disables a tenant
func DisableTenant(id string) error {
	return UpdateTenant(id, map[string]interface{}{
		"status": TenantStatusDisabled,
	})
}

// EnableTenant enables a tenant
func EnableTenant(id string) error {
	return UpdateTenant(id, map[string]interface{}{
		"status": TenantStatusEnabled,
	})
}

// SuspendTenant suspends a tenant (for billing issues or violations)
func SuspendTenant(id string) error {
	return UpdateTenant(id, map[string]interface{}{
		"status": TenantStatusSuspended,
	})
}

// DeleteTenant soft deletes a tenant
func DeleteTenant(id string) error {
	return DB.Delete(&Tenant{}, "id = ?", id).Error
}

// ListTenants retrieves all tenants with pagination
func ListTenants(offset int, limit int, status int) ([]*Tenant, int64, error) {
	var tenants []*Tenant
	var total int64

	query := DB.Model(&Tenant{})

	// Filter by status if provided
	if status > 0 {
		query = query.Where("status = ?", status)
	}

	// Get total count
	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	err = query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&tenants).Error
	if err != nil {
		return nil, 0, err
	}

	return tenants, total, nil
}

// GetTenantUserCount returns the number of users in a tenant
// by counting identity mappings (User model has no tenant_id column).
func GetTenantUserCount(tenantID string) (int64, error) {
	var count int64
	err := DB.Model(&UserIdentityMapping{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// TenantCanAddUser checks if tenant can add more users (based on max_users limit)
func TenantCanAddUser(t *Tenant) (bool, error) {
	currentUserCount, err := GetTenantUserCount(t.Id)
	if err != nil {
		return false, err
	}

	return currentUserCount < int64(t.MaxUsers), nil
}

// GenerateID generates a unique ID for tenant
// You can implement this using UUID library or custom logic
func GenerateID() string {
	// TODO: Implement UUID generation
	// For now, using a placeholder
	// In production, use: github.com/google/uuid
	return "tenant-" + time.Now().Format("20060102150405")
}

// ============================================================================
// Tenant Statistics Functions
// ============================================================================

// GetTenantStats retrieves comprehensive statistics for a tenant
func GetTenantStats(tenantID string) (*TenantStats, error) {
	stats := &TenantStats{TenantID: tenantID}
	var err error

	// Get tenant info for max_users and max_quota
	tenant, err := GetTenantByID(tenantID)
	if err != nil {
		return nil, err
	}
	stats.MaxUsers = tenant.MaxUsers
	stats.MaxQuota = tenant.MaxQuota

	// User count (from identity mappings)
	stats.UserCount, _ = GetTenantUserCount(tenantID)

	// Token count
	stats.TokenCount, _ = GetTenantTokenCount(tenantID)

	// Channel count
	stats.ChannelCount, _ = GetTenantChannelCount(tenantID)

	// Quota statistics
	usedQuota, remainingQuota, _ := GetTenantQuotaStats(tenantID)
	stats.TotalQuotaUsed = usedQuota
	stats.TotalQuotaRemaining = remainingQuota

	// Redemption count
	stats.TotalRedemptions, _ = GetTenantRedemptionCount(tenantID)

	// Log count
	stats.LogCount, _ = GetTenantLogCount(tenantID)

	// Last activity (most recent log)
	stats.LastActivityAt, _ = GetTenantLastActivityTime(tenantID)

	return stats, nil
}

// GetTenantTokenCount returns the number of tokens in a tenant
func GetTenantTokenCount(tenantID string) (int64, error) {
	var count int64
	err := DB.Model(&Token{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// GetTenantChannelCount returns the number of channels in a tenant
func GetTenantChannelCount(tenantID string) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// GetTenantQuotaStats returns used and remaining quota for a tenant
func GetTenantQuotaStats(tenantID string) (usedQuota int64, remainingQuota int64, err error) {
	// Sum used_quota and remain_quota from tokens
	type QuotaResult struct {
		UsedQuota   int64 `json:"used_quota"`
		RemainQuota int64 `json:"remain_quota"`
	}
	var result QuotaResult

	err = DB.Model(&Token{}).
		Select("COALESCE(SUM(used_quota), 0) as used_quota, COALESCE(SUM(remain_quota), 0) as remain_quota").
		Where("tenant_id = ?", tenantID).
		Scan(&result).Error

	return result.UsedQuota, result.RemainQuota, err
}

// GetTenantMonthlyQuotaUsed returns the sum of quota consumed by a tenant within
// the given billing period (format "YYYY-MM"). It queries the log table so no
// cron reset is needed — the window is computed at query time.
func GetTenantMonthlyQuotaUsed(tenantID string, period string) (int64, error) {
	// Parse period "YYYY-MM" into UTC month boundaries (unix seconds).
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return 0, fmt.Errorf("invalid period %q: %w", period, err)
	}
	startUnix := t.UTC().Unix()
	// First second of next month
	endUnix := t.AddDate(0, 1, 0).UTC().Unix()

	var used int64
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(quota), 0)").
		Where("tenant_id = ? AND type = ? AND created_at >= ? AND created_at < ?",
			tenantID, LogTypeConsume, startUnix, endUnix).
		Scan(&used).Error
	return used, err
}

// GetTenantRedemptionCount returns the number of redemption codes in a tenant
func GetTenantRedemptionCount(tenantID string) (int64, error) {
	var count int64
	err := DB.Model(&Redemption{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// GetTenantLogCount returns the number of log entries for a tenant
func GetTenantLogCount(tenantID string) (int64, error) {
	var count int64
	err := LOG_DB.Model(&Log{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// GetTenantLastActivityTime returns the timestamp of the most recent activity (log entry) for a tenant
func GetTenantLastActivityTime(tenantID string) (int64, error) {
	var lastActivity int64
	err := LOG_DB.Model(&Log{}).
		Select("COALESCE(MAX(created_at), 0)").
		Where("tenant_id = ?", tenantID).
		Scan(&lastActivity).Error
	return lastActivity, err
}
