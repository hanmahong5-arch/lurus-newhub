package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Account lookup by IDP subject with distinguishable outcomes. Split out of
// identity_client.go (pure move, same package) to keep that file under its
// source-size ceiling.

// ErrIdentityNotConfigured means no identity service URL is configured.
var ErrIdentityNotConfigured = errors.New("identity service not configured")

// ErrIdentityUnavailable means the identity service could not give a usable
// answer (network error, non-200/404 status, undecodable body).
var ErrIdentityUnavailable = errors.New("identity service unavailable")

// LookupAccountByIDPSubject looks an account up by OIDC subject and, unlike
// GetAccountByZitadelSub, tells the caller WHY there is no answer:
//   - (nil, ErrIdentityNotConfigured): URL empty
//   - (nil, nil): the platform answered 404 (no such account)
//   - (nil, error wrapping ErrIdentityUnavailable): transport/status/decode failure
func LookupAccountByIDPSubject(ctx context.Context, sub string) (*IdentityMapping, error) {
	if IdentityServiceURL == "" {
		return nil, ErrIdentityNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		IdentityServiceURL+"/internal/v1/accounts/by-idp-sub/"+url.PathEscape(sub),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("identity lookup: build request: %w", ErrIdentityUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetAccountByZitadelSub: %v", err))
		return nil, fmt.Errorf("identity lookup: %v: %w", err, ErrIdentityUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity GetAccountByZitadelSub: status %d", resp.StatusCode))
		return nil, fmt.Errorf("identity lookup: status %d: %w", resp.StatusCode, ErrIdentityUnavailable)
	}
	var a IdentityMapping
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil, fmt.Errorf("identity lookup: decode: %v: %w", err, ErrIdentityUnavailable)
	}
	return &a, nil
}
