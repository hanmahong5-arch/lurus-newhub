/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// buildRevokeRouter constructs a minimal gin router that:
//   - uses a cookie-backed session store (no Redis required in tests)
//   - sets c.Set("id", userID) to simulate UserAuth
func buildRevokeRouter(userID int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test_session", store))

	r.DELETE("/api/v2/:tenant_slug/sessions/current", func(c *gin.Context) {
		if userID != 0 {
			c.Set("id", userID)
		}
		RevokeCurrentSessionV2(c)
	})
	return r
}

// TestV2SessionRevoke_Happy verifies that an authenticated DELETE returns 200
// with success=true and the redirect field, and that Set-Cookie expires the
// session cookie (MaxAge=-1 / Expires in the past).
func TestV2SessionRevoke_Happy(t *testing.T) {
	r := buildRevokeRouter(42)

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/acme/sessions/current", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data field missing or wrong type")
	}
	// /login is the SPA's only login route; the old /console/v2/login hint
	// pointed at a path absent from the v2 route table (rendered NotFound).
	if data["redirect"] != "/login" {
		t.Errorf("redirect = %v, want /login", data["redirect"])
	}

	// At least one Set-Cookie header must be present (session expiry).
	setCookies := w.Header().Values("Set-Cookie")
	if len(setCookies) == 0 {
		t.Errorf("expected Set-Cookie headers to expire session cookies, got none")
	}
}

// TestV2SessionRevoke_Unauthenticated verifies that a request without a user
// context (userID == 0) is rejected with 401.
func TestV2SessionRevoke_Unauthenticated(t *testing.T) {
	r := buildRevokeRouter(0) // userID=0 → not authenticated

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/acme/sessions/current", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	if resp["error_code"] != "UNAUTHENTICATED" {
		t.Errorf("error_code = %v, want UNAUTHENTICATED", resp["error_code"])
	}
}

// ---------------------------------------------------------------------------
// L7 — revoke-by-id / revoke-others. These need a real DB (registry rows to
// revoke), unlike the /current tests above which only touch the session
// cookie. currentSessionFake stands in for the ACTUAL session middleware
// (which needs real Redis to produce a non-empty session.ID(); the full
// authHelper+Redis path is covered by
// TestV2SessionRevokeByID_DeletesRedisKeyAnd401 below) purely so
// currentSessionID(c) has something to compare against for is_current /
// "keep current" logic — it implements the full sessions.Session interface
// with no-ops beyond ID() because that is the only method these handlers
// call.
// ---------------------------------------------------------------------------

type currentSessionFake struct{ id string }

func (f currentSessionFake) ID() string                      { return f.id }
func (f currentSessionFake) Get(interface{}) interface{}     { return nil }
func (f currentSessionFake) Set(interface{}, interface{})    {}
func (f currentSessionFake) Delete(interface{})              {}
func (f currentSessionFake) Clear()                          {}
func (f currentSessionFake) AddFlash(interface{}, ...string) {}
func (f currentSessionFake) Flashes(...string) []interface{} { return nil }
func (f currentSessionFake) Options(sessions.Options)        {}
func (f currentSessionFake) Save() error                     { return nil }

var sessionRevokeDBTestCounter atomic.Int64

type sessionRevokeDBCtx struct {
	router *gin.Engine
	db     *gorm.DB
}

// setupSessionRevokeDBRouter wires DELETE .../sessions/:id and
// .../sessions/others against a real sqlite-backed repo.DB, with a mock auth
// that sets both "id" and a currentSessionFake (so currentSessionID(c)
// resolves to currentKey).
func setupSessionRevokeDBRouter(t *testing.T, callerID int, currentKey string) *sessionRevokeDBCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:sessionrevoke%d?mode=memory&cache=shared", sessionRevokeDBTestCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.UserSession{}); err != nil {
		t.Fatalf("auto migrate UserSession: %v", err)
	}

	prevDB := repo.DB
	prevRedisEnabled := common.RedisEnabled
	repo.DB = db
	common.RedisEnabled = false // exercised separately with a real miniredis below

	r := gin.New()
	mockAuth := func(c *gin.Context) {
		if callerID != 0 {
			c.Set("id", callerID)
		}
		if currentKey != "" {
			c.Set(sessions.DefaultKey, currentSessionFake{id: currentKey})
		}
		c.Next()
	}
	r.DELETE("/api/v2/:tenant_slug/sessions/others", mockAuth, RevokeOtherSessionsV2)
	r.DELETE("/api/v2/:tenant_slug/sessions/:id", mockAuth, RevokeSessionByIDV2)

	t.Cleanup(func() {
		repo.DB = prevDB
		common.RedisEnabled = prevRedisEnabled
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return &sessionRevokeDBCtx{router: r, db: db}
}

func seedRevokeTestSession(t *testing.T, db *gorm.DB, key string, userID int) *entity.UserSession {
	t.Helper()
	now := common.GetTimestamp()
	row := &entity.UserSession{
		SessionKey: key, UserId: userID, TenantId: "default",
		CreatedAt: now, LastSeenAt: now,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed session %s: %v", key, err)
	}
	return row
}

// TestV2SessionRevokeByID_NotOwned404: a session id belonging to a DIFFERENT
// user 404s exactly like a nonexistent id (IDOR — the caller cannot use the
// status code to enumerate other users' session ids), and the row itself is
// left untouched (revoked_at stays 0).
func TestV2SessionRevokeByID_NotOwned404(t *testing.T) {
	ctx := setupSessionRevokeDBRouter(t, 100, "")
	victimRow := seedRevokeTestSession(t, ctx.db, "sess-victim", 200) // owned by user 200, not the caller (100)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/acme/sessions/%d", victimRow.Id), nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "SESSION_NOT_FOUND" {
		t.Errorf("error_code = %v, want SESSION_NOT_FOUND", resp["error_code"])
	}

	var reloaded entity.UserSession
	if err := ctx.db.Where("id = ?", victimRow.Id).First(&reloaded).Error; err != nil {
		t.Fatalf("reload victim row: %v", err)
	}
	if reloaded.RevokedAt != 0 {
		t.Errorf("victim row revoked_at = %d, want 0 — a not-owned 404 must not touch the row", reloaded.RevokedAt)
	}
}

