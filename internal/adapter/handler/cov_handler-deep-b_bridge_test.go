package handler

// cov_handler-deep-b_bridge_test.go — closes the one remaining branch in
// v2_bridge.go: the session.Save() error path. gorilla/securecookie caps an
// encoded cookie at 4096 bytes by default; a user row with a pathologically
// long username (the kind that can arrive via an external IDP import or a
// direct DB seed that bypasses app-level length validation — not a
// synthetic nil/zero value) pushes the serialized session over that cap, so
// session.Save() genuinely fails and the handler must surface 500 rather
// than silently dropping the session.
//
// Updated in cycle-13 L8 (repair r2, D-L8-8). BridgeExchange now retires the
// pre-authentication session id (middleware.RotateSessionID) BEFORE it
// writes the identity, and that rotation Saves once itself — a single
// timestamp, far inside the 4096-byte cap — so it succeeds. The failing
// exchange therefore answers with one Set-Cookie where it used to answer
// with none, and the invariant had to be restated rather than dropped: the
// cookie that IS written carries the rotated, identity-free session, and the
// identity whose Save failed is persisted nowhere. Both halves are asserted
// below against a replay of the actual cookie, with a successful exchange as
// the positive control so "the probe saw no identity" cannot pass because
// the probe sees nothing at all.
//
// Reuses buildBridgeRouter from v2_bridge_test.go (same package).

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// bridgeSessionProbe is an engine over the SAME cookie store the bridge
// fixture uses (buildBridgeRouter: name "test_session", secret
// "bridge-test-secret"), so a cookie the handler wrote can be replayed and
// read back. A cookie store keeps no server-side state — the cookie IS the
// session — so this reads exactly what the browser would send back.
func bridgeSessionProbe() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("bridge-test-secret"))
	r.Use(sessions.Sessions("test_session", store))
	r.GET("/probe", func(c *gin.Context) {
		s := sessions.Default(c)
		c.JSON(http.StatusOK, gin.H{
			"id":       s.Get("id"),
			"username": s.Get("username"),
			"role":     s.Get("role"),
			"status":   s.Get("status"),
			"group":    s.Get("group"),
		})
	})
	return r
}

// replayBridgeSession sends ck to the probe and returns what the session
// behind it carries.
func replayBridgeSession(t *testing.T, ck *http.Cookie) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	bridgeSessionProbe().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("session probe status = %d body=%s", w.Code, w.Body.String())
	}
	return ParseV2Response(t, w)
}

// sessionCookieFrom returns the session cookie a browser would be left
// holding after w — the LAST test_session write, since each Save overwrites
// the previous one — and fails when the number of writes is not the
// expected one.
func sessionCookieFrom(t *testing.T, w *httptest.ResponseRecorder, want int) *http.Cookie {
	t.Helper()
	raw := w.Header().Values("Set-Cookie")
	if len(raw) != want {
		t.Fatalf("Set-Cookie written %d time(s), want %d: %v", len(raw), want, raw)
	}
	var last *http.Cookie
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
		if ck.Name == "test_session" {
			last = ck
		}
	}
	if last == nil {
		t.Fatalf("no test_session cookie in %v", raw)
	}
	return last
}

func TestBridgeExchange_SessionSaveOversizeCookie(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Setenv("E2E_BRIDGE_TOKEN", "correct-token")
	r := buildBridgeRouter()

	// Positive control: an ordinary user's exchange succeeds, and the cookie
	// it writes replays into a session the probe can read. Without this the
	// "no identity" assertions below would also pass against a probe that
	// cannot decode anything (a changed cookie name or secret).
	ordinary := &repo.User{
		Username:    "bridge-control-user",
		DisplayName: "Bridge Control User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "bridge-control@test.local",
		TenantId:    ctx.TenantID,
	}
	if err := ctx.DB.Create(ordinary).Error; err != nil {
		t.Fatalf("seed control user: %v", err)
	}
	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, httptest.NewRequest(http.MethodPost,
		"/api/v2/bridge/exchange?token=correct-token&user_id="+strconv.Itoa(ordinary.Id), nil))
	if cw.Code != http.StatusOK {
		t.Fatalf("control exchange status = %d, want 200: %s", cw.Code, cw.Body.String())
	}
	// Two writes on the success path: the rotation's Save and the identity's.
	control := replayBridgeSession(t, sessionCookieFrom(t, cw, 2))
	if got, want := control["id"], float64(ordinary.Id); got != want {
		t.Fatalf("the probe read id=%v from a SUCCESSFUL exchange's cookie, want %v — the probe cannot see identities, "+
			"so it cannot testify that the failed exchange persisted none", got, want)
	}

	// gorilla/securecookie's default MaxLength is 4096 bytes for the whole
	// encoded cookie (base64 + HMAC + gob overhead); a 6000-byte username
	// alone is comfortably over that once the session is serialized.
	hugeUsername := strings.Repeat("u", 6000)
	oversized := &repo.User{
		Username:    hugeUsername,
		DisplayName: "Oversized Session User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "oversize@test.local",
		TenantId:    ctx.TenantID,
	}
	if err := ctx.DB.Create(oversized).Error; err != nil {
		t.Fatalf("seed oversized-username user: %v", err)
	}

	url := "/api/v2/bridge/exchange?token=correct-token&user_id=" + strconv.Itoa(oversized.Id)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (session.Save() must fail on an oversized cookie), body: %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "failed to persist session" {
		t.Errorf("message = %v, want 'failed to persist session'", resp["message"])
	}

	// Exactly one Set-Cookie: the rotation's. The identity Save is the only
	// other writer in this handler and it is the one that just failed, so a
	// second cookie here would mean the oversized identity reached the
	// browser after all.
	ck := sessionCookieFrom(t, w, 1)
	if len(ck.Value) >= 4096 {
		t.Errorf("the surviving cookie is %d bytes — it cannot be the rotated empty session, which holds one timestamp", len(ck.Value))
	}
	after := replayBridgeSession(t, ck)
	for _, key := range []string{"id", "username", "role", "status", "group"} {
		if after[key] != nil {
			t.Errorf("the cookie written by the failed exchange carries %s=%v — Save() failed, so no part of that identity may be persisted",
				key, after[key])
		}
	}
}
