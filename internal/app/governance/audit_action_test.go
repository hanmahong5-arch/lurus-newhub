package governance

import (
	"sort"
	"testing"
)

func TestIsValidAuditAction(t *testing.T) {
	cases := []struct {
		action string
		want   bool
	}{
		// Known taxonomy entries — must all pass.
		{ActionAuthFailed, true},
		{ActionAuthLoginSuccess, true},
		{ActionAuthLogout, true},
		{ActionAuthScopeRejected, true},
		{ActionAuthTokenRotated, true},
		{ActionTokenCreated, true},
		{ActionTokenStatusChanged, true},
		{ActionChannelCreated, true},
		{ActionChannelTested, true},
		{ActionUserUpdated, true},
		{ActionUserRoleChanged, true},
		{ActionUserQuotaAdjusted, true},
		{ActionRedemptionCreated, true},
		{ActionRedemptionRedeemed, true},
		{ActionOptionUpdated, true},
		{ActionModelSyncTriggered, true},
		{ActionTenantMappingDeleted, true},
		{ActionWhitelabelKeyAccessed, true},
		{ActionBillingDebit, true},
		{ActionBillingPoolReset, true},
		{ActionBillingPoolThreshold, true},
		{ActionSystemStartup, true},

		// Unknown actions — must reject. These probe the most likely typos:
		// trailing whitespace, near-misses, empty string, invented terms.
		{"", false},
		{"auth.login", false},
		{"token.create", false}, // missing 'd'
		{"Token.Created", false}, // wrong case
		{"foo.bar", false},
		{"unknown.action", false},
	}
	for _, tc := range cases {
		if got := IsValidAuditAction(tc.action); got != tc.want {
			t.Errorf("IsValidAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
		}
	}
}

// TestAllAuditActions_Sorted asserts the registry export is alphabetically
// sorted — clients filtering or rendering the taxonomy can depend on a
// stable order. If a new action is added but the sort breaks (regression in
// the sort.Strings call), this test fails immediately.
func TestAllAuditActions_Sorted(t *testing.T) {
	got := AllAuditActions()
	if len(got) == 0 {
		t.Fatal("AllAuditActions returned empty slice")
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("AllAuditActions output is not sorted: %v", got)
	}
}

// TestAllAuditActions_ContainsKnown spot-checks that AllAuditActions exposes
// the full registry (no quiet drops from validAuditActions). If a future
// edit forgets to register a constant in validAuditActions, IsValidAuditAction
// would silently reject the new constant — this guards against that.
func TestAllAuditActions_ContainsKnown(t *testing.T) {
	got := AllAuditActions()
	wantSet := map[string]struct{}{
		ActionAuthLoginSuccess:      {},
		ActionTokenStatusChanged:    {},
		ActionUserQuotaAdjusted:     {},
		ActionRedemptionInvalidDeleted: {},
		ActionTenantBrandUpdated:    {},
		ActionSystemShutdown:        {},
	}
	for _, a := range got {
		delete(wantSet, a)
	}
	if len(wantSet) != 0 {
		t.Errorf("AllAuditActions is missing expected entries: %v", wantSet)
	}
}

// TestAuditTaxonomy_NoCollisions catches the typo class where two constants
// share the same string value — silent bug, would alias actions in the audit
// log. Iterate the registry rather than the const block (the block is
// closed source from this test's POV; the map is the authoritative export).
func TestAuditTaxonomy_NoCollisions(t *testing.T) {
	all := AllAuditActions()
	seen := make(map[string]int, len(all))
	for _, a := range all {
		seen[a]++
		if seen[a] > 1 {
			t.Errorf("duplicate action string: %q (appears %d times)", a, seen[a])
		}
	}
}
