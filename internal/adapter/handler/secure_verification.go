package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/totp"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const (
	// SecureVerificationSessionKey is the session key for secure verification
	SecureVerificationSessionKey = "secure_verified_at"
	// SecureVerificationTimeout is the verification validity period in seconds
	SecureVerificationTimeout = 300 // 5 minutes
)

type UniversalVerifyRequest struct {
	Method string `json:"method"`
	Code   string `json:"code,omitempty"`
}

type VerificationStatusResponse struct {
	Verified     bool  `json:"verified"`
	ExpiresAt    int64 `json:"expires_at,omitempty"`
	TotpEnrolled bool  `json:"totp_enrolled"`
}

// UniversalVerify marks the current session as securely verified (step-up).
//
// Policy:
//   - User HAS an active TOTP enrollment → method "session" is rejected
//     (403, code TOTP_REQUIRED); only method "totp" with a currently valid,
//     not-yet-used code passes. Wrong codes are throttled per user and
//     audited; a code can only be spent once inside the replay window.
//   - User has NO enrollment → legacy behavior: any authenticated, enabled
//     user passes with method "session". The response carries
//     totp_enrolled:false so the frontend can steer users to enroll.
func UniversalVerify(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "未登录",
		})
		return
	}

	user := &repo.User{Id: userId}
	if err := user.FillUserById(); err != nil {
		common.ApiError(c, err)
		return
	}

	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "该用户已被禁用"})
		return
	}

	var req UniversalVerifyRequest
	// Tolerate an empty body (legacy callers): it degrades to method "session".
	_ = c.ShouldBindJSON(&req)
	if req.Method == "" {
		req.Method = "session"
	}

	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	enrolled := rec != nil && rec.Enabled

	if enrolled {
		if req.Method != "totp" {
			c.JSON(http.StatusForbidden, gin.H{
				"success":       false,
				"message":       "已启用两步验证，请输入验证码",
				"code":          "TOTP_REQUIRED",
				"totp_enrolled": true,
			})
			return
		}
		if !totp.AllowAttempt(c.Request.Context(), userId) {
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
				governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"totp_throttled"}`))
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "验证失败次数过多，请稍后再试"})
			return
		}
		secret, err := totp.DecryptSecret(rec.SecretEncrypted)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if !totp.ValidateCode(secret, req.Code) {
			totp.RecordFailure(c.Request.Context(), userId)
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
				governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"totp_invalid_code"}`))
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "验证码错误，请重试"})
			return
		}
		if !totp.MarkCodeUsed(c.Request.Context(), userId, req.Code) {
			totp.RecordFailure(c.Request.Context(), userId)
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
				governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"totp_code_replayed"}`))
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "该验证码已被使用，请等待新的验证码"})
			return
		}
		totp.ClearFailures(c.Request.Context(), userId)
	} else if req.Method != "session" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":       false,
			"message":       "不支持的验证方式",
			"totp_enrolled": false,
		})
		return
	}

	session := sessions.Default(c)
	now := time.Now().Unix()
	session.Set(SecureVerificationSessionKey, now)
	if err := session.Save(); err != nil {
		common.ApiError(c, err)
		return
	}

	repo.RecordLog(userId, repo.LogTypeSystem, "安全验证成功")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "验证成功",
		"data": gin.H{
			"verified":      true,
			"expires_at":    now + SecureVerificationTimeout,
			"totp_enrolled": enrolled,
		},
	})
}

// GetVerificationStatus returns whether the current session has passed secure verification.
func GetVerificationStatus(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "未登录",
		})
		return
	}

	rec, err := repo.GetUserTOTP(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	totpEnrolled := rec != nil && rec.Enabled

	session := sessions.Default(c)
	verifiedAtRaw := session.Get(SecureVerificationSessionKey)

	if verifiedAtRaw == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled},
		})
		return
	}

	verifiedAt, ok := verifiedAtRaw.(int64)
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled},
		})
		return
	}

	elapsed := time.Now().Unix() - verifiedAt
	if elapsed >= SecureVerificationTimeout {
		session.Delete(SecureVerificationSessionKey)
		_ = session.Save()
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": VerificationStatusResponse{
			Verified:     true,
			ExpiresAt:    verifiedAt + SecureVerificationTimeout,
			TotpEnrolled: totpEnrolled,
		},
	})
}

// CheckSecureVerification returns true if the session has a valid secure verification timestamp.
func CheckSecureVerification(c *gin.Context) bool {
	session := sessions.Default(c)
	verifiedAtRaw := session.Get(SecureVerificationSessionKey)

	if verifiedAtRaw == nil {
		return false
	}

	verifiedAt, ok := verifiedAtRaw.(int64)
	if !ok {
		return false
	}

	elapsed := time.Now().Unix() - verifiedAt
	if elapsed >= SecureVerificationTimeout {
		session.Delete(SecureVerificationSessionKey)
		_ = session.Save()
		return false
	}

	return true
}
