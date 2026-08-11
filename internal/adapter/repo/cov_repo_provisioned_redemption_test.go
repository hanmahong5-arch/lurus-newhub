package repo

// cov_repo_provisioned_redemption_test.go — real-Postgres coverage for the
// idempotent distributor batch-issuance path (ProvisionRedemptionBatchIdempotent
// / RevokeProvisionedRedemptionBatch / decodeBatchCodes), which mints redemption
// codes on money's behalf: a caller (internal provisioning API) may retry a
// timed-out request, and a double-mint would let a distributor cash the same
// event twice. Mirrors credit_pool_fund_pg_test.go's assertion style: concrete
// post-state (row counts, balances), not just "err == nil".

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// repoSetupProvisionPG wires a fresh PG database with the extra tables the
// provisioning path needs beyond SetupTestDB's base list.
func repoSetupProvisionPG(t *testing.T) {
	t.Helper()
	SetupTestDB(t)
	if err := DB.AutoMigrate(&entity.ProvisionedRedemptionBatch{}); err != nil {
		t.Fatalf("migrate provisioned_redemption_batches: %v", err)
	}
}

func TestProvisionRedemptionBatchIdempotent_FirstCallMintsExactCodes(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	batch, codes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-a", "evt-mint-1", 3, 500, "distA", 0, "distributor", 7)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if batch == nil || batch.BatchSize != 3 || batch.QuotaPerCode != 500 || batch.TenantID != "tenant-a" {
		t.Fatalf("unexpected batch row: %+v", batch)
	}
	if len(codes) != 3 {
		t.Fatalf("want 3 codes, got %d: %v", len(codes), codes)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate code minted: %s", c)
		}
		seen[c] = true
	}

	// Exactly 3 redemption rows persisted, scoped to the tenant, quota/name/status correct.
	var rows []Redemption
	if err := DB.Where("tenant_id = ?", "tenant-a").Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load redemptions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 redemption rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Quota != 500 {
			t.Errorf("row %d: quota=%d want 500", i, r.Quota)
		}
		if r.Status != 1 { // RedemptionCodeStatusEnabled
			t.Errorf("row %d: status=%d want enabled(1)", i, r.Status)
		}
		if r.UserId != 7 {
			t.Errorf("row %d: user_id=%d want creator 7", i, r.UserId)
		}
		wantName := "distA-" + strconv.Itoa(i+1)
		if r.Name != wantName {
			t.Errorf("row %d: name=%q want %q", i, r.Name, wantName)
		}
	}
}

func TestProvisionRedemptionBatchIdempotent_ReplaySameEventReturnsOriginalCodesNoDoubleMint(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	firstBatch, firstCodes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-b", "evt-replay", 2, 100, "distB", 0, "distributor", 1)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	replayBatch, replayCodes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-b", "evt-replay", 2, 100, "distB", 0, "distributor", 1)
	if !errors.Is(err, ErrRedemptionBatchExists) {
		t.Fatalf("want ErrRedemptionBatchExists on replay, got %v", err)
	}
	if replayBatch == nil || replayBatch.ID != firstBatch.ID {
		t.Fatalf("replay must return the original batch row: got %+v want id=%d", replayBatch, firstBatch.ID)
	}
	if len(replayCodes) != len(firstCodes) {
		t.Fatalf("replay code count mismatch: got %d want %d", len(replayCodes), len(firstCodes))
	}
	for i := range firstCodes {
		if replayCodes[i] != firstCodes[i] {
			t.Fatalf("replay code[%d]=%s want original %s (must not mint new codes)", i, replayCodes[i], firstCodes[i])
		}
	}

	// No second batch row, no extra redemption rows.
	var batchCount int64
	DB.Model(&ProvisionedRedemptionBatch{}).Where("event_id = ?", "evt-replay").Count(&batchCount)
	if batchCount != 1 {
		t.Fatalf("want exactly 1 batch row after replay, got %d", batchCount)
	}
	var redCount int64
	DB.Model(&Redemption{}).Where("tenant_id = ?", "tenant-b").Count(&redCount)
	if redCount != 2 {
		t.Fatalf("want exactly 2 redemption rows after replay (no double mint), got %d", redCount)
	}
}

// TestProvisionRedemptionBatchIdempotent_ConcurrentReplayNeverDoubleMints fires
// N concurrent calls with the SAME event_id at a real PG database. At most one
// caller may win the insert; every caller must still receive a consistent set
// of codes (the winner's set), and the redemptions table must carry exactly
// one batch's worth of rows — never workers*count.
func TestProvisionRedemptionBatchIdempotent_ConcurrentReplayNeverDoubleMints(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	const workers = 8
	var wins int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-race", "evt-race", 4, 50, "race", 0, "distributor", 1)
			if err == nil {
				atomic.AddInt64(&wins, 1)
			} else if !errors.Is(err, ErrRedemptionBatchExists) {
				t.Errorf("unexpected error from concurrent provision: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins > 1 {
		t.Fatalf("double-mint: %d goroutines won the insert for the same event_id", wins)
	}

	var batchCount int64
	DB.Model(&ProvisionedRedemptionBatch{}).Where("event_id = ?", "evt-race").Count(&batchCount)
	if batchCount != 1 {
		t.Fatalf("want exactly 1 batch row after race, got %d", batchCount)
	}
	var redCount int64
	DB.Model(&Redemption{}).Where("tenant_id = ?", "tenant-race").Count(&redCount)
	if redCount != 4 {
		t.Fatalf("want exactly 4 redemption rows after race (one batch worth), got %d", redCount)
	}
}

func TestProvisionRedemptionBatchIdempotent_InputValidation(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	cases := []struct {
		name         string
		eventID      string
		count        int
		quotaPerCode int64
	}{
		{"empty event id", "", 1, 100},
		{"zero count", "evt-v1", 0, 100},
		{"negative count", "evt-v2", -5, 100},
		{"zero quota", "evt-v3", 1, 0},
		{"negative quota", "evt-v4", 1, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch, codes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-val", tc.eventID, tc.count, tc.quotaPerCode, "p", 0, "src", 1)
			if err == nil {
				t.Fatalf("want validation error, got batch=%+v codes=%v", batch, codes)
			}
			if batch != nil || codes != nil {
				t.Fatalf("want nil batch/codes on validation failure, got batch=%+v codes=%v", batch, codes)
			}
		})
	}

	// No rows should have been written by any of the rejected calls.
	var cnt int64
	DB.Model(&ProvisionedRedemptionBatch{}).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("validation-rejected calls must not persist rows, got %d batch rows", cnt)
	}
}

