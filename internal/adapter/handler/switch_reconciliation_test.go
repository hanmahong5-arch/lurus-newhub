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
// Switch reconciliation handler tests
//
// Like the heartbeat tests, we wire a minimal no-middleware router so the
// inline raw-token auth path is exercised directly. We additionally migrate +
// seed the Log table so the consume-log aggregation has data.
// ============================================================================

var reconcileDBCounter atomic.Int64

type reconcileTestCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	tenant  *repo.Tenant
	user    *repo.User
	cleanup func()
}

func setupReconcileTest(t *testing.T) *reconcileTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:recontest%d?mode=memory&cache=shared", reconcileDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Log{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
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

	tenantSlug := fmt.Sprintf("recontenant%d", reconcileDBCounter.Load())
	tenant := &repo.Tenant{
		Id:           tenantSlug,
		Name:         "Recon Tenant",
		Slug:         tenantSlug,
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_recon_" + tenantSlug,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	user := &repo.User{
		Username:    fmt.Sprintf("reconuser%d", reconcileDBCounter.Load()),
		DisplayName: "Recon User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "reconuser@test.local",
		TenantId:    tenantSlug,
		Quota:       1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	router := gin.New()
	router.POST("/api/v2/switch/reconciliation", SwitchReconciliation)

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}

	return &reconcileTestCtx{router: router, db: db, tenant: tenant, user: user, cleanup: cleanup}
}

func (rc *reconcileTestCtx) seedToken(t *testing.T) *repo.Token {
	t.Helper()
	tok := repo.Token{
		UserId:         rc.user.Id,
		TenantId:       rc.tenant.Id,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "recon-test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    1_000_000,
		UnlimitedQuota: false,
		Group:          "default",
	}
	if err := rc.db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return &tok
}

// seedConsumeLog inserts one LogTypeConsume row for the test user.
func (rc *reconcileTestCtx) seedConsumeLog(t *testing.T, model string, quota, prompt, completion int, createdAt int64) {
	t.Helper()
	l := &repo.Log{
		UserId:           rc.user.Id,
		TenantId:         rc.tenant.Id,
		Type:             repo.LogTypeConsume,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := rc.db.Create(l).Error; err != nil {
		t.Fatalf("seed consume log: %v", err)
	}
}

func (rc *reconcileTestCtx) post(authToken string, body any) *httptest.ResponseRecorder {
	var raw []byte
	switch v := body.(type) {
	case nil:
		raw = []byte(`{}`)
	case string:
		raw = []byte(v)
	default:
		raw, _ = json.Marshal(v)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/switch/reconciliation", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}
	w := httptest.NewRecorder()
	rc.router.ServeHTTP(w, req)
	return w
}

type reconcileResp struct {
	Success bool `json:"success"`
	Data    struct {
		TotalQuota            int64 `json:"total_quota"`
		TotalPromptTokens     int64 `json:"total_prompt_tokens"`
		TotalCompletionTokens int64 `json:"total_completion_tokens"`
		RequestCount          int64 `json:"request_count"`
		Models                []struct {
			ModelName        string `json:"model_name"`
			Quota            int64  `json:"quota"`
			PromptTokens     int64  `json:"prompt_tokens"`
			CompletionTokens int64  `json:"completion_tokens"`
			RequestCount     int64  `json:"request_count"`
		} `json:"models"`
	} `json:"data"`
}

func decodeReconcile(t *testing.T, w *httptest.ResponseRecorder) reconcileResp {
	t.Helper()
	var r reconcileResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	return r
}

func TestSwitchReconciliation_AggregatesWindow(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)

	// Two in-window consume logs (different models) + one outside the window.
	rc.seedConsumeLog(t, "claude-sonnet-4-6", 1500, 1000, 500, 1500)
	rc.seedConsumeLog(t, "gpt-4o", 800, 600, 200, 1600)
	rc.seedConsumeLog(t, "claude-sonnet-4-6", 9999, 9999, 9999, 5000) // out of window

	w := rc.post(tok.Key, map[string]int64{"start_time": 1000, "end_time": 2000})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := decodeReconcile(t, w)
	if !resp.Success {
		t.Fatalf("success false: %s", w.Body.String())
	}
	if resp.Data.RequestCount != 2 {
		t.Errorf("request_count = %d, want 2 (out-of-window row excluded)", resp.Data.RequestCount)
	}
	if resp.Data.TotalQuota != 2300 {
		t.Errorf("total_quota = %d, want 2300", resp.Data.TotalQuota)
	}
	if resp.Data.TotalPromptTokens != 1600 {
		t.Errorf("total_prompt_tokens = %d, want 1600", resp.Data.TotalPromptTokens)
	}
	if resp.Data.TotalCompletionTokens != 700 {
		t.Errorf("total_completion_tokens = %d, want 700", resp.Data.TotalCompletionTokens)
	}
	if len(resp.Data.Models) != 2 {
		t.Errorf("models breakdown = %d entries, want 2", len(resp.Data.Models))
	}
}

func TestSwitchReconciliation_ExcludesNonConsumeAndOtherUsers(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)

	// A consume log for our user (counts).
	rc.seedConsumeLog(t, "claude-sonnet-4-6", 100, 50, 50, 1500)
	// A non-consume (manage) log — must NOT be aggregated.
	if err := rc.db.Create(&repo.Log{
		UserId: rc.user.Id, TenantId: rc.tenant.Id, Type: repo.LogTypeManage,
		ModelName: "claude-sonnet-4-6", Quota: 7777, CreatedAt: 1500,
	}).Error; err != nil {
		t.Fatalf("seed manage log: %v", err)
	}
	// A consume log for a DIFFERENT user — must NOT leak in.
	if err := rc.db.Create(&repo.Log{
		UserId: rc.user.Id + 999, TenantId: rc.tenant.Id, Type: repo.LogTypeConsume,
		ModelName: "gpt-4o", Quota: 5555, PromptTokens: 5555, CreatedAt: 1500,
	}).Error; err != nil {
		t.Fatalf("seed other-user log: %v", err)
	}

	w := rc.post(tok.Key, map[string]int64{"start_time": 1000, "end_time": 2000})
	resp := decodeReconcile(t, w)
	if resp.Data.TotalQuota != 100 || resp.Data.RequestCount != 1 {
		t.Errorf("aggregation leaked rows: quota=%d count=%d, want 100/1 (body=%s)",
			resp.Data.TotalQuota, resp.Data.RequestCount, w.Body.String())
	}
}

