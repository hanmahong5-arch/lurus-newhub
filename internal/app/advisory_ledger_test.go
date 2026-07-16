package app

// advisory_ledger_test.go — Track A single-ledger convergence: with
// LOCAL_LEDGER_ADVISORY on, the local user-quota ledger is shadow bookkeeping
// for PLATFORM-GOVERNED requests only. These tests pin the four contract
// corners: (1) governed + advisory skips the local 402, (2) flag off is
// byte-identical to today's behavior, (3) ungoverned traffic NEVER gets the
// bypass (no free-ride door), (4) the per-key token cap survives advisory
// (it is a user-set limit, not ledger state). Plus the settle-vs-release
// flip on local inconsistency, asserted against a fake platform that records
// which wallet endpoint was hit.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// withAdvisory flips LOCAL_LEDGER_ADVISORY for the test and restores it after.
func withAdvisory(t *testing.T, v bool) {
	t.Helper()
	prev := common.LocalLedgerAdvisory()
	common.SetLocalLedgerAdvisory(v)
	t.Cleanup(func() { common.SetLocalLedgerAdvisory(prev) })
}

// TestPreConsumeQuota_AdvisoryBypassesLocal402 — a platform-governed request
// (re-entry guard: pre-auth already held) with ZERO local balance must NOT be
// 402'd while advisory is on: the platform wallet already vouched for it. The
// local shadow write still happens (balance goes negative — that's the drift
// the reconciliation measures).
func TestPreConsumeQuota_AdvisoryBypassesLocal402(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, true)

	userId := seedTestUser(t, db, 0) // zero local balance
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	const existingPreAuth int64 = 7171
	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 4545,
		PlatformPreAuthID: existingPreAuth, // governed via the re-entry guard
	}

	const estimate = 1_000
	if apiErr := PreConsumeQuota(c, estimate, relayInfo); apiErr != nil {
		t.Fatalf("advisory+governed must not 402 on empty local balance, got: %v", apiErr.Error())
	}
	if !relayInfo.PlatformGoverned {
		t.Error("re-entry guard must mark the request platform-governed")
	}
	// Shadow pre-deduct still happened (local ledger keeps metering).
	if got := userQuota(t, db, userId); got != -estimate {
		t.Errorf("shadow user quota = %d, want %d (write still happens)", got, -estimate)
	}
	if relayInfo.FinalPreConsumedQuota != estimate {
		t.Errorf("FinalPreConsumedQuota = %d, want %d", relayInfo.FinalPreConsumedQuota, estimate)
	}
}

// TestPreConsumeQuota_AdvisoryOffKeeps402 — flag off (A0 default) must be
// byte-identical to today's behavior: same setup as above, expect the 402.
func TestPreConsumeQuota_AdvisoryOffKeeps402(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, false)

	userId := seedTestUser(t, db, 0)
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 4545,
		PlatformPreAuthID: 7171,
	}

	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr == nil {
		t.Fatal("flag off: empty local balance must still 402")
	}
	if got := userQuota(t, db, userId); got != 0 {
		t.Errorf("user quota = %d, want 0 (untouched on 402)", got)
	}
}

// TestPreConsumeQuota_AdvisoryUngovernedStill402s — the anti-free-ride pin:
// advisory ON but the platform never admitted this request (no pre-auth, no
// unified billing) → the full local gate applies. If this test ever goes
// green with a bypass, advisory mode has become an open door for legacy
// unlinked traffic.
func TestPreConsumeQuota_AdvisoryUngovernedStill402s(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, true)

	prevUnified := common.BillingUnifiedEnabled()
	common.SetBillingUnifiedEnabled(false) // platform never consulted
	t.Cleanup(func() { common.SetBillingUnifiedEnabled(prevUnified) })

	userId := seedTestUser(t, db, 0)
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
		// IdentityAccountID 0: unlinked legacy user
	}

	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr == nil {
		t.Fatal("ungoverned traffic must keep the local 402 even under advisory")
	}
	if relayInfo.PlatformGoverned {
		t.Error("ungoverned request must not be marked governed")
	}
}

// TestPreConsumeQuota_AdvisoryKeepsTokenCap — the per-key cap is the USER'S
// own spending limit, not ledger state: a token with too little remain quota
// must still 402 under advisory+governed.
func TestPreConsumeQuota_AdvisoryKeepsTokenCap(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, true)

	userId := seedTestUser(t, db, 100_000) // plenty of local balance
	key, tokenId := seedTestToken(t, db, userId, 100, false)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 4646,
		PlatformPreAuthID: 8181, // governed
	}

	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr == nil {
		t.Fatal("per-key token cap must stay enforced under advisory")
	}
}

