package repo

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PG-gated tests for withPGAdvisoryLock — the serializer that lets boot
// migrations run on EVERY replica (not just the boot-lease winner) without
// racing AutoMigrate. Skips without TEST_POSTGRES_DSN (mirrors the migration
// package's PG suite).

func openPG(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// TestWithPGAdvisoryLock_SerializesCriticalSections: two contenders on the
// same key must never occupy the critical section at once.
func TestWithPGAdvisoryLock_SerializesCriticalSections(t *testing.T) {
	db := openPG(t)
	const key = int64(0x4C48627454657374) // "LHbtTest" — test-only key

	var inside, overlaps int32
	section := func() error {
		if atomic.AddInt32(&inside, 1) > 1 {
			atomic.AddInt32(&overlaps, 1)
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inside, -1)
		return nil
	}

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errCh <- withPGAdvisoryLock(context.Background(), db, key, section) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("withPGAdvisoryLock: %v", err)
		}
	}
	if got := atomic.LoadInt32(&overlaps); got != 0 {
		t.Fatalf("critical sections overlapped %d times — advisory lock did not serialize", got)
	}
}

// TestWithPGAdvisoryLock_ReleasesOnSameSession: after the helper returns, the
// key must be immediately acquirable from a FRESH session. This is the
// regression fence for the pool-unlock trap (unlock issued on a different
// pooled connection than the one holding the lock leaves the key stranded).
func TestWithPGAdvisoryLock_ReleasesOnSameSession(t *testing.T) {
	db := openPG(t)
	const key = int64(0x4C48627452656C73) // "LHbtRels" — test-only key

	if err := withPGAdvisoryLock(context.Background(), db, key, func() error { return nil }); err != nil {
		t.Fatalf("withPGAdvisoryLock: %v", err)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var free bool
	if err := conn.QueryRowContext(context.Background(),
		`SELECT pg_try_advisory_lock($1)`, key).Scan(&free); err != nil {
		t.Fatalf("try lock: %v", err)
	}
	if !free {
		t.Fatal("advisory lock still held after withPGAdvisoryLock returned — unlock missed the owning session")
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatalf("cleanup unlock: %v", err)
	}
}

// TestWithPGAdvisoryLock_LockWaitOutlivesStatementTimeout reproduces the
// 2026-07-15 STAGE crash-loop: every pooled connection carries a
// DSN-injected statement_timeout (P1-1, withStatementTimeout), and a replica
// waiting on the boot advisory lock longer than that cap was killed by
// Postgres and died FATAL. The lock wait must survive a cap far shorter than
// the holder's critical section.
func TestWithPGAdvisoryLock_LockWaitOutlivesStatementTimeout(t *testing.T) {
	base := os.Getenv("TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL integration test")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Skipf("TEST_POSTGRES_DSN is not URL-form; cannot inject statement_timeout (%v)", err)
	}
	q := u.Query()
	q.Set("statement_timeout", "150")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("open capped pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping capped pool: %v", err)
	}

	// The cap must actually bite on this pool — otherwise the assertions
	// below pass without exercising the exemption at all.
	if _, err := db.Exec(`SELECT pg_sleep(0.5)`); err == nil {
		t.Fatal("statement_timeout=150ms did not cancel pg_sleep(0.5) — harness is not reproducing the boot DSN shape")
	}

	const key = int64(0x4C486274546D6F73) // "LHbtTmos" — test-only key
	hold := make(chan struct{})
	held := make(chan struct{})
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- withPGAdvisoryLock(context.Background(), db, key, func() error {
			close(held)
			<-hold
			return nil
		})
	}()
	<-held

	waiterErr := make(chan error, 1)
	go func() {
		waiterErr <- withPGAdvisoryLock(context.Background(), db, key, func() error { return nil })
	}()
	// 600ms = 4x the cap; the pre-fix code was already dead at 150ms with
	// "canceling statement due to statement timeout".
	select {
	case err := <-waiterErr:
		t.Fatalf("waiter returned while lock still held: %v", err)
	case <-time.After(600 * time.Millisecond):
	}
	close(hold)
	if err := <-holderErr; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if err := <-waiterErr; err != nil {
		t.Fatalf("lock wait did not survive statement_timeout: %v", err)
	}
}

// TestWithPGAdvisoryLock_PropagatesFnError: fn's error surfaces and the lock
// is still released.
func TestWithPGAdvisoryLock_PropagatesFnError(t *testing.T) {
	db := openPG(t)
	const key = int64(0x4C48627445727273) // "LHbtErrs" — test-only key

	wantErr := os.ErrDeadlineExceeded
	if err := withPGAdvisoryLock(context.Background(), db, key, func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	var free bool
	if err := db.QueryRow(`SELECT pg_try_advisory_lock($1)`, key).Scan(&free); err != nil {
		t.Fatalf("try lock: %v", err)
	}
	if !free {
		t.Fatal("lock leaked after fn error")
	}
	if _, err := db.Exec(`SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatalf("cleanup unlock: %v", err)
	}
}
