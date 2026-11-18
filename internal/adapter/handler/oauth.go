package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultAuthorizePath  = "/oauth/v2/authorize"
	defaultTokenPath      = "/oauth/v2/token"
	defaultEndSessionPath = "/oidc/v1/end_session"
	defaultUserinfoPath   = "/oidc/v1/userinfo"
)

func oidcAuthorizePath() string {
	if v := os.Getenv("OIDC_AUTHORIZE_PATH"); v != "" {
		return v
	}
	return defaultAuthorizePath
}

func oidcTokenPath() string {
	if v := os.Getenv("OIDC_TOKEN_PATH"); v != "" {
		return v
	}
	return defaultTokenPath
}

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
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
}

// OAuthStateData contains signed state payload fields for an OAuth flow.
type OAuthStateData struct {
	TenantSlug  string    `json:"tenant_slug"`
	RedirectURL string    `json:"redirect_url"`
	Nonce       string    `json:"nonce"`
	CreatedAt   time.Time `json:"created_at"`
}

// PKCEData contains Proof Key for Code Exchange verifier and challenge values.
type PKCEData struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

// IDTokenClaims represents verified OIDC ID token claims using vendor-neutral default keys.
type IDTokenClaims struct {
	jwt.RegisteredClaims
	Email             string                 `json:"email"`
	EmailVerified     bool                   `json:"email_verified"`
	Name              string                 `json:"name"`
	PreferredUsername string                 `json:"preferred_username"`
	OrgID             string                 `json:"org_id"`
	OrgDomain         string                 `json:"org_domain"`
	Roles             map[string]interface{} `json:"roles"`
	Nonce             string                 `json:"nonce"`
}

// OIDCLoginRedirect redirects the user to the OIDC provider's login page.
func OIDCLoginRedirect(c *gin.Context) {
	tenantSlug := c.Param("tenant_slug")
	if tenantSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "租户标识不能为空 / Tenant slug is required",
		})
		return
	}

	if !isValidTenantSlug(tenantSlug) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的租户标识格式 / Invalid tenant slug format",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(tenantSlug)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "租户不存在 / Tenant not found",
		})
		return
	}

	if !tenant.IsEnabled() {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "租户已被禁用或暂停 / Tenant is disabled or suspended",
		})
		return
	}

	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		redirectURL = "/dashboard"
	}

	if !isValidRedirectURL(redirectURL) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的重定向URL / Invalid redirect URL",
		})
		return
	}

	state, nonce, err := generateOAuthState(tenantSlug, redirectURL)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to generate OAuth state: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal server error"})
		return
	}

	var pkceData *PKCEData
	if isPKCEEnabled() {
		pkceData, err = generatePKCE()
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to generate PKCE: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal server error"})
			return
		}
	}

	session := sessions.Default(c)
	if pkceData != nil {
		session.Set("pkce_code_verifier", pkceData.CodeVerifier)
	}
	session.Set("oauth_nonce", nonce)
	if err := session.Save(); err != nil {
		common.SysError(fmt.Sprintf("Failed to save session: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Internal server error"})
		return
	}

	prompt := "login"
	if c.Query("register") == "true" {
		prompt = "create"
	}

	authURL := buildOIDCAuthURL(tenant.IDPOrgID, state, nonce, pkceData, prompt)
	c.Redirect(http.StatusFound, authURL)
}

// GetSessionInfo returns the current session user profile after OIDC login.
func GetSessionInfo(c *gin.Context) {
	session := sessions.Default(c)
	id := session.Get("id")
	if id == nil {
		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("GetSessionInfo: session has no 'id' key (cookie present: %v)", c.Request.Header.Get("Cookie") != ""))
		}
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Not logged in"})
		return
	}

	userId, ok := id.(int)
	if !ok {
		common.SysError(fmt.Sprintf("GetSessionInfo: id type assertion failed: got %T(%v)", id, id))
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid session"})
		return
	}

	user, err := repo.GetUserById(userId, false)
	if err != nil {
		common.SysError(fmt.Sprintf("GetSessionInfo: failed to get user %d: %v", userId, err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load user data"})
		return
	}

	tenantSlug := sessionTenantSlug(session, user.TenantId, userId)
	tenantRole, isPayer := tenantRoleView(user)
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
			"tenant_role":  tenantRole,
			"is_payer":     isPayer,
			"quota":        user.Quota,
			"used_quota":   user.UsedQuota,
		},
	})
}

// OIDCLogout clears the local session and redirects to the OIDC end-session endpoint when an ID token exists.
func OIDCLogout(c *gin.Context) {
	session := sessions.Default(c)
	idToken, _ := session.Get("oauth_id_token").(string)
	userID, _ := session.Get("id").(int)

	session.Clear()
	_ = session.Save()

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userID,
		governance.ActionAuthLogout, governance.ResourceUser, userID,
		`{"provider":"oidc"}`))

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

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Logged out successfully"})
}
