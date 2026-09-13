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
package middleware

import (
	"net/http"
	"os"

	"github.com/gin-contrib/sessions"
)

// SessionCookieBaseOptions computes the Path/Domain/Secure/SameSite/HttpOnly
// this process's gin session cookie is configured with, from the same env
// vars cmd/server's bootstrap reads (SESSION_SECURE, GIN_MODE,
// SESSION_COOKIE_DOMAIN) — see that file for why the domain defaults to
// ".lurus.cn" and how SESSION_COOKIE_DOMAIN="" opts a standalone/IP-accessed
// deployment into a host-only cookie. MaxAge is left at zero; callers set it
// (cmd/server's bootstrap sets the store's real MaxAge, SessionClearOptions
// below sets -1 to clear).
//
// Exists so cmd/server's store setup and the session-clearing call sites
// (authHelper's SESSION_REVOKED/registry-revoked branches, RevokeCurrentSessionV2,
// RevokeSessionByIDV2) build the same Domain/Secure/SameSite instead of each
// guessing its own — a clearing Set-Cookie whose Domain/Secure/SameSite do
// not match the cookie the browser is actually holding does not delete it
// (RFC 6265 identifies a cookie by name+domain+path; a mismatched Domain
// mints an unrelated second cookie instead of clearing the stale one, and a
// Secure cookie set over HTTPS is not clearable by a non-Secure Set-Cookie).
// L7 repair round 3, finding routing-resilience-limits-13#10.
func SessionCookieBaseOptions() sessions.Options {
	secure := os.Getenv("GIN_MODE") == "release"
	if envSecure := os.Getenv("SESSION_SECURE"); envSecure != "" {
		secure = envSecure == "true"
	}
	domain := ".lurus.cn"
	if d, ok := os.LookupEnv("SESSION_COOKIE_DOMAIN"); ok {
		domain = d
	}
	return sessions.Options{
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Domain:   domain,
	}
}

// SessionClearOptions returns the sessions.Options a caller must pass to
// session.Options(...) (before session.Save()) to actually delete this
// process's session cookie in the browser — SessionCookieBaseOptions with
// MaxAge:-1. See SessionCookieBaseOptions for why the Domain/Secure/SameSite
// must match the live store's rather than a bare {Path:"/", MaxAge:-1}.
func SessionClearOptions() sessions.Options {
	opts := SessionCookieBaseOptions()
	opts.MaxAge = -1
	return opts
}
