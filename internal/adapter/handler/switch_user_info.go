package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// authenticateSwitchRawToken extracts and resolves a Switch raw relay token
// (Token.Key) from the request's `Authorization` header (optional
// "Bearer "/"sk-" prefixes, optional "-<channel>" suffix) — the same
// convention as /api/v2/switch/heartbeat, and deliberately NOT
// middleware.UserAuth(), which resolves User.AccessToken rather than
// Token.Key and would reject every Switch client.
//
// On success, httpStatus is 0 and token/user are non-nil. On failure,
// token/user are nil and httpStatus/message describe the response the
// caller should immediately return unchanged (401 for any auth failure,
// 500 for a transient lookup failure).
//
// Shared by GetSwitchUserInfo and SwitchUserTopup — keep them in lockstep.
func authenticateSwitchRawToken(c *gin.Context) (token *repo.Token, user *repo.User, httpStatus int, message string) {
	key := strings.TrimSpace(c.Request.Header.Get("Authorization"))
	if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	key = strings.TrimPrefix(key, "sk-")
	// sk-<key>-<channel> form (relay convention) — quota lookup ignores channel.
	if idx := strings.IndexByte(key, '-'); idx > 0 {
		key = key[:idx]
	}
	if key == "" {
		return nil, nil, http.StatusUnauthorized, "missing Authorization token"
	}

	tok, err := repo.GetTokenByKey(key, false)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, http.StatusInternalServerError, "token lookup failed"
	}
	if err != nil || tok == nil {
		return nil, nil, http.StatusUnauthorized, "token not found"
	}
	if tok.Status == common.TokenStatusDisabled {
		return nil, nil, http.StatusUnauthorized, "token disabled"
	}

	usr, err := repo.GetUserById(tok.UserId)
	if err != nil || usr == nil {
		return nil, nil, http.StatusUnauthorized, "user not found"
	}
	if usr.Status == common.UserStatusDisabled {
		return nil, nil, http.StatusUnauthorized, "user disabled"
	}

	return tok, usr, 0, ""
}

// GetSwitchUserInfo returns the quota/identity snapshot for the user owning
// the presented relay token.
//
// GET /api/v2/switch/user/info
//
// Contract (mirrors lurus-switch internal/billing/client.go GetUserInfo —
// keep the two in lockstep):
//
//	200: {"success":true,"data":{quota,used_quota,remaining_quota,daily_quota,
//	     group,username,display_name,role}}
//	401: missing/unknown/disabled token or user
//	500: transient lookup failure
//
// Authentication: see authenticateSwitchRawToken.
//
// remaining_quota is the amount actually spendable through THIS token: the
// user balance when the token is unlimited, else the token's own remaining
// allowance capped by the user balance.
func GetSwitchUserInfo(c *gin.Context) {
	token, user, httpStatus, message := authenticateSwitchRawToken(c)
	if httpStatus != 0 {
		c.JSON(httpStatus, gin.H{"success": false, "message": message})
		return
	}

	remaining := user.Quota
	if !token.UnlimitedQuota && token.RemainQuota < remaining {
		remaining = token.RemainQuota
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota":           user.Quota,
			"used_quota":      user.UsedQuota,
			"remaining_quota": remaining,
			"daily_quota":     user.DailyQuota,
			"group":           user.Group,
			"username":        user.Username,
			"display_name":    user.DisplayName,
			"role":            user.Role,
		},
	})
}
