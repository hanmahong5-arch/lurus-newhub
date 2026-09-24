package repo

import (
	"strings"
	"testing"
)

// TestGenerateID_UniqueWithinOneSecond pins the defect found on 2026-09-24:
// the id was "tenant-" + a second-resolution timestamp and nothing else, so
// the second tenant created in the same second failed on tenants_pkey. A tight
// loop creates far more than two ids per second.
func TestGenerateID_UniqueWithinOneSecond(t *testing.T) {
	const n = 2000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := GenerateID()
		if _, dup := seen[id]; dup {
			t.Fatalf("GenerateID returned %q twice within %d calls", id, i+1)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerateID_FitsItsConsumers checks the two properties other code relies
// on: the primary-key column is size:36, and the business rate limiter builds
// keys as prefix+tenantID+":"+model, which is only unambiguous while tenant
// ids contain no ':'.
func TestGenerateID_FitsItsConsumers(t *testing.T) {
	id := GenerateID()
	if len(id) > 36 {
		t.Errorf("GenerateID() = %q is %d bytes; entity.Tenant.Id is size:36", id, len(id))
	}
	if strings.Contains(id, ":") {
		t.Errorf("GenerateID() = %q contains ':'; business rate-limit keys would become ambiguous", id)
	}
	if !strings.HasPrefix(id, "tenant-") {
		t.Errorf("GenerateID() = %q lost the tenant- prefix operators search logs by", id)
	}
}
