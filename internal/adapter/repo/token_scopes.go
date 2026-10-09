package repo

import "strings"

// token_scopes.go — scope allowlist helpers, moved out of token.go unchanged
// (source-size ratchet).

// GetScopes returns the token's scope allowlist as a slice with whitespace
// trimmed and empty entries dropped. nil/empty result means no restriction.
func (token *Token) GetScopes() []string {
	if token.Scopes == "" {
		return nil
	}
	parts := strings.Split(token.Scopes, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasScope reports whether the token is authorized for the given scope.
// An empty Scopes field is treated as "no restriction" (backward compat
// with every token issued before migration 015) — HasScope returns true.
func (token *Token) HasScope(scope string) bool {
	scopes := token.GetScopes()
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}
