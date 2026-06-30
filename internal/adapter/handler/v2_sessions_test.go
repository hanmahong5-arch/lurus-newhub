package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var sessionsTestDBCounter atomic.Int64

type sessionsCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	userID   int
	tenantID string
	slug     string
	cleanup  func()
}

func setupSessionsRouter(t *testing.T) *sessionsCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:sessions%d?mode=memory&cache=shared", sessionsTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Log{},
	} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	// Seed tenant. Id and ZitadelOrgID must both be non-empty and unique.
	n := sessionsTestDBCounter.Load()
	tenantID := fmt.Sprintf("tid-%04d", n)
	tenant := &repo.Tenant{
		Id:           tenantID,
		IDPOrgID: fmt.Sprintf("zorg-%04d", n),
		Slug:         fmt.Sprintf("test-tenant-%04d", n),
		Name:         "Test Tenant",
		Status:       repo.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Seed user.
	user := &repo.User{
		Username:    "sessions-tester",
		DisplayName: "Sessions Tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "sessions@test.local",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ctx := &sessionsCtx{
		db:       db,
		userID:   user.Id,
		tenantID: tenantID,
		slug:     tenant.Slug,
	}

	router := gin.New()
	// Mock UserAuth: set c.Set("id", user.Id) per request.
	mockAuth := func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Next()
	}
	router.GET("/api/v2/:tenant_slug/sessions", mockAuth, ListSessionsV2)
	ctx.router = router

	ctx.cleanup = func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(ctx.cleanup)
	return ctx
}

func getSessions(ctx *sessionsCtx) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+ctx.slug+"/sessions", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parseSessions(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// 1. Happy path: 2 tokens (1 enabled, 1 disabled) + 5 logs within 30 days.
// Expect 1 session returned, active_tokens=1, request_count=5.
func TestV2Sessions_HappyPath(t *testing.T) {
	ctx := setupSessionsRouter(t)

	// Token 1: enabled (status=1)
	ctx.db.Create(&repo.Token{
		UserId:   ctx.userID,
		TenantId: ctx.tenantID,
		Key:      "sk-enabled00000000000000000000000000000000000000",
		Status:   1,
		Name:     "enabled",
	})
	// Token 2: disabled (status=2)
	ctx.db.Create(&repo.Token{
		UserId:   ctx.userID,
		TenantId: ctx.tenantID,
		Key:      "sk-disabled0000000000000000000000000000000000000",
		Status:   2,
		Name:     "disabled",
	})

	// 5 log entries within the last 30 days.
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		ctx.db.Create(&repo.Log{
			UserId:    ctx.userID,
			TenantId:  ctx.tenantID,
			CreatedAt: now - int64(i*3600), // spread across last 5 hours
			Type:      repo.LogTypeConsume,
		})
	}

	w := getSessions(ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parseSessions(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true", resp["success"])
	}

	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1", data["total"])
	}

	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}

	item := items[0].(map[string]interface{})
	if item["current"] != true {
		t.Errorf("current = %v, want true", item["current"])
	}
	if item["active_tokens"].(float64) != 1 {
		t.Errorf("active_tokens = %v, want 1 (disabled token excluded)", item["active_tokens"])
	}
	if item["request_count"].(float64) != 5 {
		t.Errorf("request_count = %v, want 5", item["request_count"])
	}
}

// 2. Tenant isolation: tenantA's token/log must not appear in tenantB's response.
func TestV2Sessions_TenantIsolation(t *testing.T) {
	ctx := setupSessionsRouter(t)

	// Seed a second tenant — needs a unique Id and ZitadelOrgID.
	tenantB := &repo.Tenant{
		Id:           "tid-b-isolation",
		IDPOrgID: "zorg-b-isolation",
		Slug:         "tenant-b-isolation",
		Name:         "Tenant B",
		Status:       repo.TenantStatusEnabled,
	}
	ctx.db.Create(tenantB)

	// Token belonging to tenantB.
	ctx.db.Create(&repo.Token{
		UserId:   ctx.userID,
		TenantId: tenantB.Id,
		Key:      "sk-tenantb000000000000000000000000000000000000000",
		Status:   1,
		Name:     "tenantB-token",
	})

	// Log belonging to tenantB.
	ctx.db.Create(&repo.Log{
		UserId:    ctx.userID,
		TenantId:  tenantB.Id,
		CreatedAt: time.Now().Unix(),
		Type:      repo.LogTypeConsume,
	})

	// Query tenantA (ctx.slug = "test-tenant", ctx.tenantID = tenantA).
	w := getSessions(ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	resp := parseSessions(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	item := items[0].(map[string]interface{})

	if item["active_tokens"].(float64) != 0 {
		t.Errorf("active_tokens = %v, want 0 — tenantB's token must not cross into tenantA", item["active_tokens"])
	}
	if item["request_count"].(float64) != 0 {
		t.Errorf("request_count = %v, want 0 — tenantB's log must not cross into tenantA", item["request_count"])
	}
}

// 3. Whitelist enforced: response body must not contain sensitive field names.
func TestV2Sessions_WhitelistEnforced(t *testing.T) {
	ctx := setupSessionsRouter(t)

	w := getSessions(ctx)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	forbidden := []string{"access_token", "cookie", "ip"}
	for _, f := range forbidden {
		if strings.Contains(body, f) {
			t.Errorf("response body contains forbidden field %q — whitelist violated. body: %s", f, body)
		}
	}
}
