package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/golang-jwt/jwt/v5"
)

// JWK represents a JSON Web Key.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKSet represents a set of JSON Web Keys.
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// JWKSManager manages JSON Web Keys from the OIDC provider's JWKS endpoint.
type JWKSManager struct {
	jwksURI            string
	publicKeys         map[string]*rsa.PublicKey
	mu                 sync.RWMutex
	lastUpdate         time.Time
	updateFailed       bool
	refreshTicker      *time.Ticker
	refreshing         bool
	refreshMu          sync.Mutex
	minRefreshInterval time.Duration
	refreshInterval    time.Duration
}

var oidcHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

// NewJWKSManager creates a new JWKS Manager instance.
func NewJWKSManager(jwksURI string) *JWKSManager {
	return NewJWKSManagerWithContext(context.Background(), jwksURI)
}

// NewJWKSManagerWithContext creates a new JWKS Manager instance with context support.
func NewJWKSManagerWithContext(ctx context.Context, jwksURI string) *JWKSManager {
	m := &JWKSManager{
		jwksURI:            jwksURI,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 30 * time.Second,
		refreshInterval:    jwksRefreshInterval,
	}

	if err := m.refreshKeys(); err != nil {
		common.SysError(fmt.Sprintf("Failed to fetch JWKS keys: %v", err))
		m.updateFailed = true
	}

	common.SafeGoWithContext(ctx, func(c context.Context) {
		common.SuperviseLoop(c, "oidc-jwks-refresh", 5*time.Second, m.autoRefreshWithContext)
	})

	return m
}

func (m *JWKSManager) refreshKeys() error {
	common.SysLog(fmt.Sprintf("Fetching JWKS from: %s", m.jwksURI))

	resp, err := oidcHTTPClient.Get(m.jwksURI)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var jwkSet JWKSet
	if err := json.NewDecoder(resp.Body).Decode(&jwkSet); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey)
	for _, jwk := range jwkSet.Keys {
		if jwk.Kty != "RSA" {
			continue
		}
		publicKey, err := jwkToRSAPublicKey(jwk)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to convert JWK to RSA public key (kid=%s): %v", jwk.Kid, err))
			continue
		}
		newKeys[jwk.Kid] = publicKey
	}

	if len(newKeys) == 0 {
		return errors.New("no valid RSA keys found in JWKS")
	}

	m.mu.Lock()
	m.publicKeys = newKeys
	m.lastUpdate = time.Now()
	m.updateFailed = false
	m.mu.Unlock()

	common.SysLog(fmt.Sprintf("Successfully refreshed %d JWKS keys", len(newKeys)))
	return nil
}

func (m *JWKSManager) autoRefreshWithContext(ctx context.Context) {
	interval := m.refreshInterval
	if interval <= 0 {
		interval = jwksRefreshInterval
	}
	m.refreshTicker = time.NewTicker(interval)
	defer m.refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("JWKS auto-refresh stopped")
			return
		case <-m.refreshTicker.C:
			if err := m.refreshKeys(); err != nil {
				common.SysError(fmt.Sprintf("Auto-refresh JWKS failed: %v", err))
			}
		}
	}
}

func (m *JWKSManager) getKey(kid string) (*rsa.PublicKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if key, ok := m.publicKeys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("public key not found for kid: %s", kid)
}

func (m *JWKSManager) tryRefreshKeys() (refreshed bool, err error) {
	m.refreshMu.Lock()

	if m.refreshing {
		m.refreshMu.Unlock()
		time.Sleep(100 * time.Millisecond)
		return true, nil
	}

	m.mu.RLock()
	lastUpdate := m.lastUpdate
	m.mu.RUnlock()

	if time.Since(lastUpdate) < m.minRefreshInterval {
		m.refreshMu.Unlock()
		return false, nil
	}

	m.refreshing = true
	m.refreshMu.Unlock()

	defer func() {
		m.refreshMu.Lock()
		m.refreshing = false
		m.refreshMu.Unlock()
	}()

	err = m.refreshKeys()
	return true, err
}

func (m *JWKSManager) getKeyWithRefresh(kid string) (*rsa.PublicKey, error) {
	key, err := m.getKey(kid)
	if err == nil {
		return key, nil
	}

	refreshed, refreshErr := m.tryRefreshKeys()
	if refreshErr != nil {
		common.SysError(fmt.Sprintf("Failed to refresh JWKS: %v", refreshErr))
		return nil, err
	}
	if !refreshed {
		return nil, fmt.Errorf("public key not found for kid: %s (refresh rate limited)", kid)
	}
	return m.getKey(kid)
}

// VerifyIDTokenWithJWKS verifies an ID token JWT signature using the JWKS keys.
func VerifyIDTokenWithJWKS(idToken string, claims jwt.Claims) (*jwt.Token, error) {
	if jwksManager == nil {
		return nil, errors.New("JWKS manager not initialized; is OIDC enabled?")
	}

	token, err := jwt.ParseWithClaims(idToken, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errors.New("missing kid in token header")
		}
		return jwksManager.getKeyWithRefresh(kid)
	})
	if err != nil {
		return nil, fmt.Errorf("ID token signature verification failed: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("ID token is not valid")
	}
	return token, nil
}

func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{
		N: n,
		E: int(e.Int64()),
	}, nil
}
