package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Redis must be disabled: the Redis client is not initialized in unit tests,
	// and calling RDB.HGetAll on a nil client panics.
	common.RedisEnabled = false
}

// buildAuthRouter creates a test router that injects session values before
// running UserAuth() middleware. Using id=0 avoids DB lookups in GetUserById.
func buildAuthRouter(sessionValues map[string]interface{}) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	store := cookie.NewStore([]byte("auth-test-secret"))
	r.Use(sessions.Sessions("session", store))

	// Session injector middleware: sets values before the auth check
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range sessionValues {
			s.Set(k, v)
		}
		s.Save()
		c.Next()
	})

	r.GET("/test", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	return r
}

// TestAuthHelper_LurusApiUserHeader tests the optional lurus-api-User header
// behavior introduced by P1-4 security fix.
func TestAuthHelper_LurusApiUserHeader(t *testing.T) {
	// Common session values used for authenticated user.
	// id=0 is intentional: GetUserById(0) returns early with an error (no DB panic),
	// and the lurus-api-User header uses "0" to match.
	validSession := map[string]interface{}{
		"username": "testuser",
		"role":     common.RoleCommonUser,
		"id":       0,
		"status":   common.UserStatusEnabled,
	}

	t.Run("header_absent_succeeds", func(t *testing.T) {
		r := buildAuthRouter(validSession)

		req := httptest.NewRequest("GET", "/test", nil)
		// No lurus-api-User header - must succeed normally
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (missing header must not cause rejection)", w.Code, http.StatusOK)
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["success"] != true {
			t.Errorf("response success = %v, want true", resp["success"])
		}
	})

	t.Run("header_matches_session_succeeds", func(t *testing.T) {
		r := buildAuthRouter(validSession)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("lurus-api-User", strconv.Itoa(0)) // matches session id=0
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (matching header must pass)", w.Code, http.StatusOK)
		}
	})

	t.Run("header_mismatches_session_rejects", func(t *testing.T) {
		r := buildAuthRouter(validSession)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("lurus-api-User", "9999") // does NOT match session id=0
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d (mismatched header must be rejected)", w.Code, http.StatusUnauthorized)
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["success"] != false {
			t.Errorf("response success = %v, want false", resp["success"])
		}
	})

	t.Run("header_invalid_format_rejects", func(t *testing.T) {
		r := buildAuthRouter(validSession)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("lurus-api-User", "not-a-number") // non-integer format
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d (invalid format must be rejected)", w.Code, http.StatusUnauthorized)
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["success"] != false {
			t.Errorf("response success = %v, want false", resp["success"])
		}
	})
}

// ---------------------------------------------------------------------------
// L7 (auth-security-08/26/29) — per-device session registry registration in
// authHelper, and the defence-in-depth revoked check.
// ---------------------------------------------------------------------------

var sessionRegistryTestDBCounter atomic.Int64

// sessionRegistryTestCtx wires a real Redis-backed session store (so
// session.ID() is a genuine non-empty id, matching production/UAT — a
// cookie-only store never registers anything and would prove nothing here)
// plus a real sqlite-backed repo.DB carrying the user_sessions table, in
// front of the REAL UserAuth() middleware.
type sessionRegistryTestCtx struct {
	router *gin.Engine
	db     *gorm.DB
	mr     *miniredis.Miniredis
}

func setupSessionRegistryTestRouter(t *testing.T) *sessionRegistryTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:sessionregistry%d?mode=memory&cache=shared", sessionRegistryTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.UserSession{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedisEnabled := common.RedisEnabled

	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	// The session STORE below talks to miniredis directly (its own client),
	// independent of common.RDB/common.RedisEnabled — those globals govern
	// the LAST-SEEN THROTTLE guard (ShouldThrottleSessionTouch) and the
	// revoke handler's RedisDel, not the gin session cookie plumbing. Left
	// disabled here (as in every other middleware test) so GetUserCache's
	// Redis-backed cache path is skipped, not the session store.
	common.RedisEnabled = false

	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("session-registry-test-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", store))
	r.GET("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		userIdStr := c.Query("id")
		userId, _ := strconv.Atoi(userIdStr)
		s.Set("username", "sessregtester")
		s.Set("role", common.RoleCommonUser)
		s.Set("id", userId)
		s.Set("status", common.UserStatusEnabled)
		if err := s.Save(); err != nil {
			t.Fatalf("save login session: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	r.GET("/probe", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedisEnabled
		mr.Close()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return &sessionRegistryTestCtx{router: r, db: db, mr: mr}
}

// login performs the fake-login request and returns the Set-Cookie header
// so callers can replay it on subsequent requests (simulating a real
// browser's cookie jar).
func (ctx *sessionRegistryTestCtx) login(t *testing.T, userId int) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/login?id=%d", userId), nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", w.Code, w.Body.String())
	}
	cookies := w.Header().Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("login produced no Set-Cookie header")
	}
	return cookies[0]
}

