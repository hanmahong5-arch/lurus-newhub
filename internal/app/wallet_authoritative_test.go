package app

// wallet_authoritative_test.go — D3: tenants.wallet_authoritative makes the
// platform wallet the only balance gate for that tenant. Governed + flag on
// admits an empty local balance; flag off, or flag on but not governed, keeps
// the 402. Global LOCAL_LEDGER_ADVISORY is held OFF so only the tenant switch
// can be what lets the request through.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

func seedWalletTenant(t *testing.T, db *gorm.DB, id string, walletAuthoritative bool) {
	t.Helper()
	repo.ResetTenantWalletCache() // the flag is cached per tenant id; ids repeat across tests
	row := &repo.Tenant{Id: id, IDPOrgID: "org-" + id, Slug: id, Name: id, Status: 1}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Set via Update: a false bool in Create would be replaced by the column default.
	if err := db.Model(&repo.Tenant{}).Where("id = ?", id).
		Update("wallet_authoritative", walletAuthoritative).Error; err != nil {
		t.Fatalf("set wallet_authoritative: %v", err)
	}
}

func walletAuthCase(t *testing.T, flag bool, governed bool) error {
	t.Helper()
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, false)
	seedWalletTenant(t, db, "t-wa", flag)

	userId := seedTestUser(t, db, 0) // zero local balance
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	c.Set("tenant_id", "t-wa")

	relayInfo := &relaycommon.RelayInfo{UserId: userId, TokenId: tokenId, TokenKey: key}
	if governed {
		relayInfo.IdentityAccountID = 4545
		relayInfo.PlatformPreAuthID = 7171 // governed via the re-entry guard
	} else {
		prev := common.BillingUnifiedEnabled()
		common.SetBillingUnifiedEnabled(false)
		t.Cleanup(func() { common.SetBillingUnifiedEnabled(prev) })
	}
	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr != nil {
		return apiErr
	}
	return nil
}

func requireInsufficientBalance(t *testing.T, err error) {
	t.Helper()
	var apiErr *types.NewAPIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		t.Fatalf("want *types.NewAPIError 402, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusPaymentRequired || apiErr.GetErrorCode() != types.ErrorCodeInsufficientUserQuota {
		t.Fatalf("want 402 insufficient_user_quota, got %d %s", apiErr.StatusCode, apiErr.GetErrorCode())
	}
}

func TestPreConsumeQuota_WalletAuthoritativeGovernedAdmitsEmptyBalance(t *testing.T) {
	before := testutil.ToFloat64(metrics.BillingAdvisoryBypassTotal.WithLabelValues("user_balance_402"))
	if err := walletAuthCase(t, true, true); err != nil {
		t.Fatalf("wallet_authoritative + governed must admit a zero local balance, got: %v", err)
	}
	after := testutil.ToFloat64(metrics.BillingAdvisoryBypassTotal.WithLabelValues("user_balance_402"))
	if after-before != 1 {
		t.Fatalf("user_balance_402 advisory counter delta = %v, want 1", after-before)
	}
}

func TestPreConsumeQuota_WalletAuthoritativeOffKeeps402(t *testing.T) {
	err := walletAuthCase(t, false, true)
	if err == nil {
		t.Fatal("flag off: zero local balance must still 402")
	}
	requireInsufficientBalance(t, err)
}

func TestPreConsumeQuota_WalletAuthoritativeUngovernedKeeps402(t *testing.T) {
	err := walletAuthCase(t, true, false)
	if err == nil {
		t.Fatal("flag on but request not platform-governed: must still 402 (no free-ride door)")
	}
	requireInsufficientBalance(t, err)
}

// postConsumeWithBrokenUserLedger runs PostConsumeQuota with the users table
// gone, so the Phase 1 local write fails; it returns that error.
func postConsumeWithBrokenUserLedger(t *testing.T, walletAuth bool) error {
	t.Helper()
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, false)
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)
	if err := db.Migrator().DropTable("users"); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId: userId, TokenId: tokenId, TokenKey: key,
		IdentityAccountID: 4545, PlatformPreAuthID: 7171, PlatformGoverned: true,
		WalletAuthoritative: walletAuth,
	}
	return PostConsumeQuota(relayInfo, 100, 0, false)
}

// A wallet-authoritative tenant settles like global advisory mode: a lost
// local shadow write is metered, not returned.
func TestPostConsumeQuota_WalletAuthoritativeToleratesShadowWriteLoss(t *testing.T) {
	if err := postConsumeWithBrokenUserLedger(t, true); err != nil {
		t.Fatalf("wallet-authoritative settle must tolerate a lost local write, got: %v", err)
	}
}

func TestPostConsumeQuota_NotWalletAuthoritativeStillReturnsWriteError(t *testing.T) {
	if err := postConsumeWithBrokenUserLedger(t, false); err == nil {
		t.Fatal("without wallet_authoritative or global advisory a local write failure must be returned")
	}
}
