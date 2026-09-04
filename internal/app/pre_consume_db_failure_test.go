package app

import (
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestPreConsumeQuota_TokenDebitDBFailureIsNotAPaymentError covers the branch
// PreConsumeQuota takes when PreConsumeTokenQuota fails for a reason that is
// NOT the per-key spending cap — i.e. the database refused the write.
//
// It answered 402 with code pre_consume_token_quota_failed, which
// types.RelayErrorType buckets as "insufficient_quota". A persistence outage
// was therefore indistinguishable from "this customer ran out of money", both
// in the response the caller reads and in the metric an operator watches: the
// relay_errors_total series would move under the label that means "the customer
// needs to top up". The branch had no test at all.
//
// The driver is a real failure, not a stub: the tokens table is dropped out
// from under an already-seeded token, so repo.DecreaseTokenQuotaIfEnough gets a
// genuine SQL error from GORM. Dropping it AFTER seeding matters — the token
// has to exist for the pre-consume path to be reached at all.
func TestPreConsumeQuota_TokenDebitDBFailureIsNotAPaymentError(t *testing.T) {
	db := setupServiceTestDB(t)

	// Quota high enough that the user-balance gate above passes and low enough
	// that the trust-quota shortcut does not zero out preConsumedQuota.
	userID := seedTestUser(t, db, 1_000_000)
	key, tokenID := seedTestToken(t, db, userID, 500_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:      userID,
		TokenId:     tokenID,
		TokenKey:    key,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	// Break the write the branch under test depends on.
	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("failed to drop tokens table: %v", err)
	}

	apiErr := PreConsumeQuota(createTestGinContext(), 1000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected an error when the token debit cannot be written, got nil — " +
			"the request would have been served without any local pre-deduction")
	}

	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d. A database that cannot write is our fault, "+
			"not the caller's; 402 tells a customer with a full wallet to go top it up.",
			apiErr.StatusCode, http.StatusInternalServerError)
	}

	if got := types.RelayErrorType(apiErr); got != "internal" {
		t.Errorf("RelayErrorType = %q, want %q. While this returned insufficient_quota, a "+
			"persistence outage moved the same metric series that a genuinely broke customer "+
			"does — the dashboard read our outage as their empty wallet.", got, "internal")
	}

	// An internal fault is exactly what the error log exists for. The 402
	// branches suppress it on purpose (a customer being out of credit is not an
	// incident); this branch must not inherit that suppression.
	if !types.IsRecordErrorLog(apiErr) {
		t.Error("NoRecordErrorLog is set on an internal failure — the one class of error " +
			"an operator needs a log row for is the one being suppressed")
	}

	// Retrying a broken local write against a different upstream channel cannot
	// help, and would multiply the failed writes.
	if !types.IsSkipRetryError(apiErr) {
		t.Error("SkipRetry is not set — a failed local DB write is not a channel fault, " +
			"so channel failover would just repeat it")
	}
}

// TestPreConsumeQuota_TokenCapRejectionStaysAPaymentError is the other half of
// the branch: the per-key spending cap IS a payment problem and must keep its
// 402, its quota bucket and its log suppression. Without this, "fix the 402"
// could be satisfied by turning every rejection into a 500.
func TestPreConsumeQuota_TokenCapRejectionStaysAPaymentError(t *testing.T) {
	db := setupServiceTestDB(t)

	userID := seedTestUser(t, db, 1_000_000)
	// remain_quota below the amount requested → the atomic debit matches no row.
	key, tokenID := seedTestToken(t, db, userID, 10, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:      userID,
		TokenId:     tokenID,
		TokenKey:    key,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	apiErr := PreConsumeQuota(createTestGinContext(), 1000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected a rejection when the token's own cap cannot cover the request")
	}
	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Errorf("StatusCode = %d, want %d — the per-key cap is a real payment problem",
			apiErr.StatusCode, http.StatusPaymentRequired)
	}
	if got := types.RelayErrorType(apiErr); got != "insufficient_quota" {
		t.Errorf("RelayErrorType = %q, want %q", got, "insufficient_quota")
	}
}
