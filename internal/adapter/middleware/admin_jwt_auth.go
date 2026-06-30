package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AdminJWTAuth validates a OIDC JWT and requires the "admin" role.
// Used by v2 API routes that receive JWT tokens from external clients.
// For v1 web UI routes, use AdminAuth() (session-based) instead.
func AdminJWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// If no Authorization header, fall back to session-based admin auth
		// to support the web frontend which uses session cookies after OAuth login.
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			authHelper(c, common.RoleAdminUser)
			return
		}

		if !oidcEnabled || jwksManager == nil {
			authHelper(c, common.RoleAdminUser)
			return
		}

		claims, err := validateJWT(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": err.Error(),
			})
			c.Abort()
			return
		}

		roles := extractRoles(claims.Roles)
		if !hasRole(roles, "admin") && !hasRole(roles, "root") {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Admin role required",
			})
			c.Abort()
			return
		}

		c.Set("admin_sub", claims.Subject)
		c.Set("admin_email", claims.Email)
		c.Set("admin_roles", roles)
		if im, _ := common.GetAccountByZitadelSubGRPC(c.Request.Context(), claims.Subject); im != nil {
			c.Set("identity_account_id", im.ID)
		}

		c.Next()
	}
}

// RootJWTAuth is like AdminJWTAuth but requires the "root" role specifically.
// Falls back to session-based auth when no JWT Bearer token is present.
func RootJWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			authHelper(c, common.RoleRootUser)
			return
		}

		if !oidcEnabled || jwksManager == nil {
			authHelper(c, common.RoleRootUser)
			return
		}

		claims, err := validateJWT(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": err.Error(),
			})
			c.Abort()
			return
		}

		roles := extractRoles(claims.Roles)
		if !hasRole(roles, "root") {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Root role required",
			})
			c.Abort()
			return
		}

		c.Set("admin_sub", claims.Subject)
		c.Set("admin_email", claims.Email)
		c.Set("admin_roles", roles)

		c.Next()
	}
}

// validateJWT extracts and validates a Bearer JWT from the Authorization header.
func validateJWT(c *gin.Context) (*OIDCClaims, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return nil, errors.New("Authorization header required")
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	tokenString = strings.TrimPrefix(tokenString, "bearer ")
	if tokenString == authHeader {
		return nil, errors.New("Bearer token required")
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
		common.SysLog(fmt.Sprintf("admin JWT validation failed: %v", err))
		return nil, errors.New("invalid or expired token")
	}

	claims, ok := token.Claims.(*OIDCClaims)
	if !ok {
		return nil, errors.New("invalid or expired token")
	}

	// Use the same multi-issuer set as the main OIDCAuth middleware so that
	// a domain rebrand does not selectively break the admin path.
	adminIssuerOK := false
	for _, accepted := range oidcIssuers {
		if claims.Issuer == accepted {
			adminIssuerOK = true
			break
		}
	}
	if !adminIssuerOK {
		common.SysLog(fmt.Sprintf("admin JWT issuer mismatch: got %s, accepted %v", claims.Issuer, oidcIssuers))
		return nil, errors.New("invalid or expired token")
	}

	return claims, nil
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
