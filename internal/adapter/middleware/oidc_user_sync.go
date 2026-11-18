package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TenantContext represents the tenant context injected into Gin context.
type TenantContext struct {
	TenantID     string   `json:"tenant_id"`
	UserID       int      `json:"user_id"`
	IDPSubject   string   `json:"idp_subject"`
	Email        string   `json:"email"`
	Username     string   `json:"username"`
	Roles        []string `json:"roles"`
	TenantRole   string   `json:"tenant_role,omitempty"`
	RoleTenantID string   `json:"role_tenant_id,omitempty"`
}

func mapOIDCUserToLurus(claims *OIDCClaims) (tenantID string, lurusUserID int, err error) {
	if strings.TrimSpace(claims.OrgID) == "" {
		return "", 0, errors.New("OIDC token has empty org_id; cannot resolve tenant")
	}

	tenant, err := repo.GetTenantByIDPOrgID(claims.OrgID)
	if err != nil {
		if os.Getenv("OIDC_AUTO_CREATE_TENANT") != "true" {
			return "", 0, fmt.Errorf("tenant not found for OIDC Org ID: %s", claims.OrgID)
		}
		tenant, err = repo.CreateTenantFromIDP(claims.OrgID, claims.OrgDomain, claims.ResourceOwnerName)
		if err != nil {
			return "", 0, fmt.Errorf("failed to create tenant: %w", err)
		}
		common.SysLog(fmt.Sprintf("Auto-created tenant: id=%s, org_id=%s, name=%s",
			tenant.Id, tenant.IDPOrgID, tenant.Name))
		if cfgErr := repo.InitializeDefaultTenantConfigs(tenant.Id); cfgErr != nil {
			common.SysError(fmt.Sprintf("Failed to initialize tenant configs: %v", cfgErr))
		}
	}

	tenantID = tenant.Id
	if !tenant.IsEnabled() {
		return "", 0, fmt.Errorf("tenant is disabled or suspended: %s", tenantID)
	}

	mapping, err := repo.GetUserMappingByIDPSubject(claims.Subject, tenantID)
	if err != nil {
		if os.Getenv("OIDC_AUTO_CREATE_USER") != "true" {
			return "", 0, fmt.Errorf("user mapping not found for OIDC subject: %s", claims.Subject)
		}
		userClaims := &repo.OIDCUserClaims{
			Sub:               claims.Subject,
			Email:             claims.Email,
			EmailVerified:     claims.EmailVerified,
			Name:              claims.Name,
			PreferredUsername: claims.PreferredUsername,
			OrgID:             claims.OrgID,
			OrgDomain:         claims.OrgDomain,
		}
		user, _, createErr := repo.CreateUserFromIDPClaims(userClaims, tenantID)
		if createErr != nil {
			return "", 0, fmt.Errorf("failed to create user: %w", createErr)
		}
		common.SysLog(fmt.Sprintf("Auto-created user: tenant=%s, lurus_user=%d, idp_subject=%s, email=%s",
			tenantID, user.Id, claims.Subject, claims.Email))
		return tenantID, user.Id, nil
	}

	if syncErr := repo.SyncUserDataFromIDP(mapping.Id, claims.Email, claims.Name, claims.PreferredUsername); syncErr != nil {
		common.SysError(fmt.Sprintf("Failed to sync user data: %v", syncErr))
	}

	return tenantID, mapping.LurusUserID, nil
}

// GetTenantContext retrieves tenant context from Gin context.
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

// RequireRole checks if the authenticated user has a specific role.
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

// RequireAnyRole checks if the authenticated user has any of the specified roles.
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
