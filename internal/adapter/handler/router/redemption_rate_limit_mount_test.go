package router

// redemption_rate_limit_mount_test.go — wiring lock for gap topup-payments-
// subscriptions-35 (cycle 8 L1): middleware.RedemptionRateLimit() (5/60s/IP,
// "RD" bucket, internal/adapter/middleware/rate-limit.go) was defined but had
// zero call sites outside its own file — none of the four routes that
// consume a redemption/activation code (two of them not requiring any
// session at all) had a redemption-specific ceiling. POST /api/user/topup
// is registered on the group that mounts GlobalAPIRateLimit (api-router.go:15,
// 180 req/180s/IP by default), which is far too loose for code guessing; the
// three routes under apiV2 do not carry that limiter at all.
//
// Same convention as r6a_rate_limit_mount_test.go / r6d_switch_redeem_mount_test.go:
// drives the REAL router entrypoints (SetApiRouter / SetApiV2Router — the
// same functions cmd/server/main.go reaches via SetRouter), not a hand-copy
// of the route table, so a mount deleted from api-router.go or
// api-v2-router.go fails here, not just in a middleware-package unit test
// that constructs its own engine.
//
// Client identity: production sits behind host nginx on a single NodePort
// (CLAUDE.md K8s deployment facts), so pod-side RemoteAddr is always that
// nginx hop's private address, never the caller's own IP — ClientIP() only
// resolves the real caller through X-Forwarded-For once
// router.ConfigureTrustedProxies has trusted that hop (see
// trusted_proxies_test.go). Every engine built below reproduces that:
// ConfigureTrustedProxies trusts a private CIDR, every request's
// RemoteAddr is fixed to an address inside it, and the per-case client
// identity travels only through X-Forwarded-For using TEST-NET addresses
// (203.0.113.0/24, 198.51.100.0/24 — RFC 5737).

