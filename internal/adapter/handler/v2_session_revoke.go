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
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// RevokeCurrentSessionV2 terminates the calling user's current session.
// Route (registered in api-v2-router.go):
//
//	DELETE /api/v2/:tenant_slug/sessions/current
//	Auth: UserAuth middleware
//
// This is a single-device model: there is no per-session store, so revocation
// clears the gin session cookie and the platform lurus_session cookie. The
// caller is expected to navigate to /login after receiving the redirect
// field (/login is the SPA's only login route — the v2 route table has no
// /console/v2/login, which used to be hinted here and rendered NotFound).
//
// Why this is enough: both UserAuth (v1 session) and the OIDC bridge read
// the session from the same cookie. Clearing it makes every subsequent
// authenticated endpoint return 401 until the user logs in again.
func RevokeCurrentSessionV2(c *gin.Context) {
	userID := c.GetInt("id")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	// Clear the gin session store entry so middleware.UserAuth() rejects the
	// next request from this browser.
	session := sessions.Default(c)
	session.Clear()
	session.Options(sessions.Options{Path: "/", MaxAge: -1})
	_ = session.Save()

	// Expire the platform lurus_session cookie on both production (.lurus.cn)
	// and dev/local (host-only) variants. This mirrors ZitaLogout so the
	// identity bridge does not auto-re-login the user on the next /login visit.
	c.SetCookie(zita.SessionCookieName, "", -1, "/", ".lurus.cn", true, true)
	c.SetCookie(zita.SessionCookieName, "", -1, "/", "", true, true)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"redirect": "/login",
		},
	})
}
