package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateSessionCookieDomain is the boot guard for a session cookie the
// browser would drop.
//
// The OIDC callback is where the session cookie is first issued. A browser
// discards a Set-Cookie whose Domain attribute does not domain-match the host
// that sent it (RFC 6265 5.3 step 6), so when SESSION_COOKIE_DOMAIN is set to
// something the OIDC_REDIRECT_URI host is not inside, every SSO login completes
// the IdP round trip and ends logged out, with nothing in any log. Refusing to
// start turns that silent failure into one clear boot error.
//
// It passes when OIDC login is off (no callback to receive the cookie), when
// the cookie Domain is empty (host-only: always accepted by the issuing host),
// and when no redirect URI is configured (nothing to compare; the login
// handlers fail on that by themselves). A redirect URI that is set but
// unparsable or hostless is refused: the guard cannot show the cookie will be
// kept, and the callback could not work either.
//
// Domain matching follows RFC 6265 5.1.3: the host equals the domain (without
// its optional leading dot) or ends with "." + domain. An IP-literal host only
// ever matches by equality.
func ValidateSessionCookieDomain(oidcEnabled bool, cookieDomain, redirectURI string) error {
	if !oidcEnabled || cookieDomain == "" || redirectURI == "" {
		return nil
	}
	u, err := url.Parse(redirectURI)
	if err != nil {
		return fmt.Errorf("OIDC_REDIRECT_URI %q is not a valid URL (%w): cannot verify that the session cookie (SESSION_COOKIE_DOMAIN=%q) will be accepted by the browser", redirectURI, err, cookieDomain)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("OIDC_REDIRECT_URI %q has no host: cannot verify that the session cookie (SESSION_COOKIE_DOMAIN=%q) will be accepted by the browser", redirectURI, cookieDomain)
	}
	domain := strings.TrimPrefix(strings.ToLower(cookieDomain), ".")
	if host == domain {
		return nil
	}
	if net.ParseIP(host) == nil && strings.HasSuffix(host, "."+domain) {
		return nil
	}
	return fmt.Errorf("SESSION_COOKIE_DOMAIN=%q does not domain-match the host %q of OIDC_REDIRECT_URI=%q: the browser would drop the session cookie set by the OIDC callback and every SSO login would end logged out; set SESSION_COOKIE_DOMAIN to a parent domain of %q, or to \"\" for a host-only cookie", cookieDomain, host, redirectURI, host)
}
