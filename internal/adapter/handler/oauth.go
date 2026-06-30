package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OIDC provider endpoint paths.
//
// Best practice (and the intended end-state) is to resolve these from the
// provider's discovery document <issuer>/.well-known/openid-configuration
// (authorization_endpoint / token_endpoint / end_session_endpoint). They are
// extracted here as configurable constants/env so the code carries no
// vendor-specific path: the defaults below match the historical layout, and a
// deploy targeting a different IdP overrides each via the matching OIDC_*_PATH
// env (or, preferably, wires a discovery step). See doc/oidc-setup-guide.md.
const (
	defaultAuthorizePath  = "/oauth/v2/authorize"
	defaultTokenPath      = "/oauth/v2/token"
	defaultEndSessionPath = "/oidc/v1/end_session"
	defaultUserinfoPath   = "/oidc/v1/userinfo"
)

// oidcAuthorizePath returns the authorization endpoint path, overridable via
// OIDC_AUTHORIZE_PATH (ideally sourced from the discovery document).
func oidcAuthorizePath() string {
	if v := os.Getenv("OIDC_AUTHORIZE_PATH"); v != "" {
		return v
	}
	return defaultAuthorizePath
}

// oidcTokenPath returns the token endpoint path, overridable via OIDC_TOKEN_PATH.
func oidcTokenPath() string {
	if v := os.Getenv("OIDC_TOKEN_PATH"); v != "" {
		return v
	}
	return defaultTokenPath
}

// oidcEndSessionPath returns the end-session (logout) endpoint path, overridable
// via OIDC_END_SESSION_PATH.
func oidcEndSessionPath() string {
	if v := os.Getenv("OIDC_END_SESSION_PATH"); v != "" {
		return v
	}
	return defaultEndSessionPath
}

// OAuthTokenResponse is the token endpoint response from the OIDC provider.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"` // JWT containing user claims
	Scope        string `json:"scope"`
}

// OAuthStateData contains state information for OAuth flow
type OAuthStateData struct {
	TenantSlug  string    `json:"tenant_slug"`
	RedirectURL string    `json:"redirect_url"`
	Nonce       string    `json:"nonce"`
	CreatedAt   time.Time `json:"created_at"`
}

// PKCEData contains PKCE (Proof Key for Code Exchange) data for OAuth flow
// PKCE prevents authorization code interception attacks
type PKCEData struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

// IDTokenClaims represents the claims in an OIDC ID token.
//
// The org/role JSON tags use vendor-NEUTRAL default keys (org_id, roles, …).
// Provider-specific claim keys (which may be vendor-prefixed URNs) are injected
// at deploy time via OIDC_CLAIM_* env vars and overlaid by the middleware's
// configurable-claim resolver — the code itself carries no vendor claim URN.
type IDTokenClaims struct {
	jwt.RegisteredClaims
	Email             string                 `json:"email"`
	EmailVerified     bool                   `json:"email_verified"`
	Name              string                 `json:"name"`
	PreferredUsername string                 `json:"preferred_username"`
	OrgID             string                 `json:"org_id"`
	OrgDomain         string                 `json:"org_domain"`
	Roles             map[string]interface{} `json:"roles"`
	Nonce             string                 `json:"nonce"` // OIDC nonce for replay protection
}

