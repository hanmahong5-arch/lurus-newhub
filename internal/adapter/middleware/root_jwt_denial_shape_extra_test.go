package middleware

// root_jwt_denial_shape_extra_test.go — L4 (cycle 12), repair round. The
// rows here are the ones the first round asserted in prose and nothing
// checked:
//
//   - the classification table covers EVERY 200-shaped refusal auth.go can
//     write (parsed out of auth.go, not hand-listed)
//   - the stale-cookie-clearing Set-Cookie survives the capture
//   - SESSION_REVOKED survives the rewrite (it is not flattened into
//     UNAUTHENTICATED)
//   - a panic inside the resolution restores c.Writer, so gin.Recovery's 500
//     reaches the client instead of landing in the discarded buffer
//
// They live beside root_jwt_denial_shape_test.go rather than inside it
// because they need their own Redis-backed session store: a cookie-only
// store never assigns a session id, and both cookie-clearing branches in
// auth.go are gated on `session.ID() != ""`.

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"sort"
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
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 1. Completeness: the classification table vs auth.go's actual branches
// ---------------------------------------------------------------------------

// twoHundredRefusalMessages parses auth.go and returns the message string of
// every `c.JSON(http.StatusOK, gin.H{...})` inside resolveSessionIdentity —
// i.e. every refusal that leaves that function with a 2xx and
// {"success":false}. Parsed rather than grepped so a reformat, a line break
// or a different key order cannot hide one.
func twoHundredRefusalMessages(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "auth.go", nil, 0)
	if err != nil {
		t.Fatalf("parse auth.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "resolveSessionIdentity" {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("auth.go has no resolveSessionIdentity — this gate cannot verify anything; fix the gate rather than deleting it")
	}

	var messages []string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "JSON" {
			return true
		}
		status, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok || status.Sel.Name != "StatusOK" {
			return true
		}
		lit, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			t.Errorf("c.JSON(http.StatusOK, <non-literal>) at %s — this gate can only read composite literals",
				fset.Position(call.Pos()))
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok || key.Value != `"message"` {
				continue
			}
			val, ok := kv.Value.(*ast.BasicLit)
			if !ok || val.Kind != token.STRING {
				t.Errorf("non-literal message in a 200 refusal at %s — classify it by hand and extend this gate",
					fset.Position(kv.Pos()))
				continue
			}
			unquoted, unqErr := strconv.Unquote(val.Value)
			if unqErr != nil {
				t.Fatalf("unquote %s: %v", val.Value, unqErr)
			}
			messages = append(messages, unquoted)
		}
		return true
	})
	sort.Strings(messages)
	return messages
}

// TestRootSessionDenials_CoverEveryTwoHundredRefusal is the completeness gate
// the first round did not have: its author enumerated three of auth.go's five
// 200-shaped refusals by hand and mapped all of them to one status. A missing
// row is now a red test, not a silently wrong answer on a live route.
func TestRootSessionDenials_CoverEveryTwoHundredRefusal(t *testing.T) {
	messages := twoHundredRefusalMessages(t)
	if len(messages) == 0 {
		t.Fatal("found 0 HTTP 200 refusals in resolveSessionIdentity — either auth.go stopped answering 200 on refusal (delete this gate and the translation with it) or the parse is broken")
	}

	seen := map[string]bool{}
	var unclassified []string
	for _, msg := range messages {
		seen[msg] = true
		if _, ok := rootSessionDenialsByMessage[msg]; !ok {
			unclassified = append(unclassified, msg)
		}
	}
	if len(unclassified) > 0 {
		t.Errorf("auth.go 200-shaped refusals with no entry in rootSessionDenialsByMessage: %q\n"+
			"Each one currently falls back to %d/%s. Decide per branch: an invalid credential is 401 UNAUTHENTICATED, a refused-but-valid caller is 403.",
			unclassified, rootSessionDenialFallback.status, rootSessionDenialFallback.errorCode)
	}

	// Reverse direction: a classification for a message auth.go no longer
	// writes is stale, and stale entries are how a table drifts into fiction.
	for msg := range rootSessionDenialsByMessage {
		if !seen[msg] {
			t.Errorf("rootSessionDenialsByMessage classifies %q but resolveSessionIdentity never writes it — auth.go's wording changed; the live refusal is now falling through to the %d fallback",
				msg, rootSessionDenialFallback.status)
		}
	}

	// Every classified refusal must actually be a refusal.
	for msg, denial := range rootSessionDenialsByMessage {
		if denial.status < 400 {
			t.Errorf("rootSessionDenialsByMessage[%q].status = %d — a refusal must not be a 2xx/3xx", msg, denial.status)
		}
		if denial.errorCode == "" {
			t.Errorf("rootSessionDenialsByMessage[%q] has no error_code — the console branches on it", msg)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. A real session store, so session.ID() is non-empty
// ---------------------------------------------------------------------------

var l4RootSessionDBCounter atomic.Int64

type l4RootSessionCtx struct {
	router *gin.Engine
	db     *gorm.DB
	mr     *miniredis.Miniredis
}

// newL4RootSessionCtx wires a Redis-backed session store (real session ids),
// a sqlite repo.DB carrying users + user_sessions, and RootJWTAuth in front
// of a terminal handler. /login mints a session and returns its id so a test
// can seed a registry row for exactly that key.
func newL4RootSessionCtx(t *testing.T) *l4RootSessionCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "file:l4rootsession" + strconv.FormatInt(l4RootSessionDBCounter.Add(1), 10) + "?mode=memory&cache=shared"
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

	prevDB, prevSQLite, prevPG, prevRedis := repo.DB, common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	// Governs GetUserCache's Redis path, not the session store below (which
	// holds its own client) — same split as auth_test.go's harness.
	common.RedisEnabled = false

	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte("l4-root-session-secret"))
	if err != nil {
		t.Fatalf("new redis session store: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", store))
	r.GET("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", "l4-root-session")
		s.Set("role", common.RoleRootUser)
		s.Set("id", 4001)
		s.Set("status", common.UserStatusEnabled)
		if saveErr := s.Save(); saveErr != nil {
			t.Errorf("save login session: %v", saveErr)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"sid": s.ID()}})
	})
	r.GET("/admin/probe", RootJWTAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
	})

	t.Cleanup(func() {
		repo.DB, common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevDB, prevSQLite, prevPG, prevRedis
		mr.Close()
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return &l4RootSessionCtx{router: r, db: db, mr: mr}
}

// login returns the Set-Cookie header to replay and the session id behind it.
func (ctx *l4RootSessionCtx) login(t *testing.T) (string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", w.Code, w.Body.String())
	}
	cookies := w.Header().Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("login produced no Set-Cookie header")
	}
	var env struct {
		Data struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode login body: %v; raw=%s", err, w.Body.String())
	}
	if env.Data.SID == "" {
		t.Fatalf("login did not report a session id — the store assigned none; body=%s", w.Body.String())
	}
	return cookies[0], env.Data.SID
}

