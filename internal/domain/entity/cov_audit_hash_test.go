package entity

// cov_audit_hash_test.go — business-acceptance tests for ComputeAuditRowHash,
// the pure digest function underpinning the tamper-evidence audit chain.
// The critical property under test is field-boundary safety: because the
// preimage length-prefixes every field, two rows whose fields differ only
// in where a boundary falls (e.g. ActorType="ab"+Action="c" vs
// ActorType="a"+Action="bc") must never collide. A naive concatenation
// (no length prefix) WOULD collide here — this test would catch a
// regression back to that scheme.

import (
	"encoding/hex"
	"testing"
)

func baseAuditEvent() *AuditEvent {
	return &AuditEvent{
		TenantID:       "tenant-1",
		Timestamp:      1700000000,
		ActorType:      "user",
		ActorID:        42,
		Action:         "token.created",
		Resource:       "token",
		ResourceID:     7,
		RequestID:      "req-abc",
		RetentionUntil: 1800000000,
		PrevHash:       "prevhash0000",
	}
}

func TestComputeAuditRowHash_Deterministic(t *testing.T) {
	e := baseAuditEvent()
	h1 := ComputeAuditRowHash(e)
	h2 := ComputeAuditRowHash(e)
	if h1 != h2 {
		t.Fatalf("ComputeAuditRowHash is not deterministic: %q != %q", h1, h2)
	}
}

func TestComputeAuditRowHash_IsWellFormedSHA256Hex(t *testing.T) {
	h := ComputeAuditRowHash(baseAuditEvent())
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars (SHA-256)", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("hash is not valid hex: %v", err)
	}
}

func TestComputeAuditRowHash_FieldBoundaryAmbiguityIsNotACollision(t *testing.T) {
	// "ab" + "c" vs "a" + "bc": identical if fields were naively concatenated
	// without length-prefixing. The chain design explicitly length-prefixes
	// to prevent exactly this forgery vector (see ComputeAuditRowHash doc).
	e1 := baseAuditEvent()
	e1.ActorType, e1.Action = "ab", "c"

	e2 := baseAuditEvent()
	e2.ActorType, e2.Action = "a", "bc"

	h1, h2 := ComputeAuditRowHash(e1), ComputeAuditRowHash(e2)
	if h1 == h2 {
		t.Fatalf("field-boundary collision: ActorType/Action split differently produced the same hash %q — length-prefixing regression", h1)
	}
}

func TestComputeAuditRowHash_EveryCoveredFieldChangesTheHash(t *testing.T) {
	base := ComputeAuditRowHash(baseAuditEvent())

	mutators := map[string]func(*AuditEvent){
		"tenant_id":       func(e *AuditEvent) { e.TenantID = "tenant-2" },
		"timestamp":       func(e *AuditEvent) { e.Timestamp++ },
		"actor_type":      func(e *AuditEvent) { e.ActorType = "admin" },
		"actor_id":        func(e *AuditEvent) { e.ActorID++ },
		"action":          func(e *AuditEvent) { e.Action = "token.deleted" },
		"resource":        func(e *AuditEvent) { e.Resource = "channel" },
		"resource_id":     func(e *AuditEvent) { e.ResourceID++ },
		"request_id":      func(e *AuditEvent) { e.RequestID = "req-xyz" },
		"retention_until": func(e *AuditEvent) { e.RetentionUntil++ },
		"prev_hash":       func(e *AuditEvent) { e.PrevHash = "differentprevhash" },
	}
	for field, mutate := range mutators {
		t.Run(field, func(t *testing.T) {
			e := baseAuditEvent()
			mutate(e)
			got := ComputeAuditRowHash(e)
			if got == base {
				t.Fatalf("mutating %s did not change the hash: still %q", field, got)
			}
		})
	}
}

func TestComputeAuditRowHash_IpAndDetailsAreExcluded(t *testing.T) {
	// ip/details are intentionally excluded (PIPL erasure rewrites them in
	// place; hashing them would break the chain on lawful redaction). Changing
	// only these two fields must NOT change the row hash.
	e1 := baseAuditEvent()
	e1.IP = "1.2.3.4"
	e1.Details = `{"note":"original"}`

	e2 := baseAuditEvent()
	e2.IP = "5.6.7.8"
	e2.Details = `{"note":"scrubbed after PIPL erasure"}`

	if ComputeAuditRowHash(e1) != ComputeAuditRowHash(e2) {
		t.Fatal("ip/details changed the hash — these fields must be excluded from the chain preimage per design")
	}
}

func TestComputeAuditRowHash_IdIsExcluded(t *testing.T) {
	// id is DB-assigned after the hash is computed; it must not affect it.
	e1 := baseAuditEvent()
	e1.ID = 1
	e2 := baseAuditEvent()
	e2.ID = 999999

	if ComputeAuditRowHash(e1) != ComputeAuditRowHash(e2) {
		t.Fatal("id changed the hash — id is DB-assigned post-hash and must be excluded")
	}
}
