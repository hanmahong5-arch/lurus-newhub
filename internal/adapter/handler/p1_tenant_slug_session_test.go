package handler

// p1_tenant_slug_session_test.go — the session half of the tenant-slug
// contract, live-diagnosed 2026-09-22 on hub.lurus.cn.
//
// The console puts a tenant slug in the path of every /api/v2/:tenant_slug/*
// call and gets that slug from whichever login established the session. When
// the browser has no slug stored (a fresh profile, cleared storage, a second
// tab) its only slug source that does NOT need a platform cookie is
// GET /api/v2/auth/session-info — and that endpoint read a session key only
// the OIDC callback ever wrote. Every bridge-established session therefore
// answered "" and the console fell back to a literal no tenant row carries,
// so TenantSlugGuard 404'd every panel (20 such 404s in one browser window,
// all on /api/v2/default/...).
//
// Two properties, asserted separately because each fixes a different half:
//   - ZitaBootstrap must store the slug in the session (new logins).
//   - GetSessionInfo must resolve it from the user's tenant when the session
//     does not carry it (every session that already exists — no re-login).
//
// The bootstrap property is read straight out of the session store rather
// than through GetSessionInfo, whose own fallback would otherwise paper over
// a bootstrap that stored nothing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// p1SlugRouter mounts the two handlers under test plus two probes: /peek
// reports the raw session key, /seed-session establishes a session carrying
// nothing but the user id — the shape every pre-fix login left behind.
func p1SlugRouter(t *testing.T, identity *zita.Identity, seedUserID int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("p1-tenant-slug-session-secret"))
	r.Use(sessions.Sessions("p1_tenant_slug_session", store))

	r.POST("/zita-bootstrap", func(c *gin.Context) {
		c.Set(zita.ContextKey, identity)
		ZitaBootstrap(c)
	})
	r.GET("/seed-session", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", seedUserID)
		if err := s.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	r.GET("/session-info", GetSessionInfo)
	r.GET("/peek", func(c *gin.Context) {
		s := sessions.Default(c)
		raw := s.Get("tenant_slug")
		slug, _ := raw.(string)
		c.JSON(http.StatusOK, gin.H{"present": raw != nil, "tenant_slug": slug})
	})
	return r
}

// p1Jar keeps the LAST Set-Cookie of each name, which is what a browser
// does. It matters here: ZitaBootstrap saves the session twice — once inside
// RotateSessionID (session_rotation.go, which must write a value for the new
// id to stick) and once with the identity — so a bootstrap response carries
// two cookies of the same name, and replaying them in order would hand the
// next request the first (rotation-only) one.
func p1Jar(jar []*http.Cookie, w *httptest.ResponseRecorder) []*http.Cookie {
	byName := map[string]*http.Cookie{}
	var order []string
	for _, ck := range append(append([]*http.Cookie{}, jar...), w.Result().Cookies()...) {
		if _, seen := byName[ck.Name]; !seen {
			order = append(order, ck.Name)
		}
		byName[ck.Name] = ck
	}
	out := make([]*http.Cookie, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

// p1Do replays the cookies a previous response set, so consecutive calls
// share one session the way a browser does.
func p1Do(r *gin.Engine, method, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func p1Body(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return m
}

// seedSlugUser creates a user in the "default"/"lurus" tenant — the live
// shape where the tenant id and its routing slug differ, which is the only
// shape that can tell "stored the slug" apart from "stored the id".
func p1SeedUser(t *testing.T, ctx *V2TestContext, username string, accountID *int64) *repo.User {
	t.Helper()
	user := &repo.User{
		Username: username, DisplayName: username, Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: username + "@test.local",
		TenantId: "default", Group: "default", LurusAccountID: accountID,
	}
	if err := ctx.DB.Create(user).Error; err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return user
}

func TestZitaBootstrap_StoresTenantSlugInSession(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenant := seedFallbackTenant(t, ctx)
	accountID := int64(920001)
	p1SeedUser(t, ctx, "p1-bootstrap-user", &accountID)

	r := p1SlugRouter(t, &zita.Identity{AccountID: accountID}, 0)

	w := p1Do(r, http.MethodPost, "/zita-bootstrap", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	cookies := p1Jar(nil, w)
	if len(cookies) == 0 {
		t.Fatal("bootstrap set no session cookie")
	}

	peek := p1Do(r, http.MethodGet, "/peek", cookies)
	body := p1Body(t, peek)
	if body["present"] != true {
		t.Fatalf("session carries no tenant_slug key after bootstrap — a second tab, or this one after localStorage is cleared, has no way to learn its routing slug (peek=%s)", peek.Body.String())
	}
	if body["tenant_slug"] != tenant.Slug {
		t.Errorf("session tenant_slug = %v, want %q (the ROUTING SLUG of tenant id %q, not the id itself)", body["tenant_slug"], tenant.Slug, tenant.Id)
	}
}

func TestGetSessionInfo_RecoversTenantSlugForASessionThatNeverStoredOne(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenant := seedFallbackTenant(t, ctx)
	user := p1SeedUser(t, ctx, "p1-existing-session-user", nil)

	r := p1SlugRouter(t, nil, user.Id)

	seeded := p1Do(r, http.MethodGet, "/seed-session", nil)
	if seeded.Code != http.StatusOK {
		t.Fatalf("seed-session status = %d (body=%s)", seeded.Code, seeded.Body.String())
	}
	cookies := p1Jar(nil, seeded)

	// Precondition: this session genuinely has no slug in it. Without this
	// the test could pass on a session that was never missing one.
	before := p1Body(t, p1Do(r, http.MethodGet, "/peek", cookies))
	if before["present"] == true {
		t.Fatalf("test setup broken: seeded session already carries tenant_slug=%v", before["tenant_slug"])
	}

	w := p1Do(r, http.MethodGet, "/session-info", cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("session-info status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	data, _ := p1Body(t, w)["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("session-info returned no data: %s", w.Body.String())
	}
	if data["tenant_slug"] != tenant.Slug {
		t.Fatalf("session-info tenant_slug = %v, want %q — an existing login cannot learn its own slug and every /api/v2/*/ panel 404s until the user re-logs in", data["tenant_slug"], tenant.Slug)
	}

	// Write-back: the recovered slug is persisted, so this costs one tenant
	// lookup per session rather than one per console request.
	after := p1Body(t, p1Do(r, http.MethodGet, "/peek", p1Jar(cookies, w)))
	if after["tenant_slug"] != tenant.Slug {
		t.Errorf("session tenant_slug after recovery = %v, want %q persisted", after["tenant_slug"], tenant.Slug)
	}
}

// The recovery must not invent a slug: a user whose tenant row is missing
// gets "" — the console leaves whatever it already had alone rather than
// adopting a value that is known to 404 (resolveTenantSlug's contract).
func TestGetSessionInfo_UnresolvableTenantLeavesSlugEmpty(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// No seedFallbackTenant here: the user points at a tenant id no row carries.
	user := p1SeedUser(t, ctx, "p1-orphan-tenant-user", nil)

	r := p1SlugRouter(t, nil, user.Id)
	cookies := p1Jar(nil, p1Do(r, http.MethodGet, "/seed-session", nil))

	w := p1Do(r, http.MethodGet, "/session-info", cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("session-info status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	data, _ := p1Body(t, w)["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("session-info returned no data: %s", w.Body.String())
	}
	if data["tenant_slug"] != "" {
		t.Errorf("tenant_slug = %v, want \"\" for a tenant that cannot be resolved", data["tenant_slug"])
	}
}
