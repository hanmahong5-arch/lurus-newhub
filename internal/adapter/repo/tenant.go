package repo

import (
	"crypto/rand"
	"encoding/hex"
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

// ErrTenantConflict is returned by CreateTenantFromIDP when the insert hits a
// unique constraint: the slug is taken, or a concurrent request created the
// same IdP organization first. The driver error names the constraint and
// echoes the key, so callers answer with this instead.
var ErrTenantConflict = errors.New("a tenant with this slug or IdP organization id already exists")

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
		if isUniqueViolation(err) {
			return nil, ErrTenantConflict
		}
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
// not, why (TenantGateReason*). It is the shared decision the tenant-lifecycle
// gates take; grep for callers rather than trusting this list, which was
// accurate on 2026-09-20 (cycle 13):
//
//   - middleware/auth.go — the session/console arm (authHelper), the relay
//     token arm and the playground token arm.
//   - middleware/oidc_auth.go — the cookie arm of OIDCAuth (the JWT arm takes
//     the same decision inline, in mapOIDCUserToLurus).
//   - handler/v2_provision.go (ProvisionV2, right after the slug resolves),
//     handler/switch_user_info.go (the raw relay-token auth shared by
//     GET /api/v2/switch/user/info and POST /api/v2/switch/user/topup),
//     handler/user_heartbeat.go and handler/switch_reconciliation.go — the
//     Switch-facing surfaces that authenticate by raw Token.Key, which no
//     auth middleware covers. handler/switch_tenant_gate_completeness_test.go
//     is the forcing function for that group.
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
			metrics.RecordTenantGate(metrics.TenantGateOutcomeDisabledDenied)
			return false, TenantGateReasonDisabled
		}
		return true, ""
	case errors.Is(err, gorm.ErrRecordNotFound):
		if TenantMissingMode() == TenantGateModeEnforce {
			metrics.RecordTenantGate(metrics.TenantGateOutcomeMissingDenied)
			return false, TenantGateReasonMissing
		}
		metrics.RecordTenantGate(metrics.TenantGateOutcomeMissingObserved)
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

// GetTenantUserCount returns the number of rows in user_identity_mappings for
// a tenant — i.e. how many users reached it through the OIDC identity path.
//
// It is NOT the tenant's seat occupancy. `users` does carry a tenant_id column
// (entity/user.go:15), and the session-bridge signup path writes a users row
// with no mapping row, so a bridge-provisioned tenant counts 0 here; the
// comment that used to sit on this function said the User model had no
// tenant_id column, which was false. TenantUserSeatCount below is the seat
// number, and it is what the admin console and the seat cap both read.
// This function stays mapping-based because TenantCanAddUser — the OIDC
// provisioning path's own ceiling check (user_mapping.go:271-277) — has always
// counted mappings, and re-basing it is a money/lockout-adjacent change that
// belongs with the projection work, not here.
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
// GetTenantUserCount keeps its mapping-based meaning for its one remaining
// caller, TenantCanAddUser (the OIDC provisioning path's ceiling);
// GetTenantStats and GET /api/v2/admin/tenants/:id moved to this seat count.
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
//
// Not a reservation: the read and the caller's INSERT are separate statements,
// so two first logins racing into the last seat can both be admitted (same
// weakness the older TenantCanAddUser has). Recorded, not fixed, in cycle 12 —
// closing it needs a DB-level guard (a partial unique index or a counted
// UPDATE … WHERE seats < max_users), which is next cycle's work; the ceiling
// is a plan limit, not a security boundary, and overshooting it by the number
// of simultaneous first logins is bounded and visible in the console's seat
// count.
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

// PendingInviteTenantID answers "if this invite code were redeemed right now,
// which tenant would it place the user in?" without redeeming it. Returns
// ok=false for an absent/unknown/revoked/already-consumed/expired code — i.e.
// exactly the cases where ConsumeTenantInvite would fail and the caller would
// fall back to the "default" tenant.
//
// It exists so handler.ZitaBootstrap can check the tenant's seat ceiling
// BEFORE consuming the code: ConsumeTenantInvite is atomic and single-use, so
// a seat refusal after consumption burns a one-time code the visitor then has
// to ask an admin to reissue.
//
// Read-only and advisory. ConsumeTenantInvite remains the single authority on
// whether a code redeems — this predicate deliberately duplicates its status
// and expiry rules rather than widening them, so a code this function rejects
// still gets its ordinary "fall back to default, log the user in" treatment
// there. Lives in tenant.go rather than next to the other invite functions
// because that is the file cycle 12 L9 owns; moving it is a tidy-up for a
// later cycle.
func PendingInviteTenantID(code string) (string, bool) {
	if code == "" {
		return "", false
	}
	var invite TenantInvite
	if err := WithoutTenantIsolation(DB).Where("code = ?", code).First(&invite).Error; err != nil {
		return "", false
	}
	if invite.Status != TenantInviteStatusPending {
		return "", false
	}
	if invite.ExpiredTime != 0 && invite.ExpiredTime < common.GetTimestamp() {
		return "", false
	}
	if invite.TenantId == "" {
		return "", false
	}
	return invite.TenantId, true
}

// GenerateID returns a new tenant primary key: "tenant-" + a second-resolution
// timestamp (kept so ids stay sortable and recognisable in logs) + "-" + 8 hex
// digits of crypto randomness.
//
// The timestamp alone was the whole id until 2026-09-24, so two tenants
// created within the same second collided on tenants_pkey and the second
// create failed with a raw "duplicate key" error. Constraints the value must
// keep: at most 36 bytes (entity.Tenant.Id is size:36) and no ':' (the
// business rate limiter's keys are prefix+tenantID+":"+model and rely on it).
func GenerateID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; if it ever does,
		// fall back to the clock rather than issuing a colliding id.
		return fmt.Sprintf("tenant-%s-%08x", time.Now().Format("20060102150405"), uint32(time.Now().UnixNano()))
	}
	return "tenant-" + time.Now().Format("20060102150405") + "-" + hex.EncodeToString(b[:])
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

	// Seat occupancy — the SAME number the seat cap enforces
	// (TenantHasFreeSeat -> TenantUserSeatCount), so the count an admin reads
	// next to max_users in the console cannot disagree with the ceiling on the
	// two bridge paths. The OIDC provisioning path (TenantCanAddUser, reached
	// from oauth.go) still counts identity mappings and can admit past this
	// number for a bridge-filled tenant. It used to be GetTenantUserCount, which counts
	// identity mappings and therefore reads 0 for a tenant whose seats were
	// filled through the session bridge.
	stats.UserCount, _ = TenantUserSeatCount(tenantID)

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
