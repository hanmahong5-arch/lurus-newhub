package middleware

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/golang-jwt/jwt/v5"
)

// InitOIDCAuth initializes the OIDC authentication system at startup.
func InitOIDCAuth() error {
	oidcEnabled = os.Getenv("OIDC_ENABLED") == "true"
	if !oidcEnabled {
		common.SysLog("OIDC authentication is disabled")
		return nil
	}

	ReloadOIDCClaimKeys()

	oidcIssuers = append(oidcIssuers, parseCommaTrimmedList(os.Getenv("OIDC_ISSUER"))...)
	oidcJwksURI = os.Getenv("OIDC_JWKS_URI")
	oidcClientID = os.Getenv("OIDC_CLIENT_ID")
	oidcAllowedAudiences = parseCommaTrimmedList(os.Getenv("OIDC_ALLOWED_AUDIENCES"))
	oidcConsumerAudiences = parseCommaTrimmedList(os.Getenv("OIDC_CONSUMER_AUDIENCES"))

	if len(oidcIssuers) == 0 {
		return errors.New("OIDC_ISSUER is not set")
	}
	if oidcJwksURI == "" {
		return errors.New("OIDC_JWKS_URI is not set")
	}
	if oidcClientID == "" {
		return errors.New("OIDC_CLIENT_ID is not set")
	}

	jwksManagerOnce.Do(func() {
		jwksManager = NewJWKSManager(oidcJwksURI)
	})

	common.SysLog("OIDC authentication initialized successfully")
	common.SysLog(fmt.Sprintf("OIDC Issuers: %v", oidcIssuers))
	common.SysLog(fmt.Sprintf("OIDC JWKS URI: %s", oidcJwksURI))

	return nil
}

// OIDCClaims represents the JWT claims issued by the upstream OIDC provider.
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

type claimKeySet struct {
	OrgID             string
	OrgDomain         string
	ResourceOwnerID   string
	ResourceOwnerName string
	Roles             string
}

var oidcClaimKeys = claimKeySet{
	OrgID:             envOr("OIDC_CLAIM_ORG_ID", "org_id"),
	OrgDomain:         envOr("OIDC_CLAIM_ORG_DOMAIN", "org_domain"),
	ResourceOwnerID:   envOr("OIDC_CLAIM_RESOURCE_OWNER_ID", "resource_owner_id"),
	ResourceOwnerName: envOr("OIDC_CLAIM_RESOURCE_OWNER_NAME", "resource_owner_name"),
	Roles:             envOr("OIDC_CLAIM_ROLES", "roles"),
}

// ReloadOIDCClaimKeys re-reads the OIDC_CLAIM_* environment into the package's claim-key set.
func ReloadOIDCClaimKeys() {
	oidcClaimKeys = claimKeySet{
		OrgID:             envOr("OIDC_CLAIM_ORG_ID", "org_id"),
		OrgDomain:         envOr("OIDC_CLAIM_ORG_DOMAIN", "org_domain"),
		ResourceOwnerID:   envOr("OIDC_CLAIM_RESOURCE_OWNER_ID", "resource_owner_id"),
		ResourceOwnerName: envOr("OIDC_CLAIM_RESOURCE_OWNER_NAME", "resource_owner_name"),
		Roles:             envOr("OIDC_CLAIM_ROLES", "roles"),
	}
}

// ConfiguredExtraClaims carries values stored under deploy-configured OIDC_CLAIM_* keys.
type ConfiguredExtraClaims struct {
	OrgID             *string
	OrgDomain         *string
	ResourceOwnerID   *string
	ResourceOwnerName *string
	Roles             map[string]interface{}
}

func configuredKeysAreNeutral() bool {
	return oidcClaimKeys.OrgID == "org_id" &&
		oidcClaimKeys.OrgDomain == "org_domain" &&
		oidcClaimKeys.ResourceOwnerID == "resource_owner_id" &&
		oidcClaimKeys.ResourceOwnerName == "resource_owner_name" &&
		oidcClaimKeys.Roles == "roles"
}

func extraClaimsFromMap(rawClaims map[string]interface{}) ConfiguredExtraClaims {
	var out ConfiguredExtraClaims
	if rawClaims == nil {
		return out
	}
	if k := oidcClaimKeys.OrgID; k != "org_id" {
		if v, ok := rawClaims[k].(string); ok {
			out.OrgID = &v
		}
	}
	if k := oidcClaimKeys.OrgDomain; k != "org_domain" {
		if v, ok := rawClaims[k].(string); ok {
			out.OrgDomain = &v
		}
	}
	if k := oidcClaimKeys.ResourceOwnerID; k != "resource_owner_id" {
		if v, ok := rawClaims[k].(string); ok {
			out.ResourceOwnerID = &v
		}
	}
	if k := oidcClaimKeys.ResourceOwnerName; k != "resource_owner_name" {
		if v, ok := rawClaims[k].(string); ok {
			out.ResourceOwnerName = &v
		}
	}
	if k := oidcClaimKeys.Roles; k != "roles" {
		if v, ok := rawClaims[k].(map[string]interface{}); ok {
			out.Roles = v
		}
	}
	return out
}

// ResolveConfiguredExtraClaims decodes tokenString's claim map without signature verification
// to overlay deploy-configured OIDC_CLAIM_* keys onto an already-verified token.
func ResolveConfiguredExtraClaims(tokenString string) ConfiguredExtraClaims {
	if configuredKeysAreNeutral() {
		return ConfiguredExtraClaims{}
	}
	raw := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(tokenString, raw); err != nil {
		return ConfiguredExtraClaims{}
	}
	return extraClaimsFromMap(map[string]interface{}(raw))
}

func (e ConfiguredExtraClaims) applyTo(c *OIDCClaims) {
	if c == nil {
		return
	}
	if e.OrgID != nil {
		c.OrgID = *e.OrgID
	}
	if e.OrgDomain != nil {
		c.OrgDomain = *e.OrgDomain
	}
	if e.ResourceOwnerID != nil {
		c.ResourceOwnerID = *e.ResourceOwnerID
	}
	if e.ResourceOwnerName != nil {
		c.ResourceOwnerName = *e.ResourceOwnerName
	}
	if e.Roles != nil {
		c.Roles = e.Roles
	}
}

func resolveOIDCClaims(c *OIDCClaims, rawClaims map[string]interface{}) {
	if c == nil || rawClaims == nil {
		return
	}
	extraClaimsFromMap(rawClaims).applyTo(c)
}

func applyConfigurableClaims(tokenString string, claims *OIDCClaims) {
	ResolveConfiguredExtraClaims(tokenString).applyTo(claims)
}

func checkAudience(aud jwt.ClaimStrings) bool {
	for _, a := range aud {
		if oidcClientID != "" && a == oidcClientID {
			return true
		}
		for _, want := range oidcAllowedAudiences {
			if a == want {
				return true
			}
		}
	}
	return false
}

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
		add(key)
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

func parseCommaTrimmedList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func extractBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	tokenString = strings.TrimPrefix(tokenString, "bearer ")
	if tokenString == authHeader {
		return ""
	}
	return tokenString
}

func isAcceptedOIDCIssuer(issuer string) bool {
	for _, accepted := range oidcIssuers {
		if issuer == accepted {
			return true
		}
	}
	return false
}
