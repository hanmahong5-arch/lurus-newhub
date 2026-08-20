package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupLeaderElectionDB wires an in-memory SQLite DB with the leader_elections
// table migrated. now is always injected explicitly into the calls under test,
// so these assertions are deterministic with no wall-clock dependency.
func setupLeaderElectionDB(t *testing.T) func() {
	t.Helper()
	cleanup := setupSQLiteDB(t)
	if err := DB.AutoMigrate(&entity.LeaderElection{}); err != nil {
		cleanup()
		t.Fatalf("migrate leader_elections: %v", err)
	}
	return cleanup
}

func currentLease(t *testing.T) entity.LeaderElection {
	t.Helper()
	var le entity.LeaderElection
	if err := DB.First(&le, "name = ?", entity.LeaderElectionName).Error; err != nil {
		t.Fatalf("read lease row: %v", err)
	}
	return le
}

func TestTryAcquireOrRenew_FirstAcquire(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	le := currentLease(t)
	if le.HolderId != "node-a" || le.AcquiredAt != 1000 || le.RenewedAt != 1000 || le.ExpiresAt != 1030 {
		t.Errorf("unexpected lease after first acquire: %+v", le)
	}
}

func TestTryAcquireOrRenew_RenewByHolder(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1020)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !ok {
		t.Fatal("expected holder to renew its own lease")
	}
	le := currentLease(t)
	if le.HolderId != "node-a" || le.RenewedAt != 1020 || le.ExpiresAt != 1050 {
		t.Errorf("unexpected lease after renew: %+v", le)
	}
	if le.AcquiredAt != 1000 {
		t.Errorf("AcquiredAt should stay at first creation (1000), got %d", le.AcquiredAt)
	}
}

func TestTryAcquireOrRenew_RejectWhileValid(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// node-b contends while node-a's lease (expires 1030) is still valid.
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 1020)
	if err != nil {
		t.Fatalf("contend: %v", err)
	}
	if ok {
		t.Fatal("node-b must not acquire while node-a holds a valid lease")
	}
	if le := currentLease(t); le.HolderId != "node-a" {
		t.Errorf("holder should remain node-a, got %q", le.HolderId)
	}
}

func TestTryAcquireOrRenew_TakeoverAfterExpiry(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// node-b contends at 1031 — node-a's lease (expires 1030) has lapsed.
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 1031)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if !ok {
		t.Fatal("node-b should take over an expired lease")
	}
	le := currentLease(t)
	if le.HolderId != "node-b" || le.RenewedAt != 1031 || le.ExpiresAt != 1061 {
		t.Errorf("unexpected lease after takeover: %+v", le)
	}
}

func TestTryAcquireOrRenew_TwoCandidatesOnlyOneWins(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	// Sequential contention at the same instant: the DB row's primary key plus
	// the conditional update guarantee exactly one holder at any instant.
	aWon, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 5000)
	if err != nil {
		t.Fatalf("node-a: %v", err)
	}
	bWon, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 5000)
	if err != nil {
		t.Fatalf("node-b: %v", err)
	}
	if !aWon || bWon {
		t.Fatalf("expected exactly node-a to win at the same instant: aWon=%v bWon=%v", aWon, bWon)
	}
}

// sqlErrorRecorder captures every statement error GORM would surface to its
// logger. The default logger prints those at ERROR level, so anything recorded
// here is a line that shows up in production logs.
type sqlErrorRecorder struct {
	logger.Interface
	errs []error
}

func (r *sqlErrorRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		r.errs = append(r.errs, err)
	}
}

