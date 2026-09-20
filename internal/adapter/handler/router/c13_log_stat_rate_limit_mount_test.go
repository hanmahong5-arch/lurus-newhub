package router

// c13_log_stat_rate_limit_mount_test.go — cycle-13 W wiring oracle for L6's
// rate-limit decision.
//
// GET /api/v2/:tenant_slug/logs/stat, .../logs/stat/all and .../logs/export
// each run a GROUP BY (or a 50k-row streaming scan) over the logs table on
// every call, and until this cycle none of the three carried any ceiling of
// its own: the tenantLogs group mounts UserAuth + TenantSlugGuard and
// nothing else, so one authenticated console session could fire them as fast
// as the global v2 limiter allowed. The admin-side twin
// (adminRoute.GET("/logs/export")) and the analytics twin
// (tenantAnalytics.GET("/rankings")) already carried
// middleware.CriticalRateLimit(); these three now match them.
//
// Same convention as redemption_rate_limit_mount_test.go: drive the REAL
// mount function (SetApiV2Router, which cmd/server reaches through
// SetRouter) and prove the limiter by the 429 it actually returns —
// fingerprinted by X-RateLimit-Limit (the configured CriticalRateLimitNum,
// not some other limiter's cap) and X-RateLimit-Scope: ip — rather than by
// reading the route table.
//
// Mutation (verified red): drop middleware.CriticalRateLimit() from
// tenantLogs.GET("/export", ...) in api-v2-router.go — the /export subtest
// stops 429ing at the budget.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var logStatRateLimitDBCounter atomic.Int64

// logStatRateLimitBudget is the CriticalRateLimit cap this file configures
// before mounting the router. Small so the table below spends few requests;
// CriticalRateLimit() reads common.CriticalRateLimitNum at ROUTE
// REGISTRATION time, so it has to be set before SetApiV2Router runs.
const logStatRateLimitBudget = 3

// logStatRateLimitNginxHop mirrors production topology: there is no k8s
// Ingress, a host nginx vhost proxies to the NodePort, so pod-side RemoteAddr
// is always that nginx hop and the caller identity the limiter keys on
// travels in X-Forwarded-For.
const logStatRateLimitNginxHop = "10.42.0.1:54321"

