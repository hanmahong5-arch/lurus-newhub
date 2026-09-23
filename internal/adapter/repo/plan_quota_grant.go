package repo

// plan_quota_grant.go — persistence for plan_quota_grants (migration 040),
// the idempotency ledger behind handler.PlanGrantV2. See
// entity.PlanQuotaGrant's doc comment for why the table exists and what
// grant_key means.

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// ErrPlanQuotaGrantExists is returned by InsertPlanQuotaGrant when a row
// with the same (tenant_id, grant_key) already exists — the grant for that
// subscription period was already applied.
var ErrPlanQuotaGrantExists = errors.New("plan quota grant already recorded for this key")

// InsertPlanQuotaGrant inserts the grant row. The UNIQUE(tenant_id,
// grant_key) index is the idempotency guard: a duplicate maps to
// ErrPlanQuotaGrantExists (race-safe — two concurrent inserts cannot both
// succeed), any other failure is returned as-is. Tenant is stamped
// explicitly by the caller; this table is not routed through the tenant
// plugin.
func InsertPlanQuotaGrant(g *entity.PlanQuotaGrant) error {
	if err := DB.Create(g).Error; err != nil {
		if isUniqueViolation(err) {
			return ErrPlanQuotaGrantExists
		}
		return err
	}
	return nil
}

// DeletePlanQuotaGrant removes a grant row by primary key. Used only to roll
// back a row whose quota credit failed, so a retry with the same key can
// succeed instead of being reported as an (unpaid) replay.
func DeletePlanQuotaGrant(id int64) error {
	return DB.Where("id = ?", id).Delete(&entity.PlanQuotaGrant{}).Error
}