// TestTryAcquireOrRenew_FollowerPathEmitsNoSQLError pins the log-noise
// contract: a follower losing the election is the steady state — every replica
// but one, on every renewal tick — so that path must not provoke a database
// error. Raised as a duplicate-key ERROR it was, measured on R6 on 2026-08-20,
// a quarter of everything each follower pod logged.
//
// The five tests above all pass against the pre-fix implementation, because
// they only ever inspect the return values, which were already correct. What
// went wrong happened on the way to the correct answer and was visible only in
// the log, so this asserts on the logger rather than on the verdict.
func TestTryAcquireOrRenew_FollowerPathEmitsNoSQLError(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	rec := &sqlErrorRecorder{Interface: DB.Logger}
	prev := DB.Logger
	DB.Logger = rec
	defer func() { DB.Logger = prev }()

	// node-b contends while node-a's lease is valid — the "someone else leads"
	// path, which reaches the INSERT because the conditional UPDATE matches no row.
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 1020)
	if err != nil {
		t.Fatalf("contend: %v", err)
	}
	if ok {
		t.Fatal("node-b must not acquire while node-a holds a valid lease")
	}
	if len(rec.errs) != 0 {
		t.Errorf("follower path must not emit SQL errors, got %d: %v", len(rec.errs), rec.errs)
	}
}

// TestTryAcquireOrRenew_PGFollowerRowsAffectedZero pins the one thing the
// SQLite tier cannot prove. The follower verdict now rides entirely on
// PostgreSQL reporting RowsAffected == 0 for an INSERT ... ON CONFLICT DO
// NOTHING that hit the conflict. If that ever became 1, every follower would
// conclude it won the election — two replicas running the master-only
// background tasks at once, with nothing in the logs to say so.
func TestTryAcquireOrRenew_PGFollowerRowsAffectedZero(t *testing.T) {
	defer SetupTestDB(t)()
	if err := DB.AutoMigrate(&entity.LeaderElection{}); err != nil {
		t.Fatalf("migrate leader_elections: %v", err)
	}

	if ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 30, 1000); err != nil || !ok {
		t.Fatalf("first acquire on PG: ok=%v err=%v", ok, err)
	}
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 1020)
	if err != nil {
		t.Fatalf("contend on PG: %v", err)
	}
	if ok {
		t.Fatal("PG follower must not win — ON CONFLICT DO NOTHING has to report RowsAffected 0")
	}
	if le := currentLease(t); le.HolderId != "node-a" {
		t.Errorf("holder should remain node-a, got %q", le.HolderId)
	}
}

func TestTryAcquireOrRenew_InvalidArgs(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	cases := []struct {
		name   string
		lease  string
		holder string
		ttl    int64
	}{
		{"empty name", "", "node-a", 30},
		{"empty holder", entity.LeaderElectionName, "", 30},
		{"zero ttl", entity.LeaderElectionName, "node-a", 0},
		{"negative ttl", entity.LeaderElectionName, "node-a", -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := TryAcquireOrRenew(tc.lease, tc.holder, tc.ttl, 1000); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestReleaseLease_EnablesImmediateTakeover(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 300, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := ReleaseLease(entity.LeaderElectionName, "node-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Even though node-a's TTL had not elapsed (expires 1300), release expired
	// it, so node-b takes over at the same instant.
	ok, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-b", 30, 1010)
	if err != nil {
		t.Fatalf("takeover after release: %v", err)
	}
	if !ok {
		t.Fatal("node-b should take over immediately after node-a releases")
	}
	if le := currentLease(t); le.HolderId != "node-b" {
		t.Errorf("holder should be node-b after takeover, got %q", le.HolderId)
	}
}

func TestReleaseLease_ByNonHolderIsNoOp(t *testing.T) {
	defer setupLeaderElectionDB(t)()

	if _, err := TryAcquireOrRenew(entity.LeaderElectionName, "node-a", 300, 1000); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// node-b tries to release a lease it does not hold.
	if err := ReleaseLease(entity.LeaderElectionName, "node-b"); err != nil {
		t.Fatalf("release by non-holder should not error: %v", err)
	}
	le := currentLease(t)
	if le.HolderId != "node-a" || le.ExpiresAt != 1300 {
		t.Errorf("non-holder release must not alter the lease, got %+v", le)
	}
}
