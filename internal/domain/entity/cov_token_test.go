package entity

// cov_token_test.go — business-acceptance tests for the Token entity's
// IP allowlist parsing, model-limit allowlist parsing, and relay-scope
// authorization decision (HasScope). HasScope in particular is a security
// gate: an empty Scopes column must mean "unrestricted" (backward compat
// with every token minted before migration 015), while a non-empty column
// must be a strict allowlist.

import "testing"

func TestToken_Clean(t *testing.T) {
	tok := &Token{Key: "sk-secret-should-be-wiped"}
	tok.Clean()
	if tok.Key != "" {
		t.Fatalf("Clean() left Key = %q, want empty", tok.Key)
	}
}

func TestToken_GetIpLimits(t *testing.T) {
	blank := ""
	tests := []struct {
		name     string
		allowIps *string
		want     []string
	}{
		{"nil pointer yields empty non-nil slice", nil, []string{}},
		{"empty string yields empty slice", &blank, []string{}},
		{"single ip", strPtr("1.2.3.4"), []string{"1.2.3.4"}},
		{"newline separated multi ip", strPtr("1.2.3.4\n5.6.7.8"), []string{"1.2.3.4", "5.6.7.8"}},
		{"strips embedded spaces and stray commas", strPtr("1.2.3.4, \n 5.6.7.8,"), []string{"1.2.3.4", "5.6.7.8"}},
		{"blank lines between ips are dropped", strPtr("1.2.3.4\n\n\n5.6.7.8"), []string{"1.2.3.4", "5.6.7.8"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := &Token{AllowIps: tt.allowIps}
			got := tok.GetIpLimits()
			if len(got) != len(tt.want) {
				t.Fatalf("GetIpLimits() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("GetIpLimits()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestToken_ModelLimits(t *testing.T) {
	t.Run("IsModelLimitsEnabled mirrors the flag verbatim", func(t *testing.T) {
		if (&Token{ModelLimitsEnabled: false}).IsModelLimitsEnabled() {
			t.Fatal("expected false")
		}
		if !(&Token{ModelLimitsEnabled: true}).IsModelLimitsEnabled() {
			t.Fatal("expected true")
		}
	})

	t.Run("GetModelLimits on empty column returns empty slice", func(t *testing.T) {
		got := (&Token{ModelLimits: ""}).GetModelLimits()
		if len(got) != 0 {
			t.Fatalf("got %#v, want empty", got)
		}
	})

	t.Run("GetModelLimits splits comma list", func(t *testing.T) {
		got := (&Token{ModelLimits: "gpt-4,claude-3"}).GetModelLimits()
		if len(got) != 2 || got[0] != "gpt-4" || got[1] != "claude-3" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("GetModelLimitsMap builds a lookup set from the comma list", func(t *testing.T) {
		got := (&Token{ModelLimits: "gpt-4,claude-3"}).GetModelLimitsMap()
		if !got["gpt-4"] || !got["claude-3"] {
			t.Fatalf("got %#v, want both models present", got)
		}
		if got["gpt-3.5"] {
			t.Fatalf("got %#v, model not in the list must be absent", got)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want exactly 2 (no phantom empty-string key)", len(got))
		}
	})

	t.Run("GetModelLimitsMap on empty column is an empty (not nil-panicking) map", func(t *testing.T) {
		got := (&Token{ModelLimits: ""}).GetModelLimitsMap()
		if len(got) != 0 {
			t.Fatalf("got %#v, want empty map", got)
		}
	})
}

func TestToken_GetScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		want   []string
	}{
		{"empty column returns nil (unrestricted sentinel)", "", nil},
		{"single scope", "relay:chat", []string{"relay:chat"}},
		{"comma separated multi scope trims whitespace", " relay:chat , relay:embeddings ", []string{"relay:chat", "relay:embeddings"}},
		{"empty entries between commas are dropped", "relay:chat,,relay:embeddings", []string{"relay:chat", "relay:embeddings"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := &Token{Scopes: tt.scopes}
			got := tok.GetScopes()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("GetScopes() = %#v, want nil", got)
				}
				return
			}
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

func TestToken_HasScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		check  string
		want   bool
	}{
		{"empty scopes column is unrestricted (backward compat with pre-015 tokens)", "", "relay:anything", true},
		{"exact match in allowlist", "relay:chat,relay:embeddings", "relay:chat", true},
		{"not in allowlist is denied", "relay:chat,relay:embeddings", "relay:images", false},
		{"whitespace-only entries don't accidentally match empty scope check", "relay:chat", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := &Token{Scopes: tt.scopes}
			if got := tok.HasScope(tt.check); got != tt.want {
				t.Fatalf("HasScope(%q) = %v, want %v", tt.check, got, tt.want)
			}
		})
	}
}
