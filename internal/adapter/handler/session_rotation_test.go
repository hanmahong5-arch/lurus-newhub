package handler

// session_rotation_test.go — cycle-13 L8, session fixation.
//
// The harness is deliberately a REAL Redis-backed gin session store over
// miniredis rather than the cookie store the other login tests use: the
// cookie store has no session id at all (the cookie value IS the data), so
// against it "the id was rotated" is unobservable and every assertion below
// would pass vacuously. Redis is also the only place the OLD key's deletion
// can be seen.
//
// Each of the three login handlers gets its own case, because each one
// calls the rotation itself — removing the call from one must not be
// covered by the other two. The helper itself (middleware.RotateSessionID,
// moved there in the cycle-13 repair round so middleware/auth.go's
// SDK-bridge self-heal arm can call it too) has its own tests next to it,
// in internal/adapter/middleware/session_rotation_test.go.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var sessionRotationDBCounter atomic.Int64

// rotationBootstrapAccountID is the platform account the fixture user is
// bridged to, so ZitaBootstrap resolves an EXISTING user (the repeat-login
// path — the one a planted cookie is actually aimed at) rather than
// auto-creating one.
const rotationBootstrapAccountID int64 = 987654

type rotationCtx struct {
	router *gin.Engine
	db     *gorm.DB
	rdb    *redis.Client
	user   *repo.User
}

// setupRotationRouter builds a Redis-session-backed engine carrying:
//
//	GET  /plant                     — mints a pre-authentication session, the
//	                                  cookie an attacker would put in the
//	                                  victim's browser
//	POST /api/v2/bridge/exchange    — the real BridgeExchange handler
//
// plus a real sqlite repo.DB so the handlers' user lookups run for real.
func setupRotationRouter(t *testing.T) *rotationCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:sessionrotation%d?mode=memory&cache=shared", sessionRotationDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.Token{}, &entity.UserSession{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}
	if err := db.Create(&repo.Tenant{
		Id: "rot-tenant", Slug: "rot-tenant", Name: "Rotation Tenant",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_rot",
	}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	accountID := rotationBootstrapAccountID
	user := &repo.User{
		Username: "rotation-user", DisplayName: "Rotation User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "rotation@test.local", TenantId: "rot-tenant",
		LurusAccountID: &accountID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("rotation-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	prevRedisEnabled, prevRDB := common.RedisEnabled, common.RDB
	repo.DB, repo.LOG_DB = db, db
	common.UsingSQLite, common.UsingPostgreSQL = true, false
	common.RedisEnabled, common.RDB = true, rdb

	r := gin.New()
	r.Use(gin.Recovery())
	// "session" — the cookie name cmd/server/main.go registers.
	r.Use(sessions.Sessions("session", store))
	r.GET("/plant", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("planted_by_attacker", "yes")
		if err := s.Save(); err != nil {
			c.String(http.StatusInternalServerError, "save: %v", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"session_id": s.ID()})
	})
	r.POST("/api/v2/bridge/exchange", BridgeExchange)
	// ZitaBootstrap reads the platform identity out of the gin context;
	// c.Set(zita.ContextKey, …) is the same seam the SDK middleware uses and
	// the one the existing bootstrap tests take.
	r.POST("/api/v2/auth/zita-bootstrap", func(c *gin.Context) {
		c.Set(zita.ContextKey, &zita.Identity{AccountID: rotationBootstrapAccountID})
		c.Next()
	}, ZitaBootstrap)

	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		common.RedisEnabled, common.RDB = prevRedisEnabled, prevRDB
		_ = rdb.Close()
		mr.Close()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return &rotationCtx{router: r, db: db, rdb: rdb, user: user}
}

// plantSession performs the attacker's half: it obtains a session cookie
// and the id behind it. Both are returned so a test can assert on the cookie
// VALUE (what the browser holds) and on the Redis KEY (what the store holds).
func (ctx *rotationCtx) plantSession(t *testing.T) (cookie *http.Cookie, id string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/plant", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plant session: status %d body=%s", w.Code, w.Body.String())
	}
	cookies := (&http.Response{Header: w.Header()}).Cookies()
	for _, ck := range cookies {
		if ck.Name == "session" {
			cookie = ck
		}
	}
	if cookie == nil {
		t.Fatalf("plant session: no session cookie in %v", w.Header().Values("Set-Cookie"))
	}
	if !strings.Contains(w.Body.String(), `"session_id"`) {
		t.Fatalf("plant session: body has no session_id: %s", w.Body.String())
	}
	id = strings.TrimSuffix(strings.SplitN(w.Body.String(), `"session_id":"`, 2)[1], `"}`)
	if id == "" {
		t.Fatalf("plant session: empty session id, body=%s", w.Body.String())
	}
	if n, err := ctx.rdb.Exists(context.Background(), "session_"+id).Result(); err != nil || n == 0 {
		t.Fatalf("plant session: session_%s should exist in Redis (err=%v exists=%d)", id, err, n)
	}
	return cookie, id
}