import (
	"bytes"
	"encoding/json"
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

var redemptionRateLimitDBCounter atomic.Int64

// redemptionRateLimitFixture is the shared isolated-DB fixture for every
// case in the table below: one tenant ("default", matching the TenantId a
// freshly-created repo.User gets by GORM column default), one enabled user
// in it, and one enabled relay token owned by that user (for the raw-token-
// authenticated switch/user/topup case).
type redemptionRateLimitFixture struct {
	DB      *gorm.DB
	User    *repo.User
	Tenant  *repo.Tenant
	TokenKy string
}

func setupRedemptionRateLimitFixture(t *testing.T) (*redemptionRateLimitFixture, func()) {
	t.Helper()

	seq := redemptionRateLimitDBCounter.Add(1)
	dbName := fmt.Sprintf("file:redemption_rl_mount_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Redemption{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	now := time.Now()
	tenant := &repo.Tenant{
		Id: "default", Name: "default", Slug: "default",
		Status: repo.TenantStatusEnabled, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	user := &repo.User{
		Id: 8200000 + int(seq), Username: fmt.Sprintf("rd-rl-user-%d", seq),
		DisplayName: "RD RateLimit User", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("rd-rl-%d@local", seq),
		TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tokenKey := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: tokenKey, Status: common.TokenStatusEnabled,
		Name: "rd-rl-token", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	cleanup := func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return &redemptionRateLimitFixture{DB: db, User: user, Tenant: tenant, TokenKy: tokenKey}, cleanup
}

// redemptionRateLimitTrustedCIDR is the trusted-proxy entry every engine
// below configures — same RFC1918 shape as trustedProxiesTestDefaults()
// (trusted_proxies_test.go), narrowed to the one range
// redemptionRateLimitNginxHop lives in.
const redemptionRateLimitTrustedCIDR = "10.0.0.0/8"

// redemptionRateLimitNginxHop is the constant private RemoteAddr every
// request in this file arrives from, mirroring production topology: pod-
// side RemoteAddr is always the host nginx reverse-proxy hop, never the
// caller's own IP (CLAUDE.md K8s deployment facts). The per-case client
// identity travels only through X-Forwarded-For — see newRedemptionRequest.
const redemptionRateLimitNginxHop = "10.42.0.1:54321"

// configureRedemptionTrustedProxies trusts redemptionRateLimitTrustedCIDR on
// engine so c.ClientIP() resolves the caller from X-Forwarded-For instead of
// RemoteAddr, the same trust boundary router.ConfigureTrustedProxies applies
// in production (cmd/server/main.go).
func configureRedemptionTrustedProxies(engine *gin.Engine) {
	if err := ConfigureTrustedProxies(engine, []string{redemptionRateLimitTrustedCIDR}); err != nil {
		panic("ConfigureTrustedProxies: " + err.Error())
	}
}

// sessionEngine wraps mountFn (SetApiRouter or SetApiV2Router) with a real
// gin-contrib/sessions store and a middleware that seeds the session with
// the fixture user identity ahead of the real router — same convention as
// api-router-totp_test.go serve() closure. UserAuth() re-reads the session
// on every request, so no login round-trip / cookie exchange is needed: the
// values are already present in the request-scoped session store by the
// time authHelper runs.
func sessionEngine(fx *redemptionRateLimitFixture, mountFn func(*gin.Engine)) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	configureRedemptionTrustedProxies(engine)
	store := cookie.NewStore([]byte("redemption-rl-test-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", fx.User.Username)
		s.Set("role", fx.User.Role)
		s.Set("id", fx.User.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	mountFn(engine)
	return engine
}

// anonEngine wraps mountFn with nothing but gin.Recovery and the trusted-
// proxy boundary — for the two routes that carry their own inline
// authentication (switch/redeem is anonymous by design; switch/user/topup
// authenticates off the raw Authorization header inside the handler).
func anonEngine(mountFn func(*gin.Engine)) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	configureRedemptionTrustedProxies(engine)
	mountFn(engine)
	return engine
}

// bogusCode32 is a 32-character string that never matches a seeded
// repo.Redemption row — exactly 32 characters so it passes RedeemCodeV2's
// exact-length format gate (v2_redemption.go `len(redeemCode) != 32`) and
// reaches repo.Redeem on an unknown code, never colliding with a real code
// because no redemption row is ever seeded in this fixture.
// TestBogusCode32IsExactlyRedeemCodeV2Length guards this length.
const bogusCode32 = "ffffffffffffffffffffffffffffffff"

func TestBogusCode32IsExactlyRedeemCodeV2Length(t *testing.T) {
	if len(bogusCode32) != 32 {
		t.Fatalf("bogusCode32 is %d characters, want exactly 32 — RedeemCodeV2's exact-length format gate (v2_redemption.go) rejects any other length before repo.Redeem ever runs, so the topup_v1 and v2_redeem table rows would assert the format-gate 400 instead of the unknown-code 400", len(bogusCode32))
	}
}

// redemptionRouteCase describes one of the four code-consuming routes: how
// to build its router, how to build one request against it (varies per
// route — session cookie vs raw bearer token vs none, and the field names
// its handler binds), and the HTTP status a request that reaches the
// handler with a nonexistent code returns (asserted so requests 1-5 are
// provably NOT swallowed by some other status — see baseline note below).
type redemptionRouteCase struct {
	name string
	// newEngine builds a fresh engine bound to a fixture, exercising the
	// production mount function (SetApiRouter or SetApiV2Router).
	newEngine func(fx *redemptionRateLimitFixture) *gin.Engine
	method    string
	path      string
	// newRequest builds one request against a live engine/fixture pair.
	newRequest func(fx *redemptionRateLimitFixture, remoteIP string) *http.Request
	// wantAdmittedStatus is the real, pre-existing status this handler
	// returns for a well-formed request carrying a code that does not
	// exist — the baseline behaviour that must stay byte-identical for
	// requests 1-5 (enterprise_acceptance criterion 3). Baseline verified by
	// reading each handler directly:
	//   - RedeemCodeV2 (topup_v1, v2_redeem, v2_redemption.go) and
	//     SwitchUserTopup (via repo.Redeem, switch_user_topup.go) answer 400
	//     on an unknown code.
	//   - SwitchRedeemAnonymous (switch_redeem) answers 200 success:false on
	//     an unknown code (switch_redeem.go:124-131); 400 there is reserved
	//     for malformed bodies (empty code/fingerprint, switch_redeem.go:97-115).
	wantAdmittedStatus int
}

// newRedemptionRequest builds one request whose RemoteAddr is the fixed
// redemptionRateLimitNginxHop (the trusted proxy every engine here
// configures) and whose client identity — the value the rate limiter and
// UserAuth's audit trail actually key on — travels via X-Forwarded-For as
// ip. This is the production shape: gin only trusts ip because
// redemptionRateLimitNginxHop is inside redemptionRateLimitTrustedCIDR.
func newRedemptionRequest(method, path, ip string, body map[string]string, headers map[string]string) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = redemptionRateLimitNginxHop
	req.Header.Set("X-Forwarded-For", ip)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func redemptionRouteCases() []redemptionRouteCase {
	return []redemptionRouteCase{
		{
			name:      "topup_v1_/api/user/topup",
			newEngine: func(fx *redemptionRateLimitFixture) *gin.Engine { return sessionEngine(fx, SetApiRouter) },
			method:    http.MethodPost,
			path:      "/api/user/topup",
			newRequest: func(fx *redemptionRateLimitFixture, ip string) *http.Request {
				return newRedemptionRequest(http.MethodPost, "/api/user/topup", ip,
					map[string]string{"key": bogusCode32}, nil)
			},
			wantAdmittedStatus: http.StatusBadRequest,
		},
		{
			name:      "v2_redeem_/api/v2/:tenant_slug/redeem",
			newEngine: func(fx *redemptionRateLimitFixture) *gin.Engine { return sessionEngine(fx, SetApiV2Router) },
			method:    http.MethodPost,
			path:      "/api/v2/default/redeem",
			newRequest: func(fx *redemptionRateLimitFixture, ip string) *http.Request {
				return newRedemptionRequest(http.MethodPost, "/api/v2/default/redeem", ip,
					map[string]string{"key": bogusCode32}, nil)
			},
			wantAdmittedStatus: http.StatusBadRequest,
		},
		{
			name:      "switch_redeem_/api/v2/switch/redeem",
			newEngine: func(_ *redemptionRateLimitFixture) *gin.Engine { return anonEngine(SetApiV2Router) },
			method:    http.MethodPost,
			path:      "/api/v2/switch/redeem",
			newRequest: func(fx *redemptionRateLimitFixture, ip string) *http.Request {
				return newRedemptionRequest(http.MethodPost, "/api/v2/switch/redeem", ip,
					map[string]string{"code": bogusCode32, "fingerprint": "rd-rl-probe"}, nil)
			},
			wantAdmittedStatus: http.StatusOK,
		},
		{
			name:      "switch_user_topup_/api/v2/switch/user/topup",
			newEngine: func(_ *redemptionRateLimitFixture) *gin.Engine { return anonEngine(SetApiV2Router) },
			method:    http.MethodPost,
			path:      "/api/v2/switch/user/topup",
			newRequest: func(fx *redemptionRateLimitFixture, ip string) *http.Request {
				return newRedemptionRequest(http.MethodPost, "/api/v2/switch/user/topup", ip,
					map[string]string{"key": bogusCode32}, map[string]string{"Authorization": "Bearer " + fx.TokenKy})
			},
			wantAdmittedStatus: http.StatusBadRequest,
		},
	}
}

// TestRedemptionRoutes_RateLimitMounted is the primary oracle: fires 6
// requests from one IP at each of the four routes and asserts requests 1-5
// reach the real handler (its pre-fix, unthrottled status) while request 6
// is refused by middleware.RedemptionRateLimit() "RD" bucket specifically —
// fingerprinted by X-RateLimit-Scope: ip (rate-limit.go rateLimitScopeForIdent)
// plus Retry-After, not just a bare 429 status, so a 429 from some other
// limiter earlier in the chain cannot false-pass.
func TestRedemptionRoutes_RateLimitMounted(t *testing.T) {
	for _, tc := range redemptionRouteCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fx, cleanup := setupRedemptionRateLimitFixture(t)
			defer cleanup()

			engine := tc.newEngine(fx)
			// One fake source IP per subtest run (TEST-NET-3, RFC 5737 — the
			// public address a real caller would have, carried in
			// X-Forwarded-For by newRedemptionRequest) so the middleware
			// package's process-wide in-memory rate-limit buckets (keyed by
			// mark+IP) stay separate from the other subtests, from a repeat
			// run of this one, and from the sibling mount test, which draws
			// from the upper half of TEST-NET-2.
			ip := fmt.Sprintf("203.0.113.%d", redemptionRateLimitDBCounter.Load()%250+1)

			for i := 1; i <= 5; i++ {
				req := tc.newRequest(fx, ip)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, req)
				if w.Code == http.StatusTooManyRequests {
					t.Fatalf("request %d already got 429 — fixture broken (RD bucket should admit 5 before tripping); body=%s", i, w.Body.String())
				}
				if w.Code != tc.wantAdmittedStatus {
					t.Fatalf("request %d: status=%d, want %d (the handler own baseline status for a nonexistent code — a mismatch here means this request never reached the real handler, so the 6th request 429 would prove nothing); body=%s",
						i, w.Code, tc.wantAdmittedStatus, w.Body.String())
				}
			}

			req6 := tc.newRequest(fx, ip)
			w6 := httptest.NewRecorder()
			engine.ServeHTTP(w6, req6)
			if w6.Code != http.StatusTooManyRequests {
				t.Fatalf("request 6: status=%d, want 429 — middleware.RedemptionRateLimit() must be mounted on %s %s; body=%s",
					w6.Code, tc.method, tc.path, w6.Body.String())
			}
			if got := w6.Header().Get("X-RateLimit-Scope"); got != "ip" {
				t.Errorf("request 6: X-RateLimit-Scope=%q, want \"ip\" — a 429 from a different limiter earlier in the chain would false-pass a bare status check", got)
			}
			if got := w6.Header().Get("X-RateLimit-Type"); got != "requests" {
				t.Errorf("request 6: X-RateLimit-Type=%q, want \"requests\"", got)
			}
			if got := w6.Header().Get("Retry-After"); got == "" {
				t.Errorf("request 6: Retry-After header missing")
			}
			if got := w6.Header().Get("X-RateLimit-Limit"); got != "5" {
				t.Errorf("request 6: X-RateLimit-Limit=%q, want \"5\" (RedemptionRateLimit configured cap)", got)
			}
			if len(w6.Body.Bytes()) != 0 {
				t.Errorf("request 6: body=%q, want empty (rate-limit.go memory-backend 429 path writes no JSON body)", w6.Body.String())
			}
		})
	}
}

