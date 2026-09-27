package middleware

// auth_token_reject.go — the repo.ValidateUserToken error → HTTP rejection
// mapping TokenAuth (auth.go) used to carry inline. Moved here as a pure
// extraction (cycle 18 L3) so the one behavioural change of that lane —
// a transient DB failure is a 503, not a 401 — sits next to the 401/402
// verdicts it must stay distinguishable from.

import (
	"errors"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// tokenStateHeader tells a caller (and the newapi bridge in front of the
// hub) which verdict a TokenAuth rejection carries. Four 401s and one 503
// used to be indistinguishable without parsing the message; the bridge
// replays every 401 to newapi on the assumption that 401 means "the hub
// does not know this key", which silently kept revoked keys alive there.
const (
	tokenStateHeader       = "X-Lurus-Token-State"
	tokenStateUnknown      = "unknown"
	tokenStateDisabled     = "disabled"
	tokenStateExpired      = "expired"
	tokenStateLookupFailed = "lookup_failed"
)

// tokenLookupRetryAfterSeconds is short on purpose: the failure is a hub-side
// DB blip, and the caller's SDK will already back off on 503.
const tokenLookupRetryAfterSeconds = "5"

// rejectTokenError renders the rejection for a repo.ValidateUserToken
// failure. token may be nil (unknown key / lookup failure); when present it
// only feeds the 402 hint metadata. Every branch aborts the context.
func rejectTokenError(c *gin.Context, token *repo.Token, err error) {
	if errors.Is(err, repo.ErrTokenQuotaExhausted) {
		// This is the TOKEN's own spending cap (repo/token.go's
		// ValidateUserToken guards at :182 Status==TokenStatusExhausted
		// and :218 RemainQuota<=0), not the user's wallet balance — a
		// top-up cannot fix it, only editing the token's remain_quota /
		// unlimited_quota can (see token_service.go's "请先修改令牌剩余
		// 额度，或者设置为无限额度" remedy). Route to the token-cap
		// error code + hint instead of the wallet
		// ErrorCodeInsufficientUserQuota/topup_url pair used for actual
		// user-balance 402s elsewhere (pre_consume_quota.go).
		//
		// repo/token.go:182's Status==TokenStatusExhausted branch does
		// NOT imply RemainQuota<=0 — an admin can raise remain_quota
		// (app.ApplyTokenUpdate) without also re-enabling the token
		// (that's a separate, explicit status=Enabled request,
		// handler/token.go:230-239). In that reachable state the real
		// remedy is re-enabling the token, not editing a quota that's
		// already fine, so the hint (reason + wire message) must not
		// claim "quota exhausted".
		remainQuota := 0
		quotaAvailable := false
		if token != nil {
			remainQuota = token.RemainQuota
			// Single source of truth shared with repo.ValidateUserToken's
			// own Status==TokenStatusExhausted branch — see
			// repo.Token.QuotaAvailable(). Re-deriving this boolean here
			// independently is exactly the drift a prior defect hit: the
			// two copies had zero consistency lock between them, so an
			// edit to one silently stopped matching the other.
			quotaAvailable = token.QuotaAvailable()
		}
		hintOption := types.ErrOptionWithTokenQuotaHint(remainQuota)
		auditReason := `{"reason":"token_quota_exhausted"}`
		if quotaAvailable {
			hintOption = types.ErrOptionWithTokenDisabledHint(remainQuota)
			auditReason = `{"reason":"token_disabled"}`
		}
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, 0,
			governance.ActionAuthFailed, governance.ResourceToken, 0,
			auditReason))
		// Deliberately NOT abortWithOpenAiMessage: that helper's tail call
		// to logger.LogError (middleware/utils.go) would put every one of
		// these 402s into stdout/DB error logs — a caller retrying against
		// an exhausted token could otherwise flood them. The audit event
		// above is the durable record; ErrOptionWithNoRecordErrorLog()
		// already opts this NewAPIError out of the relay-side error log for
		// the same reason: a caller retrying a doomed request should not be
		// able to flood the error log, but MUST still leave a durable trail
		// somewhere — the audit event above is that trail. This branch is an
		// intentional exception, not an oversight; if that tradeoff needs
		// revisiting, start from this comment (this repo has no doc/coord).
		apiErr := types.NewErrorWithStatusCode(err, types.ErrorCodeTokenQuotaExhausted, http.StatusPaymentRequired,
			types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(), hintOption)
		apiErr.SetMessage(common.MessageWithRequestId(apiErr.Error(), c.GetString(common.RequestIdKey)))
		renderRejection(c, apiErr)
		c.Abort()
		return
	}
	if errors.Is(err, repo.ErrTokenLookupFailed) {
		// Not a verdict about the key: the hub could not ask its own DB. A
		// 401 here would tell the bridge "not ours, replay to newapi" and
		// move the request — billing included — onto the other gateway
		// for the duration of a DB blip. abortWithOpenAiMessage keeps the
		// error-log line (unlike the 402 above this is rare and worth
		// seeing); the driver text stays in SysLog, off the wire.
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, 0,
			governance.ActionAuthFailed, governance.ResourceToken, 0,
			`{"reason":"token_lookup_failed"}`))
		c.Header(tokenStateHeader, tokenStateLookupFailed)
		c.Header("Retry-After", tokenLookupRetryAfterSeconds)
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "token lookup failed, retry", string(types.ErrorCodeQueryDataError))
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, 0,
		governance.ActionAuthFailed, governance.ResourceToken, 0,
		`{"reason":"invalid_token"}`))
	// ErrTokenQuotaExhausted is handled above and never reaches here;
	// this errors.Is stays as the defensive twin of that branch (a
	// future refactor of the block above must not silently start
	// leaking the sentinel down to a bare invalid_request 401).
	// ErrTokenDisabled (repo/token.go) covers the plain
	// TokenStatusDisabled case — mapped to token_disabled instead of
	// the generic invalid_request fallback below; ErrTokenExpired likewise.
	// The 401 status for disabled/expired is public contract
	// (doc/product-integration-guide.md §B) — only the state header and
	// the code tell them apart from an unknown key.
	tokenErrCode := types.ErrorCodeInvalidRequest
	tokenState := tokenStateUnknown
	if errors.Is(err, repo.ErrTokenQuotaExhausted) {
		tokenErrCode = types.ErrorCodeTokenQuotaExhausted
	} else if errors.Is(err, repo.ErrTokenDisabled) {
		tokenErrCode = types.ErrorCodeTokenDisabled
		tokenState = tokenStateDisabled
	} else if errors.Is(err, repo.ErrTokenExpired) {
		tokenErrCode = types.ErrorCodeTokenExpired
		tokenState = tokenStateExpired
	}
	c.Header(tokenStateHeader, tokenState)
	abortWithOpenAiMessage(c, http.StatusUnauthorized, err.Error(), string(tokenErrCode))
}
