package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

var (
	jwksManager           *JWKSManager
	jwksManagerOnce       sync.Once
	oidcIssuers           []string
	oidcJwksURI           string
	oidcClientID          string
	oidcEnabled           bool
	jwksRefreshInterval   time.Duration = 1 * time.Hour
	oidcAllowedAudiences  []string
	oidcConsumerAudiences []string
)

// OIDCAuth validates JWT tokens issued by the OIDC provider and injects tenant context.
func OIDCAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !oidcEnabled {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "OIDC authentication is not enabled",
			})
			c.Abort()
			return
		}

		tokenString := extractBearerToken(c.GetHeader("Authorization"))
		if tokenString == "" {
			if handled := handleSessionFallback(c); handled {
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required: provide Bearer token or establish a session via OAuth login",
			})
			c.Abort()
			return
		}

		token, err := jwt.ParseWithClaims(tokenString, &OIDCClaims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			kid, ok := token.Header["kid"].(string)
			if !ok {
				return nil, errors.New("missing kid in token header")
			}
			return jwksManager.getKeyWithRefresh(kid)
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

		claims, ok := token.Claims.(*OIDCClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid token claims",
			})
			c.Abort()
			return
		}

		applyConfigurableClaims(tokenString, claims)

		if !isAcceptedOIDCIssuer(claims.Issuer) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid issuer: got %s", claims.Issuer),
			})
			c.Abort()
			return
		}

		if !checkAudience(claims.Audience) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid audience: got %v", []string(claims.Audience)),
			})
			c.Abort()
			return
		}

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

		if im, _ := common.UpsertAccountGRPC(c.Request.Context(), claims.Subject, claims.Email, claims.Name, ""); im != nil {
			c.Set("identity_account_id", im.ID)
			if lurusUser.LurusAccountID == nil {
				if linkErr := repo.LinkUserPlatformAccount(lurusUserID, im.ID, false); linkErr != nil {
					common.SysLog(fmt.Sprintf("oidc jwt: account link failed for user %d (account=%d): %v", lurusUserID, im.ID, linkErr))
				}
			}
		}

		roles := extractRoles(claims.Roles)
		tenantCtx := &TenantContext{
			TenantID:     tenantID,
			UserID:       lurusUserID,
			IDPSubject:   claims.Subject,
			Email:        claims.Email,
			Username:     claims.PreferredUsername,
			Roles:        roles,
			TenantRole:   lurusUser.TenantRole,
			RoleTenantID: lurusUser.TenantId,
		}

		c.Set("tenant_context", tenantCtx)
		c.Set("tenant_id", tenantID)
		c.Set("user_id", lurusUserID)
		c.Set("idp_subject", claims.Subject)

		if os.Getenv("OIDC_DEBUG_LOGGING") == "true" {
			common.SysLog(fmt.Sprintf("User authenticated: tenant=%s, user=%d, email=%s, roles=%v",
				tenantID, lurusUserID, claims.Email, roles))
		}

		c.Next()
	}
}

// RequireOIDCToken verifies a consumer OIDC JWT (or platform HS256 session token) without mapping to a tenant.
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

		tokenString := extractBearerToken(c.GetHeader("Authorization"))
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required: provide a Bearer token",
			})
			c.Abort()
			return
		}

		claims := &OIDCClaims{}
		token, err := VerifyIDTokenWithJWKS(tokenString, claims)
		if err != nil || !token.Valid {
			if accountID, sessErr := common.ValidateIdentitySessionToken(tokenString); sessErr == nil && accountID > 0 {
				c.Set("identity_account_id", accountID)
				c.Set("oidc_user_id", fmt.Sprintf("identity:%d", accountID))
				c.Next()
				return
			}
			common.SysLog(fmt.Sprintf("web_search JWT validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Token 无效或已过期 / Invalid or expired token",
			})
			c.Abort()
			return
		}

		if !isAcceptedOIDCIssuer(claims.Issuer) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": fmt.Sprintf("Invalid issuer: got %s", claims.Issuer),
			})
			c.Abort()
			return
		}

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
