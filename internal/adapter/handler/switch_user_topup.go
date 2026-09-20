package handler

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// switchUserTopupRequest is the JSON body for POST /api/v2/switch/user/topup.
type switchUserTopupRequest struct {
	Key string `json:"key"`
}

// SwitchUserTopup redeems a topup/redemption code for the user owning the
// presented relay token, crediting that user's account balance.
//
// POST /api/v2/switch/user/topup
//
// Body: {"key": "<redemption code>"}
//
//	200: {"success":true,"data":{"quota":<amount credited by this call>}}
//	400: malformed body, or the code is invalid/already used/expired/another
//	     tenant's (the body then also carries error_code REDEMPTION_*, cycle13 L3)
//	401: missing/unknown/disabled token or user
//	403: the token's tenant is suspended, with error_code TENANT_DISABLED
//	     (cycle13 L9's refusal path, via authenticateSwitchRawTokenWithCode;
//	     the refusal lands before repo.Redeem, so the code stays unspent)
//	500: transient lookup failure, or a hub-side fault inside repo.Redeem
//	     (error_code REDEMPTION_FAILED; the transaction rolled back, so the
//	     code is still redeemable — a retry is the right client move, which
//	     is why it is not a 400; redemptionFailureStatus)
//
// Authentication is the raw relay token (Token.Key) — see
// authenticateSwitchRawTokenWithCode (shared with GetSwitchUserInfo).
//
// The redemption itself runs through repo.Redeem, the same
// find-FOR-UPDATE / mark-used / credit-quota transaction used by
// RedeemCodeV2 (POST /api/v2/:tenant_slug/redeem, the handler also mounted
// as the v1-compat POST /api/user/topup) and by the anonymous Switch
// redeem flow (SwitchRedeemAnonymous) — this handler does not reimplement
// any of that logic, it only resolves which user id to credit.
func SwitchUserTopup(c *gin.Context) {
	token, _, httpStatus, message, errorCode := authenticateSwitchRawTokenWithCode(c)
	if httpStatus != 0 {
		body := gin.H{"success": false, "message": message}
		// errorCode is empty for the auth failures that predate it (401s), so
		// their body shape is unchanged; the suspended-tenant 403 carries
		// TENANT_DISABLED for a client that wants a machine-readable reason.
		// The Switch client today reads no error_code at all: it classifies
		// redemption failures by Chinese substrings of `message` (its
		// redeem.go) and shows this 403 as a transient error (owner item
		// O-heartbeat) — so `message` stays the customer-readable sentence,
		// surfaced verbatim.
		if errorCode != "" {
			body["error_code"] = errorCode
		}
		c.JSON(httpStatus, body)
		return
	}

	var req switchUserTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "redemption code is required"})
		return
	}

	// repo.RedemptionErrorMessage/RedemptionErrorCode (not err.Error()
	// directly) — cycle13 L3: a genuine transaction/driver failure inside
	// repo.Redeem used to reach this response body verbatim (constraint/
	// column names included); repo.Redeem itself no longer returns that raw
	// text, and RedemptionErrorMessage is defence in depth on top.
	quota, err := repo.Redeem(key, token.UserId)
	if err != nil {
		c.JSON(redemptionFailureStatus(err, http.StatusBadRequest), gin.H{
			"success":    false,
			"message":    repo.RedemptionErrorMessage(err),
			"error_code": repo.RedemptionErrorCode(err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": int64(quota),
		},
	})
}