// OIDCLoginRedirect redirects the user to the OIDC provider's login page.
// Route: GET /api/v2/:tenant_slug/auth/login
func OIDCLoginRedirect(c *gin.Context) {
	tenantSlug := c.Param("tenant_slug")
	if tenantSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "租户标识不能为空 / Tenant slug is required",
		})
		return
	}

	// Validate tenant slug format (alphanumeric, hyphens, underscores only)
	if !isValidTenantSlug(tenantSlug) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的租户标识格式 / Invalid tenant slug format",
		})
		return
	}

	// Get tenant by slug
	tenant, err := repo.GetTenantBySlug(tenantSlug)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "租户不存在 / Tenant not found",
		})
		return
	}

	// Check if tenant is enabled
	if !tenant.IsEnabled() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "租户已被禁用或暂停 / Tenant is disabled or suspended",
		})
		return
	}

	// Get redirect URL (where to redirect after login)
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		redirectURL = "/dashboard" // Default redirect
	}

	// Validate redirect URL to prevent open redirect attacks
	if !isValidRedirectURL(redirectURL) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的重定向URL / Invalid redirect URL",
		})
		return
	}

	// Generate state parameter (contains tenant slug + redirect URL + nonce)
	state, nonce, err := generateOAuthState(tenantSlug, redirectURL)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to generate OAuth state: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Internal server error",
		})
		return
	}

	// Generate PKCE data if enabled
	var pkceData *PKCEData
	if isPKCEEnabled() {
		pkceData, err = generatePKCE()
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to generate PKCE: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Internal server error",
			})
			return
		}
	}

	// Store PKCE code_verifier and nonce in session for later verification
	session := sessions.Default(c)
	if pkceData != nil {
		session.Set("pkce_code_verifier", pkceData.CodeVerifier)
	}
	session.Set("oauth_nonce", nonce)
	if err := session.Save(); err != nil {
		common.SysError(fmt.Sprintf("Failed to save session: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Internal server error",
		})
		return
	}

	// Determine prompt mode: "create" for registration, "login" for fresh login
	prompt := "login"
	if c.Query("register") == "true" {
		prompt = "create"
	}

	// Build OIDC authorization URL
	authURL := buildOIDCAuthURL(tenant.IDPOrgID, state, nonce, pkceData, prompt)

	// Redirect to OIDC provider login page
	c.Redirect(http.StatusFound, authURL)
}

