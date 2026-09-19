package handler

// audit_writer_pin_test.go — cycle-12 L1 test hygiene.
//
// governance.SetAuditWriter installs a process-global writer. A test that
// installs one bound to its own *gorm.DB and never takes it back leaves that
// global pointing at a handle its cleanup is about to close, so the next test
// in the binary that audits anything writes into a closed database — noise at
// best, and with `go test -shuffle=on` (added to the CI race job this cycle)
// a different test every run.
//
// pinAuditWriter is the paired install/uninstall.
//
// `grep -rn SetAuditWriter --include=*_test.go internal/adapter/handler/` is
// the enumeration. At the cycle-12 baseline eleven call sites installed a
// writer bound to a per-test *gorm.DB; ten of those did nothing to take it
// back (secure_verification_test.go was the one that did, by installing an
// in-memory recorder afterwards). L1 moved nine of the eleven onto this
// helper: two in channel_sensitive_write_test.go and one each in
// relay_responses_registry_test.go, secure_verification_test.go,
// v2_admin_authz_test.go, v2_admin_routing_test.go, v2_admin_security_test.go,
// v2_pricing_write_test.go and v2_provision_models_test.go.
//
// Two are deliberately left: v2_admin_users_test.go:286 and
// v2_session_revoke_test.go:189, whose files another cycle-12 lane owns. Both
// are in this cycle's hand-off list, not forgotten.
//
// The other sites the grep returns install writers that hold no database
// handle and already pair with a cleanup of their own
// (v2_models_write_test.go, internal_credit_pool_fund_test.go,
// channel_tenant_id_immutable_test.go).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// inertAuditWriter accepts and discards events. It holds no database handle,
// so it stays safe for the whole test binary no matter what any fixture
// closes.
type inertAuditWriter struct{}

func (inertAuditWriter) CreateAuditEvent(*entity.AuditEvent) error { return nil }

// pinAuditWriter installs a governance.AuditWriter bound to db for the
// duration of t, and on cleanup replaces it with an inert one.
//
// Cleanup deliberately installs the inert writer rather than restoring
// whatever writer was current on entry: governance exposes no reader for the
// installed writer, and "restore the previous one" would in any case be the
// wrong repair here — the previous writer is itself likely to be another
// test's db-bound writer whose handle is already closed. The invariant this
// helper maintains is "no writer outlives the database it writes to", not
// "the global is byte-for-byte what it was".
func pinAuditWriter(t *testing.T, db *gorm.DB) {
	t.Helper()
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})
	t.Cleanup(func() { governance.SetAuditWriter(inertAuditWriter{}) })
}
