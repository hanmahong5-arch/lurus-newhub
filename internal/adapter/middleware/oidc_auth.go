package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OIDCClaims represents the JWT claims issued by the upstream OIDC provider.
// Includes both standard OIDC claims and provider-specific "extra" claims (org
// id / org domain / resource owner / project roles).
//
// The JSON struct tags below use vendor-NEUTRAL default keys (org_id, roles,
// …). Different IdPs advertise tenant/role information under different (often
// vendor-prefixed) claim keys, so the EFFECTIVE key is configurable per deploy
// via OIDC_CLAIM_* env vars; resolveOIDCClaims overlays those onto the struct
// after the standard JSON decode. The neutral tags keep the type usable in
// tests and as a marshal target without leaking any vendor claim URN into code.
type OIDCClaims struct {
	jwt.RegisteredClaims
	Email             string                 `json:"email"`
	EmailVerified     bool                   `json:"email_verified"`
	Name              string                 `json:"name"`
	PreferredUsername string                 `json:"preferred_username"`
	OrgID             string                 `json:"org_id"`
	OrgDomain         string                 `json:"org_domain"`
	ResourceOwnerID   string                 `json:"resource_owner_id"`
	ResourceOwnerName string                 `json:"resource_owner_name"`
	Roles             map[string]interface{} `json:"roles"`
}

// claimKeySet holds the configurable JSON claim keys for the provider's
// non-standard (tenant/role) claims. Resolved once at init from env.
type claimKeySet struct {
	OrgID             string
	OrgDomain         string
	ResourceOwnerID   string
	ResourceOwnerName string
	Roles             string
}

// oidcClaimKeys carries the deploy-configured claim keys. Defaults are
// vendor-neutral; the actual keys an IdP advertises (which may be
// vendor-prefixed URNs) are injected at deploy time via OIDC_CLAIM_* env vars
// — the code itself carries no vendor-specific claim identifiers.
var oidcClaimKeys = claimKeySet{
	OrgID:             envOr("OIDC_CLAIM_ORG_ID", "org_id"),
	OrgDomain:         envOr("OIDC_CLAIM_ORG_DOMAIN", "org_domain"),
	ResourceOwnerID:   envOr("OIDC_CLAIM_RESOURCE_OWNER_ID", "resource_owner_id"),
	ResourceOwnerName: envOr("OIDC_CLAIM_RESOURCE_OWNER_NAME", "resource_owner_name"),
	Roles:             envOr("OIDC_CLAIM_ROLES", "roles"),
}

// envOr returns the env value for key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// resolveOIDCClaims overlays the configurable extra claims (org/role) onto an
// already-parsed OIDCClaims by re-reading the raw token claim map under the
// deploy-configured keys. This is only needed when a deploy points OIDC_CLAIM_*
// at non-default keys; when the keys equal the neutral struct-tag defaults the
// standard JSON decode already populated the fields and this is a cheap no-op.
// rawClaims is the full decoded claim set (map form) of the same token.
func resolveOIDCClaims(c *OIDCClaims, rawClaims map[string]interface{}) {
	if c == nil || rawClaims == nil {
		return
	}
	if k := oidcClaimKeys.OrgID; k != "org_id" {
		if v, ok := rawClaims[k].(string); ok {
			c.OrgID = v
		}
	}
	if k := oidcClaimKeys.OrgDomain; k != "org_domain" {
		if v, ok := rawClaims[k].(string); ok {
			c.OrgDomain = v
		}
	}
	if k := oidcClaimKeys.ResourceOwnerID; k != "resource_owner_id" {
		if v, ok := rawClaims[k].(string); ok {
			c.ResourceOwnerID = v
		}
	}
	if k := oidcClaimKeys.ResourceOwnerName; k != "resource_owner_name" {
		if v, ok := rawClaims[k].(string); ok {
			c.ResourceOwnerName = v
		}
	}
	if k := oidcClaimKeys.Roles; k != "roles" {
		if v, ok := rawClaims[k].(map[string]interface{}); ok {
			c.Roles = v
		}
	}
}

// TenantContext represents the tenant context injected into Gin context
type TenantContext struct {
	TenantID   string   `json:"tenant_id"`   // Tenant ID
	UserID     int      `json:"user_id"`     // Lurus user ID
	IDPSubject string   `json:"idp_subject"` // OIDC subject (idp_subject; canonical cross-service field)
	Email      string   `json:"email"`       // User email
	Username   string   `json:"username"`    // Username
	Roles      []string `json:"roles"`       // User roles in this tenant
}

// JWK represents a JSON Web Key
type JWK struct {
	Kty string `json:"kty"` // Key Type
	Use string `json:"use"` // Public Key Use
	Kid string `json:"kid"` // Key ID
	Alg string `json:"alg"` // Algorithm
	N   string `json:"n"`   // Modulus (for RSA)
	E   string `json:"e"`   // Exponent (for RSA)
}

