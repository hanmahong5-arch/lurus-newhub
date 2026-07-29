package repo

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var testDBCounter atomic.Int64

var dbnameRe = regexp.MustCompile(`(?i)(dbname=)\S+`)

// buildTestDSN replaces the database name in a PostgreSQL DSN.
// Supports both URL format (postgres://user:pass@host:5432/dbname?params)
// and key-value format (host=localhost dbname=postgres sslmode=disable).
func buildTestDSN(baseDSN, dbName string) string {
	// URL format: postgres://... or postgresql://...
	if strings.HasPrefix(baseDSN, "postgres://") || strings.HasPrefix(baseDSN, "postgresql://") {
		u, err := url.Parse(baseDSN)
		if err == nil {
			u.Path = "/" + dbName
			return u.String()
		}
	}
	// Key-value format
	if dbnameRe.MatchString(baseDSN) {
		return dbnameRe.ReplaceAllString(baseDSN, "${1}"+dbName)
	}
	return baseDSN + " dbname=" + dbName
}

// SetupTestDB connects to PostgreSQL (via TEST_POSTGRES_DSN), creates an isolated
// database named "test_repo_<nanosecond>", runs AutoMigrate, and wires the
// package-level DB / LOG_DB globals.  The returned cleanup function drops the
// database and restores all globals.
//
// If TEST_POSTGRES_DSN is not set the test is skipped, so developers who do
// not have a local PG instance are not broken.
func SetupTestDB(t *testing.T) func() {
	t.Helper()

	baseDSN := os.Getenv("TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL integration test")
	}

	// Use nanosecond timestamp for unique DB names across parallel tests.
	dbName := fmt.Sprintf("test_repo_%d_%d", time.Now().UnixNano(), testDBCounter.Add(1))

	// Connect to the management DB to create the isolated test DB.
	adminDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open admin connection: %v", err)
	}
	if err := adminDB.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("failed to create test database %q: %v", dbName, err)
	}
	if sqlDB, err := adminDB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	// Connect to the newly created test DB.
	testDSN := buildTestDSN(baseDSN, dbName)
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database %q: %v", dbName, err)
	}

	tables := []interface{}{
		&User{},
		&Token{},
		&Log{},
		&InternalApiKey{},
		&Setup{},
		&Tenant{},
		&UserIdentityMapping{},
		&TenantConfig{},
		&Option{},
		&Channel{},
		&Ability{},
		&Midjourney{},
		&Task{},
		&QuotaData{},
		&Redemption{},
		// NOTE: AutoMigrate creates `projects` WITHOUT the partial unique
		// index — GORM cannot express `WHERE deleted_at IS NULL`, so that
		// index exists only in migration 029. Uniqueness behaviour is proven
		// against the real DDL in internal/pkg/migration/projects_pg_test.go;
		// what CreateProject guarantees on every dialect (its explicit
		// pre-check) is what these repo tests exercise.
		&entity.Project{},
	}
	if err := db.AutoMigrate(tables...); err != nil {
		t.Fatalf("failed to auto-migrate: %v", err)
	}

	// Save previous state.
	prevDB := DB
	prevLogDB := LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMySQL := common.UsingMySQL
	prevRedisEnabled := common.RedisEnabled

	DB = db
	LOG_DB = db
	common.UsingSQLite = false
	common.UsingPostgreSQL = true
	common.UsingMySQL = false
	common.RedisEnabled = false

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	initCol()

	cleanup := func() {
		// Close the test DB connection before dropping.
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}

		// Drop the isolated test database.
		dropDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{})
		if err == nil {
			_ = dropDB.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error
			if sqlDB, err := dropDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}

		DB = prevDB
		LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.UsingMySQL = prevMySQL
		common.RedisEnabled = prevRedisEnabled
	}

	t.Cleanup(cleanup)
	return cleanup
}