// OIDCCallback handles the OAuth callback from the OIDC provider.
// Route: GET /api/v2/oauth/callback
func OIDCCallback(c *gin.Context) {
	// Check for error response from the OIDC provider
	if errCode := c.Query("error"); errCode != "" {
		errDesc := c.Query("error_description")
		common.SysError(fmt.Sprintf("OAuth error from IdP: %s - %s", errCode, errDesc))
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("OAuth authentication failed: %s", errDesc),
			"code":    errCode,
		})
		return
	}

	// Get authorization code and state from query params
	code := c.Query("code")
	state := c.Query("state")

	// Validate parameters
	if code == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Missing code or state parameter",
		})
		return
	}

	// Parse and validate state
	stateData, err := parseOAuthState(state)
	if err != nil {
		common.SysError(fmt.Sprintf("Invalid OAuth state: %v", err))
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid state parameter",
		})
		return
	}

	// Check state expiration (10 minutes)
	if time.Since(stateData.CreatedAt) > 10*time.Minute {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "OAuth state expired, please try again",
		})
		return
	}

	// Get session and retrieve PKCE code_verifier and nonce
	session := sessions.Default(c)
	var codeVerifier string
	if isPKCEEnabled() {
		codeVerifier, _ = session.Get("pkce_code_verifier").(string)
		if codeVerifier == "" {
			common.SysError("PKCE code_verifier not found in session")
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "PKCE verification failed, please try again",
			})
			return
		}
	}

	// Retrieve expected nonce for ID token verification
	expectedNonce, _ := session.Get("oauth_nonce").(string)

	// Exchange authorization code for tokens (with PKCE if enabled)
	tokenResp, err := exchangeCodeForToken(code, codeVerifier)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to exchange code for token: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to obtain access token",
		})
		return
	}

	// Validate ID token (required for user identity)
	var claims *IDTokenClaims
	if tokenResp.IDToken != "" {
		claims, err = validateIDToken(tokenResp.IDToken, expectedNonce)
		if err != nil {
			common.SysError(fmt.Sprintf("ID token validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "ID token validation failed",
			})
			return
		}

		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("ID token validated for user: %s (org: %s)", claims.Email, claims.OrgID))
		}
	}

	if claims == nil || claims.Subject == "" {
		common.SysError("ID token missing or does not contain subject claim")
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "ID token is required for authentication",
		})
		return
	}

	// Find or create tenant from OIDC organization
	tenant, err := repo.GetTenantBySlug(stateData.TenantSlug)
	if err != nil && os.Getenv("OIDC_AUTO_CREATE_TENANT") == "true" && claims.OrgID != "" {
		orgDomain := claims.OrgDomain
		if orgDomain == "" {
			orgDomain = stateData.TenantSlug
		}
		orgName := orgDomain
		tenant, err = repo.CreateTenantFromIDP(claims.OrgID, orgDomain, orgName)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to auto-create tenant: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to create tenant",
			})
			return
		}
		common.SysLog(fmt.Sprintf("Auto-created tenant: %s (org: %s)", tenant.Slug, tenant.IDPOrgID))
	}
	if err != nil || tenant == nil {
		common.SysError(fmt.Sprintf("Tenant not found: %s", stateData.TenantSlug))
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found",
		})
		return
	}

	// Find or create local user from OIDC identity
	oidcClaims := &repo.OIDCUserClaims{
		Sub:               claims.Subject,
		Email:             claims.Email,
		EmailVerified:     claims.EmailVerified,
		Name:              claims.Name,
		PreferredUsername: claims.PreferredUsername,
		OrgID:             claims.OrgID,
		OrgDomain:         claims.OrgDomain,
	}

	user, _, err := repo.CreateUserFromIDPClaims(oidcClaims, tenant.Id)
	if err != nil {
		if os.Getenv("OIDC_AUTO_CREATE_USER") != "true" {
			common.SysError(fmt.Sprintf("User not found and auto-create disabled: %v", err))
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "User not registered for this tenant",
			})
			return
		}
		common.SysError(fmt.Sprintf("Failed to create user from OIDC claims: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user account",
		})
		return
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "User account is disabled",
		})
		return
	}

	// Ensure user has at least one API token (auto-create if none).
	tokenCount, _ := repo.CountUserTokens(user.Id)
	if tokenCount == 0 {
		if defaultToken, err := repo.AutoCreateDefaultToken(user.Id); err != nil {
			common.SysError(fmt.Sprintf("Failed to auto-create default token for user %d: %v", user.Id, err))
		} else {
			common.SysLog(fmt.Sprintf("Auto-created default token for user %s (id=%d, key_prefix=%s)", user.Username, user.Id, defaultToken.Key[:8]))
		}
	}

	// Resolve platform account ID for billing integration.
	// Store in session so billing endpoints work without JWT.
	if im, _ := common.GetAccountByZitadelSubGRPC(c.Request.Context(), claims.Subject); im != nil {
		session.Set("identity_account_id", im.ID)
	}

	// Clear PKCE and nonce from session (one-time use)
	session.Delete("pkce_code_verifier")
	session.Delete("oauth_nonce")

	// Set V1 session variables (required for frontend authentication)
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("group", user.Group)

	// Store OAuth tokens for V2 API use
	session.Set("oauth_access_token", tokenResp.AccessToken)
	session.Set("oauth_refresh_token", tokenResp.RefreshToken)
	session.Set("oauth_id_token", tokenResp.IDToken)
	session.Set("oauth_token_expires_at", time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second).Unix())
	session.Set("tenant_slug", stateData.TenantSlug)

	if err := session.Save(); err != nil {
		common.SysError(fmt.Sprintf("Failed to save session: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Internal server error",
		})
		return
	}

	common.SysLog(fmt.Sprintf("OAuth login successful: user=%s (id=%d) tenant=%s", user.Username, user.Id, stateData.TenantSlug))

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, user.Id,
		governance.ActionAuthLoginSuccess, governance.ResourceUser, user.Id,
		fmt.Sprintf(`{"provider":"oidc","tenant_slug":%q}`, stateData.TenantSlug)))

	// Redirect to frontend OAuth callback page to complete login
	redirectURL := stateData.RedirectURL
	if redirectURL == "" || redirectURL == "/dashboard" {
		// redirect URI /oauth/oidc 须在 IdP(Casdoor)Application 注册,owner-gated
		redirectURL = "/oauth/oidc" // frontend callback route; kept in lockstep with web/ App.jsx route
	}

	c.Redirect(http.StatusFound, redirectURL)
}

