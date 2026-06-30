package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

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
	Verified  bool  `json:"verified"`
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// UniversalVerify marks the current session as securely verified.
// Since MFA is delegated to the OIDC provider, this endpoint just records the verification timestamp.
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
			"verified":   true,
			"expires_at": now + SecureVerificationTimeout,
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

	session := sessions.Default(c)
	verifiedAtRaw := session.Get(SecureVerificationSessionKey)

	if verifiedAtRaw == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false},
		})
		return
	}

	verifiedAt, ok := verifiedAtRaw.(int64)
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    VerificationStatusResponse{Verified: false},
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
			"data":    VerificationStatusResponse{Verified: false},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": VerificationStatusResponse{
			Verified:  true,
			ExpiresAt: verifiedAt + SecureVerificationTimeout,
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
