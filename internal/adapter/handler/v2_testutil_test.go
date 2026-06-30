package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var v2TestDBCounter atomic.Int64

// V2TestContext holds test context for V2 API tests
type V2TestContext struct {
	Router     *gin.Engine
	DB         *gorm.DB
	Cleanup    func()
	TenantID   string
	UserID     int
	Roles      []string
	RootUser   *repo.User
	NormalUser *repo.User
	AdminUser  *repo.User
}

// SetupV2TestRouter initializes router with mock OIDCAuth for V2 API testing.
// Uses headers: X-Test-Tenant-ID, X-Test-User-ID, X-Test-Roles
func SetupV2TestRouter(t *testing.T) *V2TestContext {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:v2test%d?mode=memory&cache=shared", v2TestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite :memory: %v", err)
	}

	// Migrate all required tables
	tables := []interface{}{
		&repo.User{},
		&repo.Token{},
		&repo.Log{},
		&repo.Option{},
		&repo.Setup{},
		&repo.Tenant{},
		&repo.UserIdentityMapping{},
		&repo.TenantConfig{},
		&repo.Channel{},
		&repo.Ability{},
		&repo.Redemption{},
		&repo.QuotaData{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
			// SQLite uses global index names (unlike PostgreSQL which scopes to table).
			// Multiple models share the same composite index name (e.g., idx_tenant_user),
			// causing "already exists" errors that are safe to ignore during migration.
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("auto migrate failed for %T: %v", tbl, err)
		}
	}

	// Save previous state
	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol() // Initialize dialect-specific column quoting for SQLite
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.QuotaForNewUser = 0
	common.LogConsumeEnabled = false

	// Initialize OptionMap
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	// Create test tenant
	testTenantID := fmt.Sprintf("test-tenant-%d", v2TestDBCounter.Load())
	tenant := &repo.Tenant{
		Id:           testTenantID,
		Name:         "Test Tenant",
		Slug:         fmt.Sprintf("test-tenant-%d", v2TestDBCounter.Load()),
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_test_123",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	db.Create(tenant)

	// Seed root user
	rootUser := &repo.User{
		Username:    "v2testroot",
		DisplayName: "V2 Test Root",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
		Email:       "v2root@test.local",
		TenantId:    testTenantID,
		Quota:       100_000_000,
	}
	db.Create(rootUser)

	// Seed admin user (tenant admin, not platform root)
	adminUser := &repo.User{
		Username:    "v2testadmin",
		DisplayName: "V2 Test Admin",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		Email:       "v2admin@test.local",
		TenantId:    testTenantID,
		Quota:       50_000_000,
	}
	db.Create(adminUser)

	// Seed normal user
	normalUser := &repo.User{
		Username:    "v2testuser",
		DisplayName: "V2 Test User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "v2user@test.local",
		TenantId:    testTenantID,
		Quota:       1_000_000,
	}
	db.Create(normalUser)

	// Build router
	router := gin.New()

	// Mock OIDCAuth middleware that reads from test headers
	mockAuth := func(c *gin.Context) {
		tenantID := c.GetHeader("X-Test-Tenant-ID")
		if tenantID == "" {
			tenantID = testTenantID // Default to test tenant
		}

		userIDStr := c.GetHeader("X-Test-User-ID")
		userID := normalUser.Id // Default to normal user
		if userIDStr != "" {
			if id, err := strconv.Atoi(userIDStr); err == nil {
				userID = id
			}
		}

		rolesHeader := c.GetHeader("X-Test-Roles")
		var roles []string
		if rolesHeader != "" {
			roles = strings.Split(rolesHeader, ",")
		}

		tenantCtx := &middleware.TenantContext{
			TenantID:      tenantID,
			UserID:        userID,
			IDPSubject: "zitadel_test_user",
			Email:         "test@test.local",
			Username:      "testuser",
			Roles:         roles,
		}

		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", userID)
		c.Set("identity_account_id", int64(12345))

		// Also set v1 session context for admin controllers
		c.Set("id", userID)
		user, _ := repo.GetUserById(userID, false)
		if user != nil {
			c.Set("role", user.Role)
		}

		c.Next()
	}

	// Register V2 routes with mock auth
	v2 := router.Group("/api/v2/:tenant_slug")
	v2.Use(mockAuth)
	{
		// User routes
		v2.GET("/user/me", GetSelfV2)
		v2.PUT("/user/me", UpdateSelfV2)

		// Token routes
		v2.GET("/tokens", ListTokensV2)
		v2.POST("/tokens", CreateTokenV2)
		v2.POST("/tokens/batch-delete", DeleteTokensV2)
		v2.PUT("/tokens/:id", UpdateTokenV2)
		v2.DELETE("/tokens/:id", DeleteTokenV2)

		// Channel routes
		v2.GET("/channels", ListChannelsV2)
		v2.GET("/channels/:id", GetChannelV2)
		v2.POST("/channels", CreateChannelV2)
		v2.PUT("/channels/:id", UpdateChannelV2)
		v2.DELETE("/channels/:id", DeleteChannelV2)
		v2.POST("/channels/:id/test", TestChannelV2)
		v2.GET("/channels/:id/upstream-models", FetchUpstreamModelsV2)

		// Redemption routes
		v2.POST("/redeem", RedeemCodeV2)
		v2.GET("/redemptions", ListRedemptionsV2)
		v2.POST("/redemptions", CreateRedemptionV2)
		v2.DELETE("/redemptions/:id", DeleteRedemptionV2)

		// Billing routes
		v2.GET("/billing/topups", GetTopUpsV2)
		v2.POST("/billing/topup", TopUpV2)

		// Log routes
		v2.GET("/logs", GetLogsV2)
		v2.GET("/logs/all", GetAllLogsV2)
		v2.GET("/logs/cluster", GetLogClusterV2)
		v2.GET("/logs/stat", GetLogStatV2)
	}

	// Admin routes (platform-level, use v1 session auth)
	admin := router.Group("/api/v2/admin")
	admin.Use(mockAuth)
	{
		admin.GET("/mappings", ListUserMappingsV2)
		admin.GET("/mappings/:id", GetUserMappingV2)
		admin.DELETE("/mappings/:id", DeleteUserMappingV2)
		admin.GET("/stats", GetSystemStatsV2)
		admin.GET("/users", ListAdminUsersV2)
		admin.PUT("/users/:id", UpdateAdminUserV2)
		admin.DELETE("/users/:id", DeleteAdminUserV2)
		admin.GET("/options", ListAdminOptionsV2)
		admin.PUT("/options", UpdateAdminOptionV2)
	}

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	return &V2TestContext{
		Router:     router,
		DB:         db,
		Cleanup:    cleanup,
		TenantID:   testTenantID,
		UserID:     normalUser.Id,
		Roles:      []string{},
		RootUser:   rootUser,
		NormalUser: normalUser,
		AdminUser:  adminUser,
	}
}