// TestV2SessionRevokeByID_OwnedSucceeds: the mirror positive case — revoking
// the caller's own session id succeeds (204) and the row is revoked with
// reason "user_revoked".
func TestV2SessionRevokeByID_OwnedSucceeds(t *testing.T) {
	ctx := setupSessionRevokeDBRouter(t, 100, "")
	row := seedRevokeTestSession(t, ctx.db, "sess-mine", 100)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/acme/sessions/%d", row.Id), nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	var reloaded entity.UserSession
	if err := ctx.db.Where("id = ?", row.Id).First(&reloaded).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if reloaded.RevokedAt == 0 || reloaded.RevokeReason != entity.SessionRevokeReasonUserRevoked {
		t.Errorf("row not revoked as expected: revoked_at=%d reason=%q", reloaded.RevokedAt, reloaded.RevokeReason)
	}
}

// TestV2SessionsOthers_KeepsCurrent: revoking "other devices" leaves the
// CALLER'S OWN current session (matched by session_key, via
// currentSessionID(c)) active, and revokes every other active session of
// that same user with reason "user_revoked_others", returning the count.
func TestV2SessionsOthers_KeepsCurrent(t *testing.T) {
	const userID = 100
	ctx := setupSessionRevokeDBRouter(t, userID, "sess-current")

	seedRevokeTestSession(t, ctx.db, "sess-current", userID)
	seedRevokeTestSession(t, ctx.db, "sess-other-1", userID)
	seedRevokeTestSession(t, ctx.db, "sess-other-2", userID)
	// A different user's session must never be touched by this call.
	otherUserRow := seedRevokeTestSession(t, ctx.db, "sess-stranger", 999)

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/acme/sessions/others", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["revoked"].(float64) != 2 {
		t.Errorf("revoked = %v, want 2", data["revoked"])
	}

	var current, other1, other2, stranger entity.UserSession
	ctx.db.Where("session_key = ?", "sess-current").First(&current)
	ctx.db.Where("session_key = ?", "sess-other-1").First(&other1)
	ctx.db.Where("session_key = ?", "sess-other-2").First(&other2)
	ctx.db.Where("id = ?", otherUserRow.Id).First(&stranger)

	if current.RevokedAt != 0 {
		t.Errorf("current session was revoked, must stay active: revoked_at=%d", current.RevokedAt)
	}
	for name, row := range map[string]entity.UserSession{"other1": other1, "other2": other2} {
		if row.RevokedAt == 0 || row.RevokeReason != entity.SessionRevokeReasonUserRevokedOthers {
			t.Errorf("%s not revoked as expected: revoked_at=%d reason=%q", name, row.RevokedAt, row.RevokeReason)
		}
	}
	if stranger.RevokedAt != 0 {
		t.Errorf("a different user's session was revoked by this call: revoked_at=%d", stranger.RevokedAt)
	}
}

// ---------------------------------------------------------------------------
// TestV2SessionRevokeByID_DeletesRedisKeyAnd401 — the full end-to-end
// probe: two real Redis-backed browser sessions (jars A and B) for the SAME
// user, driven through the REAL middleware.UserAuth() (not a mock), a
// revoke-by-id from A targeting B, then a request from B must 401. This is
// the defence-in-depth mutation target: removing RedisDel (v2_session_
// revoke.go) AND the revoked_at check (middleware/auth.go) TOGETHER makes
// this go red; removing either ALONE leaves the other mechanism to still
// force the 401 (proving neither is redundant on its own).
// ---------------------------------------------------------------------------

