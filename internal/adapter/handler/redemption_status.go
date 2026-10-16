package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
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
	switch repo.RedemptionErrorCode(err) {
	case repo.RedemptionErrorCodeFailed:
		return http.StatusInternalServerError
	case repo.RedemptionErrorCodeWalletLedger:
		return http.StatusConflict
	}
	return userStatus
}

// redemptionFailureBody is the error envelope RedeemCodeV2 and
// SwitchUserTopup answer. A wallet-ledger refusal also carries topup_url, the
// platform wallet page where the user's money now goes.
func redemptionFailureBody(err error) gin.H {
	body := gin.H{
		"success":    false,
		"message":    repo.RedemptionErrorMessage(err),
		"error_code": repo.RedemptionErrorCode(err),
	}
	if repo.RedemptionErrorCode(err) == repo.RedemptionErrorCodeWalletLedger {
		body["topup_url"] = common.IdentityTopupURL()
	}
	return body
}
