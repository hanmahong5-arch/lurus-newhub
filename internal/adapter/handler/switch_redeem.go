package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================================
// Switch Anonymous Redeem (Phase D Track 2.1)
//
// POST /api/v2/switch/redeem  — NO authentication.
//
// Switch EndUser mode (white-label desktop client) needs to exchange an
// activation code for a usable LLM gateway token without going through
// OIDC. The flow:
//
//   1. Caller POSTs { code, fingerprint, app_version? }.
//   2. Hub looks up the redemption row by Key.
//   3. Hub validates: exists, not used, not disabled, not past expires_at.
//   4. Hub find-or-creates an anonymous user keyed on the device fingerprint
//      inside the redemption's tenant.
//   5. Hub calls repo.Redeem() to atomically mark the code used + credit
//      the anonymous user's quota (FOR UPDATE lock prevents double-spend).
//   6. Hub provisions a relay Token for that user and returns it.
//
// Error envelope keywords are intentional: the Switch client greps the
// message text in classifyRedeemFailure() to choose the localized UI copy
// — substrings "已使用" / "过期" / "禁用" / "不存在" must be preserved.
//
// Route registration is performed by a separate sequential agent to avoid
// router-file merge conflicts during the Phase D swarm; suggested line:
//   switchGroup.POST("/redeem", handler.SwitchRedeemAnonymous)
// inside the existing apiV2.Group("/switch") block of
// internal/adapter/handler/router/api-v2-router.go.
// ============================================================================

// switchRedeemRequest is the JSON body sent by the Switch EndUser client.
// Field names must match the Switch redemption.RedeemRequest in
// 2c-gui-switch/internal/redemption/redeem.go.
type switchRedeemRequest struct {
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
	AppVersion  string `json:"app_version,omitempty"`
}