type sessionE2ECtx struct {
	router *gin.Engine
	db     *gorm.DB
	rdb    *redis.Client
}

func setupSessionE2ERouter(t *testing.T) *sessionE2ECtx {
	t.Helper()
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:sessione2e%d?mode=memory&cache=shared", sessionRevokeDBTestCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &entity.UserSession{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}
	if err := db.Create(&repo.Tenant{
		Id: "e2e-tenant", Slug: "e2e-tenant", Name: "E2E Tenant",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_e2e",
	}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	sessionStore, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("e2e-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedisEnabled := common.RedisEnabled
	prevRDB := common.RDB

	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = true // governs the throttle guard AND RedisDel
	common.RDB = rdb

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", sessionStore))
	r.GET("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		userId, _ := strconv.Atoi(c.Query("id"))
		s.Set("username", "e2e-tester")
		s.Set("role", common.RoleCommonUser)
		s.Set("id", userId)
		s.Set("status", common.UserStatusEnabled)
		if err := s.Save(); err != nil {
			t.Fatalf("save login session: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	r.GET("/probe", middleware.UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	r.GET("/api/v2/:tenant_slug/sessions", middleware.UserAuth(), ListSessionsV2)
	r.DELETE("/api/v2/:tenant_slug/sessions/:id", middleware.UserAuth(), RevokeSessionByIDV2)

	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedisEnabled
		common.RDB = prevRDB
		_ = rdb.Close()
		mr.Close()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return &sessionE2ECtx{router: r, db: db, rdb: rdb}
}

func (e *sessionE2ECtx) do(method, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *sessionE2ECtx) loginAndProbe(t *testing.T, userId int) string {
	t.Helper()
	loginW := e.do(http.MethodGet, fmt.Sprintf("/login?id=%d", userId), "")
	cookies := loginW.Header().Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("login produced no Set-Cookie header")
	}
	cookie := cookies[0]
	// Registration happens inside authHelper — the login route itself does
	// not run UserAuth(), so a real authenticated request is required for
	// the row to exist.
	probeW := e.do(http.MethodGet, "/probe", cookie)
	if probeW.Code != http.StatusOK {
		t.Fatalf("probe after login status = %d, body=%s", probeW.Code, probeW.Body.String())
	}
	return cookie
}

func TestV2SessionRevokeByID_DeletesRedisKeyAnd401(t *testing.T) {
	const userID = 7
	ctx := setupSessionE2ERouter(t)

	cookieA := ctx.loginAndProbe(t, userID)
	cookieB := ctx.loginAndProbe(t, userID)

	var rows []entity.UserSession
	if err := ctx.db.Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("registered rows = %d, want 2 (one per login)", len(rows))
	}
	bID := rows[1].Id // second login registered second — B's row.
	bSessionKey := rows[1].SessionKey

	// A revokes B's session by id.
	delW := ctx.do(http.MethodDelete, fmt.Sprintf("/api/v2/e2e-tenant/sessions/%d", bID), cookieA)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204, body=%s", delW.Code, delW.Body.String())
	}

	// UAT probe artefact 4 equivalent: the session store's own Redis key for
	// B ("session_"+B's session_key — the boj/redistore default prefix) is
	// gone under the REAL (unmutated) code path — logged, not asserted:
	// the mutation "remove RedisDel only" must leave this test GREEN via
	// the revoked_at check alone (see the 401 assertion below, which IS the
	// binding oracle), so this artefact cannot be a hard failure here or it
	// would defeat that half of the defence-in-depth proof.
	if n, err := ctx.rdb.Exists(context.Background(), "session_"+bSessionKey).Result(); err != nil {
		t.Logf("redis EXISTS session_%s: %v", bSessionKey, err)
	} else {
		t.Logf("redis key session_%s exists=%v after revoke (artefact 4)", bSessionKey, n != 0)
	}

	// B's cookie must now be rejected — this is the artefact the mutation
	// targets. The exact rejection branch differs by which defence fired
	// (RedisDel gone -> "no session data" 401; revoked_at check gone but
	// RedisDel intact -> same outcome via RedisDel; revoked_at check alone
	// intact with RedisDel intact -> SESSION_REVOKED) but the status code
	// must always be 401.
	probeB := ctx.do(http.MethodGet, "/probe", cookieB)
	if probeB.Code != http.StatusUnauthorized {
		t.Fatalf("probe as B after revoke status = %d, want 401, body=%s", probeB.Code, probeB.Body.String())
	}

	// A's own session must be unaffected.
	probeA := ctx.do(http.MethodGet, "/probe", cookieA)
	if probeA.Code != http.StatusOK {
		t.Errorf("probe as A after revoking B's session status = %d, want 200, body=%s", probeA.Code, probeA.Body.String())
	}
}
