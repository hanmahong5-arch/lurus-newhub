package middleware

// root_jwt_denial_shape_test.go — L4 (cycle 12). RootJWTAuth's session
// branch used to hand a non-root console session HTTP 200 with
// {"success":false} (authHelper's v1 minRole convention). Every v2 admin
// page therefore saw a 2xx, ran its success path, and rendered a page that
// looked authorised-and-empty instead of refused — cycle-12 §1.2 item 6.
// This file pins the replacement wire shape branch by branch.
//
// Scope: the session branch only. The Bearer-JWT branch is unchanged and
// stays covered by oidc_admin_cover_test.go
// (TestRootJWTAuth_ValidRoot_Passes / TestRootJWTAuth_NonRoot_403) and
// rate_limit_factory_cover_test.go (TestRootJWTAuth_InvalidToken_401).
//
// v1 is deliberately untouched: authHelper still answers 200
// {"success":false} for the same session, because switch consumes that
// shape (cycle-12 §6 do-not-regress). TestRootDenialShape_V1AuthHelperShapeUnchanged
// below is the guard against "fix the shape everywhere" creep.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// mountRootJWTSession wires a cookie-session engine that pre-seeds the
// session (nil = anonymous) and then runs RootJWTAuth with no Authorization
// header, i.e. exactly the console's path onto /api/v2/admin/*.
func mountRootJWTSession(preset map[string]any) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("root-jwt-denial-shape-secret"))
	r.Use(sessions.Sessions("session", store))
	if preset != nil {
		r.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			for k, v := range preset {
				s.Set(k, v)
			}
			_ = s.Save()
			c.Next()
		})
	}
	r.GET("/admin/probe", RootJWTAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": true}})
	})
	return r
}

func doRootJWTProbe(engine *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/probe", nil))
	return w
}

func rootDenialSession(role, status int) map[string]any {
	return map[string]any{
		"username": "denial-shape-actor",
		"role":     role,
		"status":   status,
		"id":       1,
	}
}

func TestRootJWTAuth_SessionDenialShape(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	cases := []struct {
		name       string
		preset     map[string]any
		wantStatus int
		wantCode   string
		// wantMessagePart, when set, must appear in the body: it proves the
		// refusal still says WHICH refusal it is.
		wantMessagePart  string
		wantReachHandler bool
	}{
		{
			name:       "anonymous_401_unauthenticated",
			preset:     nil,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
		},
		{
			name:       "admin_role_10_forbidden",
			preset:     rootDenialSession(common.RoleAdminUser, common.UserStatusEnabled),
			wantStatus: http.StatusForbidden,
			wantCode:   "PERMISSION_DENIED",
		},
		{
			name:       "common_role_1_forbidden",
			preset:     rootDenialSession(common.RoleCommonUser, common.UserStatusEnabled),
			wantStatus: http.StatusForbidden,
			wantCode:   "PERMISSION_DENIED",
		},
		{
			name:            "banned_root_forbidden_keeps_its_reason",
			preset:          rootDenialSession(common.RoleRootUser, common.UserStatusDisabled),
			wantStatus:      http.StatusForbidden,
			wantCode:        "PERMISSION_DENIED",
			wantMessagePart: "封禁",
		},
		{
			name:             "root_role_100_admitted",
			preset:           rootDenialSession(common.RoleRootUser, common.UserStatusEnabled),
			wantStatus:       http.StatusOK,
			wantReachHandler: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRootJWTProbe(mountRootJWTSession(tc.preset))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
			}

			var env map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
			}
			if tc.wantReachHandler {
				if success, _ := env["success"].(bool); !success {
					t.Fatalf("admitted request: success=false; body=%s", w.Body.String())
				}
				return
			}

			if success, _ := env["success"].(bool); success {
				t.Fatalf("refused request: success=true; body=%s", w.Body.String())
			}
			if code, _ := env["error_code"].(string); code != tc.wantCode {
				t.Fatalf("error_code = %q, want %q; body=%s", code, tc.wantCode, w.Body.String())
			}
			if _, hasData := env["data"]; hasData {
				t.Errorf("refused request carries a data object — the handler ran; body=%s", w.Body.String())
			}
			if tc.wantMessagePart != "" {
				if msg, _ := env["message"].(string); !strings.Contains(msg, tc.wantMessagePart) {
					t.Errorf("message = %q, want it to contain %q (a banned root must not be told it merely lacks a role)",
						msg, tc.wantMessagePart)
				}
			}
			// The refusal must be ONE response. The translation buffers
			// authHelper's own body before re-emitting it; a buffer that also
			// got flushed would show up here as two concatenated envelopes.
			if n := strings.Count(w.Body.String(), `"success"`); n != 1 {
				t.Errorf("body contains %d \"success\" keys, want exactly 1 (double-written response); body=%s", n, w.Body.String())
			}
		})
	}
}

// TestRootDenialShape_V1AuthHelperShapeUnchanged is the do-not-regress half:
// the SAME role-10 session through v1's RootAuth still gets the 200
// {"success":false} envelope switch consumes. If a later change "fixes" the
// shape in authHelper instead of at the v2 edge, this fails.
func TestRootDenialShape_V1AuthHelperShapeUnchanged(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("root-jwt-denial-shape-v1-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range rootDenialSession(common.RoleAdminUser, common.UserStatusEnabled) {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	r.GET("/v1/probe", RootAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/probe", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("v1 RootAuth status = %d, want 200 (v1's minRole refusal shape is load-bearing for switch); body=%s",
			w.Code, w.Body.String())
	}
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if success, _ := env["success"].(bool); success {
		t.Fatalf("v1 RootAuth admitted a role-10 session; body=%s", w.Body.String())
	}
	if _, hasCode := env["error_code"]; hasCode {
		t.Errorf("v1 refusal grew an error_code — that is the v2 shape leaking into v1; body=%s", w.Body.String())
	}
}
