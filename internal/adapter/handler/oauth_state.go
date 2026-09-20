package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// oauth_state.go — the signed OIDC `state` parameter: mint, parse, and the
// HMAC both sides agree on. Split out of oauth.go by the cycle-13 wiring
// pass as a pure move (the three functions are byte-identical to the ones
// that stood in oauth.go); internal/pkg/gates' source-size ratchet holds
// oauth.go at its measured line count, so this cycle's additions to the file
// are paid for by a move rather than by raising the ceiling. The three
// belong together: state is the CSRF binding for the whole redirect, and
// generate/parse only mean anything against the same computeStateHMAC.

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
