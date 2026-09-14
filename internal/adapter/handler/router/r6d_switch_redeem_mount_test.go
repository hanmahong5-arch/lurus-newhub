package router

// r6d_switch_redeem_mount_test.go — wiring lock for operator decision D4 /
// G5a (2026-08-27).
//
// Why this file exists: the G5a handler-side lock lives in
// internal/adapter/handler/r6c_switch_redeem_tenant_test.go, but its fixture
// (setupSwitchRedeemRouter in switch_redeem_test.go) builds its own
// gin.Engine and registers `POST /api/v2/switch/redeem` itself — a hand-copy
// of api-v2-router.go's registration. Measured 2026-08-27: commenting out
// `switchGroup.POST("/redeem", handler.SwitchRedeemAnonymous)` at
// api-v2-router.go:280 left `go build ./...` at exit 0, the whole router
// package green, and both r6c tests green. So the handler tests prove "this
// code rejects a default-tenant code IF it is called", and proved nothing
// about production calling it.
//
// This test enters through SetApiV2Router — the same function
// cmd/server/main.go reaches via router.SetRouter — so the route table under
// test is the route table production serves. Modeled on the sibling lock
// r6a_rate_limit_mount_test.go, which does the same thing for
// SetRelayRouter.
//
// Two assertions, deliberately layered:
//  1. the route resolves at all (a deleted registration yields 404, and the
//     `/api/v2/:tenant_slug/...` group would otherwise silently swallow the
//     path — the same static-segment-vs-param shadowing class of defect the
//     sibling l4_tenant_slug_shadowing_test.go exists for);
//  2. dispatching through it actually reaches the G5a gate, evidenced by the
//     tenant-mismatch sentinel in the response envelope and by zero
//     anonymous accounts being created.
//
// What it does NOT prove: nothing here observes the CORS / RequestBodySizeLimit /
// OptionalZitaIdentity middleware attached to apiV2 (this test does exercise
// them because it goes through SetApiV2Router — but asserts nothing about
// them). It also does not cover repo.Redeem's other three callers.
//
// TestSetApiV2Router_SwitchRedeem_RateLimitMounted below closes ONE piece of
// that gap (cycle-8 L1, gap topup-payments-subscriptions-35): it proves
// middleware.RedemptionRateLimit() is mounted on this specific route through
// the same SetApiV2Router entrypoint — a sixth POST /api/v2/switch/redeem
// from one IP within 60s must get a 429, not a 6th pass through the G5a gate.
// This route is the one the lane spec calls out by name as needing it most:
// it is anonymous by design (no session, no token), and the apiV2 group it
// sits under carries CORS / RequestBodySizeLimit / OptionalZitaIdentity
// (api-v2-router.go:18, :23, :29) while switchGroup (:335) adds nothing of
// its own, so an IP-keyed limiter is the throttle this lane attaches. The
// four-route table test with the same oracle lives in
// redemption_rate_limit_mount_test.go; this function is the route-specific
// companion the G5a lock above asked for.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var r6dSwitchRedeemDBCounter atomic.Int64

