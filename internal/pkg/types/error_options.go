package types

import (
	"encoding/json"
	"errors"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func ErrOptionWithSkipRetry() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.skipRetry = true
	}
}

func ErrOptionWithNoRecordErrorLog() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.recordErrorLog = common.GetPointer(false)
	}
}

func ErrOptionWithHideErrMsg(replaceStr string) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		if common.DebugEnabled {
			common.SysLogf("ErrOptionWithHideErrMsg: %s, origin error: %s", replaceStr, e.Err)
		}
		e.Err = errors.New(replaceStr)
	}
}

func IsRecordErrorLog(e *NewAPIError) bool {
	if e == nil {
		return false
	}
	if e.recordErrorLog == nil {
		// default to true if not set
		return true
	}
	return *e.recordErrorLog
}

// ErrOptionWithTopupURL attaches a {"topup_url": ...} JSON object to the error
// Metadata so the client can navigate directly to the wallet top-up page.
// Used for per-user balance exhaustion (HTTP 402).
// Note: types imports common — no import cycle.
func ErrOptionWithTopupURL() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]string{
			"topup_url": common.IdentityPublicURL + walletTopupPath,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithTokenQuotaHint attaches structured metadata for a per-TOKEN
// spending-cap 402 (HTTP 402, ErrorCodeTokenQuotaExhausted): the token's own
// remain_quota, NOT a wallet top-up link. Unlike ErrOptionWithTopupURL, a
// wallet top-up cannot fix this state — the remedy is editing the token's
// remain_quota or switching it to unlimited (see token_service.go's
// "请先修改令牌剩余额度，或者设置为无限额度" guidance) — so this option
// deliberately does NOT set a management URL (none exists yet) and does NOT
// set Retry-After (no data source for "how long until it recovers").
//
// The field is named token_remain_quota_units (not token_remain_quota) and
// carries the RAW internal quota integer (common.QuotaPerUnit units == 1
// baseline USD), deliberately NOT the same unit as the human-readable
// error.message text: PreConsumeQuota's ErrTokenQuotaInsufficient path
// builds that message via logger.FormatQuotaASCII, a baseline-USD amount
// ("$0.000002") that no longer moves with the operator's display currency —
// it used to use logger.FormatQuota and rendered in whatever
// operation_setting.GetQuotaDisplayType() was set to, which also put a
// fullwidth ＄ / ¥ on the wire. Naming the raw-unit field explicitly (rather than reusing the
// ambiguous "token_remain_quota" name) is the fix for a prior defect where
// the same JSON response carried this integer under that name right next to
// a currency-formatted number in .message, differing by ~5x10^5 with no unit
// on either — see r2_token_quota_code_test.go / l3_token_quota_402_test.go.
// remainQuota is NOT floored at zero: a negative value (e.g. from a
// concurrent settlement draining the token between the read and this call)
// is passed through as-is — see the negative-value case in
// r2_token_quota_code_test.go.
func ErrOptionWithTokenQuotaHint(remainQuota int) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]any{
			"reason":                   "token_quota_exhausted",
			"token_remain_quota_units": remainQuota,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithTokenDisabledHint is the sibling of ErrOptionWithTokenQuotaHint
// for the state repo.ValidateUserToken's Status==TokenStatusExhausted branch
// can also produce: the token's Status flag is still "exhausted" but its
// RemainQuota has since been raised above zero (or switched to unlimited)
// without Status being flipped back to Enabled — handler/token.go's
// app.ApplyTokenUpdate copies RemainQuota but never touches Status; the
// enable transition only runs when the update request explicitly sets
// status=Enabled (token.go:230/CanEnableToken). Calling this "token quota
// exhausted" (ErrOptionWithTokenQuotaHint's reason) would contradict the very
// remainQuota this metadata reports, so it uses a distinct reason string —
// the remedy is re-enabling the token in the console, not editing its quota.
func ErrOptionWithTokenDisabledHint(remainQuota int) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]any{
			"reason":                   "token_disabled",
			"token_remain_quota_units": remainQuota,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithUpgradeURL attaches a {"upgrade_url": ...} JSON object to the error
// Metadata so the client can navigate to the plan pricing page.
// Used for tenant monthly-cap exhaustion (HTTP 402).
func ErrOptionWithUpgradeURL() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]string{
			"upgrade_url": common.IdentityPublicURL + pricingPath,
		})
		e.Metadata = json.RawMessage(raw)
	}
}
