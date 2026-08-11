package repo

// sqlite_testutil_test.go — shared SQLite test helper for repo unit tests.
//
// All tests in this file are pure in-memory SQLite; no TEST_POSTGRES_DSN
// required. Pattern mirrors how handler tests wire the DB globals.

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var sqliteTestDBCounter atomic.Int64

// setupSQLiteDB opens a unique in-memory SQLite database, auto-migrates all
// relevant tables, wires the package-level DB/LOG_DB globals, and returns a
// cleanup function that restores them. The caller must invoke cleanup in defer.
func setupSQLiteDB(t *testing.T) func() {
	t.Helper()

	dsn := fmt.Sprintf("file:repounittest%d?mode=memory&cache=shared", sqliteTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&User{},
		&Token{},
		&Log{},
		&Option{},
		&Setup{},
		&Tenant{},
		&UserIdentityMapping{},
		&TenantConfig{},
		&Channel{},
		&Ability{},
		&Redemption{},
		&QuotaData{},
		&InternalApiKey{},
		&TenantCreditPool{},
		&TenantCreditPoolDraw{},
		// Extended tables for broader coverage
		&entity.AuditEvent{},
		&Task{},
		&Model{},
		&PrefillGroup{},
		&PlaygroundPreset{},
		&Vendor{},
		&OpenRouterSyncJob{},
		// NOTE: AutoMigrate creates `projects` WITHOUT the partial unique
		// index — GORM cannot express `WHERE deleted_at IS NULL`, so that
		// index exists only in migration 029. The uniqueness DDL is proven in
		// internal/pkg/migration/projects_pg_test.go; what these tests
		// exercise is CreateProject's explicit pre-check, which is what
		// guarantees the behaviour on every dialect.
		&entity.Project{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
			// SQLite shares global index names — "already exists" is harmless.
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := DB
	prevLogDB := LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	DB = db
	LOG_DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.QuotaForNewUser = 0
	common.LogConsumeEnabled = false

	InitCol()

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	return func() {
		DB = prevDB
		LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// seedUser creates and persists a User with the given params; fatals on error.
func seedUser(t *testing.T, username, email string, role, status int, tenantID string) *User {
	t.Helper()
	u := &User{
		Username:    username,
		DisplayName: username,
		Role:        role,
		Status:      status,
		Email:       email,
		TenantId:    tenantID,
		Quota:       1_000_000,
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seedUser %q: %v", username, err)
	}
	return u
}

// seedTenant creates and persists a Tenant; fatals on error.
// ZitadelOrgID is set to the id to satisfy the UNIQUE constraint.
func seedTenant(t *testing.T, id, slug, name string) *Tenant {
	t.Helper()
	ten := &Tenant{
		Id:           id,
		Slug:         slug,
		Name:         name,
		Status:       TenantStatusEnabled,
		IDPOrgID: "org_" + id, // unique per seeded tenant
	}
	if err := DB.Create(ten).Error; err != nil {
		t.Fatalf("seedTenant %q: %v", id, err)
	}
	return ten
}

// seedRedemption inserts a Redemption for the given user and tenant.
func seedRedemptionRow(t *testing.T, userID int, tenantID, name string, quota int) *Redemption {
	t.Helper()
	r := &Redemption{
		UserId:      userID,
		TenantId:    tenantID,
		Key:         common.GetRandomString(32),
		Name:        name,
		Quota:       quota,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := DB.Create(r).Error; err != nil {
		t.Fatalf("seedRedemption %q: %v", name, err)
	}
	return r
}
