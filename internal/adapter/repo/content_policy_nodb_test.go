package repo

import (
	"errors"
	"testing"
)

// The relay entry asks for the ruleset on every request. With no database
// (boot not finished, hermetic tests that never open one) the answer must be
// an error the caller fails open on - never a nil-pointer panic in the relay.
func TestContentRulesetForTenant_NoDatabaseIsAnErrorNotAPanic(t *testing.T) {
	prev := DB
	DB = nil
	InvalidateContentRulesCache()
	t.Cleanup(func() {
		DB = prev
		InvalidateContentRulesCache()
	})
	rs, err := ContentRulesetForTenant("tenant-x")
	if rs != nil || !errors.Is(err, errContentRulesNoDB) {
		t.Fatalf("got rs=%v err=%v, want nil ruleset and errContentRulesNoDB", rs, err)
	}
}
