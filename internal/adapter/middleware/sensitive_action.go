package middleware

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-gonic/gin"
)

// GetUserVerificationStatus returns the user's verification status.
// Phone and 2FA are now delegated to the OIDC provider; this endpoint reports IdP-managed state.
func GetUserVerificationStatus(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "User not authenticated",
		})
		return
	}

	user, err := repo.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get user information",
		})
		return
	}

	emailVerified := user.Email != ""

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"email_verified":                emailVerified,
			"phone_verified":                false, // managed by the OIDC provider
			"2fa_enabled":                   false, // managed by the OIDC provider
			"can_perform_sensitive_actions": true,
		},
	})
}
