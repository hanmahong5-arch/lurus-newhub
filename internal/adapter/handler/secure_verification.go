package handler

import (
	"fmt"
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
	// EnrollmentRequired mirrors secureVerificationRequireEnrollment() so the
	// console can show the real policy instead of assuming the legacy one —
	// it is a process-wide setting, not something derived from this user.
	EnrollmentRequired bool `json:"enrollment_required"`
}

// secureVerificationRequireEnrollment reports whether UniversalVerify's
// no-enrollment branch must refuse step-up instead of granting it for free.
// Read fresh from the environment on every call (no init-time snapshot) so
// tests can toggle it with t.Setenv.
//
// Default false is a measured decision, not timidity: as of the cycle-9
// plan the only production account with role >= 10 (root, id 1) has no row
// in user_totps, so flipping this default would lock that account out of
// channel-key reveal and 2FA force-disable — see .env.example and
// doc/runbook/incident-response.md for the break-glass procedure.
func secureVerificationRequireEnrollment() bool {
	return common.GetEnvOrDefaultBool("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", false)
}

// UniversalVerify marks the current session as securely verified (step-up).
//
// Policy:
//   - User HAS an active TOTP enrollment → method "session" is rejected
//     (403, code TOTP_REQUIRED); only method "totp" with a currently valid,
//     not-yet-used code passes. Wrong codes are throttled per user and
//     audited; a code can only be spent once inside the replay window.
//   - User has NO enrollment → by default (SECURE_VERIFICATION_REQUIRE_ENROLLMENT
//     unset/false) this grants step-up on nothing beyond an already-authenticated
//     session: method "session" passes with no credential presented at all,
//     because there is no second factor to check. A stolen session cookie for
//     such a user satisfies the gates behind middleware.SecureVerificationRequired
//     (router/api-router.go:80 totp/disable, :84 backup-codes/regenerate, :151
//     channel-key reveal; router/api-v2-router.go:560 2FA force-disable)
//     exactly as well as the legitimate owner would. That grant is handed to
//     the audit writer on every pass through this branch
//     (governance.ActionAuthStepUpNoCredential) — the write itself is a
//     best-effort background insert (see governance.RecordAuditEvent), so it
//     is no longer invisible even when a write is dropped or fails. Setting
//     SECURE_VERIFICATION_REQUIRE_ENROLLMENT=true closes it: the
//     no-enrollment branch answers 403 STEP_UP_ENROLLMENT_REQUIRED instead of
//     passing, and the session key is never set. The response carries
//     totp_enrolled:false in this branch so the frontend can steer users to
//     enroll.
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
		if req.Method != "totp" && req.Method != "totp_backup" {
			c.JSON(http.StatusForbidden, gin.H{
				"success":       false,
				"message":       "已启用两步验证，请输入验证码",
				"code":          "TOTP_REQUIRED",
				"totp_enrolled": true,
			})
			return
		}
		// Both factors share one per-user throttle: a lost-device attacker
		// guessing backup codes burns the same failure budget as one
		// guessing TOTP codes, so switching factors gains nothing.
		if !totp.AllowAttempt(c.Request.Context(), userId) {
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
				governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"totp_throttled"}`))
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "验证失败次数过多，请稍后再试"})
			return
		}

		var remainingBackupCodes int64
		if req.Method == "totp_backup" {
			codeHash := totp.HashBackupCode(userId, req.Code)
			usedAt := common.GetTimestamp()
			consumed, cErr := repo.ConsumeUserTOTPBackupCode(userId, codeHash, usedAt)
			if cErr != nil {
				common.ApiError(c, cErr)
				return
			}
			if !consumed {
				// Wrong code AND a legitimately-replayed one both fail closed
				// here — the single atomic UPDATE (WHERE used_at=0) cannot
				// distinguish "never existed" from "already spent", by design.
				totp.RecordFailure(c.Request.Context(), userId)
				governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
					governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"totp_backup_invalid_or_replayed"}`))
				c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Invalid or already-used backup code"})
				return
			}
			remainingBackupCodes, err = repo.CountUnusedUserTOTPBackupCodes(userId)
			if err != nil {
				common.ApiError(c, err)
				return
			}
		} else {
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
		}
		totp.ClearFailures(c.Request.Context(), userId)

		if req.Method == "totp_backup" {
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
				governance.ActionAuthLoginSuccess, governance.ResourceUser, userId,
				fmt.Sprintf(`{"step":"secure_verify","method":"totp_backup","remaining":%d}`, remainingBackupCodes)))
		}
	} else if req.Method != "session" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":       false,
			"message":       "不支持的验证方式",
			"totp_enrolled": false,
		})
		return
	} else if secureVerificationRequireEnrollment() {
		// Same throttle-refusal precedent as the enrolled-TOTP branches
		// above (ActionAuthFailed with a "step" + "reason" detail blob):
		// an operator who turns enforcement on gets a record of blocked
		// step-up attempts, not just of the ones that succeeded.
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
			governance.ActionAuthFailed, governance.ResourceUser, userId, `{"step":"secure_verify","reason":"enrollment_required"}`))
		c.JSON(http.StatusForbidden, gin.H{
			"success":       false,
			"message":       "Step-up verification requires an enrolled second factor. Enrol two-factor authentication in Settings > Security, then retry.",
			"code":          "STEP_UP_ENROLLMENT_REQUIRED",
			"totp_enrolled": false,
		})
		return
	} else {
		// Credential-free grant: no TOTP enrollment exists for this user, so
		// this request proves nothing beyond an already-authenticated
		// session. Handed to the audit writer on every pass through this
		// branch (governance.ActionAuthStepUpNoCredential) — the write is a
		// best-effort background insert — so the weakness is visible even
		// while the flag above stays off.
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
			governance.ActionAuthStepUpNoCredential, governance.ResourceUser, userId,
			`{"step":"secure_verify","method":"session","reason":"no_totp_enrollment"}`))
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
	enrollmentRequired := secureVerificationRequireEnrollment()

	session := sessions.Default(c)
	verifiedAtRaw := session.Get(SecureVerificationSessionKey)

	if verifiedAtRaw == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled, EnrollmentRequired: enrollmentRequired},
		})
		return
	}

	verifiedAt, ok := verifiedAtRaw.(int64)
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled, EnrollmentRequired: enrollmentRequired},
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
			"data":    VerificationStatusResponse{Verified: false, TotpEnrolled: totpEnrolled, EnrollmentRequired: enrollmentRequired},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": VerificationStatusResponse{
			Verified:           true,
			ExpiresAt:          verifiedAt + SecureVerificationTimeout,
			TotpEnrolled:       totpEnrolled,
			EnrollmentRequired: enrollmentRequired,
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
