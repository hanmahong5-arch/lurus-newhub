package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// redemptionFailureStatus picks the HTTP status for a failed repo.Redeem.
//
// userStatus is what the caller-side outcomes answer — 400 on RedeemCodeV2
// and SwitchUserTopup, 200 on SwitchRedeemAnonymous, whose desktop client
// parses the JSON envelope whatever the status — for invalid, used, expired
// and wrong-tenant codes. REDEMPTION_FAILED is different in kind: a driver or
// transaction error inside Redeem, rolled back, the code still redeemable.
// Before this a hub outage answered exactly what a mistyped code answered,
// so nothing on the wire (a proxy's status log, a client's retry policy)
// could tell the two apart; it is a 500 now (cycle-13 L3 acceptance minor,
// closed in the hand-finish). error_code stays REDEMPTION_FAILED either way.
func redemptionFailureStatus(err error, userStatus int) int {
	if repo.RedemptionErrorCode(err) == repo.RedemptionErrorCodeFailed {
		return http.StatusInternalServerError
	}
	return userStatus
}
