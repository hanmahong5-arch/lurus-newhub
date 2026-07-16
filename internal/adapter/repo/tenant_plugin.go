package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TenantPlugin is a GORM plugin that filters queries by tenant_id and sets
// tenant_id on create operations. It only engages when the gorm.DB in use
// carries a tenant-scoped context.Context (via WithTenantID / GetTenantDB*) —
// most repo call sites use their own explicit .Where("tenant_id = ?", ...)
// filters instead and never touch a tenant-bound DB handle, so this plugin is
// NOT the primary enforcement layer; it is a defense-in-depth backstop for the
// call sites that do route through a tenant-scoped handle.
type TenantPlugin struct{}

// Name returns the plugin name
func (p *TenantPlugin) Name() string {
	return "TenantPlugin"
}

// Initialize initializes the tenant plugin
func (p *TenantPlugin) Initialize(db *gorm.DB) error {
	// Register callbacks for tenant isolation
	if err := db.Callback().Query().Before("gorm:query").Register("tenant:before_query", beforeQuery); err != nil {
		return err
	}

	if err := db.Callback().Create().Before("gorm:create").Register("tenant:before_create", beforeCreate); err != nil {
		return err
	}

	if err := db.Callback().Update().Before("gorm:update").Register("tenant:before_update", beforeUpdate); err != nil {
		return err
	}

	if err := db.Callback().Delete().Before("gorm:delete").Register("tenant:before_delete", beforeDelete); err != nil {
		return err
	}

	return nil
}

// TenantIDContextKey and SkipTenantIsolationKey are re-exported from entity via tenant.go

// beforeQuery adds tenant_id filter to all SELECT queries
func beforeQuery(db *gorm.DB) {
	// Check if tenant isolation should be skipped
	if skipTenantIsolation(db) {
		return
	}

	// Get tenant ID from context
	tenantID := getTenantIDFromContext(db)
	if tenantID == "" {
		// No tenant ID in context, skip (for system operations)
		return
	}

	// Check if the table has tenant_id column
	if !hasTenantIDColumn(db) {
		return
	}

	// Add WHERE tenant_id = ? clause
	db.Statement.AddClause(clause.Where{
		Exprs: []clause.Expression{
			clause.Expr{SQL: "tenant_id = ?", Vars: []interface{}{tenantID}},
		},
	})
}

// beforeCreate sets tenant_id before creating records
func beforeCreate(db *gorm.DB) {
	// Check if tenant isolation should be skipped
	if skipTenantIsolation(db) {
		return
	}

	// Check if the table has tenant_id column FIRST
	// Tables without a tenant_id column should skip this check entirely
	if !hasTenantIDColumn(db) {
		return
	}

	// Get tenant ID from context
	tenantID := getTenantIDFromContext(db)
	if tenantID == "" {
		// No tenant ID in context, this is an error for CREATE operations
		// on tables that require tenant_id
		db.AddError(errors.New("tenant_id is required for create operations"))
		return
	}

	// Set tenant_id field
	db.Statement.SetColumn("tenant_id", tenantID)
}

// beforeUpdate adds tenant_id filter to UPDATE queries
func beforeUpdate(db *gorm.DB) {
	// Check if tenant isolation should be skipped
	if skipTenantIsolation(db) {
		return
	}

	// Get tenant ID from context
	tenantID := getTenantIDFromContext(db)
	if tenantID == "" {
		// No tenant ID in context, skip (for system operations)
		return
	}

	// Check if the table has tenant_id column
	if !hasTenantIDColumn(db) {
		return
	}

	// Add WHERE tenant_id = ? clause
	db.Statement.AddClause(clause.Where{
		Exprs: []clause.Expression{
			clause.Expr{SQL: "tenant_id = ?", Vars: []interface{}{tenantID}},
		},
	})
}

// beforeDelete adds tenant_id filter to DELETE queries
func beforeDelete(db *gorm.DB) {
	// Check if tenant isolation should be skipped
	if skipTenantIsolation(db) {
		return
	}

	// Get tenant ID from context
	tenantID := getTenantIDFromContext(db)
	if tenantID == "" {
		// No tenant ID in context, skip (for system operations)
		return
	}

	// Check if the table has tenant_id column
	if !hasTenantIDColumn(db) {
		return
	}

	// Add WHERE tenant_id = ? clause
	db.Statement.AddClause(clause.Where{
		Exprs: []clause.Expression{
			clause.Expr{SQL: "tenant_id = ?", Vars: []interface{}{tenantID}},
		},
	})
}

