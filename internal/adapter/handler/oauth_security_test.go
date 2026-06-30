package handler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ============================================================================
// PKCE (Proof Key for Code Exchange) Security Tests
// ============================================================================

func TestGeneratePKCE_Randomness(t *testing.T) {
	// Verify code_verifier uniqueness across multiple generations
	verifiers := make(map[string]bool)
	iterations := 100

	for i := 0; i < iterations; i++ {
		pkce, err := generatePKCE()
		if err != nil {
			t.Fatalf("generatePKCE() failed on iteration %d: %v", i, err)
		}

		if verifiers[pkce.CodeVerifier] {
			t.Errorf("duplicate code_verifier generated on iteration %d", i)
		}
		verifiers[pkce.CodeVerifier] = true
	}

	if len(verifiers) != iterations {
		t.Errorf("expected %d unique verifiers, got %d", iterations, len(verifiers))
	}
}

func TestGeneratePKCE_Length(t *testing.T) {
	// RFC 7636 specifies code_verifier must be 43-128 characters
	for i := 0; i < 10; i++ {
		pkce, err := generatePKCE()
		if err != nil {
			t.Fatalf("generatePKCE() failed: %v", err)
		}

		verifierLen := len(pkce.CodeVerifier)
		if verifierLen < 43 || verifierLen > 128 {
			t.Errorf("code_verifier length %d is outside RFC 7636 range (43-128)", verifierLen)
		}
	}
}

func TestGeneratePKCE_S256Challenge(t *testing.T) {
	// Verify code_challenge = BASE64URL(SHA256(code_verifier))
	pkce, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE() failed: %v", err)
	}

	// Manually compute the expected challenge
	h := sha256.New()
	h.Write([]byte(pkce.CodeVerifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if pkce.CodeChallenge != expectedChallenge {
		t.Errorf("code_challenge mismatch:\ngot:  %s\nwant: %s", pkce.CodeChallenge, expectedChallenge)
	}
}

func TestGeneratePKCE_Base64URLEncoding(t *testing.T) {
	// Verify code_verifier uses valid base64url characters (no padding)
	pkce, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE() failed: %v", err)
	}

	// Base64URL should not contain +, /, or = (standard base64 characters)
	if strings.ContainsAny(pkce.CodeVerifier, "+/=") {
		t.Errorf("code_verifier contains invalid base64url characters: %s", pkce.CodeVerifier)
	}

	if strings.ContainsAny(pkce.CodeChallenge, "+/=") {
		t.Errorf("code_challenge contains invalid base64url characters: %s", pkce.CodeChallenge)
	}
}

// ============================================================================
// ID Token Validation Tests
// ============================================================================

func TestValidateIDToken_InvalidIssuer(t *testing.T) {
	// Set up expected issuer
	originalIssuer := os.Getenv("OIDC_ISSUER")
	os.Setenv("OIDC_ISSUER", "https://expected-issuer.example.com")
	defer os.Setenv("OIDC_ISSUER", originalIssuer)

	os.Setenv("OIDC_CLIENT_ID", "test-client-id")

	// Build claims with wrong issuer. We exercise the claim-only validator so
	// the test does not need a live JWKS manager (signature verification is a
	// separate layer, covered by middleware tests).
	claims := IDTokenClaims{}
	claims.Issuer = "https://wrong-issuer.example.com"
	claims.Audience = []string{"test-client-id"}
	claims.ExpiresAt = nil

	err := validateIDTokenClaims(&claims, "")
	if err == nil {
		t.Fatal("expected error for wrong issuer, got nil")
	}
	if !strings.Contains(err.Error(), "invalid issuer") {
		t.Errorf("expected 'invalid issuer' error, got: %v", err)
	}
}

