package repo

// tenant_plugin_registry_test.go — structural forcing function for
// TenantPlugin's hasTenantIDColumn allow-list (tablesWithTenantID,
// tenant_plugin.go). The AutoMigrate model list in migrateDB (main.go) is
// the canonical registry of persisted entities; registeredModels below
// mirrors it (keep in sync — a model added there and never added here
// silently escapes this guard).
//
// Every registered model whose GORM-derived table has a tenant_id column
// must be either:
//   - on the allow-list — the plugin backstop actively re-filters/stamps
//     queries for any call site that routes through a tenant-scoped DB
//     handle (WithTenantID / GetTenantDB*); or
//   - listed in tenantColumnExempt with a verifiable one-line reason: no
//     call site in the repo routes that table through a tenant-scoped
//     handle (grepped for WithTenantID/GetTenantDB), so the plugin backstop
//     never engages there — isolation, if any, is enforced by the repo
//     layer's own explicit .Where("tenant_id = ?", …) filters instead
//     (mirrors the documented "logs" exclusion already in tenant_plugin.go).
//
// The reverse direction is checked too: an allow-list entry that matches no
// registered model's derived table name is dead configuration — it reads as
// protection but filters nothing.
//
// This does NOT assert the plugin is the primary enforcement layer (its own
// doc comment says it isn't — most call sites use bare-handle .Where
// filters). It only forces every tenant_id column into one bucket or the
// other so allow-list drift is a loud test failure, not a silent gap.

import (
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm/schema"
)

// registeredModels mirrors migrateDB()'s AutoMigrate argument list
// (main.go, func migrateDB). This is the canonical persisted-entity
// registry — models auto-migrated only via a package-local AutoMigrate call
// (e.g. UserTOTP, BillingOutbox) or only via the embedded SQL runner (e.g.
// AuditChainHead, migration 024) are out of scope for this guard.
var registeredModels = []interface{}{
	&Channel{}, &Token{}, &User{}, &Option{}, &Redemption{}, &Ability{},
	&Log{}, &Midjourney{}, &QuotaData{}, &Task{}, &Model{}, &Vendor{},
	&PrefillGroup{}, &Setup{}, &InternalApiKey{},
	&Tenant{}, &UserIdentityMapping{}, &TenantConfig{},
	&entity.Release{}, &entity.ReleaseArtifact{}, &entity.DownloadLog{},
	&SwitchConfigPresetRow{},
	&entity.CurrencyExchange{},
	&entity.AuditEvent{},
	&entity.OpenRouterSyncJob{}, &entity.ModelUsageStat{},
	&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{},
	&entity.CreditPoolFundEvent{},
	&entity.ProvisionedRedemptionBatch{},
	&PlaygroundPreset{},
	&entity.LeaderElection{},
	&entity.PrivacyErasureRequest{},
	&entity.ModelRateLimit{},
}