// getTenantIDFromContext retrieves tenant_id from GORM context
func getTenantIDFromContext(db *gorm.DB) string {
	if db.Statement.Context == nil {
		return ""
	}

	tenantID, ok := db.Statement.Context.Value(TenantIDContextKey).(string)
	if !ok {
		return ""
	}

	return tenantID
}

// skipTenantIsolation checks if tenant isolation should be skipped
func skipTenantIsolation(db *gorm.DB) bool {
	if db.Statement.Context == nil {
		return false
	}

	skip, ok := db.Statement.Context.Value(SkipTenantIsolationKey).(bool)
	return ok && skip
}

// tablesWithTenantID is the plugin's auto-scoping allow-list: tables whose
// tenant_id column is actively re-filtered (query/update/delete) or stamped
// (create) whenever a call site routes through a tenant-scoped DB handle
// (WithTenantID / GetTenantDB*). It is package-level (not function-local) so
// tenant_plugin_registry_test.go can reflect over it directly — that test is
// the forcing function keeping this list honest against both directions of
// drift: a registered entity with a tenant_id column that isn't listed here
// (dead protection for any call site that assumes it is), and a listed table
// that no longer maps to any registered entity (dead configuration).
//
// The logs table HAS a tenant_id column (entity/log.go) but is deliberately
// excluded — admin/cross-tenant log views legitimately span tenants, no log
// call site uses a tenant-bound handle (so listing it here would add no read
// protection), beforeCreate would error on the bare-handle relay write path,
// and LOG_DB may be a separate database (LOG_SQL_DSN) that never has this
// plugin registered. Log isolation is instead enforced structurally in
// repo/log.go: every exported query is principal-scoped or requires an
// explicit TenantScope argument.
var tablesWithTenantID = map[string]bool{
	"users":       true,
	"tokens":      true,
	"channels":    true,
	"redemptions": true,
	// Every entry MUST map to a GORM-registered model with a tenant_id column
	// (tenant_plugin_registry_test.go enforces both directions). Do not add a
	// table here speculatively — a listed table with no backing model is dead
	// configuration that looks like protection but scopes nothing.
}

// hasTenantIDColumn checks if the current table has tenant_id column
func hasTenantIDColumn(db *gorm.DB) bool {
	tableName := db.Statement.Table
	if tableName == "" {
		// Try to get table name from schema
		if db.Statement.Schema != nil {
			tableName = db.Statement.Schema.Table
		}
	}

	return tablesWithTenantID[tableName]
}

// WithTenantID returns a new DB instance with tenant_id in context
// Use this helper function to inject tenant ID into GORM operations
func WithTenantID(db *gorm.DB, tenantID string) *gorm.DB {
	return db.WithContext(context.WithValue(db.Statement.Context, TenantIDContextKey, tenantID))
}

// WithoutTenantIsolation returns a new DB instance with tenant isolation disabled
// Use this for Platform Admin operations that need to access all tenants
func WithoutTenantIsolation(db *gorm.DB) *gorm.DB {
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(context.WithValue(ctx, SkipTenantIsolationKey, true))
}

// WithoutTenantIsolationCtx is the context-explicit variant of
// WithoutTenantIsolation for callers that already hold a ctx (keeps the
// caller's cancellation/deadline AND satisfies contextcheck — the implicit
// variant reads ctx from db.Statement, which the linter cannot follow).
func WithoutTenantIsolationCtx(ctx context.Context, db *gorm.DB) *gorm.DB {
	// SkipTenantIsolationKey is a plain string by repo-wide convention — the
	// tenant plugin reads the same untyped key everywhere (entity/tenant.go).
	// Retyping it is a cross-cutting change out of scope here.
	//nolint:staticcheck // SA1029: key type is a pre-existing convention shared with WithoutTenantIsolation
	return db.WithContext(context.WithValue(ctx, SkipTenantIsolationKey, true))
}

// GetTenantIDFromDB retrieves tenant ID from DB context
func GetTenantIDFromDB(db *gorm.DB) string {
	return getTenantIDFromContext(db)
}
