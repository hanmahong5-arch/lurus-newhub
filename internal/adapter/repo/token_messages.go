package repo

import "fmt"

// token_messages.go — the 402 sentence a caller sees when a token has run
// out of its own spending cap. Split out of token.go by the cycle-13 wiring
// pass as a pure move: the function is byte-identical to the one that stood
// in token.go. internal/pkg/gates' source-size ratchet holds token.go at its
// measured line count, so this cycle's addition to that file is paid for by
// a move rather than by raising the ceiling.
//
// It lives apart from the sentinels it wraps because it is the only piece of
// token.go that is customer-facing prose: the two 402 call sites in
// ValidateUserToken must render identical text, which is why there is one
// definition rather than two literals (TestL3ValidateUserToken_BothBranches_SameSuffix).

// tokenExhaustedMessage builds the human-readable 402 guidance for a token
// that has genuinely run out of its own spending cap (QuotaAvailable() ==
// false). Both the Status==TokenStatusExhausted branch below and the live
// RemainQuota<=0 downgrade call this single definition so the two call
// sites can never render diverging text for what must be the identical
// caller-facing state (TestL3ValidateUserToken_BothBranches_SameSuffix
// pins the two outputs equal). remainQuota is embedded as a raw integer —
// same figure/unit as the metadata's token_remain_quota_units — so the
// wire message itself carries a number instead of forcing the caller to
// parse metadata for one.
func tokenExhaustedMessage(remainQuota int) error {
	return fmt.Errorf("%w (available quota exhausted [remaining %d]; edit the token's remaining quota or set it to unlimited)", ErrTokenQuotaExhausted, remainQuota)
}