// JWKSet represents a set of JSON Web Keys
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// JWKSManager manages JSON Web Keys from the OIDC provider's JWKS endpoint.
// Automatically refreshes keys periodically.
type JWKSManager struct {
	jwksURI       string
	publicKeys    map[string]*rsa.PublicKey
	mu            sync.RWMutex
	lastUpdate    time.Time
	updateFailed  bool
	refreshTicker *time.Ticker
	// refreshing prevents concurrent refresh attempts (race condition fix)
	refreshing bool
	refreshMu  sync.Mutex
	// minRefreshInterval prevents too frequent refreshes on key not found
	minRefreshInterval time.Duration
}

// oidcHTTPClient is the HTTP client used for JWKS fetching.
// Using a dedicated client with timeout prevents indefinite hangs on network issues.
var oidcHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

var (
	jwksManager     *JWKSManager
	jwksManagerOnce sync.Once
	// oidcIssuers holds the parsed set of accepted issuer values.
	// OIDC_ISSUER supports comma-separated values so that a rebrand or IdP
	// migration (e.g. one issuer domain → another) can be rolled out without
	// a hard cutover: add the new issuer first, flip the IdP, then remove
	// the old entry after the alias window expires (≥90 days per contract).
	oidcIssuers         []string
	oidcJwksURI         string
	oidcClientID        string
	oidcEnabled         bool
	jwksRefreshInterval time.Duration = 1 * time.Hour
	// oidcAllowedAudiences holds the parsed set of ADDITIONAL accepted "aud"
	// values (OIDC_ALLOWED_AUDIENCES, comma-separated). oidcClientID is always
	// an accepted audience regardless of this list (see checkAudience).
	oidcAllowedAudiences []string
	// oidcConsumerAudiences holds the client_ids of first-party CONSUMER apps
	// (OIDC_CONSUMER_AUDIENCES, comma-separated) accepted by RequireOIDCToken
	// only. These are deliberately NOT accepted by OIDCAuth: a consumer app
	// token carries no org_id and must never resolve to a newhub tenant.
	oidcConsumerAudiences []string
)

// InitOIDCAuth initializes the OIDC authentication system.
// Must be called during application startup.
func InitOIDCAuth() error {
	// Load OIDC configuration from environment variables.
	oidcEnabled = os.Getenv("OIDC_ENABLED") == "true"
	if !oidcEnabled {
		common.SysLog("OIDC authentication is disabled")
		return nil
	}

	// Re-read the configurable claim keys here (post-.env). The package-level
	// oidcClaimKeys initializer runs at program init, which is BEFORE main()
	// calls godotenv.Load(".env"); without this refresh any OIDC_CLAIM_* set only
	// in a .env file would be silently ignored and the vendor-neutral defaults
	// used instead. Real process env (K8s) is honoured either way.
	oidcClaimKeys = claimKeySet{
		OrgID:             envOr("OIDC_CLAIM_ORG_ID", "org_id"),
		OrgDomain:         envOr("OIDC_CLAIM_ORG_DOMAIN", "org_domain"),
		ResourceOwnerID:   envOr("OIDC_CLAIM_RESOURCE_OWNER_ID", "resource_owner_id"),
		ResourceOwnerName: envOr("OIDC_CLAIM_RESOURCE_OWNER_NAME", "resource_owner_name"),
		Roles:             envOr("OIDC_CLAIM_ROLES", "roles"),
	}

	// OIDC_ISSUER accepts a comma-separated list so that two issuers
	// can be valid concurrently during a domain rebrand / IdP migration window.
	// Each value is trimmed; an all-whitespace entry is silently dropped.
	rawIssuer := os.Getenv("OIDC_ISSUER")
	for _, part := range strings.Split(rawIssuer, ",") {
		if v := strings.TrimSpace(part); v != "" {
			oidcIssuers = append(oidcIssuers, v)
		}
	}
	// OIDC_JWKS_URI: the provider's JWKS endpoint. Best practice is to derive
	// this from <issuer>/.well-known/openid-configuration (jwks_uri); kept as an
	// explicit env so the value can be injected without an extra discovery hop
	// and stays IdP-neutral. See doc/oidc-setup-guide.md.
	oidcJwksURI = os.Getenv("OIDC_JWKS_URI")
	oidcClientID = os.Getenv("OIDC_CLIENT_ID")

	// OIDC_ALLOWED_AUDIENCES: optional comma-separated ADDITIONAL allow-list for
	// the JWT "aud" claim. This service's own OIDC_CLIENT_ID is ALWAYS an
	// accepted audience (see checkAudience), so a legitimately-issued newhub
	// token passes without any extra configuration. This var only widens the
	// accepted set for deploys whose IdP additionally stamps tokens with a
	// project/resource id distinct from the client_id.
	oidcAllowedAudiences = nil
	rawAudiences := os.Getenv("OIDC_ALLOWED_AUDIENCES")
	for _, part := range strings.Split(rawAudiences, ",") {
		if v := strings.TrimSpace(part); v != "" {
			oidcAllowedAudiences = append(oidcAllowedAudiences, v)
		}
	}

	// OIDC_CONSUMER_AUDIENCES: comma-separated client_ids of first-party
	// CONSUMER apps (e.g. the Lutu APP) permitted on the consumer gate
	// RequireOIDCToken. Such an app holds its own client_id, so its tokens
	// never carry newhub's OIDC_CLIENT_ID in "aud" — see
	// setting.GetConsumerAudRequired for the rollout flag that governs whether
	// a non-matching audience is merely logged or rejected.
	oidcConsumerAudiences = nil
	rawConsumerAudiences := os.Getenv("OIDC_CONSUMER_AUDIENCES")
	for _, part := range strings.Split(rawConsumerAudiences, ",") {
		if v := strings.TrimSpace(part); v != "" {
			oidcConsumerAudiences = append(oidcConsumerAudiences, v)
		}
	}

	// Validate required environment variables
	if len(oidcIssuers) == 0 {
		return errors.New("OIDC_ISSUER is not set")
	}
	if oidcJwksURI == "" {
		return errors.New("OIDC_JWKS_URI is not set")
	}
	if oidcClientID == "" {
		return errors.New("OIDC_CLIENT_ID is not set")
	}

	// Initialize JWKS Manager
	jwksManagerOnce.Do(func() {
		jwksManager = NewJWKSManager(oidcJwksURI)
	})

	common.SysLog("OIDC authentication initialized successfully")
	common.SysLog(fmt.Sprintf("OIDC Issuers: %v", oidcIssuers))
	common.SysLog(fmt.Sprintf("OIDC JWKS URI: %s", oidcJwksURI))

	return nil
}

