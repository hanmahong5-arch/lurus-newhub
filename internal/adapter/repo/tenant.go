package repo

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

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

	// Reject a NEW tenant whose slug would shadow a reserved static v2 route
	// segment (D5, lane L4). Inserted AFTER the idempotent short-circuit
	// above so an already-existing tenant (e.g. the live slug="switch"
	// tenant, out of scope per X3) keeps resolving on repeat calls instead
	// of suddenly failing here.
	if err := ValidateTenantSlug(orgDomain); err != nil {
		return nil, err
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

// TenantMissingModeEnv names the environment variable that decides what
// TenantGate does when a tenant id resolves to no row. It is read fresh on
// every call — same posture as TENANT_MODEL_ALLOWLIST_MODE
// (internal/app/tenantpolicy/allowlist.go) — so an operator's change takes
// effect on the next request rather than the next restart.
const TenantMissingModeEnv = "TENANT_MISSING_MODE"

// Tenant-gate modes. The exact string "enforce" enforces; unset, empty,
// "Enforce" and any other value observe.
const (
	TenantGateModeEnforce = "enforce"
	TenantGateModeObserve = "observe"
)

// Tenant-gate denial reasons, returned by TenantGate so a caller can put the
// cause in an audit row. TenantGateReasonDisabled is spelled exactly as the
// string middleware/auth.go's relay gate has always written into its
// ActionAuthFailed detail JSON, so that audit shape does not change.
const (
	TenantGateReasonDisabled = "tenant_disabled"
	TenantGateReasonMissing  = "tenant_missing"
)

// TenantMissingMode returns the configured behaviour for a tenant id with no
// row: TenantGateModeEnforce when TENANT_MISSING_MODE is exactly "enforce",
// TenantGateModeObserve for every other value.
func TenantMissingMode() string {
	if strings.TrimSpace(os.Getenv(TenantMissingModeEnv)) == TenantGateModeEnforce {
		return TenantGateModeEnforce
	}
	return TenantGateModeObserve
}

// tenantMissingLogged throttles the observe-mode log line to one entry per
// tenant id per tenantMissingLogWindow, so a deleted tenant with live traffic
// does not write one line per request. The metric is the unthrottled signal.
// The key space is the set of tenant ids on user/token rows, not anything a
// request supplies.
var (
	tenantMissingLogged sync.Map // map[string]time.Time — tenant id -> last log
)

// tenantMissingLogWindow is how long a tenant id stays quiet after being
// logged once.
const tenantMissingLogWindow = time.Minute

// TenantGate answers whether a request owned by tenantID may proceed, and if
// not, why (TenantGateReason*). It is the single decision point behind the
// three gates in middleware/auth.go — the session/console path, the relay
// token path and the playground token path.
//
// Cases:
//
//   - Empty id, or the bootstrap/system tenant "default": allowed. "default"
//     predates the tenants table on some deployments and stays exempt here,
//     as it was before this function existed; an empty id is a legacy row.
//   - Row exists and Tenant.IsDisabled(): denied (TenantGateReasonDisabled).
//     Unchanged behaviour, now counted.
//   - No row — never created, or soft-deleted by DeleteTenant, which sets
//     deleted_at on the tenants row and touches nothing else, so that tenant's
//     token, user and log rows stay exactly as they were:
//     under the default observe mode the request is ALLOWED and only counted
//     and logged; under TENANT_MISSING_MODE=enforce it is denied
//     (TenantGateReasonMissing). Before this gate existed the lookup error was
//     indistinguishable from a DB fault and the request was simply admitted,
//     so a deleted tenant's tokens kept relaying and spending.
//   - Any other lookup error (DB down, driver fault): allowed. Fail-open on a
//     transient backend fault is the pre-existing posture at all three call
//     sites and is deliberately kept — a DB hiccup must not 403 every request.
func TenantGate(tenantID string) (ok bool, reason string) {
	if tenantID == "" || tenantID == "default" {
		return true, ""
	}

	var tenant Tenant
	err := DB.Where("id = ?", tenantID).First(&tenant).Error
	switch {
	case err == nil:
		if tenant.IsDisabled() {
			metrics.RecordTenantGate("disabled_denied")
			return false, TenantGateReasonDisabled
		}
		return true, ""
	case errors.Is(err, gorm.ErrRecordNotFound):
		if TenantMissingMode() == TenantGateModeEnforce {
			metrics.RecordTenantGate("missing_denied")
			return false, TenantGateReasonMissing
		}
		metrics.RecordTenantGate("missing_observed")
		logTenantMissingObserved(tenantID)
		return true, ""
	default:
		// Transient backend fault: keep the pre-existing fail-open.
		return true, ""
	}
}

// logTenantMissingObserved writes the observe-mode system log line for
// tenantID at most once per tenantMissingLogWindow.
func logTenantMissingObserved(tenantID string) {
	now := time.Now()
	if prev, loaded := tenantMissingLogged.Load(tenantID); loaded {
		if last, isTime := prev.(time.Time); isTime && now.Sub(last) < tenantMissingLogWindow {
			return
		}
	}
	tenantMissingLogged.Store(tenantID, now)
	common.SysLog(fmt.Sprintf("tenant gate: tenant %q has no row (deleted or never created) — admitting the request because %s is %q; set it to %q to deny",
		tenantID, TenantMissingModeEnv, TenantGateModeObserve, TenantGateModeEnforce))
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

// TenantUserSeatCount returns how many newhub users occupy a seat in tenantID,
// counting rows in `users` by users.tenant_id (entity/user.go:15).
//
// It deliberately differs from GetTenantUserCount, which counts
// user_identity_mappings rows: the session-bridge signup path
// (handler.autoCreateBridgedUser) inserts a users row and no mapping row, so a
// mapping-based count reads 0 for a tenant whose seats were filled through the
// bridge. The mapping writers, enumerated 2026-09-19 by grepping
// `UserIdentityMapping{` and `user_identity_mappings` across non-test internal/
// sources, are repo.CreateUserMapping (user_mapping.go:48) and the /internal
// provisioning self-heal (handler/internal_api_ext.go:507) — neither is on the
// bridge path.
// GetTenantUserCount is left alone: GetTenantStats and the OIDC provisioning
// path (CreateUserFromIDPClaims) keep their existing meaning.
//
// Isolation is switched off explicitly because the question is about ONE named
// tenant asked from a request that belongs to nobody yet (a first login).
func TenantUserSeatCount(tenantID string) (int64, error) {
	var count int64
	err := WithoutTenantIsolation(DB).Model(&User{}).Where("tenant_id = ?", tenantID).Count(&count).Error
	return count, err
}

// TenantHasFreeSeat reports whether tenantID can take one more user under
// tenants.max_users, together with the numbers behind the answer (used seats
// and the configured limit) so the caller can log or audit them.
//
// Fails OPEN — returns true — when the tenant id is empty, the tenant row
// cannot be read, max_users is <= 0 (no ceiling configured), or the count
// query errors. This sits on a login path: a ceiling nobody set, or a
// transient DB fault, must not lock people out. TenantCanAddUser answers
// false for max_users <= 0; that difference is intentional and its callers
// are unchanged.
func TenantHasFreeSeat(tenantID string) (ok bool, used int64, limit int) {
	if tenantID == "" {
		return true, 0, 0
	}
	tenant, err := GetTenantByID(tenantID)
	if err != nil || tenant == nil {
		return true, 0, 0
	}
	if tenant.MaxUsers <= 0 {
		return true, 0, tenant.MaxUsers
	}
	used, err = TenantUserSeatCount(tenantID)
	if err != nil {
		return true, 0, tenant.MaxUsers
	}
	return used < int64(tenant.MaxUsers), used, tenant.MaxUsers
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