func TestValidateIDToken_InvalidAudience(t *testing.T) {
	// Set up expected client ID
	originalClientID := os.Getenv("OIDC_CLIENT_ID")
	originalIssuer := os.Getenv("OIDC_ISSUER")
	os.Setenv("OIDC_CLIENT_ID", "expected-client-id")
	os.Setenv("OIDC_ISSUER", "https://issuer.example.com")
	defer func() {
		os.Setenv("OIDC_CLIENT_ID", originalClientID)
		os.Setenv("OIDC_ISSUER", originalIssuer)
	}()

	claims := IDTokenClaims{}
	claims.Issuer = "https://issuer.example.com"
	claims.Audience = []string{"wrong-client-id"}
	claims.ExpiresAt = nil

	err := validateIDTokenClaims(&claims, "")
	if err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
	if !strings.Contains(err.Error(), "invalid audience") {
		t.Errorf("expected 'invalid audience' error, got: %v", err)
	}
}

func TestValidateIDToken_ExpiredToken(t *testing.T) {
	originalClientID := os.Getenv("OIDC_CLIENT_ID")
	originalIssuer := os.Getenv("OIDC_ISSUER")
	os.Setenv("OIDC_CLIENT_ID", "test-client-id")
	os.Setenv("OIDC_ISSUER", "https://issuer.example.com")
	defer func() {
		os.Setenv("OIDC_CLIENT_ID", originalClientID)
		os.Setenv("OIDC_ISSUER", originalIssuer)
	}()

	claims := IDTokenClaims{}
	claims.Issuer = "https://issuer.example.com"
	claims.Audience = []string{"test-client-id"}
	expiredTime := time.Now().Add(-1 * time.Hour)
	claims.ExpiresAt = jwt.NewNumericDate(expiredTime)

	err := validateIDTokenClaims(&claims, "")
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected 'expired' error, got: %v", err)
	}
}

func TestValidateIDToken_InvalidNonce(t *testing.T) {
	originalClientID := os.Getenv("OIDC_CLIENT_ID")
	originalIssuer := os.Getenv("OIDC_ISSUER")
	os.Setenv("OIDC_CLIENT_ID", "test-client-id")
	os.Setenv("OIDC_ISSUER", "https://issuer.example.com")
	defer func() {
		os.Setenv("OIDC_CLIENT_ID", originalClientID)
		os.Setenv("OIDC_ISSUER", originalIssuer)
	}()

	claims := IDTokenClaims{}
	claims.Issuer = "https://issuer.example.com"
	claims.Audience = []string{"test-client-id"}
	claims.Nonce = "wrong-nonce"
	claims.ExpiresAt = nil

	err := validateIDTokenClaims(&claims, "expected-nonce")
	if err == nil {
		t.Fatal("expected error for wrong nonce (replay protection), got nil")
	}
	if !strings.Contains(err.Error(), "invalid nonce") {
		t.Errorf("expected 'invalid nonce' error, got: %v", err)
	}
}

func TestValidateIDToken_FutureIssuedAt(t *testing.T) {
	originalClientID := os.Getenv("OIDC_CLIENT_ID")
	originalIssuer := os.Getenv("OIDC_ISSUER")
	os.Setenv("OIDC_CLIENT_ID", "test-client-id")
	os.Setenv("OIDC_ISSUER", "https://issuer.example.com")
	defer func() {
		os.Setenv("OIDC_CLIENT_ID", originalClientID)
		os.Setenv("OIDC_ISSUER", originalIssuer)
	}()

	claims := IDTokenClaims{}
	claims.Issuer = "https://issuer.example.com"
	claims.Audience = []string{"test-client-id"}
	futureTime := time.Now().Add(10 * time.Minute) // Beyond 5-minute tolerance
	claims.IssuedAt = jwt.NewNumericDate(futureTime)
	claims.ExpiresAt = nil

	err := validateIDTokenClaims(&claims, "")
	if err == nil {
		t.Fatal("expected error for future issued_at, got nil")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("expected 'future' error, got: %v", err)
	}
}

// ============================================================================
// Tenant Slug Validation Tests
// ============================================================================

