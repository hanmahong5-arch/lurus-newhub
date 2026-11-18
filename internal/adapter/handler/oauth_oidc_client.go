package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

var placeholderOrgLogged sync.Map

func buildOIDCAuthURL(orgID string, state string, nonce string, pkceData *PKCEData, prompt string) string {
	issuer := os.Getenv("OIDC_ISSUER")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	redirectURI := os.Getenv("OIDC_REDIRECT_URI")
	scopes := os.Getenv("OIDC_OAUTH_SCOPES")
	if scopes == "" {
		scopes = "openid email profile offline_access"
	}

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", state)
	params.Set("nonce", nonce)

	if orgIDIsBound(orgID) {
		params.Set("organization", orgID)
	} else if orgID != "" {
		if _, seen := placeholderOrgLogged.LoadOrStore(orgID, struct{}{}); !seen {
			common.SysLog(fmt.Sprintf("oidc: tenant org id %q is a placeholder — omitting the organization hint from the authorize URL until the IdP organization is created and the row updated", orgID))
		}
	}

	if prompt != "" {
		params.Set("prompt", prompt)
	}

	if pkceData != nil {
		params.Set("code_challenge", pkceData.CodeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	return issuer + oidcAuthorizePath() + "?" + params.Encode()
}

func exchangeCodeForToken(code string, codeVerifier string) (*OAuthTokenResponse, error) {
	issuer := os.Getenv("OIDC_ISSUER")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	clientSecret := os.Getenv("OIDC_CLIENT_SECRET")
	redirectURI := os.Getenv("OIDC_REDIRECT_URI")

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("client_id", clientID)
	data.Set("redirect_uri", redirectURI)

	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}
	if codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.PostForm(issuer+oidcTokenPath(), data)
	if err != nil {
		return nil, fmt.Errorf("failed to post token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("token endpoint error: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokenResp, nil
}

func isPKCEEnabled() bool {
	return os.Getenv("OIDC_ENABLE_PKCE") == "true"
}

func generatePKCE() (*PKCEData, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate code_verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	h := sha256.New()
	h.Write([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return &PKCEData{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
	}, nil
}
