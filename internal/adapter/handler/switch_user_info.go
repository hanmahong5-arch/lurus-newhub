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

// switchTenantDisabledCode is the machine-readable error_code the raw-token
// Switch endpoints answer with when the token's owning tenant is suspended or
// disabled. Same string the console/relay gates use (middleware/auth.go), so
// one client-side branch covers every surface.
const switchTenantDisabledCode = "TENANT_DISABLED"

// switchTenantSuspendedMessage is the sentence a Switch client shows its user
// verbatim. It is deliberately the SAME sentence the anonymous redeem path
// already answers with for the same condition (switch_redeem.go's suspended-
// reseller arm), so a customer reads one wording across activation, top-up
// and the quota card. Read 2026-09-20 in 2c-gui-switch
// internal/redemption/redeem.go: classifyRedeemFailure branches on
// 禁用/停用/账户 before 不存在, so a message routed through it types as
// "disabled" rather than "code does not exist". The billing client's own
// top-up call (internal/billing/client.go doRequest) does not classify — it
// prefixes the HTTP status and shows the sentence as-is — which is why the
// machine-readable signal is error_code, not the text.
const switchTenantSuspendedMessage = "经销商账户已停用，请联系经销商"

// authenticateSwitchRawToken is the four-value form of
// authenticateSwitchRawTokenWithCode, kept for callers that do not (yet)
// surface an error_code. New call sites should take the five-value form.
func authenticateSwitchRawToken(c *gin.Context) (token *repo.Token, user *repo.User, httpStatus int, message string) {
	token, user, httpStatus, message, _ = authenticateSwitchRawTokenWithCode(c)
	return token, user, httpStatus, message
}

// authenticateSwitchRawTokenWithCode extracts and resolves a Switch raw relay token
// (Token.Key) from the request's `Authorization` header (optional
// "Bearer "/"sk-" prefixes, optional "-<channel>" suffix) — the same
// convention as /api/v2/switch/heartbeat, and deliberately NOT
// middleware.UserAuth(), which resolves User.AccessToken rather than
// Token.Key and would reject every Switch client.
//
// On success, httpStatus is 0 and token/user are non-nil. On failure,
// token/user are nil and httpStatus/message/errorCode describe the response
// the caller should immediately return unchanged (401 for any auth failure,
// 403 when the owning tenant is suspended, 500 for a transient lookup
// failure). errorCode is empty for the failures that predate it.
//
// Tenant gate (cycle-13 L9): a token whose tenant an operator suspended is
// refused here, which is what stops a suspended reseller's end user from
// burning a fresh redemption code into a dead account through
// SwitchUserTopup — the refusal lands before repo.Redeem, so the code stays
// enabled. repo.TenantGate holds the rules: "default"/"" exempt, transient
// lookup faults fail OPEN, a soft-deleted tenant follows TENANT_MISSING_MODE.
//
// Shared by GetSwitchUserInfo and SwitchUserTopup — keep them in lockstep.
func authenticateSwitchRawTokenWithCode(c *gin.Context) (token *repo.Token, user *repo.User, httpStatus int, message, errorCode string) {
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
		return nil, nil, http.StatusUnauthorized, "missing Authorization token", ""
	}

	tok, err := repo.GetTokenByKey(key, false)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, http.StatusInternalServerError, "token lookup failed", ""
	}
	if err != nil || tok == nil {
		return nil, nil, http.StatusUnauthorized, "token not found", ""
	}
	if tok.Status == common.TokenStatusDisabled {
		return nil, nil, http.StatusUnauthorized, "token disabled", ""
	}

	usr, err := repo.GetUserById(tok.UserId)
	if err != nil || usr == nil {
		return nil, nil, http.StatusUnauthorized, "user not found", ""
	}
	if usr.Status == common.UserStatusDisabled {
		return nil, nil, http.StatusUnauthorized, "user disabled", ""
	}

	// Owning tenant last: a suspended tenant is an operator decision about
	// the account, not about the credential, so the more specific token/user
	// verdicts above still surface first.
	if ok, reason := repo.TenantGate(tok.TenantId); !ok {
		common.SysLog("switch raw-token auth: refused, tenant gate closed tenant=" +
			tok.TenantId + " reason=" + reason)
		return nil, nil, http.StatusForbidden, switchTenantSuspendedMessage, switchTenantDisabledCode
	}

	return tok, usr, 0, "", ""
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
//	403: the token's owning tenant is suspended or disabled, with
//	     error_code TENANT_DISABLED (cycle-13 L9)
//	500: transient lookup failure
//
// Authentication: see authenticateSwitchRawTokenWithCode.
//
// remaining_quota is the amount actually spendable through THIS token: the
// user balance when the token is unlimited, else the token's own remaining
// allowance capped by the user balance.
func GetSwitchUserInfo(c *gin.Context) {
	token, user, httpStatus, message, errorCode := authenticateSwitchRawTokenWithCode(c)
	if httpStatus != 0 {
		body := gin.H{"success": false, "message": message}
		if errorCode != "" {
			body["error_code"] = errorCode
		}
		c.JSON(httpStatus, body)
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