func (ctx *sessionRegistryTestCtx) probe(cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// loginRaw is login's more general sibling: it optionally replays an
// existing Cookie header on the /login request (simulating a real browser's
// jar sending whatever cookie it currently holds), and returns the raw
// recorder instead of asserting 200 — TestAuthHelper_ReloginAfterRemoteRevoke_
// NotLockedOut needs both the Set-Cookie list AND the ability to send an
// EMPTY cookie (a jar that already dropped a Set-Cookie:...Max-Age=0
// response, unlike login/probe above which never simulate that).
func (ctx *sessionRegistryTestCtx) loginRaw(t *testing.T, userId int, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/login?id=%d", userId), nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// TestAuthHelper_FlagOff_NoRegistryWrites: with SESSION_REGISTRY_ENABLED
// unset (default observe/off — mirrors the CostSpikeLimit precedent), a
// normal cookie-authenticated request through the REAL UserAuth() must
// leave the user_sessions table untouched, even though the session store is
// the real Redis-backed one (session.ID() is non-empty) and would have
// something to register if the flag were on.
func TestAuthHelper_FlagOff_NoRegistryWrites(t *testing.T) {
	ctx := setupSessionRegistryTestRouter(t)

	cookieHeader := ctx.login(t, 4242)
	w := ctx.probe(cookieHeader)
	if w.Code != http.StatusOK {
		t.Fatalf("probe status = %d, body=%s", w.Code, w.Body.String())
	}

	var count int64
	ctx.db.Model(&entity.UserSession{}).Count(&count)
	if count != 0 {
		t.Errorf("user_sessions row count = %d, want 0 with the flag off", count)
	}
}

// TestAuthHelper_FlagOn_RegistersSession: the mirror case — with the flag
// on, the same request registers exactly one row, stamped with the
// caller's user id and a non-empty session_key.
func TestAuthHelper_FlagOn_RegistersSession(t *testing.T) {
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	ctx := setupSessionRegistryTestRouter(t)

	cookieHeader := ctx.login(t, 4343)
	w := ctx.probe(cookieHeader)
	if w.Code != http.StatusOK {
		t.Fatalf("probe status = %d, body=%s", w.Code, w.Body.String())
	}

	var rows []entity.UserSession
	ctx.db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("user_sessions rows = %d, want 1 with the flag on", len(rows))
	}
	if rows[0].UserId != 4343 {
		t.Errorf("registered row user_id = %d, want 4343", rows[0].UserId)
	}
	if rows[0].SessionKey == "" {
		t.Error("registered row has an empty session_key")
	}
}

// TestAuthHelper_ReloginAfterRemoteRevoke_NotLockedOut: a session revoked
// REMOTELY (another device/admin sets revoked_at on this browser's own row
// — simulated here with repo.RevokeUserSessionByKey, independent of whether
// the Redis key was also deleted) must not permanently lock this browser
// out. boj/redistore's Session.Save keeps whatever id an incoming cookie
// already carries, so without authHelper clearing the cookie in its
// SESSION_REVOKED branch, a re-login replaying the SAME (never-cleared)
// cookie would recreate session_<sameID> — whose row stays revoked — and
// keep answering SESSION_REVOKED on every subsequent request. This test's
// fake "jar" is a simplification, not a real cookie jar: it resends the OLD
// cookie unless the previous response carried ANY Set-Cookie header, in
// which case it sends none — it does not check Max-Age/Expires, so it would
// also (incorrectly, for a real jar) drop a cookie the server merely
// refreshed. That simplification holds here only because every Set-Cookie
// this flow's server sends IS a clearing one; it is not a general jar.
func TestAuthHelper_ReloginAfterRemoteRevoke_NotLockedOut(t *testing.T) {
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	ctx := setupSessionRegistryTestRouter(t)

	cookieA := ctx.login(t, 5151)
	probeA := ctx.probe(cookieA)
	if probeA.Code != http.StatusOK {
		t.Fatalf("probe after login status = %d, body=%s", probeA.Code, probeA.Body.String())
	}

	var rows []entity.UserSession
	if err := ctx.db.Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("registered rows = %d, want 1", len(rows))
	}

	// Simulate a REMOTE revoke (a different device, or an admin) — the
	// row's revoked_at is set directly, independent of whether that other
	// caller's RedisDel also ran.
	if err := repo.RevokeUserSessionByKey(rows[0].SessionKey, entity.SessionRevokeReasonAdminRevoked); err != nil {
		t.Fatalf("RevokeUserSessionByKey: %v", err)
	}

	revokedProbe := ctx.probe(cookieA)
	if revokedProbe.Code != http.StatusUnauthorized {
		t.Fatalf("probe after remote revoke status = %d, want 401, body=%s", revokedProbe.Code, revokedProbe.Body.String())
	}
	// Fake-jar simplification (see this test's doc comment above): drop the
	// cookie if the response carried any Set-Cookie at all, not specifically
	// one with an expired/zero Max-Age.
	jarCookie := cookieA
	if len(revokedProbe.Header().Values("Set-Cookie")) > 0 {
		jarCookie = "" // treated as a clear
	}

	reloginW := ctx.loginRaw(t, 5151, jarCookie)
	if reloginW.Code != http.StatusOK {
		t.Fatalf("re-login status = %d, want 200, body=%s", reloginW.Code, reloginW.Body.String())
	}
	newCookies := reloginW.Header().Values("Set-Cookie")
	if len(newCookies) == 0 {
		t.Fatal("re-login produced no Set-Cookie header")
	}
	newCookie := newCookies[0]
	if newCookie == cookieA {
		t.Fatal("re-login reused the SAME (permanently revoked) cookie — the fix did not take effect")
	}

	finalProbe := ctx.probe(newCookie)
	if finalProbe.Code != http.StatusOK {
		t.Fatalf("probe after re-login status = %d, want 200 (must NOT be locked out), body=%s", finalProbe.Code, finalProbe.Body.String())
	}
}