func TestIsValidTenantSlug_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		valid bool
	}{
		// Valid cases
		{"alphanumeric", "mycompany123", true},
		{"with hyphen", "my-company", true},
		{"with underscore", "my_company", true},
		{"mixed", "My-Company_123", true},
		{"single char", "a", true},
		{"max length 63", strings.Repeat("a", 63), true},

		// Invalid cases
		{"empty", "", false},
		{"too long (64 chars)", strings.Repeat("a", 64), false},
		{"leading hyphen", "-company", false},
		{"leading underscore", "_company", false},
		{"special chars", "company@123", false},
		{"space", "my company", false},
		{"unicode", "公司", false},
		{"dot", "my.company", false},
		{"slash", "my/company", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidTenantSlug(tt.slug)
			if result != tt.valid {
				t.Errorf("isValidTenantSlug(%q) = %v, want %v", tt.slug, result, tt.valid)
			}
		})
	}
}

// ============================================================================
// Redirect URL Validation Tests (Open Redirect Prevention)
// ============================================================================

func TestIsValidRedirectURL_OpenRedirect(t *testing.T) {
	// Set allowed domains for testing
	originalDomains := os.Getenv("OIDC_ALLOWED_REDIRECT_DOMAINS")
	os.Setenv("OIDC_ALLOWED_REDIRECT_DOMAINS", "example.com,trusted.org")
	defer os.Setenv("OIDC_ALLOWED_REDIRECT_DOMAINS", originalDomains)

	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		// Valid cases
		{"empty (uses default)", "", true},
		{"relative path", "/dashboard", true},
		{"relative with query", "/dashboard?foo=bar", true},
		{"allowed domain", "https://example.com/callback", true},
		{"allowed subdomain", "https://app.example.com/callback", true},
		{"allowed domain 2", "https://trusted.org/login", true},

		// Invalid cases (open redirect attacks)
		{"javascript protocol", "javascript:alert('xss')", false},
		{"data protocol", "data:text/html,<script>alert('xss')</script>", false},
		{"external domain", "https://evil.com/callback", false},
		{"protocol-relative (potential bypass)", "//evil.com/callback", false},
		{"no allowed domains configured", "https://any-domain.com/", false}, // When domains not configured
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Special case: test without allowed domains
			if tt.name == "no allowed domains configured" {
				os.Setenv("OIDC_ALLOWED_REDIRECT_DOMAINS", "")
				defer os.Setenv("OIDC_ALLOWED_REDIRECT_DOMAINS", "example.com,trusted.org")
			}

			result := isValidRedirectURL(tt.url)
			if result != tt.valid {
				t.Errorf("isValidRedirectURL(%q) = %v, want %v", tt.url, result, tt.valid)
			}
		})
	}
}

// ============================================================================
// OAuth State Security Tests
// ============================================================================

func TestOAuthState_TimingAttack(t *testing.T) {
	// Verify state expiration is checked correctly
	// Create a state that is exactly at the expiration boundary

	stateData := OAuthStateData{
		TenantSlug:  "test-tenant",
		RedirectURL: "/dashboard",
		Nonce:       "test-nonce",
		CreatedAt:   time.Now().Add(-10*time.Minute - 1*time.Second), // Just expired (10min + 1s)
	}
	stateJSON, _ := json.Marshal(stateData)
	payload := base64.URLEncoding.EncodeToString(stateJSON)
	sig := computeStateHMAC([]byte(payload))
	state := payload + "." + sig

	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?code=test&state="+state, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for expired state, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if msg, ok := resp["message"].(string); ok {
		if !strings.Contains(msg, "expired") {
			t.Errorf("expected 'expired' in message, got: %s", msg)
		}
	}
}

func TestOAuthState_NonceUniqueness(t *testing.T) {
	// Verify each state generation produces unique nonce
	nonces := make(map[string]bool)

	for i := 0; i < 50; i++ {
		state, nonce, err := generateOAuthState("tenant", "/callback")
		if err != nil {
			t.Fatalf("generateOAuthState() failed: %v", err)
		}

		if nonces[nonce] {
			t.Errorf("duplicate nonce generated: %s", nonce)
		}
		nonces[nonce] = true

		// Verify nonce is embedded in state
		parsedState, err := parseOAuthState(state)
		if err != nil {
			t.Fatalf("parseOAuthState() failed: %v", err)
		}
		if parsedState.Nonce != nonce {
			t.Errorf("nonce mismatch: state contains %s, returned %s", parsedState.Nonce, nonce)
		}
	}
}

