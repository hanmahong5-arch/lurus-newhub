package handler

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
)

func validateIDToken(idToken string, expectedNonce string) (*IDTokenClaims, error) {
	token, err := middleware.VerifyIDTokenWithJWKS(idToken, &IDTokenClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to verify ID token: %w", err)
	}

	claims, ok := token.Claims.(*IDTokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid ID token claims type")
	}

	extra := middleware.ResolveConfiguredExtraClaims(idToken)
	if extra.OrgID != nil {
		claims.OrgID = *extra.OrgID
	}
	if extra.OrgDomain != nil {
		claims.OrgDomain = *extra.OrgDomain
	}
	if extra.Roles != nil {
		claims.Roles = extra.Roles
	}

	if err := validateIDTokenClaims(claims, expectedNonce); err != nil {
		return nil, err
	}
	return claims, nil
}

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

func isValidTenantSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 63 {
		return false
	}
	if slug[0] == '-' || slug[0] == '_' {
		return false
	}
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func isValidRedirectURL(redirectURL string) bool {
	if redirectURL == "" {
		return true
	}

	if strings.HasPrefix(redirectURL, "/") && !strings.HasPrefix(redirectURL, "//") {
		return true
	}

	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}

	if parsedURL.Scheme == "javascript" || parsedURL.Scheme == "data" {
		return false
	}

	if parsedURL.Host != "" {
		allowedDomains := os.Getenv("OIDC_ALLOWED_REDIRECT_DOMAINS")
		if allowedDomains == "" {
			return false
		}
		for _, domain := range strings.Split(allowedDomains, ",") {
			domain = strings.TrimSpace(domain)
			if domain == parsedURL.Host || strings.HasSuffix(parsedURL.Host, "."+domain) {
				return true
			}
		}
		return false
	}

	return true
}
