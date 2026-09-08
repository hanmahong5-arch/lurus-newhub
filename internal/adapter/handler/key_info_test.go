package handler

// key_info_test.go — oracle for GET /v1/key (L2-REQUEST-IDENTITY /
// C16-KEY-INTROSPECTION): a caller holding only a relay key can read its own
// limits, rate limits and tenant pool state in one call.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var keyInfoDBCounter atomic.Int64

func setupKeyInfoDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:keyinfo%d?mode=memory&cache=shared", keyInfoDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.Token{}, &repo.Tenant{}, &repo.TenantCreditPool{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB := repo.DB
	prevRedis := common.RedisEnabled
	repo.DB = db
	common.RedisEnabled = false
	repo.InitCol()

	cleanup := func() {
		repo.DB = prevDB
		common.RedisEnabled = prevRedis
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	return db, cleanup
}

func keyInfoRequest(tokenID int) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/key", func(c *gin.Context) {
		c.Set("token_id", tokenID)
		GetKeyInfo(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/key", nil))
	return w
}

// TestGetKeyInfo_MapsRateLimitsAndUsage pins the field mapping: token-level
// rpm/remaining/expiry and the tenant-level rpm both land on the response.
func TestGetKeyInfo_MapsRateLimitsAndUsage(t *testing.T) {
	_, cleanup := setupKeyInfoDB(t)
	defer cleanup()

	tenant := &repo.Tenant{Id: "acme", Slug: "acme", Name: "Acme", RateLimitRPM: 7, RateLimitTPM: 700}
	if err := repo.DB.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	tok := &repo.Token{
		TenantId: "acme", Name: "prod-key", Group: "default",
		RemainQuota: 1234, UsedQuota: 500, RateLimitRPM: 5, RateLimitTPM: 5000,
		ExpiredTime: 4102444800, // fixed far-future unix seconds — expired=T (has a real expiry)
	}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := keyInfoRequest(tok.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		`"label":"prod-key"`,
		`"rpm":5`,
		`"tpm":5000`,
		`"tenant_rpm":7`,
		`"tenant_tpm":700`,
		`"limit_remaining":1234`,
		// limit is the total cap (remaining + already used = 1234+500),
		// not the naked remaining number — OpenRouter's vocabulary.
		`"limit":1734`,
		`"usage":500`,
		`"expires_at":4102444800`,
		`"pool":null`,
		`"scopes":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want to contain %s", body, want)
		}
	}
}

// TestGetKeyInfo_UnlimitedQuotaHasNullLimit: an unlimited token's "limit"
// must be null, not its (meaningless) remain_quota number.
func TestGetKeyInfo_UnlimitedQuotaHasNullLimit(t *testing.T) {
	_, cleanup := setupKeyInfoDB(t)
	defer cleanup()

	tok := &repo.Token{TenantId: "default", Name: "unlimited-key", UnlimitedQuota: true, ExpiredTime: -1}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := keyInfoRequest(tok.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"limit":null`) {
		t.Errorf("body = %s, want limit:null for an unlimited-quota token", body)
	}
	if !strings.Contains(body, `"limit_remaining":null`) {
		t.Errorf("body = %s, want limit_remaining:null for an unlimited-quota token", body)
	}
	if !strings.Contains(body, `"expires_at":null`) {
		t.Errorf("body = %s, want expires_at:null for ExpiredTime=-1", body)
	}
}

// TestGetKeyInfo_EmptyScopesSerializesAsEmptyArrayNotNull locks findings
// item L2-REQUEST-IDENTITY#3: a token with no scope allowlist must emit
// `"scopes":[]`, matching model_limits' `[]` shape — Token.GetScopes()
// returns nil (a meaningful "no restriction" value for HasScope), and that
// nil must not leak into the JSON response as `null`.
func TestGetKeyInfo_EmptyScopesSerializesAsEmptyArrayNotNull(t *testing.T) {
	_, cleanup := setupKeyInfoDB(t)
	defer cleanup()

	tok := &repo.Token{TenantId: "default", Name: "no-scopes-key", ExpiredTime: -1}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := keyInfoRequest(tok.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `"scopes":null`) {
		t.Errorf("body = %s, scopes serialized as null, want []", body)
	}
	if !strings.Contains(body, `"scopes":[]`) {
		t.Errorf("body = %s, want \"scopes\":[]", body)
	}
	if !strings.Contains(body, `"model_limits":[]`) {
		t.Errorf("body = %s, want \"model_limits\":[]", body)
	}
}

// TestGetKeyInfo_NoPoolRowIsNull: repo.ErrPoolNotFound must surface as a
// null pool object, not an error response — matches the relay gate's own
// "no pool row == unlimited/not gated" convention.
func TestGetKeyInfo_NoPoolRowIsNull(t *testing.T) {
	_, cleanup := setupKeyInfoDB(t)
	defer cleanup()

	tok := &repo.Token{TenantId: "no-pool-tenant", Name: "no-pool-key", ExpiredTime: -1}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := keyInfoRequest(tok.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"pool":null`) {
		t.Errorf("body = %s, want pool:null when no credit-pool row exists", w.Body.String())
	}
}

// TestGetKeyInfo_PoolPresent pins the balance/max_balance/health mapping
// when a pool row does exist.
func TestGetKeyInfo_PoolPresent(t *testing.T) {
	_, cleanup := setupKeyInfoDB(t)
	defer cleanup()

	pool := &repo.TenantCreditPool{TenantID: "pooled-tenant", CurrentBalance: 900, MaxBalance: 1000, CreatedByUserID: 1}
	if err := repo.DB.Create(pool).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	tok := &repo.Token{TenantId: "pooled-tenant", Name: "pooled-key", ExpiredTime: -1}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := keyInfoRequest(tok.Id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"balance":900`, `"max_balance":1000`, `"health":"green"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want to contain %s", body, want)
		}
	}
}