func TestSwitchReconciliation_AuthBranches(t *testing.T) {
	cases := []struct {
		name     string
		token    string
		wantHTTP int
	}{
		{"missing auth header", "", http.StatusUnauthorized},
		{"unknown token", "this-token-does-not-exist-xxxxxx", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := setupReconcileTest(t)
			defer rc.cleanup()
			w := rc.post(tc.token, map[string]int64{"start_time": 0, "end_time": 0})
			if w.Code != tc.wantHTTP {
				t.Fatalf("HTTP = %d, want %d, body=%s", w.Code, tc.wantHTTP, w.Body.String())
			}
		})
	}
}

func TestSwitchReconciliation_NoWindowAggregatesAll(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)

	rc.seedConsumeLog(t, "claude-sonnet-4-6", 10, 5, 5, 100)
	rc.seedConsumeLog(t, "claude-sonnet-4-6", 20, 10, 10, 9_000_000)

	// Empty window (both 0) → aggregate the user's entire consume history.
	w := rc.post(tok.Key, map[string]int64{})
	resp := decodeReconcile(t, w)
	if resp.Data.RequestCount != 2 || resp.Data.TotalQuota != 30 {
		t.Errorf("no-window aggregate: count=%d quota=%d, want 2/30", resp.Data.RequestCount, resp.Data.TotalQuota)
	}
}

func TestSwitchReconciliation_BearerPrefixAccepted(t *testing.T) {
	rc := setupReconcileTest(t)
	defer rc.cleanup()
	tok := rc.seedToken(t)
	rc.seedConsumeLog(t, "gpt-4o", 42, 20, 22, 1500)

	w := rc.post("Bearer "+tok.Key, map[string]int64{"start_time": 1000, "end_time": 2000})
	if w.Code != http.StatusOK {
		t.Fatalf("Bearer-prefixed token: HTTP %d, body=%s", w.Code, w.Body.String())
	}
	if resp := decodeReconcile(t, w); resp.Data.TotalQuota != 42 {
		t.Errorf("total_quota = %d, want 42", resp.Data.TotalQuota)
	}
}
