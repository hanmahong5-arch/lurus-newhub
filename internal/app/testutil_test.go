package app

import (
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// serviceTestDBCounter gives every setupServiceTestDB call its own named
// database, matching the idiom already used by openChainDB in
// internal/app/governance and by the handler-package helpers.
var serviceTestDBCounter atomic.Int64

// setupServiceTestDB creates an in-memory SQLite database with all required
// tables for service-layer tests that need DB access. It wires the global
// repo.DB and repo.LOG_DB, and restores them on cleanup.
//
// The DSN is a NAMED shared-cache database, not a bare ":memory:", and the
// pool is pinned to a single connection. Both matter:
//
// A bare ":memory:" gives every CONNECTION its own private, empty database.
// database/sql hands out a pool, so a test could seed its rows on one
// connection and then have the code under test open a second one and find no
// tables at all — "SQL logic error: no such table: release_artifacts" from a
// test that had just successfully inserted into release_artifacts. It only
// bites when the pool actually grows, which is why it read as an unrelated
// flake and surfaced under -race (different timing, more concurrent work) far
// more often than in a plain run.
//
// mode=memory&cache=shared makes all connections address the same database;
// the unique counter keeps tests from colliding with each other; and
// SetMaxOpenConns(1) serialises access so shared-cache cannot return
// SQLITE_BUSY to the async writers some of these services spawn.
func setupServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:appservice%d?mode=memory&cache=shared", serviceTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	err = db.AutoMigrate(
		&repo.User{},
		&repo.Token{},
		&repo.Log{},
		&repo.Channel{},
		&repo.Option{},
		&repo.Tenant{},
	)
	if err != nil {
		t.Fatalf("failed to auto-migrate: %v", err)
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMySQL := common.UsingMySQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.UsingMySQL = false
	common.RedisEnabled = false

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			sqlDB.Close()
		}
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.UsingMySQL = prevMySQL
		common.RedisEnabled = prevRedis
	})

	return db
}

// seedTestUser creates a test user with the given quota and returns its ID.
func seedTestUser(t *testing.T, db *gorm.DB, quota int) int {
	t.Helper()
	user := repo.User{
		Username:    "testuser-" + common.GetRandomString(6),
		DisplayName: "Test User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "test@test.local",
		Quota:       quota,
		Group:       "default",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user.Id
}

// seedTestToken creates a test token for the given user and returns the key and token ID.
func seedTestToken(t *testing.T, db *gorm.DB, userId int, remainQuota int, unlimited bool) (key string, tokenId int) {
	t.Helper()
	key = common.GetRandomString(32)
	token := repo.Token{
		UserId:         userId,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    remainQuota,
		UnlimitedQuota: unlimited,
		Group:          "default",
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
	return key, token.Id
}

// createTestGinContext creates a minimal gin context for testing.
func createTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c
}
