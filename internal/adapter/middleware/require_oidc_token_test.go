package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// RequireOIDCToken is the lightweight auth gate wired onto /lutu/search.
// Unlike OIDCAuth it must NOT require a newhub tenant/user mapping — Lutu
// APP users are consumer OIDC identities with no newhub tenant. These
// tests pin that contract: a validly-signed, correctly-issued, unexpired
// token passes even when no UserIdentityMapping exists, while anonymous /
// invalid / disabled callers are rejected so the shared Tavily quota stays
// protected. The happy-path + 401/503 cases reuse the integration harness
// (setupIntegrationTest wires jwksManager + the issuer + createJWT).

func requireTokenRouter() *gin.Engine {
	r := gin.New()
	r.POST("/lutu/search", RequireOIDCToken(), func(c *gin.Context) {
		sub, _ := c.Get("oidc_user_id")
		c.JSON(http.StatusOK, gin.H{"ok": true, "sub": sub})
	})
	return r
}

// The defining difference from OIDCAuth: a valid token whose subject has
// NO newhub mapping still passes. OIDCAuth 500s on this exact input (see
// TestOIDCAuth_UserMappingNotFound) because it maps to a tenant; this gate
// only proves identity, so Lutu consumers can use web search.
func TestRequireOIDCToken_ValidToken_NoTenantMapping_Passes(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "lutu_consumer_with_no_newhub_tenant",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "consumer@lutu.example",
	}
	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid token without a tenant mapping, got %d, body: %s",
			w.Code, w.Body.String())
	}
	// The verified subject must be exposed for optional downstream accounting.
	if !strings.Contains(w.Body.String(), "lutu_consumer_with_no_newhub_tenant") {
		t.Errorf("expected oidc_user_id (token subject) in context, body: %s", w.Body.String())
	}
}

func TestRequireOIDCToken_MissingBearer_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for a missing bearer, got %d", w.Code)
	}
}

func TestRequireOIDCToken_ExpiredToken_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "expired-sub",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for an expired token, got %d", w.Code)
	}
}

func TestRequireOIDCToken_WrongIssuer_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	router := requireTokenRouter()

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://evil.example.com",
			Subject:   "wrong-iss-sub",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for a wrong issuer, got %d", w.Code)
	}
}

// Fail-closed: when OIDC auth is globally disabled the gate returns 503 —
// it must never silently fall through to anonymous Tavily access.
func TestRequireOIDCToken_Disabled_503(t *testing.T) {
	prev := oidcEnabled
	oidcEnabled = false
	defer func() { oidcEnabled = prev }()

	router := requireTokenRouter()
	req := httptest.NewRequest(http.MethodPost, "/lutu/search", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when oidc is disabled, got %d", w.Code)
	}
}
