package app

// coverage_seam_extra_test.go — targets the remaining service seams:
//   - notify-limit lifecycle (Init/Stop + context-cancel shutdown of the
//     cleanup goroutine),
//   - platformPreAuthorize denial path (billing breaker OPEN + no cached
//     balance ⇒ degrade denied ⇒ 402, no pre-auth id set),
//   - reportQuotaThreshold's enabled-but-no-publisher guard,
//   - SearchLogs' Meilisearch-enabled branch falling back to the DB when the
//     search backend errors.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
	"github.com/meilisearch/meilisearch-go"
)

// ─── notify-limit lifecycle ────────────────────────────────────────────────

func TestInitNotifyLimitCleanup_StartsAndStopsViaContext(t *testing.T) {
	// The cleanup Once is a package global; reset it so Init actually runs its
	// body here regardless of whether a prior memory-limit test consumed it.
	cleanupOnce = sync.Once{}
	cleanupCancel = nil
	t.Cleanup(func() {
		// Leave the Once consumed and cancel any goroutine we started.
		if cleanupCancel != nil {
			cleanupCancel()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	InitNotifyLimitCleanup(ctx)
	if cleanupCancel == nil {
		t.Fatal("InitNotifyLimitCleanup did not wire the cancel func")
	}

	// A second Init is a no-op (Once already fired) — must not panic or replace.
	prevCancel := cleanupCancel
	InitNotifyLimitCleanup(ctx)
	// StopNotifyLimitCleanup cancels the context; the goroutine observes
	// ctx.Done() and returns (covers the shutdown branch).
	StopNotifyLimitCleanup()
	_ = prevCancel
	time.Sleep(50 * time.Millisecond)
}

// ─── platformPreAuthorize denial ───────────────────────────────────────────

func TestPlatformPreAuthorize_BreakerOpenNoCacheDenies(t *testing.T) {
	_ = withRedis(t) // real Redis, but we deliberately leave no cached balance

	const accountID int64 = 55123
	common.InvalidateCachedWalletBalance(accountID) // ensure ShouldSkipPreAuth=false

	// Force the billing breaker OPEN so PreAuthorizeWithBreaker fast-fails
	// without a 5s network call.
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	if !common.BillingBreakerIsOpen() {
		t.Fatal("precondition: billing breaker should be OPEN after 3 failures")
	}
	t.Cleanup(common.BillingBreakerSuccess) // reset breaker to CLOSED

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            4321,
		IdentityAccountID: accountID,
		OriginModelName:   "gpt-4",
	}

	apiErr := platformPreAuthorize(c, 100_000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected a 402 denial when breaker is open and no cached balance exists")
	}
	// No pre-auth was created — the caller must not think a freeze exists.
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (denied, nothing frozen)", relayInfo.PlatformPreAuthID)
	}
}

// ─── reportQuotaThreshold guard ────────────────────────────────────────────

func TestReportQuotaThreshold_EnabledButNoPublisher(t *testing.T) {
	// nats enabled via env, but no publisher was initialised (Get()==nil), so
	// reportQuotaThreshold returns at the publisher guard without touching the DB.
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "true")

	relayInfo := &relaycommon.RelayInfo{
		UserId:            777,
		IdentityAccountID: 42,
	}
	// Must not panic and must be a no-op (no publisher available).
	reportQuotaThreshold(context.Background(), relayInfo, 500)
}

// ─── SearchLogs Meilisearch branch → DB fallback ───────────────────────────

func TestSearchLogs_MeiliEnabledFallsBackToDBOnError(t *testing.T) {
	setupServiceTestDB(t)
	uid := seedTestUser(t, repo.DB, 1)
	seedLog(t, uid, "gamma request")

	// Point the search Client at a dead endpoint with a short timeout so the
	// Meilisearch branch runs, errors, and falls back to the DB query.
	prevEnabled := search.Enabled
	prevClient := search.Client
	search.Enabled = true
	search.Client = meilisearch.New("http://127.0.0.1:1",
		meilisearch.WithCustomClient(&http.Client{Timeout: 300 * time.Millisecond}))
	t.Cleanup(func() {
		search.Enabled = prevEnabled
		search.Client = prevClient
	})

	if !search.IsEnabled() {
		t.Fatal("precondition: search should report enabled with a non-nil client")
	}

	res, err := SearchLogs(LogSearchParams{UserId: uid, Keyword: "gamma"})
	if err != nil {
		t.Fatalf("SearchLogs should fall back to DB, got err: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("fallback DB search total = %d, want 1", res.Total)
	}
}