// V2Request builds and executes an HTTP request against the V2 router.
func V2Request(router *gin.Engine, method, path string, body interface{}, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		data, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(data))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// V2RequestWithContext is a convenience method that includes tenant context headers
func V2RequestWithContext(ctx *V2TestContext, method, path string, body interface{}) *httptest.ResponseRecorder {
	headers := map[string]string{
		"X-Test-Tenant-ID": ctx.TenantID,
		"X-Test-User-ID":   strconv.Itoa(ctx.UserID),
	}
	if len(ctx.Roles) > 0 {
		headers["X-Test-Roles"] = strings.Join(ctx.Roles, ",")
	}
	return V2Request(ctx.Router, method, path, body, headers)
}

// V2RequestAsUser makes a request as a specific user
func V2RequestAsUser(ctx *V2TestContext, user *repo.User, method, path string, body interface{}, roles []string) *httptest.ResponseRecorder {
	headers := map[string]string{
		"X-Test-Tenant-ID": ctx.TenantID,
		"X-Test-User-ID":   strconv.Itoa(user.Id),
	}
	if len(roles) > 0 {
		headers["X-Test-Roles"] = strings.Join(roles, ",")
	}
	return V2Request(ctx.Router, method, path, body, headers)
}

// ParseV2Response unmarshals the response body into a generic map.
func ParseV2Response(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response body: %v, raw: %s", err, w.Body.String())
	}
	return result
}

// AssertV2Success checks that the response indicates success.
func AssertV2Success(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	resp := ParseV2Response(t, w)
	if success, ok := resp["success"].(bool); !ok || !success {
		t.Errorf("expected success=true, got %v, body: %s", resp["success"], w.Body.String())
	}
	return resp
}

// AssertV2Error checks that the response indicates an error with the expected status code.
func AssertV2Error(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int) map[string]interface{} {
	t.Helper()
	if w.Code != expectedStatus {
		t.Errorf("expected status %d, got %d, body: %s", expectedStatus, w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if success, ok := resp["success"].(bool); ok && success {
		t.Errorf("expected success=false, got true")
	}
	return resp
}

// AssertV2Status checks that the response has the expected status code.
func AssertV2Status(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int) {
	t.Helper()
	if w.Code != expectedStatus {
		t.Errorf("expected status %d, got %d, body: %s", expectedStatus, w.Code, w.Body.String())
	}
}

// SeedV2Token creates a test token for the given user in the test context
func SeedV2Token(t *testing.T, ctx *V2TestContext, userID int, name string) *repo.Token {
	t.Helper()
	token := &repo.Token{
		UserId:         userID,
		TenantId:       ctx.TenantID,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           name,
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := ctx.DB.Create(token).Error; err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
	return token
}

// SeedV2Channel creates a test channel in the test context
func SeedV2Channel(t *testing.T, ctx *V2TestContext, name string) *repo.Channel {
	t.Helper()
	channel := &repo.Channel{
		Name:        name,
		TenantId:    ctx.TenantID,
		Key:         "sk-test-" + common.GetRandomString(24),
		Status:      common.ChannelStatusEnabled,
		Type:        1, // OpenAI type
		Models:      "gpt-4,gpt-3.5-turbo",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}
	return channel
}

// SeedV2Redemption creates a test redemption code
func SeedV2Redemption(t *testing.T, ctx *V2TestContext, userID int) *repo.Redemption {
	t.Helper()
	redemption := &repo.Redemption{
		UserId:      userID,
		TenantId:    ctx.TenantID,
		Key:         common.GetRandomString(32),
		Name:        "Test Redemption",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(redemption); err != nil {
		t.Fatalf("failed to seed redemption: %v", err)
	}
	return redemption
}

// SeedV2Log creates a test log entry
func SeedV2Log(t *testing.T, ctx *V2TestContext, userID int, logType int) *repo.Log {
	t.Helper()
	log := &repo.Log{
		UserId:    userID,
		TenantId:  ctx.TenantID,
		Type:      logType,
		Content:   "Test log content",
		CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(log).Error; err != nil {
		t.Fatalf("failed to seed log: %v", err)
	}
	return log
}