// GetSessionInfo returns current session user info for frontend integration
// after OAuth callback. No auth middleware required - session itself is the proof.
// Route: GET /api/v2/auth/session-info
func GetSessionInfo(c *gin.Context) {
	session := sessions.Default(c)
	id := session.Get("id")
	if id == nil {
		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("GetSessionInfo: session has no 'id' key (cookie present: %v)", c.Request.Header.Get("Cookie") != ""))
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Not logged in",
		})
		return
	}

	userId, ok := id.(int)
	if !ok {
		common.SysError(fmt.Sprintf("GetSessionInfo: id type assertion failed: got %T(%v)", id, id))
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid session",
		})
		return
	}

	user, err := repo.GetUserById(userId, false)
	if err != nil {
		common.SysError(fmt.Sprintf("GetSessionInfo: failed to get user %d: %v", userId, err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to load user data",
		})
		return
	}

	// Get tenant_slug from session (stored during OAuth callback)
	tenantSlug, _ := session.Get("tenant_slug").(string)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
			"tenant_slug":  tenantSlug,
			"quota":        user.Quota,
			"used_quota":   user.UsedQuota,
		},
	})
}

// OIDCLogout logs out user from the OIDC provider and clears session
// Route: POST /api/v2/oauth/logout
func OIDCLogout(c *gin.Context) {
	// Get ID token from session
	session := sessions.Default(c)
	idToken, _ := session.Get("oauth_id_token").(string)
	userID, _ := session.Get("id").(int)

	// Clear session
	session.Clear()
	session.Save()

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userID,
		governance.ActionAuthLogout, governance.ResourceUser, userID,
		`{"provider":"oidc"}`))

	// If ID token exists, redirect to the OIDC provider logout endpoint
	if idToken != "" {
		postLogoutRedirectURI := os.Getenv("OIDC_POST_LOGOUT_REDIRECT_URI")
		if postLogoutRedirectURI == "" {
			postLogoutRedirectURI = "/login"
		}

		logoutURL := fmt.Sprintf(
			"%s"+oidcEndSessionPath()+"?id_token_hint=%s&post_logout_redirect_uri=%s",
			os.Getenv("OIDC_ISSUER"),
			url.QueryEscape(idToken),
			url.QueryEscape(postLogoutRedirectURI),
		)

		c.Redirect(http.StatusFound, logoutURL)
		return
	}

	// No ID token, just return success
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logged out successfully",
	})
}

// RefreshAccessToken refreshes the access token using refresh token
// Route: POST /api/v2/oauth/refresh
func RefreshAccessToken(c *gin.Context) {
	// Get refresh token from session or request body
	session := sessions.Default(c)
	refreshToken, _ := session.Get("oauth_refresh_token").(string)

	if refreshToken == "" {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			refreshToken = req.RefreshToken
		}
	}

	if refreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Refresh token is required",
		})
		return
	}

	// Exchange refresh token for new access token
	tokenResp, err := refreshAccessToken(refreshToken)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to refresh access token: %v", err))
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Failed to refresh access token",
		})
		return
	}

	// Update session
	session.Set("oauth_access_token", tokenResp.AccessToken)
	if tokenResp.RefreshToken != "" {
		session.Set("oauth_refresh_token", tokenResp.RefreshToken)
	}
	session.Save()

	// Return new tokens
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"access_token":  tokenResp.AccessToken,
		"refresh_token": tokenResp.RefreshToken,
		"expires_in":    tokenResp.ExpiresIn,
	})
}

// ============================================================================
// Helper functions
// ============================================================================

// computeStateHMAC computes HMAC-SHA256 of data using SessionSecret as the key.
func computeStateHMAC(data []byte) string {
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// generateOAuthState generates a state parameter and nonce for OAuth flow.
// The state is HMAC-signed to prevent tampering.
// Format: base64(json).hmac_hex
// Returns: state (signed), nonce (for ID token verification), error
func generateOAuthState(tenantSlug string, redirectURL string) (string, string, error) {
	// Generate random nonce (used for both state and ID token verification)
	nonceBytes := make([]byte, 32) // 256 bits for security
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	nonce := base64.URLEncoding.EncodeToString(nonceBytes)

	// Create state data
	stateData := OAuthStateData{
		TenantSlug:  tenantSlug,
		RedirectURL: redirectURL,
		Nonce:       nonce,
		CreatedAt:   time.Now(),
	}

	// Serialize to JSON
	stateJSON, err := json.Marshal(stateData)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal state: %w", err)
	}

	// Encode as base64
	payload := base64.URLEncoding.EncodeToString(stateJSON)

	// Sign with HMAC-SHA256 using SessionSecret
	sig := computeStateHMAC([]byte(payload))

	// Final state format: payload.signature
	state := payload + "." + sig
	return state, nonce, nil
}

