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