// NewJWKSManager creates a new JWKS Manager instance.
// Deprecated: Use NewJWKSManagerWithContext instead.
func NewJWKSManager(jwksURI string) *JWKSManager {
	return NewJWKSManagerWithContext(context.Background(), jwksURI)
}

// NewJWKSManagerWithContext creates a new JWKS Manager instance with context support.
// The background refresh goroutine exits when ctx is cancelled.
func NewJWKSManagerWithContext(ctx context.Context, jwksURI string) *JWKSManager {
	m := &JWKSManager{
		jwksURI:            jwksURI,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 30 * time.Second, // Prevent refresh more than once per 30 seconds
	}

	// Initial key fetch
	err := m.refreshKeys()
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to fetch JWKS keys: %v", err))
		m.updateFailed = true
	}

	// Start background refresh with context
	common.SafeGoWithContext(ctx, func(c context.Context) {
		m.autoRefreshWithContext(c)
	})

	return m
}

// refreshKeys fetches public keys from the OIDC provider's JWKS endpoint
func (m *JWKSManager) refreshKeys() error {
	common.SysLog(fmt.Sprintf("Fetching JWKS from: %s", m.jwksURI))

	// Fetch JWKS from the OIDC provider
	resp, err := oidcHTTPClient.Get(m.jwksURI)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	// Parse JWKS response
	var jwkSet JWKSet
	err = json.NewDecoder(resp.Body).Decode(&jwkSet)
	if err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	// Convert JWKs to RSA public keys
	newKeys := make(map[string]*rsa.PublicKey)
	for _, jwk := range jwkSet.Keys {
		if jwk.Kty != "RSA" {
			continue // Only support RSA keys
		}

		publicKey, err := jwkToRSAPublicKey(jwk)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to convert JWK to RSA public key (kid=%s): %v", jwk.Kid, err))
			continue
		}

		newKeys[jwk.Kid] = publicKey
	}

	if len(newKeys) == 0 {
		return errors.New("no valid RSA keys found in JWKS")
	}

	// Update keys (thread-safe)
	m.mu.Lock()
	m.publicKeys = newKeys
	m.lastUpdate = time.Now()
	m.updateFailed = false
	m.mu.Unlock()

	common.SysLog(fmt.Sprintf("Successfully refreshed %d JWKS keys", len(newKeys)))

	return nil
}

// autoRefreshWithContext periodically refreshes JWKS keys with context support
func (m *JWKSManager) autoRefreshWithContext(ctx context.Context) {
	m.refreshTicker = time.NewTicker(jwksRefreshInterval)
	defer m.refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("JWKS auto-refresh stopped")
			return
		case <-m.refreshTicker.C:
			err := m.refreshKeys()
			if err != nil {
				common.SysError(fmt.Sprintf("Auto-refresh JWKS failed: %v", err))
			}
		}
	}
}