// parseOAuthState parses and validates the state parameter.
// Verifies HMAC-SHA256 signature before parsing to prevent tampering.
// Expected format: base64(json).hmac_hex
func parseOAuthState(state string) (*OAuthStateData, error) {
	// Split into payload and signature
	dotIdx := strings.LastIndex(state, ".")
	if dotIdx < 0 {
		return nil, fmt.Errorf("invalid state format: missing signature")
	}
	payload := state[:dotIdx]
	sig := state[dotIdx+1:]

	// Verify HMAC signature
	expectedSig := computeStateHMAC([]byte(payload))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid state signature")
	}

	// Decode base64
	stateJSON, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 encoding: %w", err)
	}

	// Parse JSON
	var stateData OAuthStateData
	if err := json.Unmarshal(stateJSON, &stateData); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return &stateData, nil
}

// buildOIDCAuthURL builds the OIDC authorization URL with PKCE and nonce support
// prompt: "login" for fresh login, "create" for registration
func buildOIDCAuthURL(orgID string, state string, nonce string, pkceData *PKCEData, prompt string) string {
	issuer := os.Getenv("OIDC_ISSUER")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	redirectURI := os.Getenv("OIDC_REDIRECT_URI")
	scopes := os.Getenv("OIDC_OAUTH_SCOPES")
	if scopes == "" {
		scopes = "openid email profile offline_access"
	}

	// Build authorization URL using url.Values for proper encoding
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", state)
	params.Set("nonce", nonce) // OIDC nonce for ID token validation

	// Add organization hint if provided
	if orgID != "" {
		params.Set("organization", orgID)
	}

	// Set prompt mode:
	// "login" - force fresh login (avoids stale session interference)
	// "create" - show registration form
	if prompt != "" {
		params.Set("prompt", prompt)
	}

	// Add PKCE parameters if enabled
	if pkceData != nil {
		params.Set("code_challenge", pkceData.CodeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	return issuer + oidcAuthorizePath() + "?" + params.Encode()
}

// exchangeCodeForToken exchanges authorization code for access token (with PKCE support)
func exchangeCodeForToken(code string, codeVerifier string) (*OAuthTokenResponse, error) {
	issuer := os.Getenv("OIDC_ISSUER")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	clientSecret := os.Getenv("OIDC_CLIENT_SECRET")
	redirectURI := os.Getenv("OIDC_REDIRECT_URI")

	// Build token request
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("client_id", clientID)
	data.Set("redirect_uri", redirectURI)

	// Add client secret (for confidential clients)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	// Add PKCE code_verifier if provided
	if codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send POST request to token endpoint
	resp, err := client.PostForm(issuer+oidcTokenPath(), data)
	if err != nil {
		return nil, fmt.Errorf("failed to post token request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Parse error response for better error messages
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token endpoint error: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokenResp, nil
}

// refreshAccessToken refreshes the access token using refresh token
func refreshAccessToken(refreshToken string) (*OAuthTokenResponse, error) {
	issuer := os.Getenv("OIDC_ISSUER")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	clientSecret := os.Getenv("OIDC_CLIENT_SECRET")

	// Build token request
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", clientID)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send POST request to token endpoint
	resp, err := client.PostForm(issuer+oidcTokenPath(), data)
	if err != nil {
		return nil, fmt.Errorf("failed to post token request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Parse error response for better error messages
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token refresh error: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokenResp, nil
}

// ============================================================================
// PKCE (Proof Key for Code Exchange) Functions
// ============================================================================

// isPKCEEnabled checks if PKCE is enabled via environment variable
func isPKCEEnabled() bool {
	return os.Getenv("OIDC_ENABLE_PKCE") == "true"
}

// generatePKCE generates PKCE code_verifier and code_challenge
// Uses S256 method (SHA256 hash of verifier, base64url encoded)
func generatePKCE() (*PKCEData, error) {
	// Generate random code_verifier (43-128 characters)
	// Using 32 bytes = 256 bits, which base64url encodes to 43 characters
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate code_verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate code_challenge using S256 method
	// code_challenge = BASE64URL(SHA256(code_verifier))
	h := sha256.New()
	h.Write([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return &PKCEData{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
	}, nil
}

// ============================================================================
// ID Token Validation Functions
// ============================================================================

// validateIDToken validates the ID token JWT and returns the claims.
// Validates: signature (via JWKS), structure, expiration, issuer (exact match),
// audience (exact match), and nonce.
//
// SECURITY: The token signature is verified against the IdP's published JWKS keys,
// preventing token forgery regardless of how the token was obtained.
func validateIDToken(idToken string, expectedNonce string) (*IDTokenClaims, error) {
	// Verify the token signature using the OIDC provider JWKS public keys.
	token, err := middleware.VerifyIDTokenWithJWKS(idToken, &IDTokenClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to verify ID token: %w", err)
	}

	claims, ok := token.Claims.(*IDTokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid ID token claims type")
	}

	if err := validateIDTokenClaims(claims, expectedNonce); err != nil {
		return nil, err
	}
	return claims, nil
}

// validateIDTokenClaims performs OIDC claim-level checks on an already-parsed
// IDTokenClaims: issuer, audience, expiry, nonce, issued-at. Signature
// verification is the caller's responsibility (see validateIDToken). Split out
// so unit tests can exercise claim logic without an initialized JWKS manager.
func validateIDTokenClaims(claims *IDTokenClaims, expectedNonce string) error {
	expectedIssuer := os.Getenv("OIDC_ISSUER")
	if claims.Issuer != expectedIssuer {
		return fmt.Errorf("invalid issuer: expected %s, got %s", expectedIssuer, claims.Issuer)
	}

	expectedAudience := os.Getenv("OIDC_CLIENT_ID")
	audienceValid := false
	for _, aud := range claims.Audience {
		if aud == expectedAudience {
			audienceValid = true
			break
		}
	}
	if !audienceValid {
		return fmt.Errorf("invalid audience: client_id %s not found in audience", expectedAudience)
	}

	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("ID token has expired")
	}

	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return fmt.Errorf("invalid nonce: expected %s, got %s", expectedNonce, claims.Nonce)
	}

	if claims.IssuedAt != nil && claims.IssuedAt.After(time.Now().Add(5*time.Minute)) {
		return fmt.Errorf("ID token issued in the future")
	}

	return nil
}

// ============================================================================
// Input Validation Functions
// ============================================================================

// isValidTenantSlug validates tenant slug format
// Allows: alphanumeric, hyphens, underscores; 1-63 characters
func isValidTenantSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 63 {
		return false
	}

	// Check each character
	for i, c := range slug {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			continue
		}
		// First character cannot be hyphen or underscore
		if i == 0 && (c == '-' || c == '_') {
			return false
		}
		return false
	}

	// First character cannot be hyphen or underscore
	if slug[0] == '-' || slug[0] == '_' {
		return false
	}

	return true
}

// isValidRedirectURL validates the redirect URL to prevent open redirect attacks
// Only allows relative URLs or URLs to allowed domains
func isValidRedirectURL(redirectURL string) bool {
	// Empty URL is valid (will use default)
	if redirectURL == "" {
		return true
	}

	// Relative URLs are always allowed
	if strings.HasPrefix(redirectURL, "/") && !strings.HasPrefix(redirectURL, "//") {
		return true
	}

	// Parse the URL
	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}

	// Disallow javascript: and data: URLs
	if parsedURL.Scheme == "javascript" || parsedURL.Scheme == "data" {
		return false
	}

	// If host is specified, check against allowed domains
	if parsedURL.Host != "" {
		allowedDomains := os.Getenv("OIDC_ALLOWED_REDIRECT_DOMAINS")
		if allowedDomains == "" {
			// No domains configured, only allow relative URLs
			return false
		}

		// Check if host is in allowed domains list
		allowed := strings.Split(allowedDomains, ",")
		for _, domain := range allowed {
			domain = strings.TrimSpace(domain)
			if domain == parsedURL.Host {
				return true
			}
			// Also allow subdomains
			if strings.HasSuffix(parsedURL.Host, "."+domain) {
				return true
			}
		}
		return false
	}

	return true
}