func TestRevokeProvisionedRedemptionBatch_DisablesOnlyUnusedCodes(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	batch, codes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-rev", "evt-rev", 3, 200, "rev", 0, "distributor", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	_ = batch

	// Mark one code as already used (simulating a redeem) before revoke.
	if err := DB.Model(&Redemption{}).Where(`"key" = ?`, codes[0]).Update("status", 3 /* used */).Error; err != nil {
		t.Fatalf("mark used: %v", err)
	}

	revoked, err := RevokeProvisionedRedemptionBatch(ctx, "tenant-rev", "evt-rev")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("want 2 codes revoked (3 minted - 1 already used), got %d", revoked)
	}

	var rows []Redemption
	DB.Where(`"key" IN ?`, codes).Order("id").Find(&rows)
	if len(rows) != 3 {
		t.Fatalf("want 3 redemption rows, got %d", len(rows))
	}
	statusByKey := map[string]int{}
	for _, r := range rows {
		statusByKey[r.Key] = r.Status
	}
	if statusByKey[codes[0]] != 3 {
		t.Errorf("used code must stay used(3), got %d", statusByKey[codes[0]])
	}
	if statusByKey[codes[1]] != 2 || statusByKey[codes[2]] != 2 {
		t.Errorf("unused codes must be disabled(2), got %d and %d", statusByKey[codes[1]], statusByKey[codes[2]])
	}

	// Idempotent: revoking again matches zero enabled rows now, returns 0 not an error.
	revokedAgain, err := RevokeProvisionedRedemptionBatch(ctx, "tenant-rev", "evt-rev")
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if revokedAgain != 0 {
		t.Fatalf("want 0 on already-revoked batch, got %d", revokedAgain)
	}
}

// TestRevokeProvisionedRedemptionBatch_TenantIsolation proves a tenant cannot
// revoke another tenant's batch even when it knows the exact event_id — the
// WHERE clause on the batch lookup is scoped by (event_id, tenant_id).
func TestRevokeProvisionedRedemptionBatch_TenantIsolation(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	_, codes, err := ProvisionRedemptionBatchIdempotent(ctx, "tenant-owner", "evt-cross", 2, 100, "x", 0, "distributor", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	revoked, err := RevokeProvisionedRedemptionBatch(ctx, "tenant-intruder", "evt-cross")
	if !errors.Is(err, ErrRedemptionBatchNotFound) {
		t.Fatalf("want ErrRedemptionBatchNotFound for cross-tenant revoke, got revoked=%d err=%v", revoked, err)
	}

	// The owning tenant's codes must remain untouched (still enabled).
	var stillEnabled int64
	DB.Model(&Redemption{}).Where(`"key" IN ? AND status = ?`, codes, 1).Count(&stillEnabled)
	if stillEnabled != 2 {
		t.Fatalf("cross-tenant revoke must not touch the real owner's codes: still-enabled=%d want 2", stillEnabled)
	}
}

func TestRevokeProvisionedRedemptionBatch_UnknownEventID(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	revoked, err := RevokeProvisionedRedemptionBatch(ctx, "tenant-x", "evt-does-not-exist")
	if !errors.Is(err, ErrRedemptionBatchNotFound) {
		t.Fatalf("want ErrRedemptionBatchNotFound, got revoked=%d err=%v", revoked, err)
	}
}

func TestRevokeProvisionedRedemptionBatch_EmptyEventIDRejected(t *testing.T) {
	repoSetupProvisionPG(t)
	ctx := context.Background()

	if _, err := RevokeProvisionedRedemptionBatch(ctx, "tenant-x", ""); err == nil {
		t.Fatal("want error for empty event_id")
	}
}

func TestDecodeBatchCodes(t *testing.T) {
	codes, err := decodeBatchCodes(`["a","b","c"]`)
	if err != nil {
		t.Fatalf("decode valid: %v", err)
	}
	if len(codes) != 3 || codes[0] != "a" || codes[2] != "c" {
		t.Fatalf("unexpected decode: %v", codes)
	}

	if _, err := decodeBatchCodes("not-json"); err == nil {
		t.Fatal("want error decoding malformed JSON")
	}

	empty, err := decodeBatchCodes(`[]`)
	if err != nil {
		t.Fatalf("decode empty array: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want empty slice, got %v", empty)
	}
}