func (ctx *l4RootSessionCtx) probe(cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// TestRootJWTAuth_StaleCookieClearingSetCookieSurvivesTheCapture drives
// auth.go's "cookie carries a session id but the session data is gone"
// branch through rootSessionAuth. That branch clears the cookie
// (session.Clear + SessionClearOptions + Save) BEFORE writing its 401, and
// the clearing Set-Cookie has to survive the response translation — without
// it the browser keeps replaying the same dead cookie and can never log back
// in (the lockout cycle 11 fixed).
//
// This asserts the PROPERTY, not a mechanism. Measured, not assumed: making
// sessionDenialCapture override Header() does not drop the cookie (the
// session store writes through the writer it captured when the session
// middleware ran, not through c.Writer as it stands during the resolution),
// so that mutation leaves this green. The falsifier is removing the clear in
// auth.go, which turns this red with "no Set-Cookie for the session cookie".
func TestRootJWTAuth_StaleCookieClearingSetCookieSurvivesTheCapture(t *testing.T) {
	withOIDCOff(t)
	ctx := newL4RootSessionCtx(t)

	cookie, _ := ctx.login(t)
	// Server-side session data gone, cookie still held by the browser: the
	// exact state a remote revoke's Redis DEL leaves behind.
	ctx.mr.FlushAll()

	w := ctx.probe(cookie)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a cookie whose session data is gone; body=%s", w.Code, w.Body.String())
	}

	resp := http.Response{Header: w.Header()}
	var cleared *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" {
			cleared = ck
		}
	}
	if cleared == nil {
		t.Fatalf("no Set-Cookie for the session cookie on the 401 — nothing cleared the dead cookie, so this browser replays it forever and can never log back in; headers=%v", w.Header())
	}
	if cleared.MaxAge >= 0 && cleared.Value != "" {
		t.Fatalf("Set-Cookie = %q (MaxAge=%d) — want an expiring cookie (Max-Age=0/negative) so the next login starts from an empty cookie",
			cleared.String(), cleared.MaxAge)
	}
}

// TestRootJWTAuth_SessionRevokedCodeSurvivesTheRewrite: the registry's
// revoked-session 401 already carries error_code SESSION_REVOKED, and the
// rewrite must leave it alone. If it were overwritten with UNAUTHENTICATED
// the console could no longer tell "an admin signed this device out" from
// "your session expired".
func TestRootJWTAuth_SessionRevokedCodeSurvivesTheRewrite(t *testing.T) {
	withOIDCOff(t)
	t.Setenv("SESSION_REGISTRY_ENABLED", "true")
	ctx := newL4RootSessionCtx(t)

	cookie, sid := ctx.login(t)
	now := common.GetTimestamp()
	if err := ctx.db.Create(&entity.UserSession{
		SessionKey:   sid,
		UserId:       4001,
		TenantId:     "default",
		AuthMethod:   "session",
		CreatedAt:    now,
		LastSeenAt:   now,
		RevokedAt:    now,
		RevokeReason: entity.SessionRevokeReasonAdminRevoked,
	}).Error; err != nil {
		t.Fatalf("seed revoked session row: %v", err)
	}

	w := ctx.probe(cookie)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a revoked session; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error_code":"SESSION_REVOKED"`) {
		t.Fatalf("body = %s, want error_code SESSION_REVOKED preserved through the v2 rewrite", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 3. Panic safety of the capture
// ---------------------------------------------------------------------------

// TestRootSessionAuth_PanicRestoresWriter: resolveSessionIdentity reaches
// repo.GetUserCache, repo.IsUserSessionRevoked, repo.TenantGate and two
// identity lookups, every one of which can panic on a nil map or pointer.
// If the capture were still installed when gin.Recovery wrote its 500, the
// 500 would land in the discarded buffer and the client would get a bare 200
// with an empty body — a 2xx on failure, the exact shape this lane removes.
//
// Mutation: drop the `defer` in captureSessionDenial (restore c.Writer after
// the call instead) and this goes red with status 200.
func TestRootSessionAuth_PanicRestoresWriter(t *testing.T) {
	prev := resolveRootSession
	resolveRootSession = func(c *gin.Context) bool {
		panic("simulated nil-map panic inside the session resolution")
	}
	t.Cleanup(func() { resolveRootSession = prev })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/admin/probe", RootJWTAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/probe", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a panic during session resolution must reach gin.Recovery's writer, not the discarded capture buffer (body=%q)",
			w.Code, w.Body.String())
	}
}