// getKey retrieves an RSA public key by Key ID (kid)
func (m *JWKSManager) getKey(kid string) (*rsa.PublicKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if key, ok := m.publicKeys[kid]; ok {
		return key, nil
	}

	return nil, fmt.Errorf("public key not found for kid: %s", kid)
}

// tryRefreshKeys attempts to refresh keys with protection against concurrent refreshes
// Returns true if refresh was performed (or already in progress), false if skipped due to rate limiting
func (m *JWKSManager) tryRefreshKeys() (refreshed bool, err error) {
	m.refreshMu.Lock()

	// Check if already refreshing
	if m.refreshing {
		m.refreshMu.Unlock()
		// Wait a bit for ongoing refresh to complete
		time.Sleep(100 * time.Millisecond)
		return true, nil // Assume ongoing refresh will succeed
	}

	// Check if refreshed too recently (rate limiting)
	m.mu.RLock()
	lastUpdate := m.lastUpdate
	m.mu.RUnlock()

	if time.Since(lastUpdate) < m.minRefreshInterval {
		m.refreshMu.Unlock()
		return false, nil // Skipped due to rate limiting
	}

	// Mark as refreshing
	m.refreshing = true
	m.refreshMu.Unlock()

	// Ensure we clear the refreshing flag when done
	defer func() {
		m.refreshMu.Lock()
		m.refreshing = false
		m.refreshMu.Unlock()
	}()

	// Perform the actual refresh
	err = m.refreshKeys()
	return true, err
}

// getKeyWithRefresh retrieves a key, attempting to refresh if not found
// This method safely handles concurrent refresh attempts
func (m *JWKSManager) getKeyWithRefresh(kid string) (*rsa.PublicKey, error) {
	// First attempt - try to get key
	key, err := m.getKey(kid)
	if err == nil {
		return key, nil
	}

	// Key not found, try to refresh
	refreshed, refreshErr := m.tryRefreshKeys()
	if refreshErr != nil {
		common.SysError(fmt.Sprintf("Failed to refresh JWKS: %v", refreshErr))
		// Return original error if refresh failed
		return nil, err
	}

	if !refreshed {
		// Rate limited, return original error
		return nil, fmt.Errorf("public key not found for kid: %s (refresh rate limited)", kid)
	}

	// Second attempt - try to get key after refresh
	return m.getKey(kid)
}

// VerifyIDTokenWithJWKS verifies an ID token JWT signature using the JWKS keys.
// This is exported so the OAuth handler can verify ID tokens received from the
// token endpoint, preventing token forgery even if TLS is compromised.
// Returns the parsed jwt.Token with verified claims, or an error.
func VerifyIDTokenWithJWKS(idToken string, claims jwt.Claims) (*jwt.Token, error) {
	if jwksManager == nil {
		return nil, errors.New("JWKS manager not initialized; is OIDC enabled?")
	}

	token, err := jwt.ParseWithClaims(idToken, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errors.New("missing kid in token header")
		}

		return jwksManager.getKeyWithRefresh(kid)
	})
	if err != nil {
		return nil, fmt.Errorf("ID token signature verification failed: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("ID token is not valid")
	}
	return token, nil
}

// jwkToRSAPublicKey converts a JWK to RSA public key
func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	// Decode modulus (n)
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}

	// Decode exponent (e)
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	// Convert to big.Int
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	// Create RSA public key
	publicKey := &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}

	return publicKey, nil
}

