package handler

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const orgIDPlaceholderSuffix = "_PLACEHOLDER"

// OIDCCallback handles the OAuth authorization-code callback from the OIDC provider.
func OIDCCallback(c *gin.Context) {
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

	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Missing code or state parameter",
		})
		return
	}

	stateData, err := parseOAuthState(state)
	if err != nil {
		common.SysError(fmt.Sprintf("Invalid OAuth state: %v", err))
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid state parameter",
		})
		return
	}

	if time.Since(stateData.CreatedAt) > 10*time.Minute {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "OAuth state expired, please try again",
		})
		return
	}

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

	expectedNonce, _ := session.Get("oauth_nonce").(string)

	tokenResp, err := exchangeCodeForToken(code, codeVerifier)
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to exchange code for token: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to obtain access token",
		})
		return
	}

	claims, ok := verifyCallbackIDToken(c, tokenResp.IDToken, expectedNonce)
	if !ok {
		return
	}

	tenant, ok := resolveOrCreateCallbackTenant(c, stateData.TenantSlug, claims)
	if !ok {
		return
	}

	if !recordOrgBindingOutcome(tenant.IDPOrgID, claims.OrgID) {
		rejectOrgMismatch(c, tenant.Id, tenant.IDPOrgID, claims.OrgID)
		return
	}

	user, ok := resolveCallbackUser(c, claims, tenant.Id)
	if !ok {
		return
	}

	persistAuthenticatedSession(c, session, user, claims.Subject, tokenResp, stateData.TenantSlug)
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

	redirectURL := stateData.RedirectURL
	if redirectURL == "" || redirectURL == "/dashboard" {
		redirectURL = "/oauth/oidc"
	}
	c.Redirect(http.StatusFound, redirectURL)
}

func verifyCallbackIDToken(c *gin.Context, idToken, expectedNonce string) (*IDTokenClaims, bool) {
	var claims *IDTokenClaims
	if idToken != "" {
		var err error
		claims, err = validateIDToken(idToken, expectedNonce)
		if err != nil {
			common.SysError(fmt.Sprintf("ID token validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "ID token validation failed"})
			return nil, false
		}
		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("ID token validated for user: %s (org: %s)", claims.Email, claims.OrgID))
		}
	}
	if claims == nil || claims.Subject == "" {
		common.SysError("ID token missing or does not contain subject claim")
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "ID token is required for authentication"})
		return nil, false
	}
	return claims, true
}

func resolveOrCreateCallbackTenant(c *gin.Context, tenantSlug string, claims *IDTokenClaims) (*repo.Tenant, bool) {
	tenant, err := repo.GetTenantBySlug(tenantSlug)
	if err != nil && os.Getenv("OIDC_AUTO_CREATE_TENANT") == "true" && claims.OrgID != "" {
		orgDomain := claims.OrgDomain
		if orgDomain == "" {
			orgDomain = tenantSlug
		}
		tenant, err = repo.CreateTenantFromIDP(claims.OrgID, orgDomain, orgDomain)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to auto-create tenant: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create tenant"})
			return nil, false
		}
		common.SysLog(fmt.Sprintf("Auto-created tenant: %s (org: %s)", tenant.Slug, tenant.IDPOrgID))
	}
	if err != nil || tenant == nil {
		common.SysError(fmt.Sprintf("Tenant not found: %s", tenantSlug))
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
		return nil, false
	}
	return tenant, true
}

func rejectOrgMismatch(c *gin.Context, tenantID, tenantOrgID, claimOrgID string) {
	common.SysError(fmt.Sprintf("oidc callback: organization mismatch — tenant %s is bound to org %s, ID token carries org %s; login refused",
		tenantID, tenantOrgID, claimOrgID))
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorSystem, 0,
		governance.ActionAuthFailed, governance.ResourceTenant, 0,
		fmt.Sprintf(`{"reason":"tenant_org_mismatch","tenant_id":%q,"tenant_org_id":%q,"claim_org_id":%q}`,
			tenantID, tenantOrgID, claimOrgID),
	))
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "此账号所属组织与该租户不匹配 / This account's organization does not match this tenant",
		"error_code": "TENANT_ORG_MISMATCH",
	})
}

