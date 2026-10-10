package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var shadowDBCounter atomic.Int64

// shadowEnv wires: sqlite with mappings (user 1 -> sub-1, user 2 -> sub-2),
// a fake platform whose status/admin flag is controllable, and a hit counter.
type shadowEnv struct {
	hits   atomic.Int64
	status atomic.Int64
	admin  atomic.Bool
	srv    *httptest.Server
}

func setupShadowEnv(t *testing.T) *shadowEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:root_shadow_%d?mode=memory&cache=shared", shadowDBCounter.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.UserIdentityMapping{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2; i++ {
		db.Create(&entity.UserIdentityMapping{LurusUserID: i, IDPSubject: fmt.Sprintf("sub-%d", i), TenantID: "default", IsActive: true})
	}
	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB, common.RedisEnabled = db, false

	e := &shadowEnv{}
	e.status.Store(200)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		st := int(e.status.Load())
		w.WriteHeader(st)
		if st == 200 {
			_, _ = fmt.Fprintf(w, `{"id":1,"is_platform_admin":%v}`, e.admin.Load())
		}
	}))
	prevURL, prevKey := common.IdentityServiceURL, common.IdentityServiceInternalKey
	common.IdentityServiceURL, common.IdentityServiceInternalKey = e.srv.URL, "k"
	shadowResetCache()
	t.Setenv("PLATFORM_ROOT_SHADOW", "")
	t.Cleanup(func() {
		e.srv.Close()
		common.IdentityServiceURL, common.IdentityServiceInternalKey = prevURL, prevKey
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		if s, _ := db.DB(); s != nil {
			_ = s.Close()
		}
		shadowResetCache()
	})
	return e
}

func TestShadowCheck_Results(t *testing.T) {
	e := setupShadowEnv(t)
	ctx := context.Background()
	check := func(name string, uid int, want string) {
		t.Helper()
		shadowResetCache()
		if got := shadowCheckPlatformRoot(ctx, uid); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
	e.admin.Store(true)
	check("match", 1, shadowMatch)
	e.admin.Store(false)
	check("mismatch", 1, shadowMismatch)
	e.status.Store(404)
	check("not_found", 1, shadowNotFound)
	e.status.Store(500)
	check("unavailable 500", 1, shadowUnavailable)
	check("no_sub", 99, shadowNoSub)

	common.IdentityServiceURL = ""
	check("not_configured", 1, shadowNotConfigured)
	common.IdentityServiceURL = e.srv.URL

	e.srv.Close()
	check("unavailable closed", 1, shadowUnavailable)

	t.Setenv("PLATFORM_ROOT_SHADOW", "false")
	check("disabled", 1, shadowDisabled)
	t.Setenv("PLATFORM_ROOT_SHADOW", "0")
	check("disabled 0", 1, shadowDisabled)
}

func TestShadowCheck_Cache(t *testing.T) {
	e := setupShadowEnv(t)
	e.admin.Store(true)
	ctx := context.Background()
	shadowCheckPlatformRoot(ctx, 1)
	shadowCheckPlatformRoot(ctx, 1)
	if h := e.hits.Load(); h != 1 {
		t.Fatalf("same user twice: hits=%d want 1", h)
	}
	shadowCheckPlatformRoot(ctx, 2)
	if h := e.hits.Load(); h != 2 {
		t.Fatalf("second user: hits=%d want 2", h)
	}
	// expiry forces a refetch
	shadowMu.Lock()
	shadowCache[1] = shadowEntry{result: shadowMatch, expires: time.Now().Add(-time.Second)}
	shadowMu.Unlock()
	shadowCheckPlatformRoot(ctx, 1)
	if h := e.hits.Load(); h != 3 {
		t.Fatalf("after expiry: hits=%d want 3", h)
	}
}

func shadowRouter(sess map[string]interface{}, reached *atomic.Bool) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("shadow-secret"))))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range sess {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	r.GET("/root", RootAuth(), func(c *gin.Context) {
		reached.Store(true)
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return r
}

func shadowSession(role, id int) map[string]interface{} {
	return map[string]interface{}{"username": "u", "role": role, "id": id, "status": common.UserStatusEnabled}
}

// Invariance: whatever the platform says, a root session is still admitted
// with the handler's own 200 body.
func TestRootAuth_ShadowNeverChangesAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int64
		admin  bool
	}{{"mismatch", 200, false}, {"unavailable", 500, false}, {"match", 200, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupShadowEnv(t)
			e.status.Store(tc.status)
			e.admin.Store(tc.admin)
			var reached atomic.Bool
			w := httptest.NewRecorder()
			shadowRouter(shadowSession(common.RoleRootUser, 1), &reached).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/root", nil))
			if w.Code != http.StatusOK || !reached.Load() {
				t.Fatalf("root blocked: code=%d reached=%v body=%s", w.Code, reached.Load(), w.Body.String())
			}
			if w.Body.String() != `{"success":true}` {
				t.Errorf("body altered: %s", w.Body.String())
			}
			if h := e.hits.Load(); h != 1 {
				t.Fatalf("shadow check reached the fake platform %d times, want 1 (synchronous)", h)
			}
		})
	}
}

func TestRootAuth_NonRootRejectedAndNoShadowHTTP(t *testing.T) {
	e := setupShadowEnv(t)
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		var reached atomic.Bool
		w := httptest.NewRecorder()
		shadowRouter(shadowSession(role, 1), &reached).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/root", nil))
		if reached.Load() {
			t.Fatalf("role %d reached root handler", role)
		}
	}
	if h := e.hits.Load(); h != 0 {
		t.Fatalf("non-root triggered %d shadow HTTP calls", h)
	}
}

// A panic inside the background check (nil DB) must be contained.
func TestPlatformRootShadow_PanicContained(t *testing.T) {
	setupShadowEnv(t)
	prev := repo.DB
	repo.DB = nil
	defer func() { repo.DB = prev }()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 1)
	platformRootShadow(c) // would have panicked the test without recover
}