// TestAuthHelper_ReloginAfterRedisKeyDeleted_NotLockedOut is the lock for L5
// repair round 3, finding routing-resilience-limits-13#10: a DIFFERENT
// revoke shape than TestAuthHelper_ReloginAfterRemoteRevoke_NotLockedOut
// above. That test revokes by setting revoked_at on the DB row while the
// session's own Redis key is left alone, so session.Get still returns
// "username" and authHelper's SESSION_REVOKED branch (which already cleared
// the cookie pre-repair) fires. This test instead deletes the session's
// Redis key directly — mirroring what a real revoke-by-id/logout endpoint
// does (redisDeleteSessionKey) — so session.Get returns nil and authHelper's
// username==nil, no-Authorization-header 401 branch fires instead: the
// branch this repair item added the clear to. Before the fix that branch
// never cleared the cookie, so jar B's replayed cookie would recreate the
// identical session key on re-login and could still be treated as reused by
// anything keying off the session id.
func TestAuthHelper_ReloginAfterRedisKeyDeleted_NotLockedOut(t *testing.T) {
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	ctx := setupSessionRegistryTestRouter(t)

	cookieA := ctx.login(t, 6161)
	probeA := ctx.probe(cookieA)
	if probeA.Code != http.StatusOK {
		t.Fatalf("probe after login status = %d, body=%s", probeA.Code, probeA.Body.String())
	}

	var rows []entity.UserSession
	if err := ctx.db.Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("registered rows = %d, want 1", len(rows))
	}

	// Simulate the real-revoke shape: delete the store's own Redis key
	// (same key format redisDeleteSessionKey/v2_session_revoke.go uses),
	// independent of the DB row's revoked_at.
	if !ctx.mr.Del("session_" + rows[0].SessionKey) {
		t.Fatalf("session_%s did not exist in miniredis before delete", rows[0].SessionKey)
	}

	revokedProbe := ctx.probe(cookieA)
	if revokedProbe.Code != http.StatusUnauthorized {
		t.Fatalf("probe after Redis key deletion status = %d, want 401, body=%s", revokedProbe.Code, revokedProbe.Body.String())
	}
	if len(revokedProbe.Header().Values("Set-Cookie")) == 0 {
		t.Fatal("probe after Redis key deletion produced no Set-Cookie header — jar B's stale cookie was never cleared")
	}
	// The probe above already asserted a Set-Cookie was sent; a jar that
	// honours it sends nothing on the next request.
	jarCookie := ""

	reloginW := ctx.loginRaw(t, 6161, jarCookie)
	if reloginW.Code != http.StatusOK {
		t.Fatalf("re-login status = %d, want 200, body=%s", reloginW.Code, reloginW.Body.String())
	}
	newCookies := reloginW.Header().Values("Set-Cookie")
	if len(newCookies) == 0 {
		t.Fatal("re-login produced no Set-Cookie header")
	}
	newCookie := newCookies[0]
	if newCookie == cookieA {
		t.Fatal("re-login reused the SAME cookie the deleted-key probe should have cleared")
	}

	finalProbe := ctx.probe(newCookie)
	if finalProbe.Code != http.StatusOK {
		t.Fatalf("probe after re-login status = %d, want 200 (must NOT be locked out), body=%s", finalProbe.Code, finalProbe.Body.String())
	}
}
