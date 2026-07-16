package app

// business_tpm_test.go — hermetic coverage for the business TPM sliding-window
// store (business_tpm.go): both backends (in-memory and miniredis), window
// sliding via the BizTPMNow clock seam, non-positive skips, concurrent
// recording, and the PostConsumeQuota settlement hook (the production write
// path). Every mutated global is restored; unique key ids per test keep the
// package -count=1 safe without a global reset hook.

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// freezeBizTPMClock pins BizTPMNow and returns an advance fn.
func freezeBizTPMClock(t *testing.T) func(d time.Duration) {
	t.Helper()
	now := time.Now()
	prev := BizTPMNow
	BizTPMNow = func() time.Time { return now }
	t.Cleanup(func() { BizTPMNow = prev })
	return func(d time.Duration) { now = now.Add(d) }
}

// withoutRedisTPM forces the in-memory backend.
func withoutRedisTPM(t *testing.T) {
	t.Helper()
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })
}

// withMiniRedisTPM wires common.RDB to a throwaway miniredis.
func withMiniRedisTPM(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		_ = client.Close()
		mr.Close()
	})
	return mr
}

func TestBusinessTPM_MemoryRecordQuerySlide(t *testing.T) {
	withoutRedisTPM(t)
	advance := freezeBizTPMClock(t)
	tok, tenant := 81001, "tpm-mem-tenant"

	RecordBusinessTPMUsage(tok, tenant, 120)
	advance(10 * time.Second)
	RecordBusinessTPMUsage(tok, tenant, 80)

	total, oldestMs, err := QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query: %v", err)
	}
	if total != 200 {
		t.Errorf("token window total = %d, want 200", total)
	}
	if oldestMs == 0 {
		t.Errorf("oldestMs = 0, want the first record's timestamp")
	}
	tenTotal, _, err := QueryBusinessTPMTenantWindow(context.Background(), tenant)
	if err != nil {
		t.Fatalf("tenant query: %v", err)
	}
	if tenTotal != 200 {
		t.Errorf("tenant window total = %d, want 200", tenTotal)
	}

	// 55s later the first record (65s old) is out, the second (55s old) stays.
	advance(55 * time.Second)
	total, _, err = QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query after slide: %v", err)
	}
	if total != 80 {
		t.Errorf("token window after slide = %d, want 80", total)
	}

	// Past the full window everything is gone and oldestMs reports empty.
	advance(61 * time.Second)
	total, oldestMs, err = QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query after full slide: %v", err)
	}
	if total != 0 || oldestMs != 0 {
		t.Errorf("empty window = (total %d, oldest %d), want (0, 0)", total, oldestMs)
	}
}

func TestBusinessTPM_RedisRecordQuerySlide(t *testing.T) {
	withMiniRedisTPM(t)
	advance := freezeBizTPMClock(t)
	tok, tenant := 82001, "tpm-redis-tenant"

	RecordBusinessTPMUsage(tok, tenant, 30)
	RecordBusinessTPMUsage(tok, tenant, 30) // same frozen instant — seq must keep both
	total, oldestMs, err := QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query: %v", err)
	}
	if total != 60 {
		t.Errorf("token window total = %d, want 60 (same-instant records must not collapse)", total)
	}
	if oldestMs == 0 {
		t.Errorf("oldestMs = 0, want a real timestamp")
	}
	tenTotal, _, err := QueryBusinessTPMTenantWindow(context.Background(), tenant)
	if err != nil {
		t.Fatalf("tenant query: %v", err)
	}
	if tenTotal != 60 {
		t.Errorf("tenant window total = %d, want 60", tenTotal)
	}

	advance(61 * time.Second)
	total, _, err = QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query after slide: %v", err)
	}
	if total != 0 {
		t.Errorf("window after slide = %d, want 0", total)
	}
}

func TestBusinessTPM_MalformedRedisMemberCountsZeroNotError(t *testing.T) {
	mr := withMiniRedisTPM(t)
	freezeBizTPMClock(t)
	tok := 82501

	RecordBusinessTPMUsage(tok, "", 25)
	// A corrupted member must be skipped (0), never fail the window read.
	key := bizTPMTokenKeyPrefix + strconv.Itoa(tok)
	if _, err := mr.ZAdd(key, float64(BizTPMNow().UnixMilli()), "garbage-no-colon"); err != nil {
		t.Fatalf("seed malformed member: %v", err)
	}
	total, _, err := QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("query with malformed member: %v", err)
	}
	if total != 25 {
		t.Errorf("total = %d, want 25 (malformed member counts as 0)", total)
	}
}