// setupLogStatRateLimitEngine builds an engine whose /api/v2 table came from
// the real SetApiV2Router, with a session already carrying a tenant-admin
// identity in the "default" tenant (so UserAuth admits and TenantSlugGuard
// matches the slug), and an isolated sqlite DB carrying the tables the three
// handlers read.
func setupLogStatRateLimitEngine(t *testing.T) *gin.Engine {
	t.Helper()

	seq := logStatRateLimitDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:c13_logstat_rl_%d?mode=memory&cache=shared", seq)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Tenant{}, &repo.Log{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	now := time.Now()
	if err := db.Create(&repo.Tenant{
		Id: "default", Name: "default", Slug: "default",
		Status: repo.TenantStatusEnabled, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{
		Id: 8300000 + int(seq), Username: fmt.Sprintf("c13-logstat-%d", seq),
		DisplayName: "C13 LogStat Admin", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("c13-logstat-%d@local", seq),
		TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevCritEnable, prevCritNum, prevCritDur := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	prevGlobalV2 := common.GlobalV2RateLimitEnable
	prevGlobalAPI := common.GlobalApiRateLimitEnable

	repo.DB, repo.LOG_DB = db, db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = true, false, false
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = logStatRateLimitBudget
	common.CriticalRateLimitDuration = 180
	// The other two limiters that sit on the same chain are turned off, so a
	// 429 seen below can only have come from the "CT" bucket.
	common.GlobalV2RateLimitEnable = false
	common.GlobalApiRateLimitEnable = false

	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = prevCritEnable, prevCritNum, prevCritDur
		common.GlobalV2RateLimitEnable = prevGlobalV2
		common.GlobalApiRateLimitEnable = prevGlobalAPI
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	if err := ConfigureTrustedProxies(engine, []string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("ConfigureTrustedProxies: %v", err)
	}
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("c13-logstat-rl-secret"))))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", user.Username)
		s.Set("role", user.Role)
		s.Set("id", user.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiV2Router(engine)
	return engine
}

// TestTenantLogStatRoutes_CriticalRateLimitMounted is the wiring oracle: for
// each of the three routes, requests 1..budget reach past the limiter and
// request budget+1 is refused by the "CT" bucket.
func TestTenantLogStatRoutes_CriticalRateLimitMounted(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"logs_stat", "/api/v2/default/logs/stat"},
		{"logs_stat_all", "/api/v2/default/logs/stat/all"},
		{"logs_export", "/api/v2/default/logs/export"},
	}

	for i, tc := range cases {
		tc, i := tc, i
		t.Run(tc.name, func(t *testing.T) {
			engine := setupLogStatRateLimitEngine(t)
			// One caller address per subtest: CriticalRateLimit's "CT"
			// bucket is shared across every route that mounts it, and the
			// in-memory limiter lives in the middleware package for the
			// whole test binary. TEST-NET-1 (RFC 5737), a range no sibling
			// mount test in this package draws from.
			ip := fmt.Sprintf("192.0.2.%d", int(logStatRateLimitDBCounter.Load())%200+i*3+1)

			do := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				req.RemoteAddr = logStatRateLimitNginxHop
				req.Header.Set("X-Forwarded-For", ip)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, req)
				return w
			}

			for n := 1; n <= logStatRateLimitBudget; n++ {
				w := do()
				if w.Code == http.StatusTooManyRequests {
					t.Fatalf("request %d already got 429 — the budget is %d, so the fixture is broken and the trip below would prove nothing; body=%s",
						n, logStatRateLimitBudget, w.Body.String())
				}
				if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
					t.Fatalf("request %d: status=%d — the session/tenant fixture did not admit the caller, so the request never reached the per-route limiter; body=%s",
						n, w.Code, w.Body.String())
				}
			}

			w := do()
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: status=%d, want 429 — GET %s must mount middleware.CriticalRateLimit(); body=%s",
					logStatRateLimitBudget+1, w.Code, tc.path, w.Body.String())
			}
			if got := w.Header().Get("X-RateLimit-Limit"); got != fmt.Sprint(logStatRateLimitBudget) {
				t.Errorf("X-RateLimit-Limit=%q, want %q — a 429 carrying some other cap came from a different limiter, which would false-pass a bare status check",
					got, fmt.Sprint(logStatRateLimitBudget))
			}
			if got := w.Header().Get("X-RateLimit-Scope"); got != "ip" {
				t.Errorf("X-RateLimit-Scope=%q, want \"ip\"", got)
			}
			if got := w.Header().Get("Retry-After"); got == "" {
				t.Error("Retry-After header missing on the 429")
			}
		})
	}
}

// TestTenantLogStatRoutes_RateLimitIsPerIP pins that the ceiling is keyed on
// the caller, not on the route: one console session exhausting its budget
// must not lock every other tenant out of the same endpoint.
func TestTenantLogStatRoutes_RateLimitIsPerIP(t *testing.T) {
	engine := setupLogStatRateLimitEngine(t)
	const path = "/api/v2/default/logs/stat"
	seq := int(logStatRateLimitDBCounter.Load())
	ip1 := fmt.Sprintf("198.51.100.%d", seq%100+130)
	ip2 := fmt.Sprintf("198.51.100.%d", seq%100+131)

	fire := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = logStatRateLimitNginxHop
		req.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	for n := 1; n <= logStatRateLimitBudget; n++ {
		if w := fire(ip1); w.Code == http.StatusTooManyRequests {
			t.Fatalf("ip1 request %d already got 429 — fixture broken; body=%s", n, w.Body.String())
		}
	}
	if w := fire(ip1); w.Code != http.StatusTooManyRequests {
		t.Fatalf("ip1 request %d: status=%d, want 429 (this test's own setup step)", logStatRateLimitBudget+1, w.Code)
	}
	if w := fire(ip2); w.Code == http.StatusTooManyRequests {
		t.Fatalf("a second caller's FIRST request got 429 right after a different caller spent its budget — the CT bucket is not keyed per client; body=%s", w.Body.String())
	}
}