// TestRedemptionRoutes_RateLimitIsPerIP is the second oracle named in the
// lane spec: exhausting one IP RD bucket on a route must not throttle a
// different IP hitting the same route (enterprise_acceptance criterion "a
// second IP is not throttled by the first").
func TestRedemptionRoutes_RateLimitIsPerIP(t *testing.T) {
	for _, tc := range redemptionRouteCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fx, cleanup := setupRedemptionRateLimitFixture(t)
			defer cleanup()

			engine := tc.newEngine(fx)
			seq := redemptionRateLimitDBCounter.Load()
			// Two distinct public addresses (TEST-NET-3 / TEST-NET-2, RFC
			// 5737) carried in X-Forwarded-For — not two RemoteAddrs, which
			// would prove nothing about ClientIP() resolving through the
			// trusted-proxy boundary the way production does.
			ip1 := fmt.Sprintf("203.0.113.%d", seq%250+1)
			// Lower half of TEST-NET-2: the upper half belongs to
			// r6d_switch_redeem_mount_test.go.
			ip2 := fmt.Sprintf("198.51.100.%d", seq%120+1)

			// Exhaust ip1 bucket: 5 admitted + 1 tripped.
			for i := 1; i <= 5; i++ {
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, tc.newRequest(fx, ip1))
				if w.Code == http.StatusTooManyRequests {
					t.Fatalf("ip1 request %d already got 429 — fixture broken; body=%s", i, w.Body.String())
				}
			}
			wTrip := httptest.NewRecorder()
			engine.ServeHTTP(wTrip, tc.newRequest(fx, ip1))
			if wTrip.Code != http.StatusTooManyRequests {
				t.Fatalf("ip1 6th request: status=%d, want 429 (fixture setup for this test); body=%s", wTrip.Code, wTrip.Body.String())
			}

			// ip2 first request on the SAME route, SAME engine, right after
			// ip1 tripped: must not be throttled by ip1 bucket.
			wOther := httptest.NewRecorder()
			engine.ServeHTTP(wOther, tc.newRequest(fx, ip2))
			if wOther.Code == http.StatusTooManyRequests {
				t.Fatalf("a second IP first request got 429 right after a different IP exhausted its own bucket — RedemptionRateLimit is not actually keyed per-IP; body=%s", wOther.Body.String())
			}
			if wOther.Code != tc.wantAdmittedStatus {
				t.Fatalf("ip2 request: status=%d, want %d (the handler baseline status)", wOther.Code, tc.wantAdmittedStatus)
			}
		})
	}
}