// tenantColumnExempt maps a GORM-derived table name (not on
// tablesWithTenantID) to why its tenant_id column is not plugin-scoped.
// Each reason names the call site(s) verified to prove it.
var tenantColumnExempt = map[string]string{
	"logs": "documented directly on tablesWithTenantID in tenant_plugin.go: " +
		"admin/cross-tenant log views legitimately span tenants, no log call " +
		"site uses a tenant-bound handle, LOG_DB may be a separate database " +
		"that never registers this plugin; isolation enforced structurally in " +
		"repo/log.go instead",
	"user_identity_mapping": "CreateUserMapping (user_mapping.go) writes via a bare " +
		"DB.Create with TenantID set explicitly in the struct literal; reads " +
		"filter by an explicit tenant_id WHERE clause (GetUserMappingByIDPSubject " +
		"etc.) — no call site routes this table through WithTenantID/GetTenantDB",
	"tenant_configs": "every read/write in tenant_config.go uses an explicit " +
		`.Where("tenant_id = ?", …) filter on the bare DB handle — never ` +
		"WithTenantID/GetTenantDB",
	"audit_events": "admin audit views are intentionally cross-tenant " +
		"(GET /api/v2/admin/audit/*, RootJWTAuth-gated); the hash-chain link " +
		"step (AuditEvent.chainLink) filters explicitly by tenant_id — no call " +
		"site uses a tenant-scoped handle",
	"tenant_credit_pools": "every read/write in tenant_credit_pool.go uses an " +
		`explicit .Where("tenant_id = ?", …) filter — never WithTenantID/GetTenantDB`,
	"tenant_credit_pool_draws": "same file/pattern as tenant_credit_pools — " +
		"explicit tenant_id filtering, not plugin-routed",
	"credit_pool_fund_events": "same file/pattern as tenant_credit_pools — " +
		"explicit tenant_id filtering, not plugin-routed",
	"provisioned_redemption_batches": "documented in provisioned_redemption.go: the " +
		"WithTenantID handle used in that transaction exists only to stamp the " +
		"nested Redemption creates (redemptions IS plugin-scoped); the batch " +
		"table itself is deliberately not on the allow-list, and the scoped " +
		"handle is harmless there",
	"playground_presets": "every read/write in playground_preset.go uses an " +
		`explicit .Where("tenant_id = ? AND user_id = ?", …) filter — never ` +
		"WithTenantID/GetTenantDB",
	"privacy_erasure_requests": "internal PIPL-erasure intake keyed by platform " +
		"account_id/event_id (lurus-platform-triggered, not a tenant-console " +
		"resource); TenantID is intake metadata only (set once on insert), " +
		"never used as a query filter",
	"model_rate_limits": "every read/write in model_rate_limit.go uses an " +
		`explicit .Where("tenant_id = ?", …) filter — never WithTenantID/GetTenantDB`,
}

// TestTenantPlugin_AllowListCoversEveryRegisteredTenantColumn is the
// structural guard: a newly-added entity with a tenant_id column that is
// wired through a tenant-scoped handle but forgotten from the allow-list
// would otherwise fail silently (beforeQuery/beforeCreate/etc. just skip
// it — see hasTenantIDColumn). This test forces the classification instead.
func TestTenantPlugin_AllowListCoversEveryRegisteredTenantColumn(t *testing.T) {
	cacheStore := &sync.Map{}
	namer := schema.NamingStrategy{}

	// seen collects the table name of every registered model that actually
	// has a tenant_id column — used below for the allow-list reverse check.
	seen := make(map[string]bool)

	for _, m := range registeredModels {
		s, err := schema.Parse(m, cacheStore, namer)
		if err != nil {
			t.Fatalf("parse GORM schema for %T: %v", m, err)
		}
		if _, ok := s.FieldsByDBName["tenant_id"]; !ok {
			continue // no tenant_id column — out of scope for this guard
		}
		seen[s.Table] = true

		if tablesWithTenantID[s.Table] {
			continue // swept: on the plugin's active allow-list
		}
		if _, ok := tenantColumnExempt[s.Table]; ok {
			continue // exempt: justified above
		}
		t.Errorf("model %T (table %q) has a tenant_id column but is neither on "+
			"tenant_plugin.go's tablesWithTenantID allow-list nor exempted in "+
			"tenantColumnExempt (tenant_plugin_registry_test.go) — classify it: "+
			"either add the table to the allow-list, or add a one-line justification "+
			"naming the call site(s) that make plugin scoping unnecessary", m, s.Table)
	}

	// Reverse check: an allow-list entry that never matched a registered
	// model's derived table name is stale — it looks like protection but
	// filters nothing, because hasTenantIDColumn's map lookup simply never
	// hits for a table no query ever targets.
	for table := range tablesWithTenantID {
		if !seen[table] {
			t.Errorf("tenant_plugin.go allow-list entry %q matches no registered "+
				"AutoMigrate model's derived table name (registeredModels in "+
				"tenant_plugin_registry_test.go) — stale entry, dead configuration", table)
		}
	}
}
