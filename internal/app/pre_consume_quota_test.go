package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestPreConsumeQuota_SufficientQuota_TrustedToken_Succeeds(t *testing.T) {
	db := setupServiceTestDB(t)
	trustQuota := common.GetTrustQuota()
	highQuota := trustQuota + 500_000
	userId := seedTestUser(t, db, highQuota)
	_, tokenId := seedTestToken(t, db, userId, highQuota, false)

	c := createTestGinContext()
	c.Set("token_quota", highQuota) // above trust quota

	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenKey:       "test-key",
		TokenUnlimited: false,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr != nil {
		t.Errorf("expected nil error, got: %v", apiErr.Error())
	}
	// Trusted path: pre-consumed quota should be zeroed
	if relayInfo.FinalPreConsumedQuota != 0 {
		t.Errorf("expected FinalPreConsumedQuota=0, got %d", relayInfo.FinalPreConsumedQuota)
	}
}

func TestPreConsumeQuota_InsufficientQuota_ReturnsError(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 0) // zero quota

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr == nil {
		t.Fatal("expected error for user with zero quota")
	}
}

func TestPreConsumeQuota_NegativeQuota_ReturnsError(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100) // small positive quota

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	// preConsumedQuota exceeds user quota
	apiErr := PreConsumeQuota(c, 200, relayInfo)
	if apiErr == nil {
		t.Fatal("expected error when preConsumedQuota exceeds user quota")
	}
}

func TestPreConsumeQuota_TrustQuota_SkipsPreConsume(t *testing.T) {
	db := setupServiceTestDB(t)
	// Create user with quota greater than trust quota (10 * QuotaPerUnit)
	trustQuota := common.GetTrustQuota()
	highQuota := trustQuota + 1_000_000
	userId := seedTestUser(t, db, highQuota)
	tokenKey, tokenId := seedTestToken(t, db, userId, highQuota, false)

	c := createTestGinContext()
	c.Set("token_quota", highQuota) // token also has enough quota

	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenKey:       tokenKey,
		TokenUnlimited: false,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr != nil {
		t.Errorf("expected nil error for trusted user, got: %v", apiErr.Error())
	}
	// When both user and token have sufficient quota (above trust threshold),
	// preConsumedQuota should be set to 0
	if relayInfo.FinalPreConsumedQuota != 0 {
		t.Errorf("expected FinalPreConsumedQuota=0 (trusted), got %d", relayInfo.FinalPreConsumedQuota)
	}
}

func TestPreConsumeQuota_UnlimitedToken_TrustSkips(t *testing.T) {
	db := setupServiceTestDB(t)
	trustQuota := common.GetTrustQuota()
	highQuota := trustQuota + 1_000_000
	userId := seedTestUser(t, db, highQuota)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr != nil {
		t.Errorf("expected nil error for unlimited token with high quota, got: %v", apiErr.Error())
	}
	// Unlimited token with high user quota => trusted, no pre-consume
	if relayInfo.FinalPreConsumedQuota != 0 {
		t.Errorf("expected FinalPreConsumedQuota=0, got %d", relayInfo.FinalPreConsumedQuota)
	}
}

func TestPreConsumeQuota_UserQuotaRecordedInRelayInfo(t *testing.T) {
	db := setupServiceTestDB(t)
	trustQuota := common.GetTrustQuota()
	userQuota := trustQuota + 100_000
	userId := seedTestUser(t, db, userQuota)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr != nil {
		t.Fatalf("expected nil error, got: %v", apiErr.Error())
	}

	// Verify user quota is recorded in relayInfo
	if relayInfo.UserQuota != userQuota {
		t.Errorf("expected UserQuota=%d, got %d", userQuota, relayInfo.UserQuota)
	}
}

func TestPreConsumeQuota_LowQuotaButPositive_ReturnsError(t *testing.T) {
	db := setupServiceTestDB(t)
	// Quota is positive but less than the preConsumedQuota
	userId := seedTestUser(t, db, 50)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr == nil {
		t.Fatal("expected error when user quota < preConsumedQuota")
	}
}

func TestPreConsumeQuota_NonExistentUser_ReturnsError(t *testing.T) {
	_ = setupServiceTestDB(t)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         999999, // non-existent
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr == nil {
		t.Fatal("expected error for non-existent user")
	}
}

// TestPreConsumeQuota_InsufficientLocalQuota_402_CarriesTopupURL verifies that the
// HTTP-402 error produced when a user has insufficient local quota carries a
// topup_url in its Metadata and in the OpenAI envelope, so clients can navigate
// the user to the wallet top-up page.
func TestPreConsumeQuota_InsufficientLocalQuota_402_CarriesTopupURL(t *testing.T) {
	setupServiceTestDB(t)

	// A user with zero quota cannot afford any request.
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 0)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenUnlimited: true,
	}

	apiErr := PreConsumeQuota(c, 100, relayInfo)
	if apiErr == nil {
		t.Fatal("expected 402 error for zero-quota user")
	}

	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", apiErr.StatusCode)
	}

	// Metadata must carry topup_url pointing at the platform's /topup page.
	if len(apiErr.Metadata) == 0 {
		t.Fatal("expected non-empty Metadata on 402 insufficient-quota error")
	}
	var meta map[string]string
	if err := json.Unmarshal(apiErr.Metadata, &meta); err != nil {
		t.Fatalf("Metadata is not valid JSON: %v", err)
	}
	topupURL, ok := meta["topup_url"]
	if !ok {
		t.Fatalf("Metadata missing 'topup_url' key; got: %s", string(apiErr.Metadata))
	}
	if topupURL != common.IdentityTopupURL() {
		t.Errorf("topup_url %q, want %q", topupURL, common.IdentityTopupURL())
	}
	if !strings.HasPrefix(topupURL, common.IdentityPublicURL) {
		t.Errorf("topup_url %q should start with IdentityPublicURL %q", topupURL, common.IdentityPublicURL)
	}

	// The rendered client envelope (what the end user actually receives via the
	// relay) MUST also carry the topup_url — ToOpenAIError forwards Metadata.
	// Asserting only apiErr.Metadata above would miss a dropped-Metadata bug in
	// the envelope conversion.
	env := apiErr.ToOpenAIError()
	if len(env.Metadata) == 0 {
		t.Fatal("expected non-empty Metadata on the rendered OpenAI envelope (user-facing)")
	}
	var envMeta map[string]string
	if err := json.Unmarshal(env.Metadata, &envMeta); err != nil {
		t.Fatalf("envelope Metadata is not valid JSON: %v", err)
	}
	if _, ok := envMeta["topup_url"]; !ok {
		t.Fatalf("rendered envelope Metadata missing 'topup_url'; got: %s", string(env.Metadata))
	}
}