// responseSessionCookie pulls the session cookie the handler just wrote.
func responseSessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
		if ck.Name == "session" && ck.Value != "" {
			return ck
		}
	}
	t.Fatalf("no session cookie in response headers %v", w.Header().Values("Set-Cookie"))
	return nil
}

// assertRotated is the shared oracle, in three parts because the first one
// alone is not binding: a handler that re-saves the session writes a freshly
// encoded cookie for the SAME id (securecookie stamps a timestamp), so
// "the cookie value changed" is satisfied without any rotation at all.
// The two Redis assertions are what actually pin it — the old key is gone
// AND some other session key exists, which separates rotation from a plain
// Clear().
func (ctx *rotationCtx) assertRotated(t *testing.T, planted *http.Cookie, plantedID string, w *httptest.ResponseRecorder) {
	t.Helper()
	fresh := responseSessionCookie(t, w)
	if fresh.Value == planted.Value {
		t.Errorf("the login handed back the SAME session cookie value it was given — a cookie planted before the login is now the authenticated session")
	}

	keys, err := ctx.rdb.Keys(context.Background(), "session_*").Result()
	if err != nil {
		t.Fatalf("redis KEYS session_*: %v", err)
	}
	minted := 0
	for _, k := range keys {
		if k == "session_"+plantedID {
			t.Errorf("%s still exists in Redis after the login — the pre-authentication session must be destroyed, not merely re-cookied", k)
			continue
		}
		minted++
	}
	if minted == 0 {
		t.Errorf("no session key other than the planted one exists after the login (keys=%v) — the login must MINT a new session, not just drop the old one", keys)
	}
}

// TestBridgeExchange_RotatesSessionID: login #3 of 3.
func TestBridgeExchange_RotatesSessionID(t *testing.T) {
	ctx := setupRotationRouter(t)
	t.Setenv("E2E_BRIDGE_TOKEN", "rotation-token")

	planted, plantedID := ctx.plantSession(t)

	url := "/api/v2/bridge/exchange?token=rotation-token&user_id=" + strconv.Itoa(ctx.user.Id)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("bridge exchange status = %d body=%s", w.Code, w.Body.String())
	}
	ctx.assertRotated(t, planted, plantedID, w)
}

// TestBridgeExchange_NoCookieStillWorks pins the do-not-regress row: the
// usual e2e shape sends no cookie at all, and rotation must not turn that
// into an error.
func TestBridgeExchange_NoCookieStillWorks(t *testing.T) {
	ctx := setupRotationRouter(t)
	t.Setenv("E2E_BRIDGE_TOKEN", "rotation-token")

	url := "/api/v2/bridge/exchange?token=rotation-token&user_id=" + strconv.Itoa(ctx.user.Id)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("bridge exchange without a cookie: status = %d body=%s", w.Code, w.Body.String())
	}
	if ck := responseSessionCookie(t, w); ck.Value == "" {
		t.Errorf("no session issued for a cookie-less bridge exchange")
	}
}

// TestZitaBootstrap_RotatesSessionID: login #2 of 3.
func TestZitaBootstrap_RotatesSessionID(t *testing.T) {
	ctx := setupRotationRouter(t)
	planted, plantedID := ctx.plantSession(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/zita-bootstrap", nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("zita-bootstrap status = %d body=%s", w.Code, w.Body.String())
	}
	ctx.assertRotated(t, planted, plantedID, w)
}

// TestOIDCCallback_RotatesSessionID: login #1 of 3, and the one an attacker
// can most easily aim at — the victim arrives at the callback carrying
// whatever session cookie their browser already held.
//
// setupOIDCCallbackHarness (oidc_callback_link_test.go, same package) gives
// the real fake-IdP wiring: JWKS, token endpoint, identity resolver, a
// seeded tenant and repo.DB. Its own engine uses a COOKIE store, in which a
// session has no id at all, so this test mounts the same handler on a
// Redis-session engine — the store production runs — and drives it with the
// harness's signed token and real generateOAuthState.
func TestOIDCCallback_RotatesSessionID(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 555001)
	defer h.cleanup()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("oidc-rotation-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	prevRedisEnabled, prevRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, rdb
	t.Cleanup(func() { common.RedisEnabled, common.RDB = prevRedisEnabled, prevRDB })

	ctx := &rotationCtx{rdb: rdb}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", store))
	r.GET("/plant", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("planted_by_attacker", "yes")
		if err := s.Save(); err != nil {
			c.String(http.StatusInternalServerError, "save: %v", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"session_id": s.ID()})
	})
	r.GET("/api/v2/oauth/callback", OIDCCallback)
	ctx.router = r

	planted, plantedID := ctx.plantSession(t)

	state, _, err := generateOAuthState(h.tenantSlug, "/dashboard")
	if err != nil {
		t.Fatalf("generateOAuthState: %v", err)
	}
	idToken := h.signIDToken(t, "rotation-subject-1", "rotation-1@example.com")
	q := url.Values{}
	q.Set("code", idToken)
	q.Set("state", state)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?"+q.Encode(), nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	ctx.assertRotated(t, planted, plantedID, w)
}
