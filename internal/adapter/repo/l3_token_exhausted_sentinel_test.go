package repo

// l3_token_exhausted_sentinel_test.go — D4 (lane L3): ValidateUserToken must
// return a matchable sentinel error (ErrTokenQuotaExhausted) for BOTH quota
// exhaustion paths (Status==TokenStatusExhausted and the live
// !UnlimitedQuota && RemainQuota<=0 downgrade), and must never leak the key
// prefix/suffix or the raw Go boolean expression into the error string —
// those leaked straight to the HTTP body pre-fix (live 2026-08-26 repro).
//
// R2 addition (B3): both exhaustion branches must carry the SAME
// human-readable suffix. Before this fix, the Status==TokenStatusExhausted
// branch (ValidateUserToken's guard now at :182) had a suffix
// ("请充值后重新启用" — itself the wrong remedy, corrected by B2 to point at
// the token, not the wallet); the live-observed remain_quota<=0 branch (the
// guard now at :218) was a bare wrap with no suffix at all, so a caller
// landing there got no human-readable guidance. Both branches now share the
// single tokenExhaustedMessage() helper (token.go) so the two outputs
// cannot drift apart again. See TestL3ValidateUserToken_BothBranches_SameSuffix.
//
// Reverse guard: TestL3ValidateUserToken_ShortKeyNoPanic locks the
// `key[:3]`/`key[len(key)-3:]` out-of-range panic surface that existed while
// the leaking slices were still computed. This is defense-in-depth /
// dead-corner cleanup, not an exploitable vulnerability: every token-write
// path (8 of them, grep-verified) mints keys via GenerateRandomCharsKey(48),
// so a <3-char key can only be reached through direct DB manipulation, not
// through a live HTTP request — auth.go's header-key truncation is not
// itself a way to CREATE a short key, only to query one that already exists.

import (
	"errors"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestL3ValidateUserToken_StatusExhausted_SentinelAndNoLeak(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_exhausted_status", "l3exstatus@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExhausted, false, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Fatal("expected error for TokenStatusExhausted token")
	}
	if !errors.Is(err, ErrTokenQuotaExhausted) {
		t.Errorf("errors.Is(err, ErrTokenQuotaExhausted) = false, want true; err=%v", err)
	}
	if strings.Contains(err.Error(), tok.Key[:3]) {
		t.Errorf("error leaks key prefix: %v", err)
	}
	if strings.Contains(err.Error(), "UnlimitedQuota") {
		t.Errorf("error leaks internal Go expression: %v", err)
	}
	if strings.Contains(err.Error(), "sk-") {
		t.Errorf("error leaks key marker 'sk-': %v", err)
	}
}

func TestL3ValidateUserToken_RemainQuotaZero_SentinelAndNoLeak(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_exhausted_remain", "l3exremain@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Fatal("expected error for UnlimitedQuota=false, RemainQuota=0 token")
	}
	if !errors.Is(err, ErrTokenQuotaExhausted) {
		t.Errorf("errors.Is(err, ErrTokenQuotaExhausted) = false, want true; err=%v", err)
	}
	if strings.Contains(err.Error(), tok.Key[:3]) {
		t.Errorf("error leaks key prefix: %v", err)
	}
	if strings.Contains(err.Error(), "UnlimitedQuota") {
		t.Errorf("error leaks internal Go expression: %v", err)
	}
	if strings.Contains(err.Error(), "sk-") {
		t.Errorf("error leaks key marker 'sk-': %v", err)
	}
}

