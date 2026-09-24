package middleware

// auth_wire_language_test.go — cycle14 L4 (wire messages), defect (5).
//
// resolveSessionIdentity writes its refusals with c.JSON directly, so the
// package's wire-message language gate could not see them (it matched error
// CONSTRUCTORS only until this cycle). The operator pulled this 401 off a
// live instance:
//
//	{"success":false,"message":"无权进行此操作，未登录且未提供 access token"}
//
// The split this file pins is by AUDIENCE, not by file:
//
//   - The three 401s (no credentials, bad user-id header, mismatched user-id
//     header) are answers to a MACHINE. The first two are reachable before any
//     identity exists — a bare curl against /api/... hits the first, and the
//     other two only fire for a caller that sends the lurus-api-User /
//     New-Api-User header at all, which is an API client's habit, not a
//     browser's. They are also the only three of the eight that no other file
//     keys on. Those are ASCII now.
//
//   - The HTTP 200 refusals (access token 无效 / 用户信息无效 / 用户已被封禁 /
//     权限不足) stay Chinese, deliberately. They are the v1 console envelope,
//     and three separate consumers key on the exact bytes:
//     middleware/admin_jwt_auth.go's rootSessionDenialsByMessage (which maps
//     the message to the v2 status + error_code, and would silently downgrade
//     every unrecognised one to 403 PERMISSION_DENIED),
//     web/src/helpers/loadState.js's PERMISSION_REFUSAL_MESSAGES, and the
//     router tests in internal/adapter/handler/router. Translating them from
//     this lane would break a classifier this lane does not own; the last test
//     here pins that they were left alone on purpose rather than by oversight.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// l4AuthRouter mounts the given auth middleware behind a cookie session store
// seeded with sessionValues (nil = an anonymous caller).
func l4AuthRouter(sessionValues map[string]interface{}, mw func() func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("l4-wire-language-secret"))))
	r.Use(func(c *gin.Context) {
		if len(sessionValues) > 0 {
			s := sessions.Default(c)
			for k, v := range sessionValues {
				s.Set(k, v)
			}
			_ = s.Save()
		}
		c.Next()
	})
	r.GET("/api/probe", mw(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func l4AuthCall(r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func l4EnvelopeMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", w.Body.String(), err)
	}
	if env.Success {
		t.Fatalf("envelope %q reports success on a refusal", w.Body.String())
	}
	return env.Message
}

// The operator's own 401. Asserted as the whole body, because the shape
// ({"message":...,"success":false}) is as much part of the contract as the
// sentence is.
func TestUserAuth_NoCredentials_WireMessageIsASCII(t *testing.T) {
	w := l4AuthCall(l4AuthRouter(nil, UserAuth), nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	const want = `{"message":"Not authenticated: no session and no access token provided","success":false}`
	if got := w.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestUserAuth_UnparsableUserIdHeader_WireMessageIsASCII(t *testing.T) {
	r := l4AuthRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 7, "status": common.UserStatusEnabled,
	}, UserAuth)
	w := l4AuthCall(r, map[string]string{"New-Api-User": "not-a-number"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	const want = "Invalid user id header: expected an integer"
	if got := l4EnvelopeMessage(t, w); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestUserAuth_MismatchedUserIdHeader_WireMessageIsASCII(t *testing.T) {
	r := l4AuthRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 7, "status": common.UserStatusEnabled,
	}, UserAuth)
	w := l4AuthCall(r, map[string]string{"New-Api-User": "8"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	const want = "User id header does not match the authenticated session"
	if got := l4EnvelopeMessage(t, w); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// The other half of the decision, pinned so a later lane reading "L4 made the
// auth messages ASCII" does not finish the job and break the classifier:
// the role-shortfall refusal is HTTP 200 with the Chinese sentence
// admin_jwt_auth.go's rootSessionDenialsByMessage keys on. If this ever needs
// to change, rootSessionDenialsByMessage (and loadState.js, and the router
// tests) must change in the same commit.
func TestAdminAuth_RoleShortfall_StaysTheV1ConsoleEnvelope(t *testing.T) {
	// Unlike the three 401s above, this branch runs past the per-request
	// re-validation, which reads the user cache — so it needs a DB. There is
	// no row for id 7, the lookup fails, and the documented fail-open keeps
	// the session's own role, which is what produces the shortfall.
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4AuthRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 7, "status": common.UserStatusEnabled,
	}, AdminAuth)
	w := l4AuthCall(r, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want the v1 200-shaped refusal; body=%s", w.Code, w.Body.String())
	}
	want := rootSessionDenialsByMessage
	got := l4EnvelopeMessage(t, w)
	if _, ok := want[got]; !ok {
		t.Errorf("message = %q is not a key of rootSessionDenialsByMessage — the v2 rewrite will now classify it as the fallback 403 PERMISSION_DENIED", got)
	}
}
