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
//	400: malformed body, or the code is invalid/already used/expired
//	401: missing/unknown/disabled token or user
//	500: transient lookup failure
//
// Authentication is the raw relay token (Token.Key) — see
// authenticateSwitchRawToken (shared with GetSwitchUserInfo).
//
// The redemption itself runs through repo.Redeem, the same
// find-FOR-UPDATE / mark-used / credit-quota transaction used by
// RedeemCodeV2 (POST /api/v2/:tenant_slug/redeem, the handler also mounted
// as the v1-compat POST /api/user/topup) and by the anonymous Switch
// redeem flow (SwitchRedeemAnonymous) — this handler does not reimplement
// any of that logic, it only resolves which user id to credit.
func SwitchUserTopup(c *gin.Context) {
	token, _, httpStatus, message := authenticateSwitchRawToken(c)
	if httpStatus != 0 {
		c.JSON(httpStatus, gin.H{"success": false, "message": message})
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

	quota, err := repo.Redeem(key, token.UserId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
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