// switchRedeemData is the success-envelope `data` payload. Mirrors Switch's
// redemption.RedeemResponse: keys + types are load-bearing on the client.
type switchRedeemData struct {
	UserToken  string `json:"user_token"`
	UserID     int    `json:"user_id"`
	Quota      int64  `json:"quota"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	TenantSlug string `json:"tenant_slug,omitempty"`
}

// switchEndUserUsernamePrefix tags users provisioned by anonymous redeem.
// Truncated fingerprint avoids username length cap (20 chars total).
const switchEndUserUsernamePrefix = "sw-eu-"

// SwitchRedeemAnonymous handles POST /api/v2/switch/redeem.
//
// No middleware should be attached: this endpoint is anonymous by design.
// It validates the request, runs the redemption transaction, provisions
// a relay token, and returns the standard {success, data, message}
// envelope used by the rest of v2.
func SwitchRedeemAnonymous(c *gin.Context) {
	var req switchRedeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求体格式错误",
		})
		return
	}

	code := strings.TrimSpace(req.Code)
	fingerprint := strings.TrimSpace(req.Fingerprint)

	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "激活码必填",
		})
		return
	}
	if fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "设备指纹必填",
		})
		return
	}

	// Step 1: look up the redemption (read-only, no lock) to classify the
	// failure mode for the UI. The authoritative used/expired check still
	// happens inside repo.Redeem under FOR UPDATE, so this pre-check is for
	// branching messages — it doesn't trust its own result for state
	// changes.
	redemption, err := findRedemptionByKey(code)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "激活码不存在",
			})
			return
		}
		common.SysError("switch redeem: lookup failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "服务暂不可用，请稍后重试",
		})
		return
	}

	switch redemption.Status {
	case common.RedemptionCodeStatusUsed:
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "激活码已使用",
		})
		return
	case common.RedemptionCodeStatusDisabled:
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "激活码已被禁用",
		})
		return
	}

	if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "激活码已过期",
		})
		return
	}

	// Step 2: resolve the tenant. Codes inserted by v1 have TenantId
	// "default" — we honor that as a wildcard (matches repo.Redeem's
	// tenant-isolation bypass) and don't surface a tenant_slug.
	tenantID := redemption.TenantId
	if tenantID == "" {
		tenantID = "default"
	}

	var tenantSlug string
	if tenantID != "default" {
		t, err := repo.GetTenantByID(tenantID)
		if err == nil && t != nil {
			tenantSlug = t.Slug
			if !t.IsEnabled() {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "经销商账户已停用，请联系经销商",
				})
				return
			}
		}
	}

	// Step 3: find or create the anonymous user keyed on fingerprint.
	user, err := findOrCreateSwitchEndUser(fingerprint, tenantID)
	if err != nil {
		common.SysError("switch redeem: provision user failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无法创建匿名账户，请稍后重试",
		})
		return
	}

	// Step 4: run the canonical redeem path. This is the same code path
	// every other redeem flow uses (RedeemCodeV2, /api/user/topup) — we
	// reuse it instead of duplicating the FOR UPDATE / mark-used logic.
	quotaAdded, err := repo.Redeem(code, user.Id)
	if err != nil {
		// repo.Redeem wraps the underlying message as "兑换失败，<inner>".
		// <inner> is usually one of a small set of known sentinel strings,
		// but on a genuine transaction failure (e.g. a constraint violation
		// on the Save/Update calls) it can also be a raw driver/GORM error.
		// Never echo that raw text to this anonymous, unauthenticated
		// caller — only the known sentinels are safe to pass through so
		// the Switch classifier still sees its substring markers; anything
		// else is replaced with a generic message and logged server-side.
		common.SysError("switch redeem: redeem failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": sanitizeRedeemError(err),
		})
		return
	}

	// Step 5: provision a relay token bound to this user. The Switch client
	// stores this as the gateway User-Token. We deliberately don't reuse
	// AutoCreateDefaultToken — that creates an UnlimitedQuota=true token in
	// the "default" tenant, which would break tenant isolation for paid
	// resellers and let the EndUser drain the pool past the redeemed
	// amount.
	token, err := provisionSwitchEndUserToken(user.Id, tenantID, quotaAdded)
	if err != nil {
		common.SysError("switch redeem: provision token failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无法签发用户 token，请联系管理员",
		})
		return
	}

	data := switchRedeemData{
		UserToken:  token.Key,
		UserID:     user.Id,
		Quota:      int64(quotaAdded),
		TenantSlug: tenantSlug,
	}
	if redemption.ExpiredTime > 0 {
		data.ExpiresAt = redemption.ExpiredTime
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "兑换成功",
		"data":    data,
	})
}

// findRedemptionByKey looks up a redemption row by Key without tenant
// isolation. Anonymous redeem has no tenant context (caller is unauth'd);
// repo.Redeem still enforces tenant ownership at the time of mark-used.
func findRedemptionByKey(key string) (*repo.Redemption, error) {
	// PG-only runtime; the SQLite test tier also accepts double-quoted identifiers.
	keyCol := `"key"`
	r := &repo.Redemption{}
	err := repo.WithoutTenantIsolation(repo.DB).Where(keyCol+" = ?", key).First(r).Error
	return r, err
}

// findOrCreateSwitchEndUser returns the User row for the given fingerprint
// in the given tenant, creating it on first sight. The username is
// derived deterministically from the fingerprint so repeated redeems by
// the same machine accumulate on one account (subject to the 20-char
// username cap; we truncate the fingerprint to fit). The lookup is
// tenant-scoped — username is per-tenant unique since migration 025, so the
// same fingerprint redeeming codes of two different tenants gets one
// account per tenant (previously the cross-tenant redeem failed on the
// global unique + repo.Redeem's tenant-ownership check).
func findOrCreateSwitchEndUser(fingerprint, tenantID string) (*repo.User, error) {
	// Username layout: "sw-eu-" + first 14 hex chars of fingerprint.
	// Total 20 chars — matches User.Username validate:"max=20".
	fpTrim := fingerprint
	if len(fpTrim) > 14 {
		fpTrim = fpTrim[:14]
	}
	username := switchEndUserUsernamePrefix + fpTrim

	var existing repo.User
	err := repo.WithoutTenantIsolation(repo.DB).Where("username = ? AND tenant_id = ?", username, tenantID).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup user: %w", err)
	}

	user := &repo.User{
		Username:    username,
		DisplayName: "Switch EndUser",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		TenantId:    tenantID,
		Group:       "default",
	}
	if err := user.Insert(); err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	// user.Insert() sets Quota to QuotaForNewUser; we need the assigned ID.
	// Insert() resolves Id via gorm's RETURNING / LastInsertId. Re-fetch by
	// username just in case Insert() didn't repopulate the receiver.
	if user.Id == 0 {
		if err := repo.WithoutTenantIsolation(repo.DB).Where("username = ? AND tenant_id = ?", username, tenantID).First(user).Error; err != nil {
			return nil, fmt.Errorf("refetch user: %w", err)
		}
	}
	return user, nil
}

// provisionSwitchEndUserToken creates a bounded-quota relay Token for the
// EndUser. Quota is set to the redeemed amount to act as a hard ceiling
// even if repo.Redeem also credits user.Quota — defense in depth against
// a misbehaving relay layer.
func provisionSwitchEndUserToken(userID int, tenantID string, quota int) (*repo.Token, error) {
	key, err := common.GenerateRandomKey(48)
	if err != nil {
		return nil, fmt.Errorf("generate token key: %w", err)
	}
	if tenantID == "" {
		tenantID = "default"
	}
	token := &repo.Token{
		UserId:         userID,
		TenantId:       tenantID,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "switch-enduser",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1, // token never auto-expires; quota is the cap
		RemainQuota:    quota,
		UnlimitedQuota: false,
		Group:          "default",
	}
	if err := token.Insert(); err != nil {
		return nil, fmt.Errorf("insert token: %w", err)
	}
	return token, nil
}

// switchRedeemKnownErrors is the exact set of inner messages
// repo.Redeem (internal/adapter/repo/redemption.go) can wrap as
// "兑换失败，<inner>". These are controlled, translated strings the Switch
// client's classifyRedeemFailure() greps substrings out of ("已使用" /
// "过期" / "禁用" / "不存在") — they are safe to return to an unauthenticated
// caller verbatim. Anything else reaching this set (e.g. a raw
// driver/GORM error from the transaction's Update/Save calls) is not a
// known sentinel and must not be echoed back; see sanitizeRedeemError.
var switchRedeemKnownErrors = map[string]bool{
	"无效的兑换码":       true,
	"该兑换码已被使用":     true,
	"该兑换码已过期":      true,
	"用户不存在":        true,
	"该兑换码不属于当前租户": true,
}

// sanitizeRedeemError strips repo.Redeem's "兑换失败，" wrapper and returns
// the inner message unchanged if it's one of the known sentinel strings.
// Any other error — including raw driver/GORM error text such as
// constraint or column names — is replaced with a generic message so it
// never reaches this endpoint's anonymous, unauthenticated caller. The
// caller of this function is responsible for logging the real error
// server-side before calling it.
func sanitizeRedeemError(err error) string {
	message := strings.TrimPrefix(err.Error(), "兑换失败，")
	if switchRedeemKnownErrors[message] {
		return message
	}
	return "服务暂不可用，请稍后重试"
}
