package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ============================================================================
// OAuth state encoding / decoding tests
// ============================================================================

func TestOAuthState_GenerateAndParse(t *testing.T) {
	state, nonce, err := generateOAuthState("my-tenant", "/dashboard")
	if err != nil {
		t.Fatalf("generateOAuthState() returned error: %v", err)
	}
	if nonce == "" {
		t.Fatal("expected non-empty nonce from generateOAuthState")
	}
	if state == "" {
		t.Fatal("expected non-empty state string")
	}

	parsed, err := parseOAuthState(state)
	if err != nil {
		t.Fatalf("parseOAuthState() returned error: %v", err)
	}
	if parsed.TenantSlug != "my-tenant" {
		t.Errorf("TenantSlug mismatch: got %q, want %q", parsed.TenantSlug, "my-tenant")
	}
	if parsed.RedirectURL != "/dashboard" {
		t.Errorf("RedirectURL mismatch: got %q, want %q", parsed.RedirectURL, "/dashboard")
	}
	if parsed.Nonce == "" {
		t.Error("expected non-empty nonce")
	}
	if parsed.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestOAuthState_UniqueNonce(t *testing.T) {
	state1, nonce1, err := generateOAuthState("tenant-a", "/page1")
	if err != nil {
		t.Fatalf("generateOAuthState() error: %v", err)
	}
	state2, nonce2, err := generateOAuthState("tenant-a", "/page1")
	if err != nil {
		t.Fatalf("generateOAuthState() error: %v", err)
	}

	// Verify returned nonces are unique
	if nonce1 == nonce2 {
		t.Error("expected different nonces from generateOAuthState calls")
	}

	if state1 == state2 {
		t.Error("expected different state strings due to unique nonces")
	}

	parsed1, _ := parseOAuthState(state1)
	parsed2, _ := parseOAuthState(state2)
	if parsed1.Nonce == parsed2.Nonce {
		t.Error("expected different nonces for two state generations")
	}
}

func TestOAuthState_ParseInvalidBase64(t *testing.T) {
	// Missing signature separator should fail
	_, err := parseOAuthState("!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64 state")
	}
}

func TestOAuthState_ParseInvalidJSON(t *testing.T) {
	payload := base64.URLEncoding.EncodeToString([]byte("not json"))
	sig := computeStateHMAC([]byte(payload))
	signed := payload + "." + sig
	_, err := parseOAuthState(signed)
	if err == nil {
		t.Fatal("expected error for invalid JSON state")
	}
}

func TestOAuthState_ParseValidManual(t *testing.T) {
	stateData := OAuthStateData{
		TenantSlug:  "acme",
		RedirectURL: "/settings",
		Nonce:       "test-nonce-123",
		CreatedAt:   time.Now(),
	}
	data, err := json.Marshal(stateData)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	payload := base64.URLEncoding.EncodeToString(data)
	sig := computeStateHMAC([]byte(payload))
	signed := payload + "." + sig

	parsed, err := parseOAuthState(signed)
	if err != nil {
		t.Fatalf("parseOAuthState() returned error: %v", err)
	}
	if parsed.TenantSlug != "acme" {
		t.Errorf("TenantSlug mismatch: got %q", parsed.TenantSlug)
	}
	if parsed.Nonce != "test-nonce-123" {
		t.Errorf("Nonce mismatch: got %q", parsed.Nonce)
	}
}

func TestOAuthState_TamperedSignature(t *testing.T) {
	// Generate a valid state
	state, _, err := generateOAuthState("tenant", "/callback")
	if err != nil {
		t.Fatalf("generateOAuthState() failed: %v", err)
	}

	// Tamper with the signature
	tampered := state + "ff"
	_, err = parseOAuthState(tampered)
	if err == nil {
		t.Fatal("expected error for tampered state signature, got nil")
	}
}

func TestOAuthState_MissingSignature(t *testing.T) {
	// State without signature separator
	stateData := OAuthStateData{
		TenantSlug:  "tenant",
		RedirectURL: "/page",
		Nonce:       "nonce",
		CreatedAt:   time.Now(),
	}
	data, _ := json.Marshal(stateData)
	payload := base64.URLEncoding.EncodeToString(data)

	// No HMAC appended
	_, err := parseOAuthState(payload)
	if err == nil {
		t.Fatal("expected error for unsigned state, got nil")
	}
}

// ============================================================================
// OAuth callback error path tests
// ============================================================================

func TestOAuthCallback_MissingCode(t *testing.T) {
	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	// No code param
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?state=somestate", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp["success"] != false {
		t.Error("expected success=false")
	}
}

func TestOAuthCallback_MissingState(t *testing.T) {
	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	// No state param
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?code=somecode", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestOAuthCallback_MissingBoth(t *testing.T) {
	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestOAuthCallback_InvalidState(t *testing.T) {
	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	// Provide code but an invalid (non-base64) state
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?code=authcode123&state=!!!invalid!!!", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp["success"] != false {
		t.Error("expected success=false")
	}
}

func TestOAuthCallback_ExpiredState(t *testing.T) {
	router := gin.New()
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	// Build a state that is expired (created 10 minutes ago)
	stateData := OAuthStateData{
		TenantSlug:  "expired-tenant",
		RedirectURL: "/home",
		Nonce:       "nonce-expired",
		CreatedAt:   time.Now().Add(-10*time.Minute - 5*time.Second), // expired (beyond 10min threshold)
	}
	stateJSON, _ := json.Marshal(stateData)
	payload := base64.URLEncoding.EncodeToString(stateJSON)
	sig := computeStateHMAC([]byte(payload))
	state := payload + "." + sig

	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?code=authcode&state="+state, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for expired state, got %d", w.Code)
	}
}

// ============================================================================
// OAuthStateData struct tests
// ============================================================================

func TestOAuthStateData_JSONRoundTrip(t *testing.T) {
	original := OAuthStateData{
		TenantSlug:  "test-slug",
		RedirectURL: "/api/v2/dashboard",
		Nonce:       "random-nonce-value",
		CreatedAt:   time.Now().Truncate(time.Second),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded OAuthStateData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.TenantSlug != original.TenantSlug {
		t.Errorf("TenantSlug mismatch: got %q, want %q", decoded.TenantSlug, original.TenantSlug)
	}
	if decoded.RedirectURL != original.RedirectURL {
		t.Errorf("RedirectURL mismatch: got %q, want %q", decoded.RedirectURL, original.RedirectURL)
	}
	if decoded.Nonce != original.Nonce {
		t.Errorf("Nonce mismatch: got %q, want %q", decoded.Nonce, original.Nonce)
	}
}

func TestOAuthTokenResponse_Fields(t *testing.T) {
	resp := OAuthTokenResponse{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "refresh-456",
		IDToken:      "id-token-789",
		Scope:        "openid email profile",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded OAuthTokenResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.AccessToken != "access-123" {
		t.Errorf("AccessToken mismatch: %q", decoded.AccessToken)
	}
	if decoded.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn mismatch: %d", decoded.ExpiresIn)
	}
	if decoded.IDToken != "id-token-789" {
		t.Errorf("IDToken mismatch: %q", decoded.IDToken)
	}
}