func TestRecordBusinessTPMUsage_NonPositiveAndNoDimensionSkips(t *testing.T) {
	withoutRedisTPM(t)
	freezeBizTPMClock(t)
	tok, tenant := 83001, "tpm-skip-tenant"

	RecordBusinessTPMUsage(tok, tenant, 0)   // zero => skip
	RecordBusinessTPMUsage(tok, tenant, -50) // refund => skip
	RecordBusinessTPMUsage(0, "", 100)       // no dimension => nothing to record

	total, _, err := QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query: %v", err)
	}
	tenTotal, _, terr := QueryBusinessTPMTenantWindow(context.Background(), tenant)
	if terr != nil {
		t.Fatalf("tenant query: %v", terr)
	}
	if total != 0 || tenTotal != 0 {
		t.Errorf("windows = (%d, %d), want (0, 0)", total, tenTotal)
	}
}

// Concurrent recorders must not lose usage: the memory backend serializes on
// one mutex, the Redis backend on single-key ZADDs with a process-unique seq
// suffix. Data-race freedom itself is the CI -race gate's job.
func TestBusinessTPM_ConcurrentRecordNoLoss(t *testing.T) {
	withoutRedisTPM(t)
	freezeBizTPMClock(t)
	tok, tenant := 84001, "tpm-conc-tenant"

	const goroutines, perRecord = 50, 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			RecordBusinessTPMUsage(tok, tenant, perRecord)
		}()
	}
	wg.Wait()

	want := int64(goroutines * perRecord)
	total, _, err := QueryBusinessTPMTokenWindow(context.Background(), tok)
	if err != nil {
		t.Fatalf("token query: %v", err)
	}
	if total != want {
		t.Errorf("token window after %d concurrent records = %d, want %d", goroutines, total, want)
	}
	tenTotal, _, terr := QueryBusinessTPMTenantWindow(context.Background(), tenant)
	if terr != nil {
		t.Fatalf("tenant query: %v", terr)
	}
	if tenTotal != want {
		t.Errorf("tenant window = %d, want %d", tenTotal, want)
	}
}

// TestPostConsumeQuota_RecordsBusinessTPMWindow drives the PRODUCTION write
// path: a real PostConsumeQuota settlement against a seeded DB must land
// quota+preConsumed in both the token's and the tenant's TPM windows, with the
// tenant resolved from the token row (no seams besides the inline AsyncGo).
func TestPostConsumeQuota_RecordsBusinessTPMWindow(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	withoutRedisTPM(t)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	tenantID := "tpm-hook-tenant"
	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)
	if err := db.Table("tokens").Where("id = ?", tokenId).Update("tenant_id", tenantID).Error; err != nil {
		t.Fatalf("bind token to tenant: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
		// IdentityAccountID stays 0: the TPM hook must fire OUTSIDE the
		// platform-wallet gate (local-quota tenants must be throttled too).
	}
	if err := PostConsumeQuota(relayInfo, 300, 50, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	total, _, err := QueryBusinessTPMTokenWindow(context.Background(), tokenId)
	if err != nil {
		t.Fatalf("token query: %v", err)
	}
	if total != 350 {
		t.Errorf("token TPM window = %d, want 350 (quota 300 + preConsumed 50)", total)
	}
	tenTotal, _, terr := QueryBusinessTPMTenantWindow(context.Background(), tenantID)
	if terr != nil {
		t.Fatalf("tenant query: %v", terr)
	}
	if tenTotal != 350 {
		t.Errorf("tenant TPM window = %d, want 350 (tenant resolved from token row)", tenTotal)
	}

	// Refund settlements (negative totals) must not pollute the window.
	if err := PostConsumeQuota(relayInfo, -100, 0, false); err != nil {
		t.Fatalf("refund PostConsumeQuota: %v", err)
	}
	total, _, _ = QueryBusinessTPMTokenWindow(context.Background(), tokenId)
	if total != 350 {
		t.Errorf("token TPM window after refund = %d, want unchanged 350", total)
	}
}
