package handler

// oidc_login_e2e_export_test.go — test-only bridge so the external-package
// login e2e (oidc_login_e2e_test.go, package handler_test) can import the
// real router (router imports handler, so an in-package test cannot) and still
// serve the SAME RSA key under the SAME kid as every other OIDC harness in
// this binary. middleware's jwksManager is a once-per-binary singleton, so
// whichever test calls InitOIDCAuth first seeds the key cache all later
// tests verify against (the shuffle failure fixed in 325f80d9); a private key
// of our own would reintroduce it.

import (
	"crypto/rsa"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
)

// OIDCSharedKeyForE2E returns the package-wide shared IdP signing key.
func OIDCSharedKeyForE2E(t *testing.T) *rsa.PrivateKey { return oidcCallbackSharedKey(t) }

// OIDCSharedKidForE2E is the kid every harness must sign and publish under.
const OIDCSharedKidForE2E = oidcCallbackTestKid

// OIDCSharedJWKForE2E is the public half of the shared key in JWKS wire shape.
func OIDCSharedJWKForE2E(t *testing.T) middleware.JWK {
	return rsaPublicKeyToJWKForCallbackTest(&oidcCallbackSharedKey(t).PublicKey)
}
