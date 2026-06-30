package handler

import (
	"bytes"
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

// ============================================================================
// User Heartbeat handler tests
//
// We deliberately do NOT reuse SetupV2TestRouter — that helper injects a mock
// tenant_context via headers, which would short-circuit the raw-token path
// the heartbeat handler is supposed to exercise. Instead we wire a minimal
// gin router with the heartbeat routes attached directly.
// ============================================================================

var heartbeatDBCounter atomic.Int64

type heartbeatTestCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	tenant  *repo.Tenant
	user    *repo.User
	cleanup func()
}

// setupHeartbeatTest spins up an in-memory SQLite, migrates the four tables
// the handler touches, and wires both routes (multi-tenant + single-tenant
// fallback) onto a no-middleware gin engine.
func setupHeartbeatTest(t *testing.T) *heartbeatTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:hbtest%d?mode=memory&cache=shared", heartbeatDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Token{},
		&repo.Tenant{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("migrate %T: %v", tbl, err)
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

	tenantSlug := fmt.Sprintf("hbtenant%d", heartbeatDBCounter.Load())
	tenant := &repo.Tenant{
		Id:           tenantSlug,
		Name:         "HB Tenant",
		Slug:         tenantSlug,
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_hb_" + tenantSlug,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	user := &repo.User{
		Username:    fmt.Sprintf("hbuser%d", heartbeatDBCounter.Load()),
		DisplayName: "HB User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "hbuser@test.local",
		TenantId:    tenantSlug,
		Quota:       1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	router := gin.New()
	apiV2 := router.Group("/api/v2")
	apiV2.POST("/:tenant_slug/user/heartbeat", UserHeartbeat)
	apiV2.POST("/switch/heartbeat", UserHeartbeat)

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

	return &heartbeatTestCtx{
		router:  router,
		db:      db,
		tenant:  tenant,
		user:    user,
		cleanup: cleanup,
	}
}

// seedToken inserts a token row with the supplied overrides. Fields not
// specified get sensible enabled-active defaults.
func (h *heartbeatTestCtx) seedToken(t *testing.T, override repo.Token) *repo.Token {
	t.Helper()
	tok := repo.Token{
		UserId:         h.user.Id,
		TenantId:       h.tenant.Id,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "hb-test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    1_000_000,
		UnlimitedQuota: false,
		Group:          "default",
	}
	// Apply overrides field-by-field for the ones tests actually flex.
	if override.Status != 0 {
		tok.Status = override.Status
	}
	if override.ExpiredTime != 0 {
		tok.ExpiredTime = override.ExpiredTime
	}
	if override.RemainQuota != 0 {
		tok.RemainQuota = override.RemainQuota
	}
	if override.UnlimitedQuota {
		tok.UnlimitedQuota = override.UnlimitedQuota
	}
	if override.UserId != 0 {
		tok.UserId = override.UserId
	}
	if override.TenantId != "" {
		tok.TenantId = override.TenantId
	}
	if override.Key != "" {
		tok.Key = override.Key
	}
	if err := h.db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return &tok
}

// doHeartbeat fires a POST to the requested path with the supplied auth
// header and body. Pass authToken="" to omit the Authorization header.
func (h *heartbeatTestCtx) doHeartbeat(path, authToken string, body any) *httptest.ResponseRecorder {
	var reqBody []byte
	switch v := body.(type) {
	case nil:
		reqBody = []byte(`{"fingerprint":"fp-test","app_version":"1.0.0"}`)
	case string:
		reqBody = []byte(v)
	default:
		reqBody, _ = json.Marshal(v)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

// decodeHeartbeat unmarshals the envelope + nested data into typed fields
// the tests can assert on directly.
type heartbeatResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Status    string `json:"status"`
		ExpiresAt int64  `json:"expires_at"`
		Quota     int    `json:"quota"`
	} `json:"data"`
}

func decodeHeartbeat(t *testing.T, w *httptest.ResponseRecorder) heartbeatResp {
	t.Helper()
	var resp heartbeatResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	return resp
}

// ----------------------------------------------------------------------------
// Table-driven test for the canonical status branches.
// ----------------------------------------------------------------------------

func TestUserHeartbeat_StatusBranches(t *testing.T) {
	type seed func(ctx *heartbeatTestCtx, t *testing.T) (authHeader, path string)

	cases := []struct {
		name         string
		seed         seed
		wantHTTP     int
		wantStatus   string
		wantSuccess  bool
		wantQuotaSet bool
	}{
		{
			name: "active token, multi-tenant path",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{})
				return tok.Key, "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:     http.StatusOK,
			wantStatus:   "active",
			wantSuccess:  true,
			wantQuotaSet: true,
		},
		{
			name: "active token, single-tenant fallback path",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{})
				return tok.Key, "/api/v2/switch/heartbeat"
			},
			wantHTTP:    http.StatusOK,
			wantStatus:  "active",
			wantSuccess: true,
		},
		{
			name: "expired by status",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{Status: common.TokenStatusExpired})
				return tok.Key, "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusOK,
			wantStatus:  "expired",
			wantSuccess: true,
		},
		{
			name: "expired by clock",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{ExpiredTime: common.GetTimestamp() - 3600})
				return tok.Key, "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusOK,
			wantStatus:  "expired",
			wantSuccess: true,
		},
		{
			name: "exhausted token",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{Status: common.TokenStatusExhausted})
				return tok.Key, "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusOK,
			wantStatus:  "expired",
			wantSuccess: true,
		},
		{
			name: "disabled token",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				tok := ctx.seedToken(t, repo.Token{Status: common.TokenStatusDisabled})
				return tok.Key, "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusUnauthorized,
			wantStatus:  "revoked",
			wantSuccess: false,
		},
		{
			name: "unknown token",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				return "this-token-does-not-exist-32chars", "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusUnauthorized,
			wantStatus:  "revoked",
			wantSuccess: false,
		},
		{
			name: "missing auth header",
			seed: func(ctx *heartbeatTestCtx, t *testing.T) (string, string) {
				return "", "/api/v2/" + ctx.tenant.Slug + "/user/heartbeat"
			},
			wantHTTP:    http.StatusUnauthorized,
			wantStatus:  "revoked",
			wantSuccess: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := setupHeartbeatTest(t)
			defer ctx.cleanup()

			auth, path := tc.seed(ctx, t)
			w := ctx.doHeartbeat(path, auth, nil)
			if w.Code != tc.wantHTTP {
				t.Fatalf("HTTP code: want %d, got %d, body=%s", tc.wantHTTP, w.Code, w.Body.String())
			}
			resp := decodeHeartbeat(t, w)
			if resp.Success != tc.wantSuccess {
				t.Errorf("success: want %v, got %v", tc.wantSuccess, resp.Success)
			}
			if resp.Data.Status != tc.wantStatus {
				t.Errorf("data.status: want %q, got %q", tc.wantStatus, resp.Data.Status)
			}
			if tc.wantQuotaSet && resp.Data.Quota == 0 {
				t.Errorf("expected non-zero quota in response for active token, got 0")
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Independent tests that need more setup wiring than the table covers.
// ----------------------------------------------------------------------------

func TestUserHeartbeat_DisabledUser(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	// Flip the user to disabled, leaving the token row enabled.
	if err := ctx.db.Model(ctx.user).Update("status", common.UserStatusDisabled).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}
	tok := ctx.seedToken(t, repo.Token{})

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Data.Status != "revoked" {
		t.Errorf("status: want revoked, got %q", resp.Data.Status)
	}
}

func TestUserHeartbeat_TenantMismatch(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	// Create a second tenant and seed a token bound to *that* tenant.
	otherTenant := &repo.Tenant{
		Id:           "hb-other",
		Name:         "Other",
		Slug:         "hb-other",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_hb_other",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ctx.db.Create(otherTenant).Error; err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	tok := ctx.seedToken(t, repo.Token{TenantId: otherTenant.Id})

	// Hit /api/v2/<original_tenant_slug>/user/heartbeat with that token.
	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d, body=%s", w.Code, w.Body.String())
	}

	// And confirm the single-tenant fallback path bypasses the check.
	w = ctx.doHeartbeat("/api/v2/switch/heartbeat", tok.Key, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("fallback path: want 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestUserHeartbeat_MalformedBody(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()
	tok := ctx.seedToken(t, repo.Token{})

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, "not-json-{{{{")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestUserHeartbeat_BearerPrefixStripped(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()
	tok := ctx.seedToken(t, repo.Token{})

	// Switch sends raw, but accept Bearer for forward compatibility.
	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", "Bearer "+tok.Key, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if resp := decodeHeartbeat(t, w); resp.Data.Status != "active" {
		t.Errorf("status: want active, got %q", resp.Data.Status)
	}
}