// handleSessionFallback checks for valid session-based OAuth data when no Bearer token is present.
// Returns true if it handled the request (either successfully authenticated via session, or aborted).
// Returns false if no session auth data is available and caller should try other methods.
func handleSessionFallback(c *gin.Context) bool {
	// Check if session middleware is available (prevents panic when session store not configured)
	if _, exists := c.Get(sessions.DefaultKey); !exists {
		return false
	}

	session := sessions.Default(c)

	// Check for session user ID (set during OAuth callback)
	sessionID := session.Get("id")
	if sessionID == nil {
		return false // No session, let caller handle
	}

	userID, ok := sessionID.(int)
	if !ok || userID == 0 {
		return false
	}

	// Check if session has OAuth access token
	accessToken, _ := session.Get("oauth_access_token").(string)
	expiresAt, _ := session.Get("oauth_token_expires_at").(int64)

	// If access token exists and is not expired, use it to validate
	if accessToken != "" && expiresAt > time.Now().Unix() {
		// Valid session with non-expired OAuth token
		// Build tenant context from session data
		user, err := repo.GetUserById(userID, false)
		if err != nil {
			common.SysError(fmt.Sprintf("Session fallback: failed to get user %d: %v", userID, err))
			return false
		}

		// SECURITY: reject a disabled/banned user even though their session
		// cookie is still valid — mirrors authHelper (auth.go).
		if user.Status == common.UserStatusDisabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "account disabled",
			})
			c.Abort()
			return true
		}

		// SECURITY (cross-tenant isolation): scope the tenant to the
		// authenticated user's OWN record — never the URL :tenant_slug.
		// Deriving it from the slug let any session-authenticated user
		// assume another tenant's context just by changing the path; worse,
		// downstream guards compare tenantCtx.TenantID against that same
		// slug (e.g. GetCreditPoolForEndUser's TENANT_MISMATCH check), so
		// the slug was being compared to itself and always passed. Mirrors
		// authHelper (auth.go), which scopes tenant via the user's TenantId.
		tenantID := user.TenantId
		if tenantID == "" {
			tenantID = "default"
		}

		// Construct tenant context from session
		tenantCtx := &TenantContext{
			TenantID:   tenantID,
			UserID:     user.Id,
			IDPSubject: "", // Not available in session
			Email:      user.Email,
			Username:   user.Username,
			Roles:      []string{},
		}

		// Inject into gin context
		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", user.Id)
		c.Set("id", user.Id)

		c.Next()
		return true
	}

	// Session exists but token expired or missing — try constructing from session data directly
	if userID > 0 {
		user, err := repo.GetUserById(userID, false)
		if err != nil {
			return false
		}

		// SECURITY: reject a disabled/banned user (see non-expired branch above).
		if user.Status == common.UserStatusDisabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "account disabled",
			})
			c.Abort()
			return true
		}

		// SECURITY (cross-tenant isolation): see the non-expired branch
		// above — tenant comes from the user's own record, not the URL
		// :tenant_slug, so a session user cannot read another tenant.
		tenantID := user.TenantId
		if tenantID == "" {
			tenantID = "default"
		}

		tenantCtx := &TenantContext{
			TenantID:   tenantID,
			UserID:     user.Id,
			IDPSubject: "",
			Email:      user.Email,
			Username:   user.Username,
			Roles:      []string{},
		}

		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", user.Id)
		c.Set("id", user.Id)

		c.Next()
		return true
	}

	return false
}

// OIDCAuth is the Gin middleware for OIDC JWT authentication.
// Validates JWT tokens issued by the OIDC provider and injects tenant context.
func OIDCAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if OIDC is enabled
		if !oidcEnabled {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "OIDC authentication is not enabled",
			})
			c.Abort()
			return
		}

		// Extract Bearer token from Authorization header
		authHeader := c.GetHeader("Authorization")
		tokenString := ""

		if authHeader != "" {
			// Remove "Bearer " prefix
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			tokenString = strings.TrimPrefix(tokenString, "bearer ")
			if tokenString == authHeader {
				tokenString = "" // No valid Bearer prefix found
			}
		}

		// Session fallback: when no Bearer token, check session for OAuth data
		if tokenString == "" {
			if handled := handleSessionFallback(c); handled {
				return
			}
			// Session fallback failed — no auth available
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required: provide Bearer token or establish a session via OAuth login",
			})
			c.Abort()
			return
		}

		// Parse token to get Key ID (kid) from header
		token, err := jwt.ParseWithClaims(tokenString, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
			// Validate signing method
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			// Get Key ID from token header
			kid, ok := token.Header["kid"].(string)
			if !ok {
				return nil, errors.New("missing kid in token header")
			}

			// Get public key from JWKS Manager with safe refresh on key not found
			// Uses getKeyWithRefresh to prevent concurrent refresh race conditions
			publicKey, err := jwksManager.getKeyWithRefresh(kid)
			if err != nil {
				return nil, err
			}

			return publicKey, nil
		})

		if err != nil || !token.Valid {
			common.SysLog(fmt.Sprintf("JWT validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Token 无效或已过期 / Invalid or expired token",
			})
			c.Abort()
			return
		}

		// Extract claims
		claims, ok := token.Claims.(*OIDCClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid token claims",
			})
			c.Abort()
			return
		}

		// Overlay deploy-configured (possibly vendor-prefixed) org/role claim
		// keys onto the standard-decoded claims. No-op when OIDC_CLAIM_* keys
		// equal the neutral defaults already covered by the struct tags.
		applyConfigurableClaims(tokenString, claims)

		// Verify issuer against the accepted set.
		// OIDC_JWKS_URI is an independent env — it does not derive from
		// the issuer domain — so accepting multiple issuers does not require
		// fetching from multiple JWKS endpoints; the signing keys are the same
		// regardless of which domain the IdP advertises as its issuer.
		issuerOK := false
		for _, accepted := range oidcIssuers {
			if claims.Issuer == accepted {
				issuerOK = true
				break
			}
		}
		if !issuerOK {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid issuer: got %s", claims.Issuer),
			})
			c.Abort()
			return
		}

		// Verify audience: reject tokens minted for a different client/resource.
		if !checkAudience(claims.Audience) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid audience: got %v", []string(claims.Audience)),
			})
			c.Abort()
			return
		}

		// Map OIDC user to lurus user and tenant
		tenantID, lurusUserID, err := mapOIDCUserToLurus(claims)
		if err != nil {
			common.SysError(fmt.Sprintf("User mapping failed: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "用户身份映射失败 / User identity mapping failed",
			})
			c.Abort()
			return
		}

		// SECURITY: reject a disabled/banned local user even though their OIDC
		// token is otherwise valid — mirrors authHelper (auth.go) and the
		// session-fallback branches above.
		lurusUser, err := repo.GetUserById(lurusUserID, false)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to load mapped user %d: %v", lurusUserID, err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "用户身份映射失败 / User identity mapping failed",
			})
			c.Abort()
			return
		}
		if lurusUser.Status == common.UserStatusDisabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "account disabled",
			})
			c.Abort()
			return
		}

		// Sync account to lurus-platform (upsert) and carry identity_account_id
		// for wallet bridging. Best-effort: platform unavailability does not block auth.
		if im, _ := common.UpsertAccountGRPC(c.Request.Context(), claims.Subject, claims.Email, claims.Name, ""); im != nil {
			c.Set("identity_account_id", im.ID)
		}

		// Extract roles from claims
		roles := extractRoles(claims.Roles)

		// Create tenant context
		tenantCtx := &TenantContext{
			TenantID:   tenantID,
			UserID:     lurusUserID,
			IDPSubject: claims.Subject,
			Email:      claims.Email,
			Username:   claims.PreferredUsername,
			Roles:      roles,
		}

		// Inject tenant context into Gin context
		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", lurusUserID)
		c.Set("idp_subject", claims.Subject)

		// Log successful authentication (debug mode)
		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("User authenticated: tenant=%s, user=%d, email=%s, roles=%v",
				tenantID, lurusUserID, claims.Email, roles))
		}

		c.Next()
	}
}