func resolveCallbackUser(c *gin.Context, claims *IDTokenClaims, tenantID string) (*repo.User, bool) {
	oidcClaims := &repo.OIDCUserClaims{
		Sub:               claims.Subject,
		Email:             claims.Email,
		EmailVerified:     claims.EmailVerified,
		Name:              claims.Name,
		PreferredUsername: claims.PreferredUsername,
		OrgID:             claims.OrgID,
		OrgDomain:         claims.OrgDomain,
	}
	user, _, err := repo.CreateUserFromIDPClaims(oidcClaims, tenantID)
	if err != nil {
		if os.Getenv("OIDC_AUTO_CREATE_USER") != "true" {
			common.SysError(fmt.Sprintf("User not found and auto-create disabled: %v", err))
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "User not registered for this tenant"})
			return nil, false
		}
		common.SysError(fmt.Sprintf("Failed to create user from OIDC claims: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create user account"})
		return nil, false
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "User account is disabled"})
		return nil, false
	}
	return user, true
}

func linkCallbackPlatformAndDefaultToken(c *gin.Context, session sessions.Session, user *repo.User, subject string) {
	if im, _ := common.GetAccountByZitadelSubGRPC(c.Request.Context(), subject); im != nil {
		session.Set("identity_account_id", im.ID)
		if linkErr := repo.LinkUserPlatformAccount(user.Id, im.ID, false); linkErr != nil {
			common.SysLog(fmt.Sprintf("oidc callback: account link failed for user %d (account=%d): %v", user.Id, im.ID, linkErr))
		}
	}
	if tokenCount, _ := repo.CountUserTokens(user.Id); tokenCount == 0 {
		if defaultToken, err := repo.AutoCreateDefaultToken(user.Id); err != nil {
			common.SysError(fmt.Sprintf("Failed to auto-create default token for user %d: %v", user.Id, err))
		} else {
			common.SysLog(fmt.Sprintf("Auto-created default token for user %s (id=%d, key_prefix=%s)", user.Username, user.Id, defaultToken.Key[:8]))
		}
	}
}

func persistAuthenticatedSession(c *gin.Context, session sessions.Session, user *repo.User, subject string, tokenResp *OAuthTokenResponse, tenantSlug string) {
	if err := middleware.RotateSessionID(c); err != nil {
		common.SysError(fmt.Sprintf("oidc callback: session rotation failed for user %d: %v", user.Id, err))
	}
	linkCallbackPlatformAndDefaultToken(c, session, user, subject)
	session.Delete("pkce_code_verifier")
	session.Delete("oauth_nonce")
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("group", user.Group)
	session.Set("oauth_access_token", tokenResp.AccessToken)
	session.Set("oauth_refresh_token", tokenResp.RefreshToken)
	session.Set("oauth_id_token", tokenResp.IDToken)
	session.Set("oauth_token_expires_at", time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second).Unix())
	session.Set("tenant_slug", tenantSlug)
}

func orgIDIsBound(orgID string) bool {
	return orgID != "" && !strings.HasSuffix(orgID, orgIDPlaceholderSuffix)
}

func recordOrgBindingOutcome(tenantOrgID, claimOrgID string) (admit bool) {
	switch {
	case claimOrgID == "":
		metrics.RecordTenantGate(metrics.TenantGateOutcomeOrgClaimAbsent)
		return true
	case !orgIDIsBound(tenantOrgID):
		metrics.RecordTenantGate(metrics.TenantGateOutcomeOrgTenantUnbound)
		return true
	case tenantOrgID == claimOrgID:
		metrics.RecordTenantGate(metrics.TenantGateOutcomeOrgMatched)
		return true
	default:
		metrics.RecordTenantGate(metrics.TenantGateOutcomeOrgMismatchDenied)
		return false
	}
}
