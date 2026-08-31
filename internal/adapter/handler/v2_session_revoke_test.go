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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
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