// checkAudience validates a token's "aud" claim against the audiences this
// service accepts. A token passes when at least one of its audiences is either:
//   - oidcClientID (OIDC_CLIENT_ID) — this service's own client identifier, the
//     audience a legitimately-issued newhub token always carries (mirrors the
//     OAuth handler's validateIDTokenClaims, which requires OIDC_CLIENT_ID); or
//   - an entry in OIDC_ALLOWED_AUDIENCES — an optional extra allow-list for
//     deploys whose IdP additionally stamps tokens with a project/resource id.
//
// Enforcement is NOT opt-in: a token whose "aud" matches neither is rejected,
// so a token minted for a different client under the same issuer cannot be
// replayed against this service. When oidcClientID is empty (startup normally
// fast-fails first — OIDC_CLIENT_ID is required) the check fails closed rather
// than silently accepting every audience.
func checkAudience(aud jwt.ClaimStrings) bool {
	for _, a := range aud {
		// This service's own client_id is always an accepted audience.
		if oidcClientID != "" && a == oidcClientID {
			return true
		}
		// Any explicitly allow-listed audience is also accepted.
		for _, want := range oidcAllowedAudiences {
			if a == want {
				return true
			}
		}
	}
	return false
}

// applyConfigurableClaims overlays the deploy-configured org/role claim keys
// onto claims. It only re-decodes the token's raw claim map when at least one
// configured key differs from the neutral default — keeping the hot path
// allocation-free in the common (neutral-key) deployment.
func applyConfigurableClaims(tokenString string, claims *OIDCClaims) {
	if oidcClaimKeys.OrgID == "org_id" &&
		oidcClaimKeys.OrgDomain == "org_domain" &&
		oidcClaimKeys.ResourceOwnerID == "resource_owner_id" &&
		oidcClaimKeys.ResourceOwnerName == "resource_owner_name" &&
		oidcClaimKeys.Roles == "roles" {
		return // all keys are neutral defaults; struct tags already handled them
	}
	raw := jwt.MapClaims{}
	// Parse without verification: signature was already verified upstream;
	// here we only need the decoded claim map to read configurable keys.
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(tokenString, raw); err != nil {
		return
	}
	resolveOIDCClaims(claims, map[string]interface{}(raw))
}

