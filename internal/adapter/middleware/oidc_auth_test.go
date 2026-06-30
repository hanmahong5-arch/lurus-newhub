package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ============================================================================
// Test helpers
// ============================================================================

// generateTestRSAKeyPair generates a 2048-bit RSA key pair for testing.
func generateTestRSAKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key pair: %v", err)
	}
	return priv, &priv.PublicKey
}

// rsaPublicKeyToJWK converts an RSA public key to JWK format.
func rsaPublicKeyToJWK(pub *rsa.PublicKey, kid string) JWK {
	nBytes := pub.N.Bytes()
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return JWK{
		Kty: "RSA",
		Use: "sig",
		Kid: kid,
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(nBytes),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}

// createTestJWKSServer creates an httptest.Server that serves the given JWKSet.
func createTestJWKSServer(t *testing.T, jwks JWKSet) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// createSignedJWT creates a signed JWT string using the given private key, kid, and claims.
func createSignedJWT(t *testing.T, privateKey *rsa.PrivateKey, kid string, claims OIDCClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return signed
}

// ============================================================================
// JWKS Manager tests
// ============================================================================

func TestJWKS_FetchKeys_Success(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwk := rsaPublicKeyToJWK(pub, "test-kid-1")
	jwks := JWKSet{Keys: []JWK{jwk}}
	srv := createTestJWKSServer(t, jwks)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	err := mgr.refreshKeys()
	if err != nil {
		t.Fatalf("refreshKeys() returned error: %v", err)
	}

	if len(mgr.publicKeys) != 1 {
		t.Errorf("expected 1 key, got %d", len(mgr.publicKeys))
	}
	if _, ok := mgr.publicKeys["test-kid-1"]; !ok {
		t.Error("expected key with kid 'test-kid-1' to be present")
	}
	if mgr.updateFailed {
		t.Error("expected updateFailed to be false")
	}
	if mgr.lastUpdate.IsZero() {
		t.Error("expected lastUpdate to be set")
	}
}

func TestJWKS_FetchKeys_MultipleKeys(t *testing.T) {
	_, pub1 := generateTestRSAKeyPair(t)
	_, pub2 := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{
		rsaPublicKeyToJWK(pub1, "kid-alpha"),
		rsaPublicKeyToJWK(pub2, "kid-beta"),
	}}
	srv := createTestJWKSServer(t, jwks)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("refreshKeys() returned error: %v", err)
	}
	if len(mgr.publicKeys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(mgr.publicKeys))
	}
}

func TestJWKS_FetchKeys_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	err := mgr.refreshKeys()
	if err == nil {
		t.Fatal("expected error from refreshKeys() when server returns 500")
	}
}

