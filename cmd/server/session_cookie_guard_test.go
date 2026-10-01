package main

import (
	"os"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
)

// The session cookie carries the login. A browser silently drops a Set-Cookie
// whose Domain does not domain-match the host that sent it (RFC 6265 5.3 step
// 6), so an OIDC deployment whose callback host is outside the cookie Domain
// completes the whole IdP round trip and then has no session: every SSO login
// "succeeds" and lands logged out. That is how hub-domain SSO was broken until
// 2026-08-30 (cookie Domain pinned to a different host than the redirect URI).
func TestValidateSessionCookieDomain(t *testing.T) {
	cases := []struct {
		name        string
		oidcEnabled bool
		domain      string
		redirectURI string
		wantErr     bool
	}{
		{"mismatch: cookie domain on another host", true, "test-newhub.lurus.cn", "https://hub.lurus.cn/api/v2/oauth/callback", true},
		{"mismatch: different registrable domain", true, ".example.com", "https://hub.lurus.cn/cb", true},
		{"mismatch: lookalike suffix without a label boundary", true, "lurus.cn", "https://evillurus.cn/cb", true},
		{"match: leading dot, subdomain host", true, ".lurus.cn", "https://hub.lurus.cn/api/v2/oauth/callback", false},
		{"match: no leading dot, subdomain host", true, "lurus.cn", "https://hub.lurus.cn/api/v2/oauth/callback", false},
		{"match: host equals domain", true, "hub.lurus.cn", "https://hub.lurus.cn/cb", false},
		{"match: host equals dotted domain", true, ".hub.lurus.cn", "https://hub.lurus.cn/cb", false},
		{"match: case-insensitive", true, ".Lurus.CN", "https://HUB.lurus.cn/cb", false},
		{"match: port in redirect URI is not part of the host", true, "lurus.cn", "https://hub.lurus.cn:8443/cb", false},
		{"host-only cookie (empty domain) always passes", true, "", "https://hub.lurus.cn/cb", false},
		{"OIDC off: a mismatching domain is not a login problem", false, "test-newhub.lurus.cn", "https://hub.lurus.cn/cb", false},
		{"OIDC off, empty everything", false, "", "", false},
		{"OIDC on but no redirect URI configured: nothing to compare", true, ".lurus.cn", "", false},
		{"IP host cannot suffix-match a domain", true, "0.0.1", "http://10.0.0.1/cb", true},
		{"IP host equal to domain passes", true, "10.0.0.1", "http://10.0.0.1/cb", false},
		{"unparsable redirect URI is refused, not skipped", true, ".lurus.cn", "http://[::1", true},
		{"redirect URI without a host is refused", true, ".lurus.cn", "/relative/callback", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSessionCookieDomain(tc.oidcEnabled, tc.domain, tc.redirectURI)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSessionCookieDomain(%v, %q, %q) error = %v, wantErr %v", tc.oidcEnabled, tc.domain, tc.redirectURI, err, tc.wantErr)
			}
		})
	}
}

// A refusal must name both values, so the operator sees which env var to fix
// without reading code.
func TestValidateSessionCookieDomain_ErrorNamesBothValues(t *testing.T) {
	err := ValidateSessionCookieDomain(true, "test-newhub.lurus.cn", "https://hub.lurus.cn/api/v2/oauth/callback")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{"SESSION_COOKIE_DOMAIN", "test-newhub.lurus.cn", "OIDC_REDIRECT_URI", "hub.lurus.cn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// The three deployments that matter, driven through the same env readers the
// boot path uses: SessionCookieBaseOptions().Domain defaults to ".lurus.cn"
// when SESSION_COOKIE_DOMAIN is unset.
func TestValidateSessionCookieDomain_RealDeployments(t *testing.T) {
	setOrUnset := func(t *testing.T, key string, val *string) {
		t.Helper()
		prev, had := os.LookupEnv(key)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, prev)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		if val == nil {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, *val)
		}
	}
	str := func(s string) *string { return &s }

	cases := []struct {
		name        string
		cookieEnv   *string // nil = unset
		oidcEnabled bool
		redirectURI string
		wantErr     bool
	}{
		{"unset default (.lurus.cn) + hub.lurus.cn", nil, true, "https://hub.lurus.cn/api/v2/oauth/callback", false},
		{"production: host-only cookie, OIDC on", str(""), true, "https://hub.lurus.cn/api/v2/oauth/callback", false},
		{"UAT: OIDC off, domain left at the default", nil, false, "", false},
		{"unset default but a redirect host outside .lurus.cn", nil, true, "https://hub.example.com/cb", true},
		{"explicit stale domain pinned to the UAT host", str("test-newhub.lurus.cn"), true, "https://hub.lurus.cn/cb", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setOrUnset(t, "SESSION_COOKIE_DOMAIN", tc.cookieEnv)
			opts := middleware.SessionCookieBaseOptions()
			err := ValidateSessionCookieDomain(tc.oidcEnabled, opts.Domain, tc.redirectURI)
			if (err != nil) != tc.wantErr {
				t.Fatalf("domain=%q error = %v, wantErr %v", opts.Domain, err, tc.wantErr)
			}
		})
	}
}
