package repo

import (
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// project_spend.go — per-project showback over consume logs (migration 029).
//
// WHY THIS TAKES A TenantScope AND GetSpendByProduct DOES NOT: the savings /
// product aggregates in savings.go are platform-admin analytics — they take
// only a startTime and span EVERY tenant. Cloning that signature for a
// tenant-facing report would publish one customer's spend to another. So this
// function requires the same explicit tenant decision the rest of repo/log.go
// does; the zero TenantScope is fail-closed (matches no rows).
//
// WHY THE JOIN IS DONE IN Go AND NOT IN SQL: when LOG_SQL_DSN is set, `logs`
// lives in a different database from `projects` (LOG_DB != DB), so a SQL join
// across them is not expressible. The aggregate runs against LOG_DB, the name
// lookup against DB, and the merge happens here — the same two-step shape
// GetAllLogs already uses to attach channel_name to log rows.

// ProjectSpendRow is one project's consume spend over the requested window.
type ProjectSpendRow struct {
	ProjectId int    `json:"project_id"`
	Name      string `json:"name"`
	// Unassigned marks the project_id = 0 bucket. Clients render it as
	// "Unassigned" rather than as a project with a blank name.
	Unassigned       bool  `json:"unassigned"`
	TotalQuota       int64 `json:"total_quota"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	Count            int64 `json:"count"`
}

// projectSpendAgg is the raw GROUP BY result before names are attached.
type projectSpendAgg struct {
	ProjectId        int   `gorm:"column:project_id"`
	TotalQuota       int64 `gorm:"column:total_quota"`
	PromptTokens     int64 `gorm:"column:prompt_tokens"`
	CompletionTokens int64 `gorm:"column:completion_tokens"`
	Count            int64 `gorm:"column:count"`
}

// GetSpendByProject aggregates consume spend grouped by project for one tenant
// over [startTime, endTime] (0 on either bound means "unbounded on that side").
//
// INVARIANT: the returned rows sum to the tenant's total consume spend for the
// window. That is why project_id = 0 is emitted as a first-class "unassigned"
// row instead of being filtered out — a report whose parts do not add up to
// the whole is a report finance stops trusting immediately. It is also why
// SOFT-deleted projects still resolve to their name (ResolveProjectNames reads
// Unscoped): dropping their rows would silently under-report the total.
//
// A project id that no longer resolves to a name at all (deleted from the
// database out-of-band, or — impossible through this API but possible through
// direct SQL — belonging to another tenant) keeps its row with an empty Name.
// The spend is real and must stay in the total; the NAME is what the tenant
// clause withholds.
func GetSpendByProject(scope TenantScope, startTime, endTime int64) ([]ProjectSpendRow, error) {
	tx := scope.apply(LOG_DB.Model(&entity.Log{})).
		Where("type = ?", LogTypeConsume)
	if startTime > 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}

	var aggs []projectSpendAgg
	err := tx.Select(`project_id,
			COALESCE(SUM(quota), 0) as total_quota,
			COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) as completion_tokens,
			COUNT(*) as count`).
		Group("project_id").
		Order("total_quota DESC").
		Find(&aggs).Error
	if err != nil {
		return nil, err
	}
	if len(aggs) == 0 {
		return []ProjectSpendRow{}, nil
	}

	ids := make([]int, 0, len(aggs))
	for _, a := range aggs {
		ids = append(ids, a.ProjectId)
	}
	// Name resolution is tenant-scoped even though the aggregate above already
	// was: ResolveProjectNames has no id-only variant precisely so a stray id
	// can never resolve to another tenant's confidential project name.
	//
	// Under AllTenantsForAdmin() the scope carries no tenant id, so NO name
	// resolves and every row renders id-only. That is the honest outcome: a
	// cross-tenant aggregate has no single tenant whose names it may show.
	// Every caller of this function today passes ForTenant.
	names, err := ResolveProjectNames(scope.tenantID, ids)
	if err != nil {
		return nil, err
	}

	rows := make([]ProjectSpendRow, 0, len(aggs))
	for _, a := range aggs {
		rows = append(rows, ProjectSpendRow{
			ProjectId:        a.ProjectId,
			Name:             names[a.ProjectId],
			Unassigned:       a.ProjectId == entity.ProjectUnassigned,
			TotalQuota:       a.TotalQuota,
			PromptTokens:     a.PromptTokens,
			CompletionTokens: a.CompletionTokens,
			Count:            a.Count,
		})
	}
	return rows, nil
}