// r6dSeedDefaultTenantCode wires an isolated in-memory SQLite DB into repo.DB
// holding exactly one enabled redemption code whose TenantId is "default" —
// the shape G5a refuses. Returns the code and a cleanup that restores every
// global it touched.
func r6dSeedDefaultTenantCode(t *testing.T) (code string, db *gorm.DB, cleanup func()) {
	t.Helper()

	seq := r6dSwitchRedeemDBCounter.Add(1)
	dbName := fmt.Sprintf("file:r6d_switch_redeem_mount_%d?mode=memory&cache=shared", seq)
	sqlDB, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := sqlDB.AutoMigrate(&repo.Redemption{}, &repo.User{}, &repo.Token{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	code = common.GetRandomString(32)
	red := &repo.Redemption{
		UserId:      1,
		TenantId:    "default",
		Key:         code,
		Name:        "r6d default-tenant code",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := sqlDB.Create(red).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = sqlDB
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	return code, sqlDB, func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if raw, dbErr := sqlDB.DB(); dbErr == nil && raw != nil {
			_ = raw.Close()
		}
	}
}

func TestSetApiV2Router_SwitchRedeem_MountedAndRejectsDefaultTenantCode(t *testing.T) {
	code, db, cleanup := r6dSeedDefaultTenantCode(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	const path = "/api/v2/switch/redeem"

	// Assertion 1: the route is registered on the engine production builds.
	// Checked against the route table itself as well as by dispatch, so the
	// failure message distinguishes "not registered" from "registered but
	// shadowed/misbehaving".
	registered := false
	for _, ri := range engine.Routes() {
		if ri.Method == http.MethodPost && ri.Path == path {
			registered = true
			break
		}
	}
	if !registered {
		t.Fatalf("POST %s is not in SetApiV2Router's route table — the anonymous-redeem gate cannot run if the route production serves does not reach the handler", path)
	}

	body, err := json.Marshal(map[string]string{
		"code":        code,
		"fingerprint": "r6d-default-tenant-fp-0001",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("POST %s dispatched to 404 — route table says it is registered but gin resolves it elsewhere; body=%s", path, w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s status=%d, want 200 (the gate answers inside a success=false envelope, not an HTTP error); body=%s", path, w.Code, w.Body.String())
	}

	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if success, _ := env["success"].(bool); success {
		t.Fatalf("a default-tenant code was accepted through the production route: %s", w.Body.String())
	}
	msg, _ := env["message"].(string)
	if msg != "该兑换码不属于当前租户" {
		t.Fatalf("message=%q, want the tenant-mismatch sentinel — any other rejection reason means the request died before reaching the G5a gate, so this test would pass without observing it", msg)
	}

	// Assertion 2: nothing was sedimented. This is the half that catches a
	// gate moved to after findOrCreateSwitchEndUser, which would still
	// produce the same envelope.
	var userCount int64
	if err := db.Model(&repo.User{}).Where("username LIKE ?", "sw-eu-%").Count(&userCount).Error; err != nil {
		t.Fatalf("count anonymous users: %v", err)
	}
	if userCount != 0 {
		t.Errorf("a rejected default-tenant code provisioned %d sw-eu-* account(s) through the production route", userCount)
	}

	var tokenCount int64
	if err := db.Model(&repo.Token{}).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("a rejected default-tenant code issued %d relay token(s)", tokenCount)
	}

	var reloaded repo.Redemption
	if err := db.Where(`"key" = ?`, code).First(&reloaded).Error; err != nil {
		t.Fatalf("refetch redemption: %v", err)
	}
	if reloaded.Status != common.RedemptionCodeStatusEnabled {
		t.Errorf("the code was consumed by a rejected attempt: status=%d, want %d", reloaded.Status, common.RedemptionCodeStatusEnabled)
	}
}

// TestSetApiV2Router_SwitchRedeem_RateLimitMounted is the route-specific
// companion to redemption_rate_limit_mount_test.go's four-route table,
// scoped to this file because this is the route the cycle-8 L1 lane spec
// names as needing an IP limiter most: it is anonymous by design (no
// session, no token — see SwitchRedeemAnonymous's doc comment), and the
// apiV2 group it sits under carries CORS / RequestBodySizeLimit /
// OptionalZitaIdentity with nothing switch-specific added by switchGroup
// (api-v2-router.go:18, :23, :29, :335), so middleware.RedemptionRateLimit()'s
// "RD" bucket is the throttle this lane attaches to it.
//
// Enters through SetApiV2Router, same as the mount test above — a
// regression that deletes the middleware.RedemptionRateLimit() argument
// from api-v2-router.go's `switchGroup.POST("/redeem", ...)` line fails
// here (the 6th request gets 200 instead of 429).
//
// Client identity: same production shape as redemption_rate_limit_mount_test.go
// — RemoteAddr is fixed to the trusted nginx hop, the per-request source IP
// travels only through X-Forwarded-For, and ConfigureTrustedProxies is what
// makes gin honour it.
func TestSetApiV2Router_SwitchRedeem_RateLimitMounted(t *testing.T) {
	code, _, cleanup := r6dSeedDefaultTenantCode(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := ConfigureTrustedProxies(engine, []string{redemptionRateLimitTrustedCIDR}); err != nil {
		t.Fatalf("ConfigureTrustedProxies() error = %v", err)
	}
	SetApiV2Router(engine)

	const path = "/api/v2/switch/redeem"
	// Fake source IP for this run. The two mount-test files in this package
	// share one process and one in-memory "RD" bucket keyed by mark+IP, so
	// they draw from disjoint ranges: this file uses the upper half of
	// TEST-NET-2 (198.51.100.128/25) and redemption_rate_limit_mount_test.go
	// the lower half plus TEST-NET-3. Avoiding TEST-NET-1 matters too:
	// 192.0.2.1 is httptest.NewRequest's default RemoteAddr, which sibling
	// tests in this package send without a forwarded-for header, and that
	// would key the same bucket.
	ip := fmt.Sprintf("198.51.100.%d", 128+r6dSwitchRedeemDBCounter.Load()%120)

	fire := func() *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{
			"code":        code,
			"fingerprint": "r6d-rate-limit-fp-0001",
		})
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = redemptionRateLimitNginxHop
		req.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	for i := 1; i <= 5; i++ {
		w := fire()
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d already got 429 — fixture broken (RD bucket should admit 5 before tripping); body=%s", i, w.Body.String())
		}
		// This code IS a default-tenant code (r6dSeedDefaultTenantCode), so
		// the G5a gate rejects it every time with a 200 envelope — same
		// baseline as TestSetApiV2Router_SwitchRedeem_MountedAndRejectsDefaultTenantCode
		// above. Reusing that baseline here (rather than a fresh unknown
		// code) is deliberate: it proves the rate limiter and the G5a gate
		// are two independent, both-mounted checks on the same route.
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200 (G5a gate answers inside a success=false envelope); body=%s", i, w.Code, w.Body.String())
		}
	}

	w6 := fire()
	if w6.Code != http.StatusTooManyRequests {
		t.Fatalf("request 6: status=%d, want 429 — middleware.RedemptionRateLimit() must be mounted on POST %s; body=%s", w6.Code, path, w6.Body.String())
	}
	if got := w6.Header().Get("X-RateLimit-Scope"); got != "ip" {
		t.Errorf("request 6: X-RateLimit-Scope=%q, want \"ip\"", got)
	}
	if got := w6.Header().Get("Retry-After"); got == "" {
		t.Errorf("request 6: Retry-After header missing")
	}
}