// RequireOIDCToken is a lightweight authentication gate that verifies an
// OIDC-issued JWT (signature via JWKS + issuer) and aborts 401 when it is
// absent or invalid. Unlike OIDCAuth it deliberately does NOT map the
// caller to a newhub tenant/user or touch the DB: Lutu APP users are consumer
// OIDC identities with no newhub tenant, so OIDCAuth's tenant mapping
// would 500 (or, with OIDC_AUTO_CREATE_* on, pollute the tenant/user
// tables). This gate only answers "is this a valid logged-in Lurus user?",
// which is all endpoints like /lutu/search need to protect a shared upstream
// quota (Tavily). The verified subject is exposed as `oidc_user_id` for
// optional per-user accounting downstream.
func RequireOIDCToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !oidcEnabled {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "OIDC authentication is not enabled",
			})
			c.Abort()
			return
		}

		authHeader := c.GetHeader("Authorization")
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		tokenString = strings.TrimPrefix(tokenString, "bearer ")
		if authHeader == "" || tokenString == authHeader {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required: provide a Bearer token",
			})
			c.Abort()
			return
		}

		// Signature is checked against JWKS; exp/nbf are validated inside
		// jwt.ParseWithClaims (OIDCClaims embeds jwt.RegisteredClaims).
		claims := &OIDCClaims{}
		token, err := VerifyIDTokenWithJWKS(tokenString, claims)
		if err != nil || !token.Valid {
			common.SysLog(fmt.Sprintf("web_search JWT validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Token 无效或已过期 / Invalid or expired token",
			})
			c.Abort()
			return
		}

		// Issuer must be one of the accepted values (mirrors OIDCAuth — the
		// rebrand window may admit more than one issuer concurrently).
		issuerOK := false
		for _, accepted := range oidcIssuers {
			if claims.Issuer == accepted {
				issuerOK = true
				break
			}
		}
		if !issuerOK {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid issuer: got %s", claims.Issuer),
			})
			c.Abort()
			return
		}

		// Audience: a valid issuer alone does not say the token was minted FOR
		// this service. Without this check any token from any client under the
		// same issuer — every other Lurus product — can be replayed here and
		// spend the shared upstream quota this gate exists to protect.
		// Consumer apps hold their own client_id, so the accepted set is wider
		// than OIDCAuth's; see setting.GetConsumerAudRequired for why this is
		// staged rather than enforced outright.
		if !checkConsumerAudience(claims.Audience) {
			mode := setting.GetConsumerAudRequired()
			if mode == setting.ConsumerAudRequiredEnforce {
				metrics.RecordConsumerAudienceMismatch("enforce")
				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": fmt.Sprintf("Invalid audience: got %v", []string(claims.Audience)),
				})
				c.Abort()
				return
			}
			if mode == setting.ConsumerAudRequiredLog {
				metrics.RecordConsumerAudienceMismatch("log")
				common.SysLog(fmt.Sprintf(
					"consumer gate audience mismatch (admitted; OIDC_CONSUMER_AUD_REQUIRED=log): aud=%v sub=%s — add this aud to OIDC_CONSUMER_AUDIENCES before switching to enforce",
					[]string(claims.Audience), claims.Subject))
			}
		}

		c.Set("oidc_user_id", claims.Subject)
		c.Next()
	}
}

// checkConsumerAudience validates "aud" for the consumer gate
// (RequireOIDCToken). It accepts everything checkAudience accepts — this
// service's own OIDC_CLIENT_ID plus OIDC_ALLOWED_AUDIENCES — and additionally
// the first-party consumer client_ids in OIDC_CONSUMER_AUDIENCES.
//
// A token with no "aud" at all never passes: an audience-less token is exactly
// the shape this check exists to stop.
func checkConsumerAudience(aud jwt.ClaimStrings) bool {
	if checkAudience(aud) {
		return true
	}
	for _, a := range aud {
		for _, want := range oidcConsumerAudiences {
			if a == want {
				return true
			}
		}
	}
	return false
}

