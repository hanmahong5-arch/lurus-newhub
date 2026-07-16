package repo

// quota_if_enough_concurrency_pg_test.go — money-path e2e: the atomic pre-consume
// debit helpers must NOT overdraw under concurrent writers.
//
// DecreaseTokenQuotaIfEnough / DecreaseUserQuotaIfEnough issue a single
// conditional UPDATE (`... WHERE ... >= ?`) whose RowsAffected==0 signals
// "insufficient balance". The overdraw invariant — successful debits can never
// exceed the seeded balance, and the row never goes negative — is only
// meaningful under REAL write contention, which SQLite's single-writer model
// can't create. These tests run in the pg-integration CI job (TEST_POSTGRES_DSN
// set) and self-skip everywhere else via SetupTestDB.
//
// Before the atomic guard existed, the pre-consume gate read remain/quota, ran a
// Go-level compare, then unconditionally debited — so N racing requests all read
// the same balance, all passed, and all debited past zero (bounded overdraft).
// These tests assert that path is closed.

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestIntegrationTokenQuota_ConcurrentIfEnough_NoOverdraw(t *testing.T) {
	cleanup := SetupTestDB(t) // skips when TEST_POSTGRES_DSN is unset
	defer cleanup()

	// SetupTestDB disables Redis, so the cache-refresh goroutine inside
	// DecreaseTokenQuotaIfEnough is a no-op and only the synchronous conditional
	// UPDATE moves remain_quota.
	const (
		workers   = 50
		debitEach = 30
		initial   = 100 // << workers*debitEach (1500): most debits MUST be refused
		maxOK     = initial / debitEach
	)

	user := &User{
		Username: "tok-overdraw-user",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Quota:    1_000_000,
		Group:    "default",
	}
	if err := DB.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	tok := &Token{
		UserId:      user.Id,
		Key:         "sk-tok-overdraw-" + common.GetRandomString(16),
		Status:      common.TokenStatusEnabled,
		Name:        "overdraw",
		CreatedTime: common.GetTimestamp(),
		ExpiredTime: -1,
		RemainQuota: initial,
		Group:       "default",
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	var wg sync.WaitGroup
	var okCount int64
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ok, err := DecreaseTokenQuotaIfEnough(tok.Id, tok.Key, debitEach)
			if err != nil {
				t.Errorf("DecreaseTokenQuotaIfEnough under contention: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&okCount, 1)
			}
		}()
	}
	wg.Wait()

	got := int(okCount)
	// Conservation: total granted quota can never exceed what was seeded.
	if got*debitEach > initial {
		t.Fatalf("overdraw: %d granted * %d = %d > initial %d", got, debitEach, got*debitEach, initial)
	}
	if got > maxOK {
		t.Fatalf("too many debits granted: %d (max %d)", got, maxOK)
	}
	remain := readTokenRemain(t, tok.Id)
	if remain < 0 {
		t.Fatalf("remain_quota went negative: %d", remain)
	}
	if want := initial - got*debitEach; remain != want {
		t.Fatalf("remain_quota = %d, want %d (initial - granted*each)", remain, want)
	}
}

func TestIntegrationUserQuota_ConcurrentIfEnough_NoOverdraw(t *testing.T) {
	cleanup := SetupTestDB(t) // skips when TEST_POSTGRES_DSN is unset
	defer cleanup()

	const (
		workers   = 50
		debitEach = 10
		initial   = 50 // << workers*debitEach (500): most debits MUST be refused
		maxOK     = initial / debitEach
	)

	user := &User{
		Username: "user-overdraw",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Quota:    initial,
		Group:    "default",
	}
	if err := DB.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var wg sync.WaitGroup
	var okCount int64
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ok, err := DecreaseUserQuotaIfEnough(user.Id, debitEach)
			if err != nil {
				t.Errorf("DecreaseUserQuotaIfEnough under contention: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&okCount, 1)
			}
		}()
	}
	wg.Wait()

	got := int(okCount)
	if got*debitEach > initial {
		t.Fatalf("overdraw: %d granted * %d = %d > initial %d", got, debitEach, got*debitEach, initial)
	}
	if got > maxOK {
		t.Fatalf("too many debits granted: %d (max %d)", got, maxOK)
	}
	quota := readUserQuota(t, user.Id)
	if quota < 0 {
		t.Fatalf("user quota went negative: %d", quota)
	}
	if want := initial - got*debitEach; quota != want {
		t.Fatalf("user quota = %d, want %d (initial - granted*each)", quota, want)
	}
}