// TestRedemptionRoutes_RateLimitBucketIsSharedAcrossRoutes pins the budget's
// SHAPE, which the integration guide states as a contract: the five attempts
// per minute are one bucket per client IP across all four redemption routes,
// not five per route. Without this, splitting the bucket per route (the
// obvious "improvement": keying the mark on c.FullPath()) would quadruple a
// code-guessing attacker's budget and every existing test would stay green.
func TestRedemptionRoutes_RateLimitBucketIsSharedAcrossRoutes(t *testing.T) {
	cases := redemptionRouteCases()
	if len(cases) < 2 {
		t.Fatalf("redemptionRouteCases() returned %d cases, want at least 2", len(cases))
	}
	// Two different routes, one client IP. Drawn from the same TEST-NET-3
	// range this file owns, offset so it cannot collide with the per-case
	// addresses the sibling tests use.
	ip := fmt.Sprintf("203.0.113.%d", redemptionRateLimitDBCounter.Load()%50+200)

	first, second := cases[0], cases[len(cases)-1]
	fxFirst, cleanupFirst := setupRedemptionRateLimitFixture(t)
	defer cleanupFirst()
	engineFirst := first.newEngine(fxFirst)
	fxSecond, cleanupSecond := setupRedemptionRateLimitFixture(t)
	defer cleanupSecond()
	engineSecond := second.newEngine(fxSecond)

	// Three on the first route, then three on the second: the sixth request
	// overall is the one that must trip, wherever it lands.
	fire := func(tc redemptionRouteCase, engine *gin.Engine, fx *redemptionRateLimitFixture) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, tc.newRequest(fx, ip))
		return w
	}
	for i := 1; i <= 3; i++ {
		if w := fire(first, engineFirst, fxFirst); w.Code == http.StatusTooManyRequests {
			t.Fatalf("%s request %d already got 429 — fixture broken; body=%s", first.name, i, w.Body.String())
		}
	}
	for i := 4; i <= 5; i++ {
		if w := fire(second, engineSecond, fxSecond); w.Code == http.StatusTooManyRequests {
			t.Fatalf("%s request %d got 429 before the shared budget was spent; body=%s", second.name, i, w.Body.String())
		}
	}
	w := fire(second, engineSecond, fxSecond)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the sixth request from one IP (three on %s, three on %s) got %d, want 429 — the RD budget must be shared across the redemption routes, not per route; body=%s",
			first.name, second.name, w.Code, w.Body.String())
	}
	if scope := w.Header().Get("X-RateLimit-Scope"); scope != "ip" {
		t.Errorf("X-RateLimit-Scope = %q, want ip", scope)
	}
}
