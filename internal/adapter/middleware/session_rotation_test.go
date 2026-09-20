package middleware

// session_rotation_test.go — cycle-13 L8 repair round, operator decision
// D-L8-1.
//
// The first round rotated the session id at the three login handlers and
// missed the fourth place an authenticated identity is written into a
// browser session: the SDK-bridge self-heal arm of resolveSessionIdentity
// (auth.go). That arm resolves the platform lurus_session cookie into a
// local user and then writes id/username/role/status/group into whatever
// gin session the incoming cookie already named — the same session-fixation
// shape the three logins had, on the production /api/v2 chain.
//
// The harness is a REAL Redis-backed gin session store over miniredis, not
// the cookie store most middleware tests use: a cookie store has no session
// id (the cookie value IS the data), so against it "the id was rotated" is
// unobservable and every assertion below would pass vacuously. Redis is
// also the only place the OLD key's deletion can be seen.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
	"github.com/redis/go-redis/v9"
)

// rotationSelfHealAccountID is the platform account the fixture user is
// bridged to. The arm under test only runs for a user that already exists
// locally and carries this linkage.
const rotationSelfHealAccountID int64 = 764321

type rotationProbe struct {
	engine *gin.Engine
	rdb    *redis.Client
	user   *repo.User
}

// setupRotationProbe builds a Redis-session-backed engine carrying:
//
//	GET /plant           — mints a pre-authentication session, the cookie an
//	                       attacker would plant in the victim's browser
//	GET /rotate-probe    — RotateSessionID on its own
//	GET /api/v2/probe    — the real UserAuth() chain behind an injected
//	                       platform identity, i.e. the SDK self-heal arm
//
// plus the package's sqlite repo fixture so the arm's user lookup runs for
// real.
func setupRotationProbe(t *testing.T) *rotationProbe {
	t.Helper()
	_, coverCleanup := setupCoverDB(t)
	t.Cleanup(coverCleanup)

	account := rotationSelfHealAccountID
	user := &repo.User{
		Username: "selfheal-user", DisplayName: "Self Heal User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "selfheal@test.local", TenantId: "default",
		LurusAccountID: &account,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("rotation-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	// Registered after coverCleanup so it runs BEFORE it (t.Cleanup is
	// LIFO): setupCoverDB turned Redis off, and this restores the value it
	// left behind, not the process-wide original.
	prevRedisEnabled, prevRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, rdb
	t.Cleanup(func() { common.RedisEnabled, common.RDB = prevRedisEnabled, prevRDB })

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
	r.GET("/rotate-probe", func(c *gin.Context) {
		if err := RotateSessionID(c); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"session_id": sessions.Default(c).ID()})
	})
	// c.Set(zita.ContextKey, …) is the seam OptionalZitaIdentity uses to
	// publish the platform identity ahead of UserAuth on the real
	// /api/v2 chain (api-v2-router.go), and the seam the existing
	// SDK-bridge tests take.
	r.GET("/api/v2/probe", func(c *gin.Context) {
		c.Set(zita.ContextKey, &zita.Identity{AccountID: rotationSelfHealAccountID})
		c.Next()
	}, UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id")})
	})

	return &rotationProbe{engine: r, rdb: rdb, user: user}
}