func TestJWKS_FetchKeys_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not valid json {{{`))
	}))
	t.Cleanup(srv.Close)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	err := mgr.refreshKeys()
	if err == nil {
		t.Fatal("expected error when server returns invalid JSON")
	}
}

func TestJWKS_FetchKeys_UnreachableServer(t *testing.T) {
	mgr := &JWKSManager{
		jwksURI:    "http://127.0.0.1:1", // unlikely to have anything listening
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	err := mgr.refreshKeys()
	if err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestJWKS_FetchKeys_NoRSAKeys(t *testing.T) {
	// Return a JWKS with only non-RSA key types
	jwks := JWKSet{Keys: []JWK{
		{Kty: "EC", Use: "sig", Kid: "ec-key", Alg: "ES256"},
	}}
	srv := createTestJWKSServer(t, jwks)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	err := mgr.refreshKeys()
	if err == nil {
		t.Fatal("expected error when no RSA keys are found")
	}
}

func TestJWKS_CacheHit(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "cached-kid")}}

	var hitCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hitCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	// First fetch
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("first refreshKeys() failed: %v", err)
	}
	if atomic.LoadInt64(&hitCount) != 1 {
		t.Fatalf("expected 1 server hit, got %d", atomic.LoadInt64(&hitCount))
	}

	// getKey should return the cached key without another fetch
	key, err := mgr.getKey("cached-kid")
	if err != nil {
		t.Fatalf("getKey() returned error: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil key")
	}
	if atomic.LoadInt64(&hitCount) != 1 {
		t.Errorf("expected still 1 server hit after getKey, got %d", atomic.LoadInt64(&hitCount))
	}
}

func TestJWKS_GetKey_NotFound(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "existing-kid")}}
	srv := createTestJWKSServer(t, jwks)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("refreshKeys() failed: %v", err)
	}

	_, err := mgr.getKey("nonexistent-kid")
	if err == nil {
		t.Fatal("expected error for nonexistent kid")
	}
}

func TestJWKS_KeyRotation(t *testing.T) {
	_, pub1 := generateTestRSAKeyPair(t)
	_, pub2 := generateTestRSAKeyPair(t)

	// Start with kid1
	currentJWKS := &JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub1, "kid-v1")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentJWKS)
	}))
	t.Cleanup(srv.Close)

	mgr := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("initial refreshKeys() failed: %v", err)
	}
	if _, ok := mgr.publicKeys["kid-v1"]; !ok {
		t.Fatal("expected kid-v1 to be present after initial fetch")
	}

	// Rotate: replace kid-v1 with kid-v2
	currentJWKS.Keys = []JWK{rsaPublicKeyToJWK(pub2, "kid-v2")}

	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("refreshKeys() after rotation failed: %v", err)
	}
	if _, ok := mgr.publicKeys["kid-v2"]; !ok {
		t.Error("expected kid-v2 to be present after rotation")
	}
	if _, ok := mgr.publicKeys["kid-v1"]; ok {
		t.Error("expected kid-v1 to be absent after rotation")
	}
}

// ============================================================================
// JWK to RSA conversion tests
// ============================================================================

func TestJWKToRSAPublicKey_Valid(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwk := rsaPublicKeyToJWK(pub, "conv-test")

	converted, err := jwkToRSAPublicKey(jwk)
	if err != nil {
		t.Fatalf("jwkToRSAPublicKey() returned error: %v", err)
	}
	if converted.N.Cmp(pub.N) != 0 {
		t.Error("modulus mismatch after conversion")
	}
	if converted.E != pub.E {
		t.Error("exponent mismatch after conversion")
	}
}

func TestJWKToRSAPublicKey_InvalidModulus(t *testing.T) {
	jwk := JWK{
		Kty: "RSA",
		Kid: "bad-n",
		N:   "!!!not-valid-base64!!!",
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(65537).Bytes()),
	}
	_, err := jwkToRSAPublicKey(jwk)
	if err == nil {
		t.Fatal("expected error for invalid modulus encoding")
	}
}

func TestJWKToRSAPublicKey_InvalidExponent(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwk := JWK{
		Kty: "RSA",
		Kid: "bad-e",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   "!!!not-valid-base64!!!",
	}
	_, err := jwkToRSAPublicKey(jwk)
	if err == nil {
		t.Fatal("expected error for invalid exponent encoding")
	}
}

// ============================================================================
// OIDCClaims struct tests
// ============================================================================

func TestClaims_ParseOrgId(t *testing.T) {
	claims := OIDCClaims{
		OrgID: "org-12345",
	}
	if claims.OrgID != "org-12345" {
		t.Errorf("expected OrgID 'org-12345', got %q", claims.OrgID)
	}
}

func TestClaims_ParseRoles(t *testing.T) {
	roles := map[string]interface{}{
		"admin":  map[string]interface{}{"org_id": "org-1"},
		"editor": map[string]interface{}{"org_id": "org-1"},
	}
	claims := OIDCClaims{Roles: roles}

	if len(claims.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(claims.Roles))
	}
	if _, ok := claims.Roles["admin"]; !ok {
		t.Error("expected 'admin' role to be present")
	}
}

func TestClaims_ParseEmail(t *testing.T) {
	claims := OIDCClaims{
		Email:         "user@example.com",
		EmailVerified: true,
	}
	if claims.Email != "user@example.com" {
		t.Errorf("expected email 'user@example.com', got %q", claims.Email)
	}
	if !claims.EmailVerified {
		t.Error("expected EmailVerified to be true")
	}
}

func TestClaims_JSONRoundTrip(t *testing.T) {
	original := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  "https://zitadel.example.com",
			Subject: "user-abc-123",
		},
		Email:             "test@example.com",
		Name:              "Test User",
		PreferredUsername: "testuser",
		OrgID:             "org-999",
		OrgDomain:         "example.com",
		ResourceOwnerID:   "ro-123",
		ResourceOwnerName: "Example Org",
		Roles: map[string]interface{}{
			"viewer": map[string]interface{}{},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded OIDCClaims
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.OrgID != original.OrgID {
		t.Errorf("OrgID mismatch: got %q, want %q", decoded.OrgID, original.OrgID)
	}
	if decoded.Email != original.Email {
		t.Errorf("Email mismatch: got %q, want %q", decoded.Email, original.Email)
	}
	if decoded.Subject != original.Subject {
		t.Errorf("Subject mismatch: got %q, want %q", decoded.Subject, original.Subject)
	}
}

// ============================================================================
// JWT validation tests (parsing with known public keys)
// ============================================================================

func TestJWT_ValidToken(t *testing.T) {
	priv, pub := generateTestRSAKeyPair(t)
	kid := "valid-kid"

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://zitadel.example.com",
			Subject:   "user-001",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
		},
		Email:             "valid@example.com",
		Name:              "Valid User",
		PreferredUsername: "validuser",
		OrgID:             "org-valid",
	}

	tokenStr := createSignedJWT(t, priv, kid, claims)

	// Parse with the matching public key
	parsed, err := jwt.ParseWithClaims(tokenStr, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		return pub, nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims returned error: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("expected token to be valid")
	}

	parsedClaims, ok := parsed.Claims.(*OIDCClaims)
	if !ok {
		t.Fatal("failed to cast claims to OIDCClaims")
	}
	if parsedClaims.Email != "valid@example.com" {
		t.Errorf("email mismatch: got %q", parsedClaims.Email)
	}
	if parsedClaims.OrgID != "org-valid" {
		t.Errorf("OrgID mismatch: got %q", parsedClaims.OrgID)
	}
	if parsedClaims.Subject != "user-001" {
		t.Errorf("Subject mismatch: got %q", parsedClaims.Subject)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	priv, pub := generateTestRSAKeyPair(t)

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://zitadel.example.com",
			Subject:   "user-expired",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // expired
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
		Email: "expired@example.com",
	}

	tokenStr := createSignedJWT(t, priv, "expired-kid", claims)

	_, err := jwt.ParseWithClaims(tokenStr, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		return pub, nil
	})
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWT_InvalidSignature(t *testing.T) {
	priv1, _ := generateTestRSAKeyPair(t)
	_, pub2 := generateTestRSAKeyPair(t) // different key pair

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://zitadel.example.com",
			Subject:   "user-badsig",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "badsig@example.com",
	}

	// Sign with priv1, validate with pub2
	tokenStr := createSignedJWT(t, priv1, "sig-kid", claims)

	_, err := jwt.ParseWithClaims(tokenStr, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		return pub2, nil
	})
	if err == nil {
		t.Fatal("expected error when validating with wrong public key")
	}
}

func TestJWT_WrongIssuer(t *testing.T) {
	priv, pub := generateTestRSAKeyPair(t)

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://wrong-issuer.example.com",
			Subject:   "user-wrong-iss",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "wrongiss@example.com",
	}

	tokenStr := createSignedJWT(t, priv, "iss-kid", claims)

	// Parse with issuer validation
	expectedIssuer := "https://zitadel.example.com"
	_, err := jwt.ParseWithClaims(tokenStr, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		return pub, nil
	}, jwt.WithIssuer(expectedIssuer))
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestJWT_NotYetValid(t *testing.T) {
	priv, pub := generateTestRSAKeyPair(t)

	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://zitadel.example.com",
			Subject:   "user-future",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)), // future
		},
		Email: "future@example.com",
	}

	tokenStr := createSignedJWT(t, priv, "nbf-kid", claims)

	_, err := jwt.ParseWithClaims(tokenStr, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
		return pub, nil
	})
	if err == nil {
		t.Fatal("expected error for token not yet valid")
	}
}

// ============================================================================
// Multi-issuer acceptance tests
//
// These tests exercise the issuer-set logic directly (parseOIDCIssuers +
// issuerInSet) without spinning up a full Gin stack or a DB, so they stay
// in the -short tier.
// ============================================================================

// parseOIDCIssuers mirrors the production parsing logic so that the test
// does not reach into package-level state or call InitOIDCAuth.
func parseOIDCIssuers(raw string) []string {
	var result []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			result = append(result, v)
		}
	}
	return result
}

// issuerInSet returns true when iss is present in the accepted list.
func issuerInSet(iss string, accepted []string) bool {
	for _, a := range accepted {
		if iss == a {
			return true
		}
	}
	return false
}

func TestOIDCIssuer_MultiIssuer(t *testing.T) {
	const (
		oldIssuer = "https://auth.lurus.cn"
		newIssuer = "https://identity.lurus.cn"
		badIssuer = "https://evil.example.com"
	)

	tests := []struct {
		name       string
		envValue   string // raw OIDC_ISSUER env string
		tokenIssuer string
		wantOK     bool
	}{
		{
			name:        "single_issuer_match",
			envValue:    oldIssuer,
			tokenIssuer: oldIssuer,
			wantOK:      true,
		},
		{
			name:        "second_issuer_accepted_during_rebrand_window",
			envValue:    oldIssuer + "," + newIssuer,
			tokenIssuer: newIssuer,
			wantOK:      true,
		},
		{
			name:        "first_issuer_still_accepted_during_rebrand_window",
			envValue:    oldIssuer + "," + newIssuer,
			tokenIssuer: oldIssuer,
			wantOK:      true,
		},
		{
			name:        "unknown_issuer_rejected",
			envValue:    oldIssuer + "," + newIssuer,
			tokenIssuer: badIssuer,
			wantOK:      false,
		},
		{
			name:        "whitespace_tolerance_around_comma",
			envValue:    "  " + oldIssuer + " , " + newIssuer + "  ",
			tokenIssuer: newIssuer,
			wantOK:      true,
		},
		{
			name:        "empty_entry_between_commas_ignored",
			envValue:    oldIssuer + ",," + newIssuer,
			tokenIssuer: newIssuer,
			wantOK:      true,
		},
		{
			name:        "single_issuer_no_match",
			envValue:    oldIssuer,
			tokenIssuer: newIssuer,
			wantOK:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issuers := parseOIDCIssuers(tc.envValue)
			got := issuerInSet(tc.tokenIssuer, issuers)
			if got != tc.wantOK {
				t.Errorf("issuerInSet(%q, parsed(%q)) = %v, want %v",
					tc.tokenIssuer, tc.envValue, got, tc.wantOK)
			}
		})
	}
}

func TestOIDCIssuer_ParseEmptyEnv(t *testing.T) {
	// Empty env must yield zero issuers — preserves fast-fail semantics.
	issuers := parseOIDCIssuers("")
	if len(issuers) != 0 {
		t.Errorf("expected 0 issuers from empty string, got %d: %v", len(issuers), issuers)
	}
}

func TestOIDCIssuer_ParseWhitespaceOnlyEnv(t *testing.T) {
	issuers := parseOIDCIssuers("   ,  ,  ")
	if len(issuers) != 0 {
		t.Errorf("expected 0 issuers from whitespace-only string, got %d: %v", len(issuers), issuers)
	}
}

// ============================================================================
// extractRoles tests
// ============================================================================

func TestExtractRoles_Empty(t *testing.T) {
	roles := extractRoles(nil)
	if len(roles) != 0 {
		t.Errorf("expected 0 roles from nil map, got %d", len(roles))
	}
}

func TestExtractRoles_Multiple(t *testing.T) {
	rolesMap := map[string]interface{}{
		"admin":  map[string]interface{}{},
		"editor": map[string]interface{}{},
		"viewer": map[string]interface{}{},
	}
	roles := extractRoles(rolesMap)
	if len(roles) != 3 {
		t.Errorf("expected 3 roles, got %d", len(roles))
	}

	// Verify all roles are present (order not guaranteed)
	roleSet := make(map[string]bool)
	for _, r := range roles {
		roleSet[r] = true
	}
	for _, expected := range []string{"admin", "editor", "viewer"} {
		if !roleSet[expected] {
			t.Errorf("expected role %q to be present", expected)
		}
	}
}

// ============================================================================
// GetTenantContext / TenantContext tests
// ============================================================================

func TestTenantContext_Fields(t *testing.T) {
	ctx := &TenantContext{
		TenantID:      "tenant-abc",
		UserID:        42,
		IDPSubject: "zitadel-user-xyz",
		Email:         "ctx@example.com",
		Username:      "ctxuser",
		Roles:         []string{"admin", "viewer"},
	}

	if ctx.TenantID != "tenant-abc" {
		t.Errorf("TenantID mismatch: %q", ctx.TenantID)
	}
	if ctx.UserID != 42 {
		t.Errorf("UserID mismatch: %d", ctx.UserID)
	}
	if len(ctx.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(ctx.Roles))
	}
}
