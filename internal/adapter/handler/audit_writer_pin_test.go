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
// pinAuditWriter is the paired install/uninstall for THIS package.
//
// The enumeration is repo-wide, not package-wide — the first round scoped it to
// this directory, and the acceptor was right that a directory-scoped grep reads
// as a completeness claim it cannot make. The command is
// `grep -rn 'SetAuditWriter(' --include=*_test.go .` (excluding web/), and on
// 2026-09-19 it returns sites in five Go packages:
//
//   - internal/adapter/handler — eleven installs of a writer bound to a per-test
//     *gorm.DB at the cycle-12 baseline; ten of them did nothing to take it back
//     (secure_verification_test.go was the one that did, by installing an
//     in-memory recorder afterwards). L1 moved nine onto this helper: two in
//     channel_sensitive_write_test.go and one each in
//     relay_responses_registry_test.go, secure_verification_test.go,
//     v2_admin_authz_test.go, v2_admin_routing_test.go,
//     v2_admin_security_test.go, v2_pricing_write_test.go and
//     v2_provision_models_test.go. Two are deliberately left —
//     v2_admin_users_test.go and v2_session_revoke_test.go, whose files another
//     cycle-12 lane owns — and are in this cycle's hand-off list, not forgotten.
//     This package's other installs hold no database handle and already pair
//     with a cleanup (v2_models_write_test.go, internal_credit_pool_fund_test.go,
//     channel_tenant_id_immutable_test.go).
//   - internal/adapter/repo — user_session_test.go had the identical unpaired
//     shape (a writer holding the package's DB global, no cleanup at all) and
//     now uses its own pinSessionAuditWriter; admin_permission_grant_test.go
//     writes through the DB global and clears the writer in t.Cleanup.
//   - internal/adapter/middleware, internal/app, internal/app/governance — every
//     install there is an in-memory recorder paired with a cleanup.
//
// The other half of the repair is the seam: internal/adapter/handler,
// internal/adapter/handler/router, internal/adapter/repo,
// internal/adapter/middleware and internal/app all set
// governance.AsyncGo = func(f func()) { f() } in their TestMain, so the write
// has happened before the test that triggered it returns. The helper stops a
// writer outliving its database; the seam stops the write outliving the test.

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