// TestL3ValidateUserToken_ShortKeyNoPanic locks the out-of-range panic
// surface that existed while the exhausted-quota branches still computed
// key[:3]/key[len(key)-3:] against the caller-supplied key. This is
// dead-corner cleanup / defense-in-depth, NOT an exploitable vulnerability
// from a live request: every token-write path mints 48-char keys via
// GenerateRandomCharsKey (grep-verified, 8 call sites), so a <3-char key
// must already exist in the DB by some out-of-band means before
// ValidateUserToken can ever be called with one — a production deployment
// cannot reach this branch through normal key issuance.
func TestL3ValidateUserToken_ShortKeyNoPanic(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_shortkey", "l3shortkey@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	shortKey := "ab"
	tok := &Token{
		UserId:         u.Id,
		TenantId:       "default",
		Key:            shortKey,
		Name:           "short-key-token",
		Status:         common.TokenStatusEnabled,
		UnlimitedQuota: false,
		RemainQuota:    0,
		ExpiredTime:    -1,
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed short-key token: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidateUserToken panicked on short key: %v", r)
		}
	}()

	_, err := ValidateUserToken(shortKey)
	if err == nil {
		t.Fatal("expected error for exhausted short-key token")
	}
	if !errors.Is(err, ErrTokenQuotaExhausted) {
		t.Errorf("errors.Is(err, ErrTokenQuotaExhausted) = false, want true; err=%v", err)
	}
}

// tokenExhaustedHintSuffix is the human-readable suffix both exhaustion
// branches (Status==TokenStatusExhausted and RemainQuota<=0) must share —
// pointing the caller at the TOKEN's own remain_quota/unlimited_quota
// setting (the token_service.go remedy), not the wallet.
// The literal " [剩余 0]" reflects both seeded fixtures below carrying
// RemainQuota=0 — tokenExhaustedMessage(token.go) embeds the actual figure,
// so this constant only matches when both branches share that same value.
const tokenExhaustedHintSuffix = "（该令牌可用额度已用尽 [剩余 0]，请修改令牌剩余额度或设置为无限额度）"

// TestL3ValidateUserToken_BothBranches_SameSuffix is the B3 regression lock:
// before this fix, Status==TokenStatusExhausted (ValidateUserToken's guard
// now at :182) carried a suffix ("请充值后重新启用" — itself the wrong
// remedy, a wallet top-up cannot raise a token's own cap) while the
// live-observed RemainQuota<=0 branch (the guard now at :218, the one live
// 2026-08-26 traffic actually hit) was a bare `fmt.Errorf("%w", ...)` with
// NO human-readable text at all. Both branches must now carry the
// identical, correct (token-focused) suffix, and neither may leak the key.
func TestL3ValidateUserToken_BothBranches_SameSuffix(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_suffix_symmetry", "l3suffixsymmetry@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	statusTok := seedToken(t, u.Id, common.TokenStatusExhausted, false, 0, -1)
	_, statusErr := ValidateUserToken(statusTok.Key)
	if statusErr == nil {
		t.Fatal("expected error for TokenStatusExhausted token")
	}

	u2 := seedUser(t, "l3_suffix_symmetry_2", "l3suffixsymmetry2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	remainTok := seedToken(t, u2.Id, common.TokenStatusEnabled, false, 0, -1)
	_, remainErr := ValidateUserToken(remainTok.Key)
	if remainErr == nil {
		t.Fatal("expected error for RemainQuota=0 token")
	}

	if !strings.Contains(statusErr.Error(), tokenExhaustedHintSuffix) {
		t.Errorf("Status==TokenStatusExhausted branch missing hint suffix: %v", statusErr)
	}
	if !strings.Contains(remainErr.Error(), tokenExhaustedHintSuffix) {
		t.Errorf("RemainQuota<=0 branch missing hint suffix: %v", remainErr)
	}
	if statusErr.Error() != remainErr.Error() {
		t.Errorf("both exhaustion branches must render the identical message; status=%q remain=%q",
			statusErr.Error(), remainErr.Error())
	}
	if strings.Contains(statusErr.Error(), "请充值") || strings.Contains(remainErr.Error(), "请充值") {
		t.Errorf("hint must not tell the caller to top up the wallet (wrong remedy for a per-token cap): status=%q remain=%q",
			statusErr.Error(), remainErr.Error())
	}
}

// tokenExhaustedWrongRemedyPhrase is the remedy-specific text that would be
// FALSE to show a caller whose token's RemainQuota is actually positive (or
// unlimited): "please edit the token's remaining quota" when there is
// nothing wrong with it. Note ErrTokenQuotaExhausted's own sentinel text
// ("令牌不可用") is present in EVERY wrapped message by construction
// (fmt.Errorf("%w...", ErrTokenQuotaExhausted) always renders the sentinel's
// Error() first) — but that shared prefix is NOT what errors.Is matches on:
// errors.Is compares the wrapped error VALUE's identity through Unwrap(),
// not the rendered string, so callers would still detect
// ErrTokenQuotaExhausted correctly even if the two branches' prefixes
// diverged in text. The prefix is present here purely for the human reading
// the wire message; this constant is the specific remedy clause that must
// NOT appear once RemainQuota/UnlimitedQuota contradict it.
const tokenExhaustedWrongRemedyPhrase = "请修改令牌剩余额度"

// TestL3ValidateUserToken_StatusExhaustedButRemainQuotaPositive_DoesNotClaimExhausted
// is the R2/B2 regression lock: Status==TokenStatusExhausted does NOT imply
// RemainQuota<=0. handler/token.go's app.ApplyTokenUpdate copies RemainQuota
// from an update request but never touches Status — the enable transition
// only happens when the request body explicitly sets status=Enabled
// (token.go:230/CanEnableToken) — so an admin can raise remain_quota on an
// already-exhausted-status token without re-enabling it. Before this fix the
// error text still told the caller to edit remain_quota even though the very
// RemainQuota this token carries (5000) contradicts that claim; the message
// must instead point at re-enabling the token.
func TestL3ValidateUserToken_StatusExhaustedButRemainQuotaPositive_DoesNotClaimExhausted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_status_remain_positive", "l3statusremainpositive@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExhausted, false, 5000, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Fatal("expected error for TokenStatusExhausted token even with positive remain_quota")
	}
	if !errors.Is(err, ErrTokenQuotaExhausted) {
		t.Errorf("errors.Is(err, ErrTokenQuotaExhausted) = false, want true (still routes to 402); err=%v", err)
	}
	if strings.Contains(err.Error(), tokenExhaustedWrongRemedyPhrase) {
		t.Errorf("message must not tell the caller to edit remain_quota when RemainQuota=5000: %v", err)
	}
	if !strings.Contains(err.Error(), "重新启用") {
		t.Errorf("message should point the caller at re-enabling the token, got: %v", err)
	}
	assertSentinelPrefixDoesNotClaimExhausted(t, err, "RemainQuota=5000")
}