// plant performs the attacker's half: it obtains a session cookie and the id
// behind it. Both are returned so a test can assert on the cookie VALUE
// (what the browser holds) and on the Redis KEY (what the store holds).
func (p *rotationProbe) plant(t *testing.T) (cookie *http.Cookie, id string) {
	t.Helper()
	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plant", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("plant session: status %d body=%s", w.Code, w.Body.String())
	}
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
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
	if n, err := p.rdb.Exists(context.Background(), sessionStoreKeyPrefix+id).Result(); err != nil || n == 0 {
		t.Fatalf("plant session: %s%s should exist in Redis (err=%v exists=%d)", sessionStoreKeyPrefix, id, err, n)
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
// alone is not binding: a handler that merely re-saves the session writes a
// freshly encoded cookie for the SAME id (securecookie stamps a timestamp),
// so "the cookie value changed" is satisfied without any rotation. The two
// Redis assertions are what pin it — the old key is gone AND some other
// session key exists, which separates rotation from a plain Clear().
func (p *rotationProbe) assertRotated(t *testing.T, planted *http.Cookie, plantedID string, w *httptest.ResponseRecorder) {
	t.Helper()
	fresh := responseSessionCookie(t, w)
	if fresh.Value == planted.Value {
		t.Errorf("the request handed back the SAME session cookie value it was given — a cookie planted before authentication is now the authenticated session")
	}

	keys, err := p.rdb.Keys(context.Background(), sessionStoreKeyPrefix+"*").Result()
	if err != nil {
		t.Fatalf("redis KEYS %s*: %v", sessionStoreKeyPrefix, err)
	}
	minted := 0
	for _, k := range keys {
		if k == sessionStoreKeyPrefix+plantedID {
			t.Errorf("%s still exists in Redis after the identity write — the pre-authentication session must be destroyed, not merely re-cookied", k)
			continue
		}
		minted++
	}
	if minted == 0 {
		t.Errorf("no session key other than the planted one exists afterwards (keys=%v) — the identity write must MINT a new session, not just drop the old one", keys)
	}
}

// TestSDKSelfHealArm_RotatesSessionID is the fourth-site oracle (D-L8-1).
// It drives the real UserAuth chain, so the identity write it observes is
// the production one in resolveSessionIdentity, not a hand-built shape.
func TestSDKSelfHealArm_RotatesSessionID(t *testing.T) {
	p := setupRotationProbe(t)
	planted, plantedID := p.plant(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/probe", nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("probe status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	// Without this the test could pass for the wrong reason (a request that
	// never reached the self-heal arm writes no identity and leaves no key
	// to find).
	if want := fmt.Sprintf(`"id":%d`, p.user.Id); !strings.Contains(w.Body.String(), want) {
		t.Fatalf("probe body %s does not carry %s — the self-heal arm did not authenticate the request", w.Body.String(), want)
	}
	p.assertRotated(t, planted, plantedID, w)
}

// TestSDKSelfHealArm_SessionIsUsableAfterRotation: the rotated session has
// to be the one the browser keeps using, identity and all — otherwise the
// fix would log every SDK-bridge user out on their own next request.
func TestSDKSelfHealArm_SessionIsUsableAfterRotation(t *testing.T) {
	p := setupRotationProbe(t)
	planted, _ := p.plant(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/probe", nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first probe status = %d body=%s", w.Code, w.Body.String())
	}

	// Second request with the NEW cookie and no platform identity at all:
	// it must authenticate off the rotated session alone.
	next := httptest.NewRequest(http.MethodGet, "/api/v2/probe", nil)
	next.AddCookie(responseSessionCookie(t, w))
	nw := httptest.NewRecorder()
	p.engine.ServeHTTP(nw, next)
	if nw.Code != http.StatusOK {
		t.Fatalf("the rotated session is not usable on the next request: %d %s", nw.Code, nw.Body.String())
	}
	if want := fmt.Sprintf(`"id":%d`, p.user.Id); !strings.Contains(nw.Body.String(), want) {
		t.Errorf("second probe body %s does not carry %s — the identity did not survive the rotation", nw.Body.String(), want)
	}
}

// TestRotateSessionID_MintsNewIDAndDropsOldKey exercises the helper on its
// own, so a failure in one of the four call sites can be told apart from a
// failure in the rotation itself. (Moved here from
// internal/adapter/handler with the helper, per D-L8-1.)
func TestRotateSessionID_MintsNewIDAndDropsOldKey(t *testing.T) {
	p := setupRotationProbe(t)
	planted, plantedID := p.plant(t)

	req := httptest.NewRequest(http.MethodGet, "/rotate-probe", nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-probe status = %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"session_id":"`+plantedID+`"`) {
		t.Errorf("session id unchanged after rotation: %s", w.Body.String())
	}
	p.assertRotated(t, planted, plantedID, w)
}

// TestRotateSessionID_NoIncomingCookie pins the do-not-regress row that the
// cookie-less POST /api/v2/bridge/exchange depends on: with no prior id
// there is nothing to retire, and rotation must not turn that into an
// error.
func TestRotateSessionID_NoIncomingCookie(t *testing.T) {
	p := setupRotationProbe(t)

	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rotate-probe", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-probe without a cookie: status = %d body=%s", w.Code, w.Body.String())
	}
	if ck := responseSessionCookie(t, w); ck.Value == "" {
		t.Errorf("no session issued for a cookie-less rotation")
	}
}

// TestRotateSessionID_RetiresTheOldRegistryRow covers the per-device
// registry leg (SESSION_REGISTRY_ENABLED, off by default): the row for the
// id that was just retired has to stop counting as an active device, with a
// reason of its own — nobody logged out and nobody revoked anything, so
// reusing "logout" or "user_revoked" would lie to whoever reads the sessions
// page or the audit trail.
func TestRotateSessionID_RetiresTheOldRegistryRow(t *testing.T) {
	p := setupRotationProbe(t)
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	if err := repo.DB.AutoMigrate(&entity.UserSession{}); err != nil {
		t.Fatalf("migrate user_sessions: %v", err)
	}

	planted, plantedID := p.plant(t)
	now := common.GetTimestamp()
	if err := repo.DB.Create(&entity.UserSession{
		SessionKey: plantedID, UserId: p.user.Id, TenantId: "default",
		AuthMethod: "session", CreatedAt: now, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatalf("seed registry row: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/rotate-probe", nil)
	req.AddCookie(planted)
	w := httptest.NewRecorder()
	p.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-probe status = %d body=%s", w.Code, w.Body.String())
	}

	var reloaded entity.UserSession
	if err := repo.DB.Where("session_key = ?", plantedID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload registry row: %v", err)
	}
	if reloaded.RevokedAt == 0 {
		t.Errorf("the registry row for the retired id is still active — it would keep counting toward the per-user session cap and show as a live device")
	}
	if reloaded.RevokeReason != entity.SessionRevokeReasonRotated {
		t.Errorf("revoke_reason = %q, want %q", reloaded.RevokeReason, entity.SessionRevokeReasonRotated)
	}
}