// SeedTestUsers creates three users: root (role 100), normal (role 1), and
// disabled (role 1, status disabled). Returns pointers to the created User records.
func SeedTestUsers(t *testing.T) (root, normal, disabled *User) {
	t.Helper()

	root = &User{
		Username:    "testroot",
		DisplayName: "Test Root",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
		Email:       "root@test.local",
		Quota:       100_000_000,
		Group:       "default",
	}

	normal = &User{
		Username:    "testnormal",
		DisplayName: "Test Normal",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "normal@test.local",
		Quota:       1_000_000,
		Group:       "default",
	}

	disabled = &User{
		Username:    "testdisabled",
		DisplayName: "Test Disabled",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusDisabled,
		Email:       "disabled@test.local",
		Quota:       0,
		Group:       "default",
	}

	for _, u := range []*User{root, normal, disabled} {
		if err := DB.Create(u).Error; err != nil {
			t.Fatalf("failed to seed user %q: %v", u.Username, err)
		}
	}

	return root, normal, disabled
}

// SeedTestApiKeys creates two InternalApiKey records: one with full access
// (ScopeAll) and one with read-only scopes. It returns the persisted key
// objects and a slice of raw (unhashed) key strings in the same order
// [fullRawKey, readOnlyRawKey].
func SeedTestApiKeys(t *testing.T) (fullKey, readOnlyKey *InternalApiKey, rawKeys []string) {
	t.Helper()

	makeKey := func(name, rawKey string, scopes []string, createdBy int, expiresAt int64, desc string) *InternalApiKey {
		t.Helper()
		scopesJSON, err := json.Marshal(scopes)
		if err != nil {
			t.Fatalf("json.Marshal scopes: %v", err)
		}
		k := &InternalApiKey{
			Name:        name,
			KeyHash:     hashKey(rawKey),
			KeyPrefix:   rawKey[:16],
			Scopes:      string(scopesJSON),
			CreatedBy:   createdBy,
			CreatedAt:   common.GetTimestamp(),
			ExpiresAt:   expiresAt,
			Enabled:     true,
			Description: desc,
		}
		if err := DB.Create(k).Error; err != nil {
			t.Fatalf("failed to seed api key %q: %v", name, err)
		}
		return k
	}

	fullRaw := "lurus_ik_" + common.GetRandomString(32)
	readOnlyRaw := "lurus_ik_" + common.GetRandomString(32)

	fullKey = makeKey(
		"full-access-key",
		fullRaw,
		[]string{ScopeAll},
		1,
		0, // never expires
		"Full access test key",
	)

	readOnlyKey = makeKey(
		"read-only-key",
		readOnlyRaw,
		[]string{ScopeUserRead, ScopeQuotaRead, ScopeTokenRead, ScopeBalanceRead},
		1,
		time.Now().Add(24*time.Hour).Unix(),
		"Read-only test key",
	)

	rawKeys = []string{fullRaw, readOnlyRaw}
	return fullKey, readOnlyKey, rawKeys
}

// SeedTestTokens creates several Token records for the given userId:
//   - an active unlimited-quota token
//   - an active token with limited quota (500000 remain)
//   - an expired token
//   - a disabled token
func SeedTestTokens(t *testing.T, userId int) {
	t.Helper()

	now := common.GetTimestamp()

	tokens := []Token{
		{
			UserId:         userId,
			Key:            "sk-test-unlimited-" + common.GetRandomString(24),
			Status:         common.TokenStatusEnabled,
			Name:           "unlimited-token",
			CreatedTime:    now,
			AccessedTime:   now,
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Group:          "default",
		},
		{
			UserId:         userId,
			Key:            "sk-test-limited-" + common.GetRandomString(26),
			Status:         common.TokenStatusEnabled,
			Name:           "limited-token",
			CreatedTime:    now,
			AccessedTime:   now,
			ExpiredTime:    now + 86400,
			RemainQuota:    int(common.QuotaPerUnit),
			UnlimitedQuota: false,
			Group:          "default",
		},
		{
			UserId:      userId,
			Key:         "sk-test-expired-" + common.GetRandomString(26),
			Status:      common.TokenStatusEnabled,
			Name:        "expired-token",
			CreatedTime: now - 172800,
			ExpiredTime: now - 86400, // expired yesterday
			Group:       "default",
		},
		{
			UserId:         userId,
			Key:            "sk-test-disabled-" + common.GetRandomString(25),
			Status:         common.TokenStatusEnabled + 1, // disabled status
			Name:           "disabled-token",
			CreatedTime:    now,
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Group:          "default",
		},
	}

	for i := range tokens {
		if err := DB.Create(&tokens[i]).Error; err != nil {
			t.Fatalf("failed to seed token %q: %v", tokens[i].Name, err)
		}
	}
}
