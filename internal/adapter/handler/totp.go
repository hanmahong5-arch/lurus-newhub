package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/totp"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// totp.go — enroll / confirm / disable endpoints for the TOTP step-up factor.
//
// Flow: POST /api/user/totp/enroll returns the secret + otpauth:// URL once
// (enrollment stays pending), POST /api/user/totp/confirm activates it after
// the user proves possession with one valid code, and POST
// /api/user/totp/disable (behind SecureVerificationRequired, i.e. a fresh
// TOTP-verified session for enrolled users) removes it.
// The secret is stored AES-256-GCM encrypted and is never logged.

type totpCodeRequest struct {
	Code string `json:"code"`
}

// GetTotpStatus reports the current user's TOTP enrollment state for the
// settings UI. pending=true means enroll was called but confirm has not
// succeeded yet.
func GetTotpStatus(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return
	}
	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"enrolled": rec != nil && rec.Enabled,
			"pending":  rec != nil && !rec.Enabled,
		},
	})
}

// TotpEnroll generates a fresh TOTP secret for the current user and stores it
// encrypted in pending state. Re-calling before confirm rotates the pending
// secret; an active enrollment must be disabled first.
func TotpEnroll(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return
	}
	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if rec != nil && rec.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "两步验证已启用，如需重新配置请先禁用"})
		return
	}

	user := &repo.User{Id: userId}
	if err := user.FillUserById(); err != nil {
		common.ApiError(c, err)
		return
	}
	account := user.Username
	if account == "" {
		account = user.Email
	}

	secret, url, err := totp.GenerateEnrollment(account)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	encrypted, err := totp.EncryptSecret(secret)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId:          userId,
		SecretEncrypted: encrypted,
		Enabled:         false,
		CreatedAt:       common.GetTimestamp(),
		ConfirmedAt:     0,
	}); err != nil {
		common.ApiError(c, err)
		return
	}

	// The plaintext secret / URL leave the server exactly once, here. Never log them.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"secret":      secret,
			"otpauth_url": url,
		},
	})
}

// TotpConfirm activates a pending enrollment after validating one code, which
// proves the authenticator app actually holds the secret.
func TotpConfirm(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return
	}
	var req totpCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请输入验证码"})
		return
	}
	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if rec == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请先获取两步验证密钥"})
		return
	}
	if rec.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "两步验证已启用"})
		return
	}

	if !totp.AllowAttempt(c.Request.Context(), userId) {
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "验证失败次数过多，请稍后再试"})
		return
	}
	secret, err := totp.DecryptSecret(rec.SecretEncrypted)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !totp.ValidateCode(secret, req.Code) || !totp.MarkCodeUsed(c.Request.Context(), userId, req.Code) {
		totp.RecordFailure(c.Request.Context(), userId)
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
			governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"totp_confirm","reason":"invalid_code"}`))
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "验证码错误，请重试"})
		return
	}
	totp.ClearFailures(c.Request.Context(), userId)

	rec.Enabled = true
	rec.ConfirmedAt = common.GetTimestamp()
	if err := repo.UpsertUserTOTP(rec); err != nil {
		common.ApiError(c, err)
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		governance.ActionUserSelfUpdated, governance.ResourceUser, userId, `{"change":"totp_enrolled"}`))
	repo.RecordLog(userId, repo.LogTypeSystem, "两步验证已启用")

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "两步验证启用成功"})
}

// TotpDisable removes the user's enrollment. The route mounts
// SecureVerificationRequired, so an enrolled user can only reach this with a
// session that recently passed TOTP verification.
func TotpDisable(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未登录"})
		return
	}
	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if rec == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "两步验证未启用"})
		return
	}
	if err := repo.DeleteUserTOTP(userId); err != nil {
		common.ApiError(c, err)
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		governance.ActionUserSelfUpdated, governance.ResourceUser, userId, `{"change":"totp_disabled"}`))
	repo.RecordLog(userId, repo.LogTypeSystem, "两步验证已禁用")

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "两步验证已禁用"})
}