// mapOIDCUserToLurus maps an OIDC user to a lurus user and tenant.
// Auto-creates tenant and user if they don't exist.
func mapOIDCUserToLurus(claims *OIDCClaims) (tenantID string, lurusUserID int, err error) {
	// SECURITY (cross-tenant isolation): a blank org_id must never resolve to a
	// tenant. Without this guard the first user carrying no org_id would create
	// a tenant keyed on zitadel_org_id='' and every later org_id-less user from
	// any unrelated IdP would be folded into that same tenant, sharing its
	// channels/tokens/credit pool/logs. Reject the mapping so the request fails
	// authentication instead of silently cross-wiring tenants.
	if strings.TrimSpace(claims.OrgID) == "" {
		return "", 0, errors.New("OIDC token has empty org_id; cannot resolve tenant")
	}

	// Step 1: Map tenant (OIDC Org ID → lurus Tenant ID)
	tenant, err := repo.GetTenantByIDPOrgID(claims.OrgID)
	if err != nil {
		// Tenant doesn't exist, auto-create if enabled
		if os.Getenv("OIDC_AUTO_CREATE_TENANT") == "true" {
			tenant, err = repo.CreateTenantFromIDP(claims.OrgID, claims.OrgDomain, claims.ResourceOwnerName)
			if err != nil {
				return "", 0, fmt.Errorf("failed to create tenant: %w", err)
			}
			common.SysLog(fmt.Sprintf("Auto-created tenant: id=%s, org_id=%s, name=%s",
				tenant.Id, tenant.IDPOrgID, tenant.Name))

			// Initialize default configs for new tenant
			err = repo.InitializeDefaultTenantConfigs(tenant.Id)
			if err != nil {
				common.SysError(fmt.Sprintf("Failed to initialize tenant configs: %v", err))
			}
		} else {
			return "", 0, fmt.Errorf("tenant not found for OIDC Org ID: %s", claims.OrgID)
		}
	}

	tenantID = tenant.Id

	// Check if tenant is enabled
	if !tenant.IsEnabled() {
		return "", 0, fmt.Errorf("tenant is disabled or suspended: %s", tenantID)
	}

	// Step 2: Map user (OIDC subject → lurus User ID)
	mapping, err := repo.GetUserMappingByIDPSubject(claims.Subject, tenantID)
	if err != nil {
		// User mapping doesn't exist, auto-create if enabled
		if os.Getenv("OIDC_AUTO_CREATE_USER") == "true" {
			// Convert claims to model struct
			userClaims := &repo.OIDCUserClaims{
				Sub:               claims.Subject,
				Email:             claims.Email,
				EmailVerified:     claims.EmailVerified,
				Name:              claims.Name,
				PreferredUsername: claims.PreferredUsername,
				OrgID:             claims.OrgID,
				OrgDomain:         claims.OrgDomain,
			}

			// Create user and mapping
			user, _, err := repo.CreateUserFromIDPClaims(userClaims, tenantID)
			if err != nil {
				return "", 0, fmt.Errorf("failed to create user: %w", err)
			}

			common.SysLog(fmt.Sprintf("Auto-created user: tenant=%s, lurus_user=%d, idp_subject=%s, email=%s",
				tenantID, user.Id, claims.Subject, claims.Email))

			return tenantID, user.Id, nil
		}
		return "", 0, fmt.Errorf("user mapping not found for OIDC subject: %s", claims.Subject)
	}

	// Sync user data from the IdP (update email, display name, etc.)
	err = repo.SyncUserDataFromIDP(mapping.Id, claims.Email, claims.Name, claims.PreferredUsername)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to sync user data: %v", err))
		// Non-fatal error, continue
	}

	return tenantID, mapping.LurusUserID, nil
}

// extractRoles extracts role names from the OIDC roles claim.
// Accepts both shapes that IdPs commonly emit:
//   - a map keyed by role name (project-roles style, e.g. project-roles-shaped tokens)
//     → the map keys are the role names
//   - a map whose values are role-name strings or []string (some IdPs surface a
//     flat "roles" claim under the configured key) → those values are added too
//
// The actual claim key is configurable (oidcClaimKeys.Roles); this only
// normalizes the decoded value into a flat, de-duplicated []string.
func extractRoles(rolesMap map[string]interface{}) []string {
	var roles []string
	seen := map[string]bool{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			roles = append(roles, r)
		}
	}
	for key, val := range rolesMap {
		// Shape 1: role name is the map key (project-roles style).
		add(key)
		// Shape 2: the value itself carries role name(s).
		switch v := val.(type) {
		case string:
			add(v)
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					add(s)
				}
			}
		}
	}
	return roles
}

// GetTenantContext retrieves tenant context from Gin context
func GetTenantContext(c *gin.Context) (*TenantContext, error) {
	ctx, exists := c.Get("tenant_context")
	if !exists {
		return nil, errors.New("tenant context not found")
	}

	tenantCtx, ok := ctx.(*TenantContext)
	if !ok {
		return nil, errors.New("invalid tenant context type")
	}

	return tenantCtx, nil
}

// RequireRole middleware checks if user has a specific role
func RequireRole(requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantCtx, err := GetTenantContext(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Tenant context not found",
			})
			c.Abort()
			return
		}

		// Check if user has required role
		hasRole := false
		for _, role := range tenantCtx.Roles {
			if role == requiredRole {
				hasRole = true
				break
			}
		}

		if !hasRole {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": fmt.Sprintf("Insufficient permissions, required role: %s", requiredRole),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAnyRole middleware checks if user has any of the specified roles
func RequireAnyRole(requiredRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantCtx, err := GetTenantContext(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Tenant context not found",
			})
			c.Abort()
			return
		}

		// Check if user has any of the required roles
		hasRole := false
		for _, userRole := range tenantCtx.Roles {
			for _, requiredRole := range requiredRoles {
				if userRole == requiredRole {
					hasRole = true
					break
				}
			}
			if hasRole {
				break
			}
		}

		if !hasRole {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": fmt.Sprintf("Insufficient permissions, required roles: %v", requiredRoles),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
