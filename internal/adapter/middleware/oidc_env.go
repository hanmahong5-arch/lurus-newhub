package middleware

// oidc_env.go — environment helper for the OIDC claim-key set. Pure move out
// of oidc_auth.go (source-size ratchet); behaviour unchanged.

import (
	"os"
	"strings"
)

// envOr returns the env value for key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
