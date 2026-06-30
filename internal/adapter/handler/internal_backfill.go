package handler

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// InternalBackfillTokenAccountIDs populates IdentityAccountID on tokens
// by joining through user_identity_mapping → platform account lookup.
// This is a one-time migration endpoint. Idempotent: re-running is safe.
// POST /internal/admin/backfill-token-accounts
func InternalBackfillTokenAccountIDs(c *gin.Context) {
	if common.IdentityServiceURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identity service not configured"})
		return
	}

	// Find all tokens where IdentityAccountID is 0 (not yet backfilled)
	var tokens []repo.Token
	if err := repo.DB.Where("identity_account_id = 0 OR identity_account_id IS NULL").
		Select("id, user_id").
		Find(&tokens).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query tokens failed"})
		return
	}

	if len(tokens) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "no tokens to backfill", "updated": 0})
		return
	}

	// Collect unique user IDs
	userIDs := make(map[int]struct{})
	for _, t := range tokens {
		userIDs[t.UserId] = struct{}{}
	}

	// For each user, look up their platform account via user_identity_mapping
	userToAccount := make(map[int]int64)
	for userID := range userIDs {
		// Try all tenant mappings for this user
		var mappings []repo.UserIdentityMapping
		repo.DB.Where("lurus_user_id = ? AND is_active = ?", userID, true).Find(&mappings)

		for _, m := range mappings {
			if m.IDPSubject == "" {
				continue
			}
			// Look up platform account by OIDC subject
			account, err := common.GetAccountByZitadelSub(c.Request.Context(), m.IDPSubject)
			if err != nil || account == nil {
				continue
			}
			userToAccount[userID] = account.ID
			break
		}
	}

	// Batch update tokens
	updated := 0
	for _, t := range tokens {
		accountID, ok := userToAccount[t.UserId]
		if !ok || accountID <= 0 {
			continue
		}
		if err := repo.DB.Model(&repo.Token{}).Where("id = ?", t.Id).
			Update("identity_account_id", accountID).Error; err != nil {
			common.SysLog(fmt.Sprintf("backfill token %d failed: %s", t.Id, err.Error()))
			continue
		}
		updated++
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "backfill complete",
		"total_tokens":  len(tokens),
		"unique_users":  len(userIDs),
		"users_matched": len(userToAccount),
		"tokens_updated": updated,
	})
}
