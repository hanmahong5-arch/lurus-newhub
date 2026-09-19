package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOIDCPathHelpers_Defaults(t *testing.T) {
	t.Setenv("OIDC_AUTHORIZE_PATH", "")
	t.Setenv("OIDC_TOKEN_PATH", "")
	t.Setenv("OIDC_END_SESSION_PATH", "")
	if got := oidcAuthorizePath(); got != defaultAuthorizePath {
		t.Errorf("oidcAuthorizePath default = %q", got)
	}
	if got := oidcTokenPath(); got != defaultTokenPath {
		t.Errorf("oidcTokenPath default = %q", got)
	}
	if got := oidcEndSessionPath(); got != defaultEndSessionPath {
		t.Errorf("oidcEndSessionPath default = %q", got)
	}
}

func TestOIDCPathHelpers_Overrides(t *testing.T) {
	t.Setenv("OIDC_AUTHORIZE_PATH", "/custom/authorize")
	t.Setenv("OIDC_TOKEN_PATH", "/custom/token")
	t.Setenv("OIDC_END_SESSION_PATH", "/custom/logout")
	if got := oidcAuthorizePath(); got != "/custom/authorize" {
		t.Errorf("oidcAuthorizePath override = %q", got)
	}
	if got := oidcTokenPath(); got != "/custom/token" {
		t.Errorf("oidcTokenPath override = %q", got)
	}
	if got := oidcEndSessionPath(); got != "/custom/logout" {
		t.Errorf("oidcEndSessionPath override = %q", got)
	}
}

func TestIsPKCEEnabled(t *testing.T) {
	t.Setenv("OIDC_ENABLE_PKCE", "true")
	if !isPKCEEnabled() {
		t.Error("expected PKCE enabled when env=true")
	}
	t.Setenv("OIDC_ENABLE_PKCE", "false")
	if isPKCEEnabled() {
		t.Error("expected PKCE disabled when env=false")
	}
	t.Setenv("OIDC_ENABLE_PKCE", "")
	if isPKCEEnabled() {
		t.Error("expected PKCE disabled when env unset")
	}
}

func TestBuildOIDCAuthURL(t *testing.T) {
	t.Setenv("OIDC_ISSUER", "https://idp.test")
	t.Setenv("OIDC_CLIENT_ID", "client-123")
	t.Setenv("OIDC_REDIRECT_URI", "https://app.test/callback")
	t.Setenv("OIDC_OAUTH_SCOPES", "")
	t.Setenv("OIDC_AUTHORIZE_PATH", "/oauth/v2/authorize")

	pkce := &PKCEData{CodeVerifier: "v", CodeChallenge: "chal"}
	raw := buildOIDCAuthURL("org-9", "state-xyz", "nonce-abc", pkce, "login")

	if !strings.HasPrefix(raw, "https://idp.test/oauth/v2/authorize?") {
		t.Fatalf("unexpected prefix: %s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             "client-123",
		"redirect_uri":          "https://app.test/callback",
		"response_type":         "code",
		"scope":                 "openid email profile offline_access",
		"state":                 "state-xyz",
		"nonce":                 "nonce-abc",
		"organization":          "org-9",
		"prompt":                "login",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("param %q = %q, want %q", k, got, want)
		}
	}
}

func TestBuildOIDCAuthURL_NoOrgNoPKCE(t *testing.T) {
	t.Setenv("OIDC_ISSUER", "https://idp.test")
	t.Setenv("OIDC_CLIENT_ID", "c")
	t.Setenv("OIDC_REDIRECT_URI", "https://app/cb")
	t.Setenv("OIDC_OAUTH_SCOPES", "openid")
	t.Setenv("OIDC_AUTHORIZE_PATH", "/authorize")

	raw := buildOIDCAuthURL("", "s", "n", nil, "")
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Has("organization") {
		t.Error("organization must be absent when orgID empty")
	}
	if q.Has("prompt") {
		t.Error("prompt must be absent when empty")
	}
	if q.Has("code_challenge") {
		t.Error("code_challenge must be absent when PKCE nil")
	}
	if q.Get("scope") != "openid" {
		t.Errorf("scope override lost: %q", q.Get("scope"))
	}
}

func TestExchangeCodeForToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		if r.FormValue("code") != "the-code" {
			t.Errorf("code = %q", r.FormValue("code"))
		}
		if r.FormValue("code_verifier") != "verifier-1" {
			t.Errorf("code_verifier = %q", r.FormValue("code_verifier"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","id_token":"idt","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	t.Setenv("OIDC_ISSUER", srv.URL)
	t.Setenv("OIDC_TOKEN_PATH", "/token")
	t.Setenv("OIDC_CLIENT_ID", "c")
	t.Setenv("OIDC_CLIENT_SECRET", "s")
	t.Setenv("OIDC_REDIRECT_URI", "https://app/cb")

	tok, err := exchangeCodeForToken("the-code", "verifier-1")
	if err != nil {
		t.Fatalf("exchange err: %v", err)
	}
	if tok.AccessToken != "at" || tok.IDToken != "idt" {
		t.Errorf("unexpected token: %+v", tok)
	}
}

func TestExchangeCodeForToken_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer srv.Close()

	t.Setenv("OIDC_ISSUER", srv.URL)
	t.Setenv("OIDC_TOKEN_PATH", "/token")
	t.Setenv("OIDC_CLIENT_ID", "c")
	t.Setenv("OIDC_CLIENT_SECRET", "")
	t.Setenv("OIDC_REDIRECT_URI", "https://app/cb")

	_, err := exchangeCodeForToken("bad", "")
	if err == nil {
		t.Fatal("expected error for invalid_grant")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should mention the OIDC error: %v", err)
	}
}
