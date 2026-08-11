package entity

// cov_internal_api_key_test.go — business-acceptance tests for InternalApiKey
// scope parsing and the HasScope authorization check, including the ScopeAll
// wildcard that must grant every scope (a security-relevant "master key"
// behavior worth pinning down explicitly).

import "testing"

func TestInternalApiKey_GetScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		want   []string
	}{
		{"empty column returns empty slice", "", []string{}},
		{"malformed json returns empty slice, not panic", "{not-an-array", []string{}},
		{"valid json array round trips", `["user:read","token:write"]`, []string{"user:read", "token:write"}},
		{"json object (wrong shape) fails to unmarshal into []string, returns empty", `{"a":1}`, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &InternalApiKey{Scopes: tt.scopes}
			got := k.GetScopes()
			if len(got) != len(tt.want) {
				t.Fatalf("GetScopes() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("GetScopes()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestInternalApiKey_HasScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		check  string
		want   bool
	}{
		{"empty scopes column grants nothing (deny by default, unlike Token)", "", ScopeUserRead, false},
		{"exact scope match is granted", `["user:read","token:write"]`, ScopeUserRead, true},
		{"scope not present is denied", `["user:read"]`, ScopeUserWrite, false},
		{"wildcard ScopeAll grants any requested scope", `["*"]`, ScopeAdmin, true},
		{"wildcard ScopeAll grants an entirely unrelated scope too", `["*"]`, ScopeCurrencyExchange, true},
		{"admin scope does not implicitly grant a narrower data scope", `["admin"]`, ScopeUserRead, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &InternalApiKey{Scopes: tt.scopes}
			if got := k.HasScope(tt.check); got != tt.want {
				t.Fatalf("HasScope(%q) = %v, want %v", tt.check, got, tt.want)
			}
		})
	}
}

func TestInternalApiKey_TableName(t *testing.T) {
	if got := (InternalApiKey{}).TableName(); got != "internal_api_keys" {
		t.Fatalf("TableName() = %q, want internal_api_keys", got)
	}
}