// outboxActions reads back the actions of every billing outbox row. The
// HTTP/gRPC seam is not hermetically reachable in this package (the gRPC
// wrapper's WaitForReady burns the caller's ctx budget before the HTTP
// fallback runs — see billing_seam_extra_test.go's header), so settle-vs-
// release is asserted via which OUTBOX action the failure path enqueued:
// EnqueueSettle ⇒ the code chose to charge, EnqueueRelease ⇒ it chose to void.
func outboxActions(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var rows []entity.BillingOutbox
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	actions := make([]string, 0, len(rows))
	for _, r := range rows {
		actions = append(actions, r.Action)
	}
	return actions
}

// TestPostConsumeQuota_AdvisoryTokenFailureStillSettles — the hierarchy pin:
// in advisory mode a LOCAL token-write failure (shadow bookkeeping) must not
// drop revenue. With the platform unreachable, choosing settle materializes as
// an outbox settle row (retry-until-charged); nil error; compensation applied.
func TestPostConsumeQuota_AdvisoryTokenFailureStillSettles(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	withAdvisory(t, true)

	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prevOutbox := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prevOutbox })

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = "" // platform unreachable → settle fails → outbox
	common.BillingBreakerSuccess()
	t.Cleanup(func() { common.IdentityServiceURL = prevURL; common.BillingBreakerSuccess() })

	const start = 10_000
	userId := seedTestUser(t, db, start)

	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId: userId,
		// Unique fake id (tokens table dropped anyway) — the TPM window store
		// is process-global keyed by token id, and this package's convention
		// is unique ids per test (see business_tpm_test.go header).
		TokenId:           881_200,
		TokenKey:          "irrelevant",
		IdentityAccountID: 42,
		PlatformPreAuthID: 991_200,
		PlatformGoverned:  true,
	}

	if err := PostConsumeQuota(relayInfo, 300, 0, false); err != nil {
		t.Fatalf("advisory: shadow token failure must not surface an error, got: %v", err)
	}
	actions := outboxActions(t, db)
	settleSeen := false
	for _, a := range actions {
		if a == outboxActionSettle {
			settleSeen = true
		}
		if a == outboxActionRelease {
			t.Errorf("advisory must SETTLE on shadow inconsistency, but a release was enqueued (%v)", actions)
		}
	}
	if !settleSeen {
		t.Errorf("no settle enqueued; outbox actions: %v", actions)
	}
	// Compensation still keeps the shadow ledger consistent.
	if got := userQuota(t, db, userId); got != start {
		t.Errorf("user quota = %d, want %d (compensated)", got, start)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (handled)", relayInfo.PlatformPreAuthID)
	}
}

// TestPostConsumeQuota_EnforcingTokenFailureStillReleases — control twin: with
// the flag OFF the pre-existing conservation behavior is untouched (release
// enqueued, not settle; error surfaced).
func TestPostConsumeQuota_EnforcingTokenFailureStillReleases(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	withAdvisory(t, false)

	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prevOutbox := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prevOutbox })

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = ""
	common.BillingBreakerSuccess()
	t.Cleanup(func() { common.IdentityServiceURL = prevURL; common.BillingBreakerSuccess() })

	const start = 10_000
	userId := seedTestUser(t, db, start)

	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           881_300, // unique fake id — see settle twin above
		TokenKey:          "irrelevant",
		IdentityAccountID: 42,
		PlatformPreAuthID: 991_300,
		PlatformGoverned:  true, // governed but flag off → enforcing semantics
	}

	if err := PostConsumeQuota(relayInfo, 300, 0, false); err == nil {
		t.Fatal("enforcing mode must surface the token-quota error")
	}
	actions := outboxActions(t, db)
	releaseSeen := false
	for _, a := range actions {
		if a == outboxActionRelease {
			releaseSeen = true
		}
		if a == outboxActionSettle {
			t.Errorf("enforcing mode must RELEASE on local inconsistency, but a settle was enqueued (%v)", actions)
		}
	}
	if !releaseSeen {
		t.Errorf("no release enqueued; outbox actions: %v", actions)
	}
}
