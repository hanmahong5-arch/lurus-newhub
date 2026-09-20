package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/app/governance"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// ZitaLogout symmetrically terminates a Layer C bridged session by:
//  1. Clearing the Hub gin session so middleware.UserAuth() rejects the
//     next request.
//  2. Expiring the platform-issued lurus_session cookie on both the parent
//     domain (.lurus.cn, production) and the host-only domain (dev/local)
//     so identity stops auto-recognizing the visitor.
//
// Without #2 the user clicks "退出" → Hub session clears → next /login →
// bridge sees the still-valid lurus_session → instant re-login. Looks
// like logout was a no-op. The asymmetry is invisible until you debug it.
//
// Routes: GET + POST /api/v2/auth/zita-logout (no auth — must work even
// when the session is already stale). GET supports plain anchor hrefs
// (e.g. "切换账号" on the disabled-account page); POST suits in-app calls.
func ZitaLogout(c *gin.Context) {
	session := sessions.Default(c)
	userID, _ := session.Get("id").(int)
	session.Clear()
	// middleware.SessionClearOptions, not a {Path,MaxAge} literal: the
	// clearing Set-Cookie has to carry the same Domain/Secure/SameSite the
	// live cookie was written with or the browser keeps the session cookie
	// and the "switch account" link silently does nothing.
	session.Options(middleware.SessionClearOptions())
	_ = session.Save()

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userID,
		governance.ActionAuthLogout, governance.ResourceUser, userID,
		`{"provider":"zita-bridge"}`))

	// Production: platform writes the cookie with Domain=.lurus.cn so
	// every subdomain shares it; clearing requires the same Domain attr.
	c.SetCookie(zita.SessionCookieName, "", -1, "/", ".lurus.cn", true, true)
	// Dev/local: cookie is host-only; an empty domain clears that variant
	// without affecting the production path above. Both Set-Cookie headers
	// can coexist in one response — the browser applies whichever matches.
	c.SetCookie(zita.SessionCookieName, "", -1, "/", "", true, true)

	// Open-redirect gate. This endpoint is unauthenticated by design, so the
	// link that reaches it is whatever a mail or a chat message said; before
	// cycle-13 L8 its return_to was echoed verbatim into the Location header
	// (GET) and into data.redirect_to (POST), which the SPA assigns to
	// window.location. isLurusReturnURL is the same predicate ZitaLogin
	// (zita_login.go) applies to its own return_to: https, host lurus.cn or
	// a subdomain of it. Anything else — including a protocol-relative
	// "//host/" and a same-origin relative path — falls back to /login,
	// which is where the three in-repo callers already end up (none of them
	// passes return_to; useHeaderBar.js, HFShell.jsx and
	// v2/AccountDisabled/index.jsx).
	returnTo := c.Query("return_to")
	if returnTo == "" || !isLurusReturnURL(returnTo) {
		returnTo = "/login"
	}

	if c.Request.Method == http.MethodGet {
		c.Redirect(http.StatusFound, returnTo)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"redirect_to": returnTo},
	})
}