// assertSentinelPrefixDoesNotClaimExhausted pins the SENTINEL text itself, not
// just the parenthesised remedy clause. fmt.Errorf("%w（…）", sentinel, …) always
// renders the sentinel first, so a sentinel that says "already used up" puts
// that claim at the head of the wire message and the parenthesised detail then
// contradicts it in the same breath — which is exactly the self-refuting body
// this branch exists to prevent. The remedy-phrase guard above cannot catch
// that: it only inspects the suffix, and the quota-available branch does not
// even call the suffix builder.
func assertSentinelPrefixDoesNotClaimExhausted(t *testing.T, err error, state string) {
	t.Helper()
	prefix := err.Error()
	if i := strings.Index(prefix, "（"); i >= 0 {
		prefix = prefix[:i]
	}
	if strings.Contains(prefix, "已用尽") {
		t.Errorf("sentinel prefix must not claim the quota is used up when %s; prefix=%q full=%v", state, prefix, err)
	}
}

// TestL3ValidateUserToken_StatusExhaustedUnlimitedQuota_DoesNotClaimExhausted
// covers the UnlimitedQuota sibling of the same reachable state.
func TestL3ValidateUserToken_StatusExhaustedUnlimitedQuota_DoesNotClaimExhausted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "l3_status_unlimited", "l3statusunlimited@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExhausted, true, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Fatal("expected error for TokenStatusExhausted token even with UnlimitedQuota")
	}
	if strings.Contains(err.Error(), tokenExhaustedWrongRemedyPhrase) {
		t.Errorf("message must not tell the caller to edit remain_quota when UnlimitedQuota=true: %v", err)
	}
	assertSentinelPrefixDoesNotClaimExhausted(t, err, "UnlimitedQuota=true")
}
