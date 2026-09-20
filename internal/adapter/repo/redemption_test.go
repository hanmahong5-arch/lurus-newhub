package repo

// redemption_test.go — cycle13 L3: unit coverage for the typed redemption
// sentinels and their handler-facing mapping helpers (RedemptionErrorCode /
// RedemptionErrorMessage), which replaced switch_redeem.go's hand-rolled
// switchRedeemKnownErrors map + sanitizeRedeemError string-prefix-stripping
// function (moved here so v2_redemption.go and switch_user_topup.go can share
// the same mapping instead of re-implementing it, and each handler's own test
// file — switch_redeem_test.go, v2_redemption_test.go,
// switch_user_topup_test.go — separately drives the full HTTP path to prove
// the fix reaches its response body).

import (
	"errors"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestRedemptionErrorMessage_KnownSentinelsPassThrough is the "no regression
// on the sentinel path" guarantee: every sentinel Redeem() can return comes
// back from RedemptionErrorMessage unchanged (its own .Error() text).
func TestRedemptionErrorMessage_KnownSentinelsPassThrough(t *testing.T) {
	for _, sentinel := range knownRedemptionErrors {
		got := RedemptionErrorMessage(sentinel)
		if got != sentinel.Error() {
			t.Errorf("RedemptionErrorMessage(%v) = %q, want %q (unchanged passthrough)", sentinel, got, sentinel.Error())
		}
	}
}

// TestRedemptionErrorMessage_UnknownErrorReplaced asserts any error that
// isn't one of the known sentinels — in particular, raw driver/GORM error
// text containing schema details like constraint or column names — is
// replaced with ErrRedemptionFailed's generic text and never leaks a
// recognizable substring of the original.
func TestRedemptionErrorMessage_UnknownErrorReplaced(t *testing.T) {
	cases := []error{
		errors.New(`pq: duplicate key value violates unique constraint "idx_redemptions_key"`),
		errors.New("no such column: quota"),
		errors.New("dial tcp 10.0.0.5:5432: connect: connection refused"),
		// A pre-cycle13 caller might still construct the OLD wrapped shape —
		// that string is not itself one of the sentinel VALUES (errors.Is
		// compares identity, not text), so it must also be replaced.
		errors.New("兑换失败，pq: duplicate key value violates unique constraint \"idx_redemptions_key\""),
	}
	for _, raw := range cases {
		got := RedemptionErrorMessage(raw)
		if got != ErrRedemptionFailed.Error() {
			t.Errorf("RedemptionErrorMessage(%v) = %q, want the generic fallback %q", raw, got, ErrRedemptionFailed.Error())
		}
	}
}

// TestRedemptionErrorMessage_Nil asserts the nil-input edge the three
// handlers never actually hit (they only call this after checking err !=
// nil) still behaves sanely rather than panicking.
func TestRedemptionErrorMessage_Nil(t *testing.T) {
	if got := RedemptionErrorMessage(nil); got != "" {
		t.Errorf("RedemptionErrorMessage(nil) = %q, want empty string", got)
	}
}

// TestRedemptionErrorCode_MapsToTaxonomy pins RedemptionErrorCode's mapping
// for each of the four states a v2 console/API caller needs to branch on
// individually, plus the two folded into the generic FAILED bucket
// (ErrRedemptionUserNotFound has no dedicated code in the cycle13 plan's
// four-code list; a genuinely unknown error must also fail safe to FAILED,
// not "").
func TestRedemptionErrorCode_MapsToTaxonomy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"invalid", ErrRedemptionInvalid, RedemptionErrorCodeInvalid},
		{"used", ErrRedemptionUsed, RedemptionErrorCodeUsed},
		{"expired", ErrRedemptionExpired, RedemptionErrorCodeExpired},
		{"wrong_tenant", ErrRedemptionWrongTenant, RedemptionErrorCodeTenantMismatch},
		{"failed", ErrRedemptionFailed, RedemptionErrorCodeFailed},
		{"user_not_found_folds_to_failed", ErrRedemptionUserNotFound, RedemptionErrorCodeFailed},
		{"unknown_folds_to_failed", errors.New("some raw driver text"), RedemptionErrorCodeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedemptionErrorCode(tc.err); got != tc.want {
				t.Errorf("RedemptionErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestRedeemMessagesKeepSwitchClassifierMarkers is the cycle13 plan finding
// #7 regression lock. The Switch desktop client
// (2c-gui-switch/internal/redemption/redeem.go's classifyRedeemFailure,
// :215-234) greps four substrings out of the message text to pick its
// localized UI copy: 已使用 (used), 过期 (expired), 禁用 (disabled), 不存在 (not
// found). ErrRedemptionUsed and ErrRedemptionExpired are the two sentinels
// with a direct substring correspondence and are checked here; this codebase
// pre-cycle13 (redemption.go's old "该兑换码已被使用" literal) would fail the
// ErrRedemptionUsed check below — 已被使用 does not contain 已使用 as a
// contiguous run (已,被,使,用 vs the target 已,使,用) — which is exactly finding
// #7's defect. ErrRedemptionWrongTenant and ErrRedemptionInvalid are NOT
// checked against these substrings: switch_redeem.go's own G5a comment
// already documents that ErrRedemptionWrongTenant's text matches none of the
// classifier's branches (a pre-existing, accepted gap, not this cycle's fix).
func TestRedeemMessagesKeepSwitchClassifierMarkers(t *testing.T) {
	if !strings.Contains(ErrRedemptionUsed.Error(), "已使用") {
		t.Errorf("ErrRedemptionUsed.Error() = %q, want it to contain the switch classifier substring '已使用'", ErrRedemptionUsed.Error())
	}
	if !strings.Contains(ErrRedemptionExpired.Error(), "过期") {
		t.Errorf("ErrRedemptionExpired.Error() = %q, want it to contain the switch classifier substring '过期'", ErrRedemptionExpired.Error())
	}
	// Sanity check on the rule itself: the OLD (pre-cycle13) text must NOT
	// satisfy it — otherwise this test would not have caught finding #7.
	const oldBuggyUsedText = "该兑换码已被使用"
	if strings.Contains(oldBuggyUsedText, "已使用") {
		t.Fatalf("test bug: the pre-cycle13 buggy text %q unexpectedly contains '已使用' — the substring check above would not have caught finding #7", oldBuggyUsedText)
	}
	// And the CURRENT sentinel must not equal the old buggy text (guards
	// against a future edit reverting only the doc comment).
	if ErrRedemptionUsed.Error() == oldBuggyUsedText {
		t.Errorf("ErrRedemptionUsed.Error() still equals the pre-cycle13 buggy text %q", oldBuggyUsedText)
	}
}

// TestRedeem_DriverErrorNeverReachesCaller manufactures a genuine driver
// failure inside Redeem()'s transaction (dropping users.quota so the
// `UPDATE users SET quota = quota + ?` statement fails with a real SQL
// error, not one of the typed sentinels) and asserts the error Redeem()
// returns is exactly ErrRedemptionFailed — never the raw driver text. This
// is the repo-level half of the fix; each handler's own test file
// (switch_redeem_test.go's TestSwitchRedeemAnonymous_RawDBErrorSanitized,
// v2_redemption_test.go's TestRedeemCodeV2_RawDBErrorReturnsGenericMessage,
// switch_user_topup_test.go's TestSwitchUserTopup_RawDBErrorReturnsGenericMessage)
// proves the same property through the full HTTP handler.
func TestRedeem_DriverErrorNeverReachesCaller(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "redeem_driver_err", "redeemdrivererr@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	code := seedRedemptionRow(t, u.Id, "default", "driver-err-code", 100000)

	if err := DB.Exec(`ALTER TABLE users DROP COLUMN quota`).Error; err != nil {
		t.Fatalf("drop quota column: %v", err)
	}

	_, err := Redeem(code.Key, u.Id)
	if err == nil {
		t.Fatal("expected an error when the underlying UPDATE fails")
	}
	if !errors.Is(err, ErrRedemptionFailed) {
		t.Errorf("errors.Is(err, ErrRedemptionFailed) = false, want true; err=%v", err)
	}
	if err.Error() != ErrRedemptionFailed.Error() {
		t.Errorf("err.Error() = %q, want the generic fallback %q (must not leak driver text)", err.Error(), ErrRedemptionFailed.Error())
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(err.Error(), "quota") || strings.Contains(lower, "column") || strings.Contains(lower, "sql") {
		t.Errorf("err.Error() leaks raw DB error text: %q", err.Error())
	}
}

// TestRedeem_KnownFailuresReturnTypedSentinels drives Redeem() through each
// of its early-reject branches and asserts errors.Is matches the
// corresponding exported sentinel — the contract every handler's
// RedemptionErrorCode/RedemptionErrorMessage call depends on.
func TestRedeem_KnownFailuresReturnTypedSentinels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "redeem_sentinel_user", "redeemsentinel@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	t.Run("invalid_code", func(t *testing.T) {
		_, err := Redeem("does-not-exist-anywhere", u.Id)
		if !errors.Is(err, ErrRedemptionInvalid) {
			t.Errorf("errors.Is(err, ErrRedemptionInvalid) = false; err=%v", err)
		}
	})

	t.Run("already_used", func(t *testing.T) {
		code := seedRedemptionRow(t, u.Id, "default", "sentinel-used", 1000)
		if _, err := Redeem(code.Key, u.Id); err != nil {
			t.Fatalf("first redeem must succeed: %v", err)
		}
		_, err := Redeem(code.Key, u.Id)
		if !errors.Is(err, ErrRedemptionUsed) {
			t.Errorf("errors.Is(err, ErrRedemptionUsed) = false; err=%v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		code := &Redemption{
			UserId: u.Id, TenantId: "default", Key: common.GetRandomString(32),
			Name: "sentinel-expired", Quota: 1000, Status: common.RedemptionCodeStatusEnabled,
			CreatedTime: common.GetTimestamp(), ExpiredTime: common.GetTimestamp() - 3600,
		}
		if err := DB.Create(code).Error; err != nil {
			t.Fatalf("seed expired code: %v", err)
		}
		_, err := Redeem(code.Key, u.Id)
		if !errors.Is(err, ErrRedemptionExpired) {
			t.Errorf("errors.Is(err, ErrRedemptionExpired) = false; err=%v", err)
		}
	})

	t.Run("wrong_tenant", func(t *testing.T) {
		otherUser := seedUser(t, "redeem_sentinel_other_tenant", "redeemsentinelother@test.com", common.RoleCommonUser, common.UserStatusEnabled, "some-other-tenant")
		code := seedRedemptionRow(t, u.Id, "default", "sentinel-tenant-mismatch", 1000)
		_, err := Redeem(code.Key, otherUser.Id)
		if !errors.Is(err, ErrRedemptionWrongTenant) {
			t.Errorf("errors.Is(err, ErrRedemptionWrongTenant) = false; err=%v", err)
		}
	})

	t.Run("user_not_found", func(t *testing.T) {
		code := seedRedemptionRow(t, u.Id, "default", "sentinel-no-user", 1000)
		_, err := Redeem(code.Key, 99999999)
		if !errors.Is(err, ErrRedemptionUserNotFound) {
			t.Errorf("errors.Is(err, ErrRedemptionUserNotFound) = false; err=%v", err)
		}
	})
}
